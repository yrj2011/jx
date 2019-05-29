package cmd

import (
	"io/ioutil"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	v1 "github.com/jenkins-x/jx/pkg/apis/jenkins.io/v1"
	"github.com/jenkins-x/jx/pkg/client/clientset/versioned"
	v1fake "github.com/jenkins-x/jx/pkg/client/clientset/versioned/fake"
	typev1 "github.com/jenkins-x/jx/pkg/client/clientset/versioned/typed/jenkins.io/v1"
	"github.com/jenkins-x/jx/pkg/gits"
	"github.com/jenkins-x/jx/pkg/helm"
	"github.com/jenkins-x/jx/pkg/jx/cmd/clients"
	"github.com/jenkins-x/jx/pkg/jx/cmd/opts"
	"github.com/jenkins-x/jx/pkg/kube"
	"github.com/jenkins-x/jx/pkg/kube/resources"
	"github.com/jenkins-x/jx/pkg/log"
	"github.com/jenkins-x/jx/pkg/util"
	kservefake "github.com/knative/serving/pkg/client/clientset/versioned/fake"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	apifake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/ghodss/yaml"
)

// ConfigureTestOptions lets configure the options for use in tests
// using fake APIs to k8s cluster
func ConfigureTestOptions(o *opts.CommonOptions, git gits.Gitter, helm helm.Helmer) {
	ConfigureTestOptionsWithResources(o, nil, nil, git, nil, helm, nil)
}

// ConfigureTestOptions lets configure the options for use in tests
// using fake APIs to k8s cluster.
func ConfigureTestOptionsWithResources(o *opts.CommonOptions, k8sObjects []runtime.Object, jxObjects []runtime.Object,
	git gits.Gitter, fakeGitProvider *gits.FakeProvider, helm helm.Helmer, resourcesInstaller resources.Installer) {
	//o.Out = tests.Output()
	o.BatchMode = true
	factory := o.GetFactory()
	if factory == nil {
		o.SetFactory(clients.NewFactory())
	}
	currentNamespace := "jx"
	o.SetCurrentNamespace(currentNamespace)

	namespacesRequired := []string{currentNamespace}
	namespaceMap := map[string]*corev1.Namespace{}

	for _, ro := range k8sObjects {
		ns, ok := ro.(*corev1.Namespace)
		if ok {
			namespaceMap[ns.Name] = ns
		}
	}
	hasDev := false
	for _, ro := range jxObjects {
		env, ok := ro.(*v1.Environment)
		if ok {
			ns := env.Spec.Namespace
			if ns != "" && util.StringArrayIndex(namespacesRequired, ns) < 0 {
				namespacesRequired = append(namespacesRequired, ns)
			}
			if env.Name == "dev" {
				hasDev = true
			}
		}
	}

	// ensure we've the dev environment
	if !hasDev {
		devEnv := kube.NewPermanentEnvironment("dev")
		devEnv.Spec.Namespace = currentNamespace
		devEnv.Spec.Kind = v1.EnvironmentKindTypeDevelopment

		jxObjects = append(jxObjects, devEnv)
	}

	// add any missing namespaces
	for _, ns := range namespacesRequired {
		if namespaceMap[ns] == nil {
			k8sObjects = append(k8sObjects, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: ns,
					Labels: map[string]string{
						"tag": "",
					},
				},
			})
		}
	}

	client := fake.NewSimpleClientset(k8sObjects...)
	o.SetKubeClient(client)
	o.SetJxClient(v1fake.NewSimpleClientset(jxObjects...))
	o.SetAPIExtensionsClient(apifake.NewSimpleClientset())
	o.SetKnativeServeClient(kservefake.NewSimpleClientset())
	o.SetGit(git)
	if fakeGitProvider != nil {
		o.SetFakeGitProvider(fakeGitProvider)
	}
	o.SetHelm(helm)
	o.SetResourcesInstaller(resourcesInstaller)
}

// CreateTestJxHomeDir creates a temporary JX_HOME directory for the tests, copying over any existing config, returning
// the original JX_HOME directory, the temporary JX_HOME value, and any error.
func CreateTestJxHomeDir() (string, string, error) {
	originalDir, err := util.ConfigDir()
	if err != nil {
		return "", "", err
	}
	exists, err := util.FileExists(path.Join(originalDir, "gitAuth.yaml"))
	if err != nil {
		return "", "", err
	}
	if exists {
		newDir, err := ioutil.TempDir("", ".jx")
		if err != nil {
			return "", "", err
		}
		contents, err := ioutil.ReadDir(originalDir)
		if err != nil {
			return "", "", err
		}
		for _, f := range contents {
			if strings.HasSuffix(f.Name(), ".yaml") {
				err = util.CopyFileOrDir(path.Join(originalDir, f.Name()), path.Join(newDir, f.Name()), true)
				if err != nil {
					return "", "", err
				}
			}
		}
		err = os.Setenv("JX_HOME", newDir)
		if err != nil {
			os.Unsetenv("JX_HOME")
			return "", "", err
		}
		return originalDir, newDir, nil
	}
	return "", "", nil
}

// CleanupTestJxHomeDir should be called in a deferred function whenever CreateTestJxHomeDir is called
func CleanupTestJxHomeDir(originalDir, tempDir string) error {
	os.Unsetenv("JX_HOME")
	if originalDir != "" {
		// Don't delete if it's not a temp dir or if it's the original dir
		if strings.HasPrefix(tempDir, os.TempDir()) && originalDir != tempDir {
			err := os.RemoveAll(tempDir)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// CreateTestKubeConfigDir creates a temporary KUBECONFIG directory for the tests, copying over any existing config, returning
// the original KUBECONFIG value, the temporary KUBECONFIG value, and any error.
func CreateTestKubeConfigDir() (string, string, error) {
	originalFile := util.KubeConfigFile()
	exists, err := util.FileExists(originalFile)
	if err != nil {
		return "", "", err
	}
	if exists {
		newDir, err := ioutil.TempDir("", ".kube")
		if err != nil {
			return "", "", err
		}
		err = util.CopyFile(originalFile, path.Join(newDir, "config"))
		if err != nil {
			return "", "", err
		}
		err = os.Setenv("KUBECONFIG", path.Join(newDir, "config"))
		if err != nil {
			os.Unsetenv("KUBECONFIG")
			return "", "", err
		}
		return originalFile, newDir, nil
	}
	return "", "", nil
}

// CleanupTestKubeConfigDir should be called in a deferred function whenever CreateTestKubeConfigDir is called
func CleanupTestKubeConfigDir(originalFile, tempDir string) error {
	os.Unsetenv("KUBECONFIG")
	if originalFile != "" {
		// Don't delete if it's not a temp dir or if it's the original dir
		if strings.HasPrefix(tempDir, os.TempDir()) && !strings.HasPrefix(originalFile, tempDir) {
			err := os.RemoveAll(tempDir)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

//CreateTestEnvironmentDir will create a temporary environment dir for the tests, copying over any existing config,
// and updating CommonOptions.EnvironmentDir() - this is useful for testing git operations on the environments without
// clobbering the local environments and risking the cluster getting contaminated - use with gits.GitLocal
func CreateTestEnvironmentDir(o *opts.CommonOptions) error {
	// Create a temp dir for environments
	origEnvironmentsDir, err := o.EnvironmentsDir()
	if err != nil {
		return err
	}
	environmentsDir, err := ioutil.TempDir("", "jx-environments")
	if err != nil {
		return err
	}
	o.SetEnvironmentsDir(environmentsDir)
	// Copy over any existing environments
	err = util.CopyDir(origEnvironmentsDir, environmentsDir, true)
	if err != nil {
		return err
	}
	return nil
}

// CleanupTestEnvironmentDir should be called in a deferred function whenever CreateTestEnvironmentDir is called
func CleanupTestEnvironmentDir(o *opts.CommonOptions) error {
	// Let's not accidentally remove the real one!
	environmentsDir, err := o.EnvironmentsDir()
	if err != nil {
		return err
	}
	if strings.HasPrefix(environmentsDir, os.TempDir()) {
		err := os.RemoveAll(environmentsDir)
		if err != nil {
			return err
		}
	}
	return nil
}

func CreateTestPipelineActivity(jxClient versioned.Interface, ns string, folder string, repo string, branch string, build string, workflow string) (*v1.PipelineActivity, error) {
	activities := jxClient.JenkinsV1().PipelineActivities(ns)
	key := &kube.PromoteStepActivityKey{
		PipelineActivityKey: kube.PipelineActivityKey{
			Name:     folder + "-" + repo + "-" + branch + "-" + build,
			Pipeline: folder + "/" + repo + "/" + branch,
			Build:    build,
			GitInfo: &gits.GitRepository{
				Name:         "my-app",
				Organisation: "myorg",
			},
		},
	}
	a, _, err := key.GetOrCreate(jxClient, ns)
	version := "1.0." + build
	a.Spec.GitOwner = folder
	a.Spec.GitRepository = repo
	a.Spec.GitURL = "http://fake.git/" + folder + "/" + repo + ".git"
	a.Spec.Version = version
	a.Spec.Workflow = workflow
	_, err = activities.Update(a)
	return a, err
}

func AssertHasPullRequestForEnv(t *testing.T, activities typev1.PipelineActivityInterface, name string, envName string) {
	activity, err := activities.Get(name, metav1.GetOptions{})
	if err != nil {
		assert.NoError(t, err, "Could not find PipelineActivity %s", name)
		return
	}
	for _, step := range activity.Spec.Steps {
		promote := step.Promote
		if promote != nil {
			if promote.Environment == envName {
				failed := false
				pullRequestStep := promote.PullRequest
				if pullRequestStep == nil {
					assert.Fail(t, "No PullRequest object on PipelineActivity %s for Promote step for Environment %s", name, envName)
					failed = true
				}
				u := pullRequestStep.PullRequestURL
				log.Infof("Found Promote PullRequest %s on PipelineActivity %s for Environment %s\n", u, name, envName)

				if !assert.True(t, u != "", "No PullRequest URL on PipelineActivity %s for Promote step for Environment %s", name, envName) {
					failed = true
				}
				if failed {
					dumpFailedActivity(activity)
				}
				return
			}
		}
	}
	assert.Fail(t, "Missing Promote", "No Promote found on PipelineActivity %s for Environment %s", name, envName)
	dumpFailedActivity(activity)
}

func WaitForPullRequestForEnv(t *testing.T, activities typev1.PipelineActivityInterface, name string, envName string) {
	activity, err := activities.Get(name, metav1.GetOptions{})
	if err != nil {
		assert.NoError(t, err, "Could not find PipelineActivity %s", name)
		return
	}
	waitTime, _ := time.ParseDuration("20s")
	end := time.Now().Add(waitTime)
	for {
		for _, step := range activity.Spec.Steps {
			promote := step.Promote
			if promote != nil {
				if promote.Environment == envName {
					failed := false
					pullRequestStep := promote.PullRequest
					if pullRequestStep == nil {
						failed = true
					}
					u := pullRequestStep.PullRequestURL
					log.Infof("Found Promote PullRequest %s on PipelineActivity %s for Environment %s\n", u, name, envName)

					if !assert.True(t, u != "", "No PullRequest URL on PipelineActivity %s for Promote step for Environment %s", name, envName) {
						failed = true
					}
					if !failed {
						return
					}

				}
			}
		}
		if time.Now().After(end) {
			log.Infof("No Promote PR found on PipelineActivity %s for Environment %s\n", name, envName)
			//assert.Fail(t, "Missing Promote PR", "No Promote PR found on PipelineActivity %s for Environment %s", name, envName)
			//dumpFailedActivity(activity)
			return
		}
		log.Infof("Waiting 1s for PullRequest in Enviroment %s\n", envName)
		v, _ := time.ParseDuration("2s")
		time.Sleep(v)
		activity, _ = activities.Get(name, metav1.GetOptions{})
	}
}

func AssertWorkflowStatus(t *testing.T, activities typev1.PipelineActivityInterface, name string, status v1.ActivityStatusType) {
	activity, err := activities.Get(name, metav1.GetOptions{})
	if err != nil {
		assert.NoError(t, err, "Could not find PipelineActivity %s", name)
		return
	}
	if !assert.Equal(t, string(status), string(activity.Spec.Status), "PipelineActivity status for %s", activity.Name) ||
		!assert.Equal(t, string(status), string(activity.Spec.WorkflowStatus), "PipelineActivity workflow status for %s", activity.Name) {
		dumpFailedActivity(activity)
	}
}

func AssertSetPullRequestComplete(t *testing.T, provider *gits.FakeProvider, repository *gits.FakeRepository, prNumber int) bool {
	fakePR := repository.PullRequests[prNumber]
	if !assert.NotNil(t, fakePR, "No PullRequest found on repository %s for number #%d", repository.String(), prNumber) {
		return false
	}

	l := len(fakePR.Commits)
	if l > 0 {
		fakePR.Commits[l-1].Status = gits.CommitSatusSuccess

		// ensure the commit is on the repo r
		lastCommit := fakePR.Commits[l-1]
		if len(repository.Commits) == 0 {
			repository.Commits = append(repository.Commits, lastCommit)
		} else {
			repository.Commits[len(repository.Commits)-1] = lastCommit
		}
		log.Infof("PR %s has commit status success\n", fakePR.PullRequest.URL)
	}

	// validate the fake Git provider concurs
	repoOwner := repository.Owner
	repoName := repository.Name()
	testGitInfo := &gits.GitRepository{
		Organisation: repoOwner,
		Name:         repoName,
	}
	pr, err := provider.GetPullRequest(repoOwner, testGitInfo, prNumber)
	assert.NoError(t, err, "Finding PullRequest %d", prNumber)
	if !assert.NotNil(t, pr, "Could not find PR %d", prNumber) {
		return false
	}
	if !assert.NotNil(t, pr.MergeCommitSHA, "PR %d has no MergeCommitSHA", prNumber) {
		return false
	}

	statuses, err := provider.ListCommitStatus(repoOwner, repoName, *pr.MergeCommitSHA)
	assert.NoError(t, err, "Finding PullRequest %d commit status", prNumber)
	if assert.True(t, len(statuses) > 0, "PullRequest %d statuses are empty", prNumber) {
		lastStatus := statuses[len(statuses)-1]
		return assert.Equal(t, "success", lastStatus.State, "Last commit status of PullRequest 1 at %s", pr.URL)
	}
	return false
}

func SetSuccessCommitStatusInPR(t *testing.T, repository *gits.FakeRepository, prNumber int) {
	fakePR := repository.PullRequests[prNumber]
	assert.NotNil(t, fakePR, "No PullRequest found on repository %s for number #%d", repository.String(), prNumber)

	l := len(fakePR.Commits)
	if l > 0 {
		fakePR.Commits[l-1].Status = gits.CommitSatusSuccess
	}
}

func AssertHasPromoteStatus(t *testing.T, activities typev1.PipelineActivityInterface, name string, envName string, status v1.ActivityStatusType) {
	activity, err := activities.Get(name, metav1.GetOptions{})
	if err != nil {
		assert.NoError(t, err, "Could not find PipelineActivity %s", name)
		return
	}
	for _, step := range activity.Spec.Steps {
		promote := step.Promote
		if promote != nil {
			if promote.Environment == envName {
				if !assert.Equal(t, string(status), string(promote.Status), "activity status for %s promote %s", name, envName) {
					dumpFailedActivity(activity)
				}
				return
			}
		}
	}
	assert.Fail(t, "Missing Promote", "No Promote found on PipelineActivity %s for Environment %s", name, envName)
	dumpFailedActivity(activity)
}

func AssertHasPipelineStatus(t *testing.T, activities typev1.PipelineActivityInterface, name string, status v1.ActivityStatusType) {
	activity, err := activities.Get(name, metav1.GetOptions{})
	if err != nil {
		assert.NoError(t, err, "Could not find PipelineActivity %s", name)
		return
	}
	if !assert.Equal(t, string(status), string(activity.Spec.Status), "activity status for PipelineActivity %s", name) {
		dumpFailedActivity(activity)
	}
}

func AssertAllPromoteStepsSuccessful(t *testing.T, activities typev1.PipelineActivityInterface, name string) {
	activity, err := activities.Get(name, metav1.GetOptions{})
	if err != nil {
		assert.NoError(t, err, "Could not find PipelineActivity %s", name)
		return
	}
	assert.Equal(t, string(v1.ActivityStatusTypeSucceeded), string(activity.Spec.Status), "PipelineActivity status for %s", activity.Name)
	assert.Equal(t, string(v1.ActivityStatusTypeSucceeded), string(activity.Spec.WorkflowStatus), "PipelineActivity workflow status for %s", activity.Name)
	for _, step := range activity.Spec.Steps {
		promote := step.Promote
		if promote != nil {
			assert.Equal(t, string(v1.ActivityStatusTypeSucceeded), string(promote.Status), "PipelineActivity %s status for Promote to Environment %s", activity.Name, promote.Environment)
		}
	}
}

func AssertHasNoPullRequestForEnv(t *testing.T, activities typev1.PipelineActivityInterface, name string, envName string) {
	activity, err := activities.Get(name, metav1.GetOptions{})
	if err != nil {
		assert.NoError(t, err, "Could not find PipelineActivity %s", name)
		return
	}
	for _, step := range activity.Spec.Steps {
		promote := step.Promote
		if promote != nil {
			if promote.Environment == envName {
				assert.Fail(t, "Should not have a Promote for Environment %s but has %v", envName, promote)
				return
			}
		}
	}
}

func SetPullRequestClosed(pr *gits.FakePullRequest) {
	now := time.Now()
	pr.PullRequest.ClosedAt = &now

	log.Infof("PR %s is now closed\n", pr.PullRequest.URL)
}

// AssertSetPullRequestMerged validates that the fake PR has merged
func AssertSetPullRequestMerged(t *testing.T, provider *gits.FakeProvider, orgName string, repositoryName string,
	prNumber int) bool {
	repos := provider.Repositories[orgName]
	var repository *gits.FakeRepository
	for _, r := range repos {
		if r.Name() == repositoryName {
			repository = r
		}
	}
	assert.NotNil(t, repository)
	fakePR := repository.PullRequests[prNumber]
	if !assert.NotNil(t, fakePR, "No PullRequest found on repository %s for number #%d", repository.String(), prNumber) {
		return false
	}
	commitLen := len(fakePR.Commits)
	if !assert.True(t, commitLen > 0, "PullRequest #%d on repository %s has no commits", prNumber, repository.String()) {
		return false
	}
	lastFakeCommit := fakePR.Commits[commitLen-1].Commit
	if !assert.NotNil(t, lastFakeCommit, "PullRequest #%d on repository %s last commit status has no commits", prNumber, repository.String()) {
		return false
	}
	sha := lastFakeCommit.SHA
	merged := true
	fakePR.PullRequest.MergeCommitSHA = &sha
	fakePR.PullRequest.Merged = &merged

	log.Infof("PR %s is now merged\n", fakePR.PullRequest.URL)

	// validate the fake Git provider concurs
	testGitInfo := &gits.GitRepository{
		Organisation: repository.Owner,
		Name:         repository.Name(),
	}
	pr, err := provider.GetPullRequest(repository.Owner, testGitInfo, prNumber)
	assert.NoError(t, err, "Finding PullRequest %d", prNumber)
	return assert.True(t, pr.Merged != nil && *pr.Merged, "Fake PR %d is merged", prNumber)
}

func AssertPromoteStep(t *testing.T, step *v1.WorkflowStep, expectedEnvironment string) {
	promote := step.Promote
	assert.True(t, promote != nil, "step is a promote step")

	if promote != nil {
		assert.Equal(t, expectedEnvironment, promote.Environment, "environment name")
	}
}

func PollGitStatusAndReactToPipelineChanges(t *testing.T, o *ControllerWorkflowOptions, jxClient versioned.Interface, ns string) error {
	o.ReloadAndPollGitPipelineStatuses(jxClient, ns)
	err := o.Run()
	assert.NoError(t, err, "Failed to react to PipelineActivity changes")
	return err
}

func dumpFailedActivity(activity *v1.PipelineActivity) {
	data, err := yaml.Marshal(activity)
	if err == nil {
		log.Warnf("YAML: %s\n", string(data))
	}
}
