// +build integration

package cmd_test

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	v1 "github.com/jenkins-x/jx/pkg/apis/jenkins.io/v1"
	"github.com/jenkins-x/jx/pkg/gits"
	"github.com/jenkins-x/jx/pkg/helm"
	"github.com/jenkins-x/jx/pkg/jx/cmd"
	"github.com/jenkins-x/jx/pkg/jx/cmd/opts"
	"github.com/jenkins-x/jx/pkg/kube"
	resources_test "github.com/jenkins-x/jx/pkg/kube/resources/mocks"
	"github.com/jenkins-x/jx/pkg/log"
	"github.com/jenkins-x/jx/pkg/workflow"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestSequentialWorkflow(t *testing.T) {
	originalJxHome, tempJxHome, err := cmd.CreateTestJxHomeDir()
	assert.NoError(t, err)
	defer func() {
		err := cmd.CleanupTestJxHomeDir(originalJxHome, tempJxHome)
		assert.NoError(t, err)
	}()
	originalKubeCfg, tempKubeCfg, err := cmd.CreateTestKubeConfigDir()
	assert.NoError(t, err)
	defer func() {
		err := cmd.CleanupTestKubeConfigDir(originalKubeCfg, tempKubeCfg)
		assert.NoError(t, err)
	}()

	testOrgName := "jstrachan"
	testRepoName := "myrepo"
	stagingRepoName := "environment-staging"
	prodRepoName := "environment-production"

	fakeRepo := gits.NewFakeRepository(testOrgName, testRepoName)
	stagingRepo := gits.NewFakeRepository(testOrgName, stagingRepoName)
	prodRepo := gits.NewFakeRepository(testOrgName, prodRepoName)

	fakeGitProvider := gits.NewFakeProvider(fakeRepo, stagingRepo, prodRepo)
	fakeGitProvider.User.Username = testOrgName

	staging := kube.NewPermanentEnvironmentWithGit("staging", "http://fake.git/"+testOrgName+"/"+stagingRepoName+".git")
	production := kube.NewPermanentEnvironmentWithGit("production", "http://fake.git/"+testOrgName+"/"+prodRepoName+".git")
	staging.Spec.Order = 100
	production.Spec.Order = 200

	configureGitFn := func(dir string, gitInfo *gits.GitRepository, gitter gits.Gitter) error {
		err := gitter.Init(dir)
		if err != nil {
			return err
		}
		// Really we should have a dummy environment chart but for now let's just mock it out as needed
		err = os.MkdirAll(filepath.Join(dir, "templates"), 0700)
		if err != nil {
			return err
		}
		data, err := json.Marshal(staging)
		if err != nil {
			return err
		}
		err = ioutil.WriteFile(filepath.Join(dir, "templates", "environment-staging.yaml"), data, 0755)
		if err != nil {
			return err
		}
		data, err = json.Marshal(production)
		if err != nil {
			return err
		}
		err = ioutil.WriteFile(filepath.Join(dir, "templates", "environment-production.yaml"), data, 0755)
		if err != nil {
			return err
		}
		return gitter.AddCommit(dir, "Initial Commit")
	}

	o := &cmd.ControllerWorkflowOptions{
		ControllerOptions: cmd.ControllerOptions{
			CommonOptions: &opts.CommonOptions{},
		},
		NoWatch:        true,
		Namespace:      "jx",
		ConfigureGitFn: configureGitFn,
	}

	myFlowName := "myflow"

	step1 := workflow.CreateWorkflowPromoteStep("staging")
	step2 := workflow.CreateWorkflowPromoteStep("production", step1)

	cmd.ConfigureTestOptionsWithResources(o.CommonOptions,
		[]runtime.Object{},
		[]runtime.Object{
			staging,
			production,
			kube.NewPreviewEnvironment("jx-jstrachan-demo96-pr-1"),
			kube.NewPreviewEnvironment("jx-jstrachan-another-pr-3"),
			workflow.CreateWorkflow("jx", myFlowName,
				step1,
				step2,
			),
		},
		gits.NewGitLocal(),
		fakeGitProvider,
		helm.NewHelmCLI("helm", helm.V2, "", true),
		resources_test.NewMockInstaller(),
	)

	err = cmd.CreateTestEnvironmentDir(o.CommonOptions)
	assert.NoError(t, err)

	jxClient, ns, err := o.JXClientAndDevNamespace()
	assert.NoError(t, err)
	if err == nil {
		workflow, err := workflow.GetWorkflow("", jxClient, ns)
		assert.NoError(t, err)
		if err == nil {
			assert.Equal(t, "default", workflow.Name, "name")
			spec := workflow.Spec
			assert.Equal(t, 2, len(spec.Steps), "number of steps")
			if len(spec.Steps) > 0 {
				cmd.AssertPromoteStep(t, &spec.Steps[0], "staging")
			}
			if len(spec.Steps) > 1 {
				cmd.AssertPromoteStep(t, &spec.Steps[1], "production")
			}
		}
	}

	a, err := cmd.CreateTestPipelineActivity(jxClient, ns, testOrgName, testRepoName, "master", "1", myFlowName)
	assert.NoError(t, err)
	if err != nil {
		return
	}
	err = o.Run()
	assert.NoError(t, err)
	if err != nil {
		return
	}
	activities := jxClient.JenkinsV1().PipelineActivities(ns)
	cmd.AssertHasPullRequestForEnv(t, activities, a.Name, "staging")
	cmd.AssertWorkflowStatus(t, activities, a.Name, v1.ActivityStatusTypeRunning)

	// lets make sure we don't create a PR for production as we have not completed the staging PR yet
	err = o.Run()
	cmd.AssertHasNoPullRequestForEnv(t, activities, a.Name, "production")

	// still no PR merged so cannot create a PR for production
	cmd.PollGitStatusAndReactToPipelineChanges(t, o, jxClient, ns)
	cmd.AssertHasNoPullRequestForEnv(t, activities, a.Name, "production")

	// test no PR on production until staging completed
	if !cmd.AssertSetPullRequestMerged(t, fakeGitProvider, stagingRepo.Owner, stagingRepo.GitRepo.Name, 1) {
		return
	}

	cmd.PollGitStatusAndReactToPipelineChanges(t, o, jxClient, ns)
	cmd.AssertHasNoPullRequestForEnv(t, activities, a.Name, "production")

	if !cmd.AssertSetPullRequestComplete(t, fakeGitProvider, stagingRepo, 1) {
		return
	}

	// now lets poll again due to change to the activity to detect the staging is complete
	cmd.PollGitStatusAndReactToPipelineChanges(t, o, jxClient, ns)

	cmd.AssertHasPromoteStatus(t, activities, a.Name, "staging", v1.ActivityStatusTypeSucceeded)
	cmd.AssertHasPullRequestForEnv(t, activities, a.Name, "production")
	cmd.AssertHasPromoteStatus(t, activities, a.Name, "production", v1.ActivityStatusTypeRunning)
	cmd.AssertHasPipelineStatus(t, activities, a.Name, v1.ActivityStatusTypeRunning)

	if !cmd.AssertSetPullRequestMerged(t, fakeGitProvider, prodRepo.Owner, prodRepo.GitRepo.Name, 1) {
		return
	}
	if !cmd.AssertSetPullRequestComplete(t, fakeGitProvider, prodRepo, 1) {
		return
	}

	cmd.PollGitStatusAndReactToPipelineChanges(t, o, jxClient, ns)

	cmd.AssertHasPromoteStatus(t, activities, a.Name, "staging", v1.ActivityStatusTypeSucceeded)
	cmd.AssertHasPromoteStatus(t, activities, a.Name, "production", v1.ActivityStatusTypeSucceeded)

	cmd.AssertAllPromoteStepsSuccessful(t, activities, a.Name)
}

func TestWorkflowManualPromote(t *testing.T) {
	originalJxHome, tempJxHome, err := cmd.CreateTestJxHomeDir()
	assert.NoError(t, err)
	defer func() {
		err := cmd.CleanupTestJxHomeDir(originalJxHome, tempJxHome)
		assert.NoError(t, err)
	}()
	originalKubeCfg, tempKubeCfg, err := cmd.CreateTestKubeConfigDir()
	assert.NoError(t, err)
	defer func() {
		err := cmd.CleanupTestKubeConfigDir(originalKubeCfg, tempKubeCfg)
		assert.NoError(t, err)
	}()

	testOrgName := "jstrachan"
	testRepoName := "manual"
	stagingRepoName := "environment-staging"
	prodRepoName := "environment-production"

	fakeRepo := gits.NewFakeRepository(testOrgName, testRepoName)
	stagingRepo := gits.NewFakeRepository(testOrgName, stagingRepoName)
	prodRepo := gits.NewFakeRepository(testOrgName, prodRepoName)

	fakeGitProvider := gits.NewFakeProvider(fakeRepo, stagingRepo, prodRepo)
	fakeGitProvider.User.Username = testOrgName

	staging := kube.NewPermanentEnvironmentWithGit("staging", "http://fake.git/"+testOrgName+"/"+stagingRepoName+".git")
	production := kube.NewPermanentEnvironmentWithGit("production", "http://fake.git/"+testOrgName+"/"+prodRepoName+".git")
	production.Spec.PromotionStrategy = v1.PromotionStrategyTypeManual

	configureGitFn := func(dir string, gitInfo *gits.GitRepository, gitter gits.Gitter) error {
		err := gitter.Init(dir)
		if err != nil {
			return err
		}
		// Really we should have a dummy environment chart but for now let's just mock it out as needed
		err = os.MkdirAll(filepath.Join(dir, "templates"), 0700)
		if err != nil {
			return err
		}
		data, err := json.Marshal(staging)
		if err != nil {
			return err
		}
		err = ioutil.WriteFile(filepath.Join(dir, "templates", "environment-staging.yaml"), data, 0755)
		if err != nil {
			return err
		}
		data, err = json.Marshal(production)
		if err != nil {
			return err
		}
		err = ioutil.WriteFile(filepath.Join(dir, "templates", "environment-production.yaml"), data, 0755)
		if err != nil {
			return err
		}
		return gitter.AddCommit(dir, "Initial Commit")
	}

	o := &cmd.ControllerWorkflowOptions{
		ControllerOptions: cmd.ControllerOptions{
			CommonOptions: &opts.CommonOptions{},
		},
		NoWatch:        true,
		Namespace:      "jx",
		ConfigureGitFn: configureGitFn,
	}

	workflowName := "default"

	cmd.ConfigureTestOptionsWithResources(o.CommonOptions,
		[]runtime.Object{},
		[]runtime.Object{
			staging,
			production,
			kube.NewPreviewEnvironment("jx-jstrachan-demo96-pr-1"),
			kube.NewPreviewEnvironment("jx-jstrachan-another-pr-3"),
		},
		gits.NewGitLocal(),
		fakeGitProvider,
		helm.NewHelmCLI("helm", helm.V2, "", true),
		resources_test.NewMockInstaller(),
	)

	err = cmd.CreateTestEnvironmentDir(o.CommonOptions)
	assert.NoError(t, err)

	jxClient, ns, err := o.JXClientAndDevNamespace()
	assert.NoError(t, err)

	a, err := cmd.CreateTestPipelineActivity(jxClient, ns, testOrgName, testRepoName, "master", "1", workflowName)
	assert.NoError(t, err)
	if err != nil {
		return
	}
	err = o.Run()
	assert.NoError(t, err)
	if err != nil {
		return
	}
	activities := jxClient.JenkinsV1().PipelineActivities(ns)
	cmd.AssertHasPullRequestForEnv(t, activities, a.Name, "staging")
	cmd.AssertWorkflowStatus(t, activities, a.Name, v1.ActivityStatusTypeRunning)

	// lets make sure we don't create a PR for production as its manual
	cmd.PollGitStatusAndReactToPipelineChanges(t, o, jxClient, ns)
	cmd.AssertHasNoPullRequestForEnv(t, activities, a.Name, "production")

	if !cmd.AssertSetPullRequestMerged(t, fakeGitProvider, stagingRepo.Owner, stagingRepo.GitRepo.Name, 1) {
		return
	}
	if !cmd.AssertSetPullRequestComplete(t, fakeGitProvider, stagingRepo, 1) {
		return
	}

	cmd.PollGitStatusAndReactToPipelineChanges(t, o, jxClient, ns)

	cmd.AssertWorkflowStatus(t, activities, a.Name, v1.ActivityStatusTypeSucceeded)

	cmd.AssertHasNoPullRequestForEnv(t, activities, a.Name, "production")
	cmd.AssertHasPromoteStatus(t, activities, a.Name, "staging", v1.ActivityStatusTypeSucceeded)
	cmd.AssertAllPromoteStepsSuccessful(t, activities, a.Name)

	// now lets do a manual promotion
	version := a.Spec.Version
	po := &cmd.PromoteOptions{
		Application:          testRepoName,
		Environment:          "production",
		Pipeline:             a.Spec.Pipeline,
		Build:                a.Spec.Build,
		Version:              version,
		NoPoll:               true,
		IgnoreLocalFiles:     true,
		HelmRepositoryURL:    helm.InClusterHelmRepositoryURL,
		LocalHelmRepoName:    kube.LocalHelmRepoName,
		Namespace:            "jx",
		ConfigureGitCallback: configureGitFn,
	}
	po.CommonOptions = o.CommonOptions
	po.BatchMode = true
	log.Infof("Promoting to production version %s for app %s\n", version, testRepoName)
	err = po.Run()
	assert.NoError(t, err)
	if err != nil {
		return
	}

	cmd.AssertHasPullRequestForEnv(t, activities, a.Name, "production")
	cmd.AssertWorkflowStatus(t, activities, a.Name, v1.ActivityStatusTypeRunning)
	cmd.AssertHasPipelineStatus(t, activities, a.Name, v1.ActivityStatusTypeRunning)

	cmd.AssertHasPromoteStatus(t, activities, a.Name, "staging", v1.ActivityStatusTypeSucceeded)
	cmd.AssertHasPromoteStatus(t, activities, a.Name, "production", v1.ActivityStatusTypeRunning)

	cmd.PollGitStatusAndReactToPipelineChanges(t, o, jxClient, ns)
	cmd.AssertHasPromoteStatus(t, activities, a.Name, "staging", v1.ActivityStatusTypeSucceeded)
	cmd.AssertHasPromoteStatus(t, activities, a.Name, "production", v1.ActivityStatusTypeRunning)
	cmd.AssertWorkflowStatus(t, activities, a.Name, v1.ActivityStatusTypeSucceeded)
	cmd.AssertHasPipelineStatus(t, activities, a.Name, v1.ActivityStatusTypeSucceeded)

	cmd.PollGitStatusAndReactToPipelineChanges(t, o, jxClient, ns)
	cmd.AssertHasPromoteStatus(t, activities, a.Name, "staging", v1.ActivityStatusTypeSucceeded)
	cmd.AssertHasPromoteStatus(t, activities, a.Name, "production", v1.ActivityStatusTypeRunning)
	cmd.AssertWorkflowStatus(t, activities, a.Name, v1.ActivityStatusTypeSucceeded)
	cmd.AssertHasPipelineStatus(t, activities, a.Name, v1.ActivityStatusTypeSucceeded)

	if !cmd.AssertSetPullRequestMerged(t, fakeGitProvider, prodRepo.Owner, prodRepo.GitRepo.Name, 1) {
		return
	}

	cmd.PollGitStatusAndReactToPipelineChanges(t, o, jxClient, ns)
	cmd.AssertHasPromoteStatus(t, activities, a.Name, "staging", v1.ActivityStatusTypeSucceeded)
	cmd.AssertHasPromoteStatus(t, activities, a.Name, "production", v1.ActivityStatusTypeRunning)
	cmd.AssertWorkflowStatus(t, activities, a.Name, v1.ActivityStatusTypeSucceeded)
	cmd.AssertHasPipelineStatus(t, activities, a.Name, v1.ActivityStatusTypeSucceeded)

	cmd.PollGitStatusAndReactToPipelineChanges(t, o, jxClient, ns)
	cmd.AssertHasPromoteStatus(t, activities, a.Name, "staging", v1.ActivityStatusTypeSucceeded)
	cmd.AssertHasPromoteStatus(t, activities, a.Name, "production", v1.ActivityStatusTypeRunning)
	cmd.AssertWorkflowStatus(t, activities, a.Name, v1.ActivityStatusTypeSucceeded)
	cmd.AssertHasPipelineStatus(t, activities, a.Name, v1.ActivityStatusTypeSucceeded)

	if !cmd.AssertSetPullRequestComplete(t, fakeGitProvider, prodRepo, 1) {
		return
	}

	cmd.PollGitStatusAndReactToPipelineChanges(t, o, jxClient, ns)
	cmd.AssertHasPromoteStatus(t, activities, a.Name, "staging", v1.ActivityStatusTypeSucceeded)
	cmd.AssertHasPromoteStatus(t, activities, a.Name, "production", v1.ActivityStatusTypeSucceeded)
	cmd.AssertAllPromoteStepsSuccessful(t, activities, a.Name)
}

// TestParallelWorkflow lets test promoting to A + B then when A + B is complete then C
func TestParallelWorkflow(t *testing.T) {
	originalJxHome, tempJxHome, err := cmd.CreateTestJxHomeDir()
	assert.NoError(t, err)
	defer func() {
		err := cmd.CleanupTestJxHomeDir(originalJxHome, tempJxHome)
		assert.NoError(t, err)
	}()
	originalKubeCfg, tempKubeCfg, err := cmd.CreateTestKubeConfigDir()
	assert.NoError(t, err)
	defer func() {
		err := cmd.CleanupTestKubeConfigDir(originalKubeCfg, tempKubeCfg)
		assert.NoError(t, err)
	}()

	testOrgName := "jstrachan"
	testRepoName := "parallelrepo"

	envNameA := "a"
	envNameB := "b"
	envNameC := "c"

	envRepoNameA := "environment-" + envNameA
	envRepoNameB := "environment-" + envNameB
	envRepoNameC := "environment-" + envNameC

	fakeRepo := gits.NewFakeRepository(testOrgName, testRepoName)
	repoA := gits.NewFakeRepository(testOrgName, envRepoNameA)
	repoB := gits.NewFakeRepository(testOrgName, envRepoNameB)
	repoC := gits.NewFakeRepository(testOrgName, envRepoNameC)

	fakeGitProvider := gits.NewFakeProvider(fakeRepo, repoA, repoB, repoC)

	envA := kube.NewPermanentEnvironmentWithGit(envNameA, "http://fake.git/"+testOrgName+"/"+envRepoNameA+".git")
	envB := kube.NewPermanentEnvironmentWithGit(envNameB, "http://fake.git/"+testOrgName+"/"+envRepoNameB+".git")
	envC := kube.NewPermanentEnvironmentWithGit(envNameC, "http://fake.git/"+testOrgName+"/"+envRepoNameC+".git")

	configureGitFn := func(dir string, gitInfo *gits.GitRepository, gitter gits.Gitter) error {
		err := gitter.Init(dir)
		if err != nil {
			return err
		}
		// Really we should have a dummy environment chart but for now let's just mock it out as needed
		err = os.MkdirAll(filepath.Join(dir, "templates"), 0700)
		if err != nil {
			return err
		}
		data, err := json.Marshal(envA)
		if err != nil {
			return err
		}
		err = ioutil.WriteFile(filepath.Join(dir, "templates", fmt.Sprintf("%s.yaml", envRepoNameA)), data, 0755)
		if err != nil {
			return err
		}
		data, err = json.Marshal(envB)
		if err != nil {
			return err
		}
		err = ioutil.WriteFile(filepath.Join(dir, "templates", fmt.Sprintf("%s.yaml", envRepoNameB)), data, 0755)
		if err != nil {
			return err
		}
		data, err = json.Marshal(envC)
		if err != nil {
			return err
		}
		err = ioutil.WriteFile(filepath.Join(dir, "templates", fmt.Sprintf("%s.yaml", envRepoNameC)), data, 0755)
		if err != nil {
			return err
		}
		return gitter.AddCommit(dir, "Initial Commit")
	}

	o := &cmd.ControllerWorkflowOptions{
		ControllerOptions: cmd.ControllerOptions{
			CommonOptions: &opts.CommonOptions{},
		},
		NoWatch:        true,
		Namespace:      "jx",
		ConfigureGitFn: configureGitFn,
	}

	myFlowName := "myflow"

	step1 := workflow.CreateWorkflowPromoteStep(envNameA)
	step2 := workflow.CreateWorkflowPromoteStep(envNameB)
	step3 := workflow.CreateWorkflowPromoteStep(envNameC, step1, step2)

	cmd.ConfigureTestOptionsWithResources(o.CommonOptions,
		[]runtime.Object{},
		[]runtime.Object{
			envA,
			envB,
			envC,
			kube.NewPreviewEnvironment("jx-jstrachan-demo96-pr-1"),
			kube.NewPreviewEnvironment("jx-jstrachan-another-pr-3"),
			workflow.CreateWorkflow("jx", myFlowName,
				step1,
				step2,
				step3,
			),
		},
		gits.NewGitLocal(),
		fakeGitProvider,
		helm.NewHelmCLI("helm", helm.V2, "", true),
		resources_test.NewMockInstaller(),
	)
	err = cmd.CreateTestEnvironmentDir(o.CommonOptions)
	assert.NoError(t, err)

	jxClient, ns, err := o.JXClientAndDevNamespace()
	assert.NoError(t, err)
	if err == nil {
		workflow, err := workflow.GetWorkflow("", jxClient, ns)
		assert.NoError(t, err)
		if err == nil {
			assert.Equal(t, "default", workflow.Name, "name")
			spec := workflow.Spec
			assert.Equal(t, 3, len(spec.Steps), "number of steps")
			if len(spec.Steps) > 0 {
				cmd.AssertPromoteStep(t, &spec.Steps[0], envNameA)
			}
			if len(spec.Steps) > 1 {
				cmd.AssertPromoteStep(t, &spec.Steps[1], envNameB)
			}
			if len(spec.Steps) > 2 {
				cmd.AssertPromoteStep(t, &spec.Steps[2], envNameC)
			}
		}
	}

	a, err := cmd.CreateTestPipelineActivity(jxClient, ns, testOrgName, testRepoName, "master", "1", myFlowName)
	assert.NoError(t, err)
	if err != nil {
		return
	}
	err = o.Run()
	assert.NoError(t, err)
	if err != nil {
		return
	}
	activities := jxClient.JenkinsV1().PipelineActivities(ns)
	cmd.AssertHasPullRequestForEnv(t, activities, a.Name, envNameA)
	cmd.AssertHasPullRequestForEnv(t, activities, a.Name, envNameB)
	cmd.AssertWorkflowStatus(t, activities, a.Name, v1.ActivityStatusTypeRunning)

	// lets make sure we don't create a PR for production as we have not completed the staging PR yet
	err = o.Run()
	cmd.AssertHasNoPullRequestForEnv(t, activities, a.Name, envNameC)

	// still no PR merged so cannot create a PR for C until A and B complete
	cmd.PollGitStatusAndReactToPipelineChanges(t, o, jxClient, ns)
	cmd.AssertHasNoPullRequestForEnv(t, activities, a.Name, envNameC)

	// test no PR on production until staging completed
	if !cmd.AssertSetPullRequestMerged(t, fakeGitProvider, repoA.Owner, repoA.GitRepo.Name, 1) {
		return
	}

	cmd.PollGitStatusAndReactToPipelineChanges(t, o, jxClient, ns)
	cmd.AssertHasNoPullRequestForEnv(t, activities, a.Name, envNameC)

	if !cmd.AssertSetPullRequestComplete(t, fakeGitProvider, repoA, 1) {
		return
	}

	// now lets poll again due to change to the activity to detect the staging is complete
	cmd.PollGitStatusAndReactToPipelineChanges(t, o, jxClient, ns)

	cmd.AssertHasNoPullRequestForEnv(t, activities, a.Name, envNameC)
	cmd.AssertHasPromoteStatus(t, activities, a.Name, envNameA, v1.ActivityStatusTypeSucceeded)
	cmd.AssertHasPromoteStatus(t, activities, a.Name, envNameB, v1.ActivityStatusTypeRunning)
	cmd.AssertHasPipelineStatus(t, activities, a.Name, v1.ActivityStatusTypeRunning)

	if !cmd.AssertSetPullRequestMerged(t, fakeGitProvider, repoB.Owner, repoB.GitRepo.Name, 1) {
		return
	}
	if !cmd.AssertSetPullRequestComplete(t, fakeGitProvider, repoB, 1) {
		return
	}

	cmd.PollGitStatusAndReactToPipelineChanges(t, o, jxClient, ns)

	// C should have started now
	cmd.AssertHasPullRequestForEnv(t, activities, a.Name, envNameC)
	cmd.AssertHasPromoteStatus(t, activities, a.Name, envNameA, v1.ActivityStatusTypeSucceeded)
	cmd.AssertHasPromoteStatus(t, activities, a.Name, envNameB, v1.ActivityStatusTypeSucceeded)
	cmd.AssertHasPromoteStatus(t, activities, a.Name, envNameC, v1.ActivityStatusTypeRunning)

	if !cmd.AssertSetPullRequestMerged(t, fakeGitProvider, repoC.Owner, repoC.GitRepo.Name, 1) {
		return
	}
	if !cmd.AssertSetPullRequestComplete(t, fakeGitProvider, repoC, 1) {
		return
	}

	// should be complete now
	cmd.PollGitStatusAndReactToPipelineChanges(t, o, jxClient, ns)

	cmd.AssertHasPromoteStatus(t, activities, a.Name, envNameA, v1.ActivityStatusTypeSucceeded)
	cmd.AssertHasPromoteStatus(t, activities, a.Name, envNameB, v1.ActivityStatusTypeSucceeded)
	cmd.AssertHasPromoteStatus(t, activities, a.Name, envNameC, v1.ActivityStatusTypeSucceeded)

	cmd.AssertAllPromoteStepsSuccessful(t, activities, a.Name)
}

// TestNewVersionWhileExistingWorkflow lets test that we create a new workflow and terminate
// the old workflow if we find a new version
func TestNewVersionWhileExistingWorkflow(t *testing.T) {
	originalJxHome, tempJxHome, err := cmd.CreateTestJxHomeDir()
	assert.NoError(t, err)
	defer func() {
		err := cmd.CleanupTestJxHomeDir(originalJxHome, tempJxHome)
		assert.NoError(t, err)
	}()
	originalKubeCfg, tempKubeCfg, err := cmd.CreateTestKubeConfigDir()
	assert.NoError(t, err)
	defer func() {
		err := cmd.CleanupTestKubeConfigDir(originalKubeCfg, tempKubeCfg)
		assert.NoError(t, err)
	}()

	testOrgName := "jstrachan"
	testRepoName := "myrepo"
	stagingRepoName := "environment-staging"
	prodRepoName := "environment-production"

	fakeRepo := gits.NewFakeRepository(testOrgName, testRepoName)
	stagingRepo := gits.NewFakeRepository(testOrgName, stagingRepoName)
	prodRepo := gits.NewFakeRepository(testOrgName, prodRepoName)

	fakeGitProvider := gits.NewFakeProvider(fakeRepo, stagingRepo, prodRepo)

	staging := kube.NewPermanentEnvironmentWithGit("staging", "http://fake.git/"+testOrgName+"/"+stagingRepoName+".git")
	production := kube.NewPermanentEnvironmentWithGit("production", "http://fake.git/"+testOrgName+"/"+prodRepoName+".git")
	staging.Spec.Order = 100
	production.Spec.Order = 200

	configureGitFn := func(dir string, gitInfo *gits.GitRepository, gitter gits.Gitter) error {
		err := gitter.Init(dir)
		if err != nil {
			return err
		}
		// Really we should have a dummy environment chart but for now let's just mock it out as needed
		err = os.MkdirAll(filepath.Join(dir, "templates"), 0700)
		if err != nil {
			return err
		}
		data, err := json.Marshal(staging)
		if err != nil {
			return err
		}
		err = ioutil.WriteFile(filepath.Join(dir, "templates", "environment-staging.yaml"), data, 0755)
		if err != nil {
			return err
		}
		data, err = json.Marshal(production)
		if err != nil {
			return err
		}
		err = ioutil.WriteFile(filepath.Join(dir, "templates", "environment-production.yaml"), data, 0755)
		if err != nil {
			return err
		}
		return gitter.AddCommit(dir, "Initial Commit")
	}

	o := &cmd.ControllerWorkflowOptions{
		ControllerOptions: cmd.ControllerOptions{
			CommonOptions: &opts.CommonOptions{},
		},
		NoWatch:        true,
		Namespace:      "jx",
		ConfigureGitFn: configureGitFn,
	}

	myFlowName := "myflow"

	step1 := workflow.CreateWorkflowPromoteStep("staging")
	step2 := workflow.CreateWorkflowPromoteStep("production", step1)

	cmd.ConfigureTestOptionsWithResources(o.CommonOptions,
		[]runtime.Object{},
		[]runtime.Object{
			staging,
			production,
			kube.NewPreviewEnvironment("jx-jstrachan-demo96-pr-1"),
			kube.NewPreviewEnvironment("jx-jstrachan-another-pr-3"),
			workflow.CreateWorkflow("jx", myFlowName,
				step1,
				step2,
			),
		},
		gits.NewGitLocal(),
		fakeGitProvider,
		helm.NewHelmCLI("helm", helm.V2, "", true),
		resources_test.NewMockInstaller(),
	)
	err = cmd.CreateTestEnvironmentDir(o.CommonOptions)
	assert.NoError(t, err)

	jxClient, ns, err := o.JXClientAndDevNamespace()
	assert.NoError(t, err)
	if err == nil {
		workflow, err := workflow.GetWorkflow("", jxClient, ns)
		assert.NoError(t, err)
		if err == nil {
			assert.Equal(t, "default", workflow.Name, "name")
			spec := workflow.Spec
			assert.Equal(t, 2, len(spec.Steps), "number of steps")
			if len(spec.Steps) > 0 {
				cmd.AssertPromoteStep(t, &spec.Steps[0], "staging")
			}
			if len(spec.Steps) > 1 {
				cmd.AssertPromoteStep(t, &spec.Steps[1], "production")
			}
		}
	}

	a, err := cmd.CreateTestPipelineActivity(jxClient, ns, testOrgName, testRepoName, "master", "1", myFlowName)
	assert.NoError(t, err)
	if err != nil {
		return
	}
	err = o.Run()
	assert.NoError(t, err)
	if err != nil {
		return
	}
	activities := jxClient.JenkinsV1().PipelineActivities(ns)
	cmd.AssertHasPullRequestForEnv(t, activities, a.Name, "staging")
	cmd.AssertWorkflowStatus(t, activities, a.Name, v1.ActivityStatusTypeRunning)

	// lets trigger a new pipeline release which should close the old version
	aOld := a
	a, err = cmd.CreateTestPipelineActivity(jxClient, ns, testOrgName, testRepoName, "master", "2", myFlowName)

	cmd.PollGitStatusAndReactToPipelineChanges(t, o, jxClient, ns)

	cmd.AssertHasPullRequestForEnv(t, activities, a.Name, "staging")
	cmd.AssertWorkflowStatus(t, activities, a.Name, v1.ActivityStatusTypeRunning)

	// lets make sure we don't create a PR for production as we have not completed the staging PR yet
	cmd.PollGitStatusAndReactToPipelineChanges(t, o, jxClient, ns)
	cmd.AssertHasNoPullRequestForEnv(t, activities, a.Name, "production")

	cmd.AssertWorkflowStatus(t, activities, aOld.Name, v1.ActivityStatusTypeAborted)

	// still no PR merged so cannot create a PR for production
	cmd.PollGitStatusAndReactToPipelineChanges(t, o, jxClient, ns)
	cmd.AssertHasNoPullRequestForEnv(t, activities, a.Name, "production")

	// test no PR on production until staging completed
	if !cmd.AssertSetPullRequestMerged(t, fakeGitProvider, stagingRepo.Owner, stagingRepo.GitRepo.Name, 2) {
		return
	}

	cmd.PollGitStatusAndReactToPipelineChanges(t, o, jxClient, ns)
	cmd.AssertHasNoPullRequestForEnv(t, activities, a.Name, "production")

	if !cmd.AssertSetPullRequestComplete(t, fakeGitProvider, stagingRepo, 2) {
		return
	}

	// now lets poll again due to change to the activity to detect the staging is complete
	cmd.PollGitStatusAndReactToPipelineChanges(t, o, jxClient, ns)

	cmd.AssertHasPromoteStatus(t, activities, a.Name, "staging", v1.ActivityStatusTypeSucceeded)
	cmd.AssertHasPullRequestForEnv(t, activities, a.Name, "production")
	cmd.AssertHasPromoteStatus(t, activities, a.Name, "production", v1.ActivityStatusTypeRunning)
	cmd.AssertHasPipelineStatus(t, activities, a.Name, v1.ActivityStatusTypeRunning)

	if !cmd.AssertSetPullRequestMerged(t, fakeGitProvider, prodRepo.Owner, prodRepo.GitRepo.Name, 1) {
		return
	}
	if !cmd.AssertSetPullRequestComplete(t, fakeGitProvider, prodRepo, 1) {
		return
	}

	cmd.PollGitStatusAndReactToPipelineChanges(t, o, jxClient, ns)

	cmd.AssertHasPromoteStatus(t, activities, a.Name, "staging", v1.ActivityStatusTypeSucceeded)
	cmd.AssertHasPromoteStatus(t, activities, a.Name, "production", v1.ActivityStatusTypeSucceeded)

	cmd.AssertAllPromoteStepsSuccessful(t, activities, a.Name)
}

func TestPullRequestNumber(t *testing.T) {
	failUrls := []string{"http://fake.git/foo/bar/pulls"}
	for _, u := range failUrls {
		_, err := cmd.PullRequestURLToNumber(u)
		assert.Errorf(t, err, "Expected error for pullRequestURLToNumber() with %s", u)
	}

	tests := map[string]int{
		"http://fake.git/foo/bar/pulls/12": 12,
	}

	for u, expected := range tests {
		actual, err := cmd.PullRequestURLToNumber(u)
		assert.NoError(t, err, "pullRequestURLToNumber() should not fail for %s", u)
		if err == nil {
			assert.Equal(t, expected, actual, "pullRequestURLToNumber() for %s", u)
		}
	}
}
