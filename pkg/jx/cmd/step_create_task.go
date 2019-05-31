package cmd

import (
	"context"
	"fmt"
	"github.com/jenkins-x/jx/pkg/jx/cmd/helper"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jenkins-x/jx/pkg/apis/jenkins.io"
	"github.com/jenkins-x/jx/pkg/prow"

	"github.com/ghodss/yaml"
	v1 "github.com/jenkins-x/jx/pkg/apis/jenkins.io/v1"
	"github.com/jenkins-x/jx/pkg/config"
	"github.com/jenkins-x/jx/pkg/gits"
	"github.com/jenkins-x/jx/pkg/jenkinsfile"
	"github.com/jenkins-x/jx/pkg/jenkinsfile/gitresolver"
	"github.com/jenkins-x/jx/pkg/jx/cmd/opts"
	"github.com/jenkins-x/jx/pkg/jx/cmd/templates"
	"github.com/jenkins-x/jx/pkg/kube"
	"github.com/jenkins-x/jx/pkg/log"
	"github.com/jenkins-x/jx/pkg/tekton"
	"github.com/jenkins-x/jx/pkg/tekton/syntax"
	"github.com/jenkins-x/jx/pkg/util"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	pipelineapi "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	kanikoDockerImage     = "gcr.io/kaniko-project/executor:9912ccbf8d22bbafbf971124600fbb0b13b9cbd6"
	kanikoSecretMount     = "/kaniko-secret/secret.json"
	kanikoSecretName      = "kaniko-secret"
	kanikoSecretKey       = "kaniko-secret"
	defaultContainerImage = "gcr.io/jenkinsxio/builder-maven"
)

var (
	createTaskLong = templates.LongDesc(`
		Creates a Tekton Pipeline Run for a project
`)

	createTaskExample = templates.Examples(`
		# create a Tekton Pipeline Run and render to the console
		jx step create task

		# create a Tekton Pipeline Task
		jx step create task -o mytask.yaml

		# view the steps that would be created
		jx step create task --view

			`)

	ipAddressRegistryRegex = regexp.MustCompile(`\d+\.\d+\.\d+\.\d+.\d+(:\d+)?`)
)

// StepCreateTaskOptions contains the command line flags
type StepCreateTaskOptions struct {
	StepOptions

	Pack              string
	Dir               string
	BuildPackURL      string
	BuildPackRef      string
	PipelineKind      string
	Context           string
	CustomLabels      []string
	CustomEnvs        []string
	NoApply           bool
	DryRun            bool
	Trigger           string
	TargetPath        string
	SourceName        string
	CustomImage       string
	DefaultImage      string
	CloneGitURL       string
	Branch            string
	Revision          string
	PullRequestNumber string
	DeleteTempDir     bool
	ViewSteps         bool
	NoReleasePrepare  bool
	Duration          time.Duration
	FromRepo          bool
	NoKaniko          bool
	KanikoImage       string
	KanikoSecretMount string
	KanikoSecret      string
	KanikoSecretKey   string
	ProjectID         string
	DockerRegistry    string
	DockerRegistryOrg string

	PodTemplates        map[string]*corev1.Pod
	MissingPodTemplates map[string]bool

	stepCounter          int
	GitInfo              *gits.GitRepository
	BuildNumber          string
	labels               map[string]string
	envVars              []corev1.EnvVar
	Results              StepCreateTaskResults
	version              string
	previewVersionPrefix string
	VersionResolver      *opts.VersionResolver
}

// StepCreateTaskResults stores the generated results
type StepCreateTaskResults struct {
	Pipeline       *pipelineapi.Pipeline
	Tasks          []*pipelineapi.Task
	Resources      []*pipelineapi.PipelineResource
	PipelineRun    *pipelineapi.PipelineRun
	Structure      *v1.PipelineStructure
	PipelineParams []pipelineapi.Param
}

// NewCmdStepCreateTask Creates a new Command object
func NewCmdStepCreateTask(commonOpts *opts.CommonOptions) *cobra.Command {
	options := &StepCreateTaskOptions{
		StepOptions: StepOptions{
			CommonOptions: commonOpts,
		},
	}

	cmd := &cobra.Command{
		Use:     "task",
		Short:   "Creates a Tekton Pipeline Run for the current folder or given build pack",
		Long:    createTaskLong,
		Example: createTaskExample,
		Aliases: []string{"bt"},
		Run: func(cmd *cobra.Command, args []string) {
			options.Cmd = cmd
			options.Args = args
			err := options.Run()
			helper.CheckErr(err)
		},
	}

	cmd.Flags().StringVarP(&options.Dir, "dir", "d", "", "The directory to query to find the projects .git directory")
	cmd.Flags().StringVarP(&options.OutDir, "output", "o", "out", "The directory to write the output to as YAML. Defaults to 'out'")
	cmd.Flags().StringVarP(&options.Branch, "branch", "", "", "The git branch to trigger the build in. Defaults to the current local branch name")
	cmd.Flags().StringVarP(&options.Revision, "revision", "", "", "The git revision to checkout, can be a branch name or git sha")
	cmd.Flags().StringVarP(&options.PipelineKind, "kind", "k", "release", "The kind of pipeline to create such as: "+strings.Join(jenkinsfile.PipelineKinds, ", "))
	cmd.Flags().StringArrayVarP(&options.CustomLabels, "label", "l", nil, "List of custom labels to be applied to resources that are created")
	cmd.Flags().StringArrayVarP(&options.CustomEnvs, "env", "e", nil, "List of custom environment variables to be applied to resources that are created")
	cmd.Flags().StringVarP(&options.Trigger, "trigger", "t", string(pipelineapi.PipelineTriggerTypeManual), "The kind of pipeline trigger")
	cmd.Flags().StringVarP(&options.CloneGitURL, "clone-git-url", "", "", "Specify the git URL to clone to a temporary directory to get the source code")
	cmd.Flags().StringVarP(&options.PullRequestNumber, "pr-number", "", "", "If a Pull Request this is it's number")
	cmd.Flags().BoolVarP(&options.NoApply, "no-apply", "", false, "Disables creating the Pipeline resources in the kubernetes cluster and just outputs the generated Task to the console or output file")
	cmd.Flags().BoolVarP(&options.DryRun, "dry-run", "", false, "Disables creating the Pipeline resources in the kubernetes cluster and just outputs the generated Task to the console or output file, without side effects")
	cmd.Flags().BoolVarP(&options.ViewSteps, "view", "", false, "Just view the steps that would be created")

	options.AddCommonFlags(cmd)
	return cmd
}

// AddCommonFlags adds common CLI options
func (o *StepCreateTaskOptions) AddCommonFlags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&o.Pack, "pack", "p", "", "The build pack name. If none is specified its discovered from the source code")
	cmd.Flags().StringVarP(&o.BuildPackURL, "url", "u", "", "The URL for the build pack Git repository")
	cmd.Flags().StringVarP(&o.BuildPackRef, "ref", "r", "", "The Git reference (branch,tag,sha) in the Git repository to use")
	cmd.Flags().StringVarP(&o.Context, "context", "c", "", "The pipeline context if there are multiple separate pipelines for a given branch")
	cmd.Flags().StringVarP(&o.ServiceAccount, "service-account", "", "tekton-bot", "The Kubernetes ServiceAccount to use to run the pipeline")
	cmd.Flags().StringVarP(&o.TargetPath, "target-path", "", "", "The target path appended to /workspace/${source} to clone the source code")
	cmd.Flags().StringVarP(&o.SourceName, "source", "", "source", "The name of the source repository")
	cmd.Flags().StringVarP(&o.CustomImage, "image", "", "", "Specify a custom image to use for the steps which overrides the image in the PodTemplates")
	cmd.Flags().StringVarP(&o.DefaultImage, "default-image", "", defaultContainerImage, "Specify the docker image to use if there is no image specified for a step and there's no Pod Template")
	cmd.Flags().BoolVarP(&o.DeleteTempDir, "delete-temp-dir", "", true, "Deletes the temporary directory of cloned files if using the 'clone-git-url' option")
	cmd.Flags().BoolVarP(&o.NoReleasePrepare, "no-release-prepare", "", false, "Disables creating the release version number and tagging git and triggering the release pipeline from the new tag")
	cmd.Flags().BoolVarP(&o.NoKaniko, "no-kaniko", "", false, "Disables using kaniko directly for building docker images")
	cmd.Flags().StringVarP(&o.KanikoImage, "kaniko-image", "", kanikoDockerImage, "The docker image for Kaniko")
	cmd.Flags().StringVarP(&o.KanikoSecretMount, "kaniko-secret-mount", "", kanikoSecretMount, "The mount point of the Kaniko secret")
	cmd.Flags().StringVarP(&o.KanikoSecret, "kaniko-secret", "", kanikoSecretName, "The name of the kaniko secret")
	cmd.Flags().StringVarP(&o.KanikoSecretKey, "kaniko-secret-key", "", kanikoSecretKey, "The key in the Kaniko Secret to mount")
	cmd.Flags().StringVarP(&o.ProjectID, "project-id", "", "", "The cloud project ID. If not specified we default to the install project")
	cmd.Flags().StringVarP(&o.DockerRegistry, "docker-registry", "", "", "The Docker Registry host name to use which is added as a prefix to docker images")
	cmd.Flags().StringVarP(&o.DockerRegistryOrg, "docker-registry-org", "", "", "The Docker registry organisation. If blank the git repository owner is used")
	cmd.Flags().DurationVarP(&o.Duration, "duration", "", time.Second*30, "Retry duration when trying to create a PipelineRun")
}

// Run implements this command
func (o *StepCreateTaskOptions) Run() error {
	settings, err := o.TeamSettings()
	if err != nil {
		return err
	}
	kubeClient, ns, err := o.KubeClientAndDevNamespace()
	if err != nil {
		return err
	}

	if o.ProjectID == "" {
		if !o.RemoteCluster {
			data, err := kube.ReadInstallValues(kubeClient, ns)
			if err != nil {
				return errors.Wrapf(err, "failed to read install values from namespace %s", ns)
			}
			o.ProjectID = data["projectID"]
		}
		if o.ProjectID == "" {
			o.ProjectID = "todo"
		}
	}
	if o.DefaultImage == "" {
		o.DefaultImage = defaultContainerImage
	}
	if o.KanikoImage == "" {
		o.KanikoImage = kanikoDockerImage
	}
	if o.KanikoSecretMount == "" {
		o.KanikoSecretMount = kanikoSecretMount
	}

	log.Infof("my jx log:cloning git for %s\n", o.CloneGitURL)
	log.Infof("my jx log:cloning git for %s\n", o.CloneGitURL)
	o.CloneGitURL = strings.Replace(o.CloneGitURL, "http://192.168.1.167", "http://192.168.1.228:1080", 1)
	log.Infof("my jx log:cloning git for %s\n", o.CloneGitURL)
	if o.Verbose {
		log.Infof("cloning git for %s\n", o.CloneGitURL)
	}
	if o.VersionResolver == nil {
		o.VersionResolver, err = o.CreateVersionResolver("", "")
		if err != nil {
			return err
		}
	}
	if o.CloneGitURL != "" {
		err = o.Retry(3, time.Second*2, func() error {
			err := o.cloneGitRepositoryToTempDir(o.CloneGitURL)
			if err != nil {
				o.deleteTempDir()
				return err
			}
			return nil
		})
		// if we have failed to clone three times it's likely things wont recover so lets kill the process and let
		// kubernetes reschedule a new pod
		if err != nil {
			log.Fatalf("failed to clone three times it's likely things wont recover so lets kill the process %v", err)
			panic(err)
		}

		var pr *prow.PullRefs

		for _, envVar := range o.CustomEnvs {
			parts := strings.Split(envVar, "=")
			if parts[0] == "PULL_REFS" {
				pr, err = prow.ParsePullRefs(parts[1])
				if err != nil {
					return err
				}
			}
		}

		if pr != nil {
			var shas []string
			for _, sha := range pr.ToMerge {
				shas = append(shas, sha)
			}

			mergeOpts := StepGitMergeOptions{
				StepOptions: StepOptions{
					CommonOptions: o.CommonOptions,
				},
				Dir:        o.Dir,
				BaseSHA:    pr.BaseSha,
				SHAs:       shas,
				BaseBranch: pr.BaseBranch,
			}
			mergeOpts.Verbose = true
			err = mergeOpts.Run()
			if err != nil {
				return errors.Wrapf(err, "failed to merge git shas %s with base sha %s", shas, pr.BaseSha)
			}
		}

		if o.DeleteTempDir {
			defer o.deleteTempDir()
		}
	}

	if o.Verbose {
		log.Infof("setting up docker registry for %s\n", o.CloneGitURL)
	}

	if o.DockerRegistry == "" {
		data, err := kube.GetConfigMapData(kubeClient, kube.ConfigMapJenkinsDockerRegistry, ns)
		if err != nil {
			return fmt.Errorf("Could not find ConfigMap %s in namespace %s: %s", kube.ConfigMapJenkinsDockerRegistry, ns, err)
		}
		o.DockerRegistry = data["docker.registry"]
		if o.DockerRegistry == "" {
			return util.MissingOption("docker-registry")
		}
	}

	if o.Dir == "" {
		o.Dir, err = os.Getwd()
		if err != nil {
			return err
		}
	}

	o.GitInfo, err = o.FindGitInfo(o.Dir)
	if err != nil {
		return errors.Wrapf(err, "failed to find git information from dir %s", o.Dir)
	}
	if o.Branch == "" {
		o.Branch, err = o.Git().Branch(o.Dir)
		if err != nil {
			return errors.Wrapf(err, "failed to find git branch from dir %s", o.Dir)
		}
	}

	if o.NoApply || o.DryRun {
		o.BuildNumber = "1"
	} else {
		jxClient, _, err := o.JXClient()
		if err != nil {
			return err
		}
		tektonClient, _, err := o.TektonClient()
		if err != nil {
			return err
		}

		if o.Verbose {
			log.Infof("generating build number...\n")
		}

		pipelineResourceName := tekton.PipelineResourceName(o.GitInfo, o.Branch, o.Context)

		o.BuildNumber, err = tekton.GenerateNextBuildNumber(tektonClient, jxClient, ns, o.GitInfo, o.Branch, o.Duration, pipelineResourceName)
		if err != nil {
			return err
		}
		if o.Verbose {
			log.Infof("generated build number %s for %s\n", o.BuildNumber, o.CloneGitURL)
		}
	}
	projectConfig, projectConfigFile, err := o.loadProjectConfig()
	if err != nil {
		return errors.Wrapf(err, "failed to load project config in dir %s", o.Dir)
	}
	if o.BuildPackURL == "" || o.BuildPackRef == "" {
		if projectConfig.BuildPackGitURL != "" {
			o.BuildPackURL = projectConfig.BuildPackGitURL
		} else if o.BuildPackURL == "" {
			o.BuildPackURL = settings.BuildPackURL
		}
		if projectConfig.BuildPackGitURef != "" {
			o.BuildPackRef = projectConfig.BuildPackGitURef
		} else if o.BuildPackRef == "" {
			o.BuildPackRef = settings.BuildPackRef
		}
	}
	if o.BuildPackURL == "" {
		return util.MissingOption("url")
	}
	if o.BuildPackRef == "" {
		return util.MissingOption("ref")
	}
	if o.PipelineKind == "" {
		return util.MissingOption("kind")
	}

	if o.Pack == "" {
		o.Pack = projectConfig.BuildPack
	}
	if o.Pack == "" {
		o.Pack, err = o.DiscoverBuildPack(o.Dir, projectConfig, o.Pack)
		if err != nil {
			return errors.Wrapf(err, "failed to discover the build pack")
		}
	}

	if o.Pack == "" {
		return util.MissingOption("pack")
	}

	o.PodTemplates, err = kube.LoadPodTemplates(kubeClient, ns)
	if err != nil {
		return err
	}
	o.MissingPodTemplates = map[string]bool{}

	packsDir, err := gitresolver.InitBuildPack(o.Git(), o.BuildPackURL, o.BuildPackRef)
	if err != nil {
		return err
	}

	resolver, err := gitresolver.CreateResolver(packsDir, o.Git())
	if err != nil {
		return err
	}

	if o.Verbose {
		log.Infof("about to create the tekton CRDs\n")
	}
	pipeline, tasks, resources, run, structure, err := o.GenerateTektonCRDs(packsDir, projectConfig, projectConfigFile, resolver, ns)
	if err != nil {
		return errors.Wrap(err, "failed to generate Tekton CRD")
	}

	if o.ViewSteps {
		o.viewSteps(tasks...)
		return nil
	}

	if o.Verbose {
		log.Infof("created tekton CRDs for %s\n", run.Name)
	}

	activityKey := o.GeneratePipelineActivity(ns)

	if o.Verbose {
		log.Infof(" PipelineActivity for %s created successfully", pipeline.Name)
	}

	//output results for invokers of this command like the pipelinerunner
	o.Results.Pipeline = pipeline
	o.Results.Tasks = tasks
	o.Results.Resources = resources
	o.Results.PipelineRun = run
	o.Results.Structure = structure

	if o.NoApply || o.DryRun {
		log.Infof("Writing output ")
		err := o.writeOutput(o.OutDir, pipeline, tasks, run, resources, structure, activityKey)
		if err != nil {
			return errors.Wrapf(err, "Failed to output Tekton CRDs")
		}
	} else {
		log.Infof("Applying changes ")
		err := o.applyPipeline(pipeline, tasks, resources, structure, run, o.GitInfo, o.Branch, activityKey)
		if err != nil {
			return errors.Wrapf(err, "failed to apply Tekton CRDs")
		}
		// only include labels on PipelineRuns because they're unique, Task and Pipeline are static resources so we'd overwrite existing labels if applied to them too
		run.Labels = util.MergeMaps(run.Labels, o.labels)

		if o.Verbose {
			log.Infof(" for %s\n", run.Name)
		}
	}
	return nil
}

// GeneratePipelineActivity generates a initial PipelineActivity CRD so UI/get act can get an earlier notification that the jobs have been scheduled
func (o *StepCreateTaskOptions) GeneratePipelineActivity(ns string) *kube.PromoteStepActivityKey {

	build := o.BuildNumber
	org := o.GitInfo.Organisation
	repo := o.GitInfo.Name
	branch := o.Branch

	name := org + "-" + repo + "-" + branch + "-" + build
	pipeline := org + "/" + repo + "/" + branch
	log.Infof("PipelineActivity for %s", name)
	return &kube.PromoteStepActivityKey{
		PipelineActivityKey: kube.PipelineActivityKey{
			Name:     name,
			Pipeline: pipeline,
			Build:    build,
			GitInfo:  o.GitInfo,
		},
	}
}

// GenerateTektonCRDs creates the Pipeline, Task, PipelineResource, PipelineRun, and PipelineStructure CRDs that will be applied to actually kick off the pipeline
func (o *StepCreateTaskOptions) GenerateTektonCRDs(packsDir string, projectConfig *config.ProjectConfig, projectConfigFile string, resolver jenkinsfile.ImportFileResolver, ns string) (*pipelineapi.Pipeline, []*pipelineapi.Task, []*pipelineapi.PipelineResource, *pipelineapi.PipelineRun, *v1.PipelineStructure, error) {
	name := o.Pack
	packDir := filepath.Join(packsDir, name)

	ctx := context.Background()
	pipelineConfig := projectConfig.PipelineConfig
	if name != "none" {
		pipelineFile := filepath.Join(packDir, jenkinsfile.PipelineConfigFileName)
		exists, err := util.FileExists(pipelineFile)
		if err != nil {
			return nil, nil, nil, nil, nil, errors.Wrapf(err, "failed to find build pack pipeline YAML: %s", pipelineFile)
		}
		if !exists {
			return nil, nil, nil, nil, nil, fmt.Errorf("no build pack for %s exists at directory %s", name, packDir)
		}
		pipelineConfig, err = jenkinsfile.LoadPipelineConfig(pipelineFile, resolver, true, false)
		if err != nil {
			return nil, nil, nil, nil, nil, errors.Wrapf(err, "failed to load build pack pipeline YAML: %s", pipelineFile)
		}
		localPipelineConfig := projectConfig.PipelineConfig
		if localPipelineConfig != nil {
			err = localPipelineConfig.ExtendPipeline(pipelineConfig, false)
			if err != nil {
				return nil, nil, nil, nil, nil, errors.Wrapf(err, "failed to override PipelineConfig using configuration in file %s", projectConfigFile)
			}
			pipelineConfig = localPipelineConfig
		}
	}

	if pipelineConfig == nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("failed to find PipelineConfig in file %s", projectConfigFile)
	}

	err := o.combineEnvVars(pipelineConfig)
	if err != nil {
		return nil, nil, nil, nil, nil, errors.Wrapf(err, "failed to combine env vars")
	}

	// lets allow a `jenkins-x.yml` to specify we want to disable release prepare mode which can be useful for
	// working with custom jenkins pipelines in custom jenkins servers
	if projectConfig.NoReleasePrepare {
		o.NoReleasePrepare = true
	}
	err = o.setVersionOnReleasePipelines(pipelineConfig)
	if err != nil {
		return nil, nil, nil, nil, nil, errors.Wrapf(err, "failed to set the version on release pipelines")
	}

	var lifecycles *jenkinsfile.PipelineLifecycles
	kind := o.PipelineKind
	pipelines := pipelineConfig.Pipelines
	switch kind {
	case jenkinsfile.PipelineKindRelease:
		lifecycles = pipelines.Release

		// lets add a pre-step to setup the credentials
		if lifecycles.Setup == nil {
			lifecycles.Setup = &jenkinsfile.PipelineLifecycle{}
		}
		steps := []*syntax.Step{
			{
				Command: "jx step git credentials",
				Name:    "jx-git-credentials",
			},
		}
		lifecycles.Setup.Steps = append(steps, lifecycles.Setup.Steps...)

	case jenkinsfile.PipelineKindPullRequest:
		lifecycles = pipelines.PullRequest
	case jenkinsfile.PipelineKindFeature:
		lifecycles = pipelines.Feature
	default:
		return nil, nil, nil, nil, nil, fmt.Errorf("Unknown pipeline kind %s. Supported values are %s", kind, strings.Join(jenkinsfile.PipelineKinds, ", "))
	}

	var tasks []*pipelineapi.Task
	var pipeline *pipelineapi.Pipeline
	var run *pipelineapi.PipelineRun
	var resources []*pipelineapi.PipelineResource
	var structure *v1.PipelineStructure

	pipelineResourceName := tekton.PipelineResourceName(o.GitInfo, o.Branch, o.Context)

	err = o.setBuildValues()
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	var parsed *syntax.ParsedPipeline

	if lifecycles != nil && lifecycles.Pipeline != nil {
		parsed = lifecycles.Pipeline
	} else {
		stage, err := o.CreateStageForBuildPack(name, projectConfig, pipelineConfig, lifecycles, kind, ns)
		if err != nil {
			return nil, nil, nil, nil, nil, errors.Wrapf(err, "Failed to generate stage from build pack")
		}

		parsed = &syntax.ParsedPipeline{
			Stages: []syntax.Stage{*stage},
		}

		// If agent.container is specified, use that for default container configuration for step images.
		containerName := pipelineConfig.Agent.GetImage()
		if containerName != "" {
			if o.PodTemplates != nil && o.PodTemplates[containerName] != nil {
				podTemplate := o.PodTemplates[containerName]
				container := podTemplate.Spec.Containers[0]
				if !equality.Semantic.DeepEqual(container, corev1.Container{}) {
					container.Name = ""
					container.Command = []string{}
					container.Args = []string{}
					container.Image = ""
					container.WorkingDir = ""
					container.Stdin = false
					container.TTY = false
					parsed.Options.ContainerOptions = &container
				}
			}
		}
	}

	parsed.AddContainerEnvVarsToPipeline(pipelineConfig.Env)

	if pipelineConfig.ContainerOptions != nil {
		mergedContainer, err := syntax.MergeContainers(pipelineConfig.ContainerOptions, parsed.Options.ContainerOptions)
		if err != nil {
			return nil, nil, nil, nil, nil, errors.Wrapf(err, "Could not merge containerOptions from parent")
		}
		parsed.Options.ContainerOptions = mergedContainer
	}

	// TODO: Seeing weird behavior seemingly related to http://golang.org/doc/faq#nil_error
	// if err is reused, maybe we need to switch return types (perhaps upstream in build-pipeline)?
	if validateErr := parsed.Validate(ctx); validateErr != nil {
		return nil, nil, nil, nil, nil, errors.Wrapf(validateErr, "Validation failed for Pipeline")
	}

	pipeline, tasks, structure, err = parsed.GenerateCRDs(pipelineResourceName, o.BuildNumber, ns, o.PodTemplates, o.GetDefaultTaskInputs().Params, o.SourceName)
	if err != nil {
		return nil, nil, nil, nil, nil, errors.Wrapf(err, "Generation failed for Pipeline")
	}

	resources = append(resources, o.generateSourceRepoResource(pipelineResourceName))
	tasks, pipeline = o.EnhanceTasksAndPipeline(tasks, pipeline, pipelineConfig)
	run = o.CreatePipelineRun(pipeline, resources)

	if validateErr := pipeline.Spec.Validate(ctx); validateErr != nil {
		return nil, nil, nil, nil, nil, errors.Wrapf(validateErr, "Validation failed for generated Pipeline")
	}
	for _, task := range tasks {
		if validateErr := task.Spec.Validate(ctx); validateErr != nil {
			data, _ := yaml.Marshal(task)
			return nil, nil, nil, nil, nil, errors.Wrapf(validateErr, "Validation failed for generated Task: %s %s", task.Name, string(data))
		}
	}
	if validateErr := run.Spec.Validate(ctx); validateErr != nil {
		return nil, nil, nil, nil, nil, errors.Wrapf(validateErr, "Validation failed for generated PipelineRun")
	}

	return pipeline, tasks, resources, run, structure, nil
}

func (o *StepCreateTaskOptions) loadProjectConfig() (*config.ProjectConfig, string, error) {
	if o.Context != "" {
		fileName := filepath.Join(o.Dir, fmt.Sprintf("jenkins-x-%s.yml", o.Context))
		exists, err := util.FileExists(fileName)
		if err != nil {
			return nil, fileName, errors.Wrapf(err, "failed to check if file exists %s", fileName)
		}
		if exists {
			config, err := config.LoadProjectConfigFile(fileName)
			return config, fileName, err
		}
	}
	return config.LoadProjectConfig(o.Dir)
}

// CreateStageForBuildPack generates the Task for a build pack
func (o *StepCreateTaskOptions) CreateStageForBuildPack(languageName string, projectConfig *config.ProjectConfig, pipelineConfig *jenkinsfile.PipelineConfig, lifecycles *jenkinsfile.PipelineLifecycles, templateKind, ns string) (*syntax.Stage, error) {
	if lifecycles == nil {
		return nil, errors.New("generatePipeline: no lifecycles")
	}

	// lets generate the pipeline using the build packs
	container := pipelineConfig.Agent.GetImage()
	if o.CustomImage != "" {
		container = o.CustomImage
	}
	if container == "" {
		container = o.DefaultImage
	}
	dir := o.getWorkspaceDir()

	steps := []syntax.Step{}
	for _, n := range lifecycles.All() {
		l := n.Lifecycle
		if l == nil {
			continue
		}
		if !o.NoReleasePrepare && n.Name == "setversion" {
			continue
		}

		for _, s := range l.Steps {
			steps = append(steps, o.createSteps(languageName, projectConfig, pipelineConfig, templateKind, s, container, dir, n.Name)...)
		}
	}

	stage := &syntax.Stage{
		Name: syntax.DefaultStageNameForBuildPack,
		Agent: syntax.Agent{
			Image: container,
		},
		Steps: steps,
	}

	return stage, nil
}

// GetDefaultTaskInputs gets the base, built-in task parameters as an Input.
func (o *StepCreateTaskOptions) GetDefaultTaskInputs() *pipelineapi.Inputs {
	inputs := &pipelineapi.Inputs{}
	taskParams := o.createTaskParams()
	if len(taskParams) > 0 {
		inputs.Params = taskParams
	}
	return inputs
}

func (o *StepCreateTaskOptions) enhanceTaskWithVolumesEnvAndInputs(task *pipelineapi.Task, pipelineConfig *jenkinsfile.PipelineConfig, inputs pipelineapi.Inputs) {
	volumes := task.Spec.Volumes
	for i, step := range task.Spec.Steps {
		volumes = o.modifyVolumes(&step, volumes)
		o.modifyEnvVars(&step, pipelineConfig.Env)
		task.Spec.Steps[i] = step
	}

	task.Spec.Volumes = volumes
	if task.Spec.Inputs == nil {
		task.Spec.Inputs = &inputs
	} else {
		task.Spec.Inputs.Params = inputs.Params
	}
}

// EnhanceTasksAndPipeline takes a slice of Tasks and a Pipeline and modifies them to include built-in volumes, environment variables, and parameters
func (o *StepCreateTaskOptions) EnhanceTasksAndPipeline(tasks []*pipelineapi.Task, pipeline *pipelineapi.Pipeline, pipelineConfig *jenkinsfile.PipelineConfig) ([]*pipelineapi.Task, *pipelineapi.Pipeline) {
	taskInputs := o.GetDefaultTaskInputs()

	for _, t := range tasks {
		o.enhanceTaskWithVolumesEnvAndInputs(t, pipelineConfig, *taskInputs)
	}

	taskParams := o.createPipelineTaskParams()

	for i, pt := range pipeline.Spec.Tasks {
		for _, tp := range taskParams {
			if !hasPipelineParam(pt.Params, tp.Name) {
				pt.Params = append(pt.Params, tp)
				pipeline.Spec.Tasks[i] = pt
			}
		}
	}

	pipeline.Spec.Params = o.createPipelineParams()

	if pipeline.APIVersion == "" {
		pipeline.APIVersion = syntax.TektonAPIVersion
	}
	if pipeline.Kind == "" {
		pipeline.Kind = "Pipeline"
	}

	return tasks, pipeline
}

// CreatePipelineRun creates a PipelineRun for a given Pipeline
func (o *StepCreateTaskOptions) CreatePipelineRun(pipeline *pipelineapi.Pipeline, resources []*pipelineapi.PipelineResource) *pipelineapi.PipelineRun {
	var resourceBindings []pipelineapi.PipelineResourceBinding
	for _, resource := range resources {
		resourceBindings = append(resourceBindings, pipelineapi.PipelineResourceBinding{
			Name: resource.Name,
			ResourceRef: pipelineapi.PipelineResourceRef{
				Name:       resource.Name,
				APIVersion: resource.APIVersion,
			},
		})
	}

	return &pipelineapi.PipelineRun{
		TypeMeta: metav1.TypeMeta{
			APIVersion: syntax.TektonAPIVersion,
			Kind:       "PipelineRun",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:   pipeline.Name,
			Labels: util.MergeMaps(o.labels),
		},
		Spec: pipelineapi.PipelineRunSpec{
			ServiceAccount: o.ServiceAccount,
			Trigger: pipelineapi.PipelineTrigger{
				Type: pipelineapi.PipelineTriggerType(o.Trigger),
			},
			PipelineRef: pipelineapi.PipelineRef{
				Name:       pipeline.Name,
				APIVersion: pipeline.APIVersion,
			},
			Resources: resourceBindings,
			Params:    o.Results.PipelineParams,
		},
	}
}

func (o *StepCreateTaskOptions) createTaskParams() []pipelineapi.TaskParam {
	taskParams := []pipelineapi.TaskParam{}
	for _, param := range o.Results.PipelineParams {
		name := param.Name
		description := ""
		defaultValue := ""
		switch name {
		case "version":
			description = "the version number for this pipeline which is used as a tag on docker images and helm charts"
			defaultValue = o.version
		case "build_id":
			description = "the PipelineRun build number"
			defaultValue = o.BuildNumber
		}
		taskParams = append(taskParams, pipelineapi.TaskParam{
			Name:        name,
			Description: description,
			Default:     defaultValue,
		})
	}
	return taskParams
}

func (o *StepCreateTaskOptions) createPipelineParams() []pipelineapi.PipelineParam {
	answer := []pipelineapi.PipelineParam{}
	taskParams := o.createTaskParams()
	for _, tp := range taskParams {
		answer = append(answer, pipelineapi.PipelineParam{
			Name:        tp.Name,
			Description: tp.Description,
			Default:     tp.Default,
		})
	}
	return answer
}

func (o *StepCreateTaskOptions) createPipelineTaskParams() []pipelineapi.Param {
	ptp := []pipelineapi.Param{}
	for _, p := range o.Results.PipelineParams {
		ptp = append(ptp, pipelineapi.Param{
			Name:  p.Name,
			Value: fmt.Sprintf("${params.%s}", p.Name),
		})
	}
	return ptp
}

func (o *StepCreateTaskOptions) generateSourceRepoResource(name string) *pipelineapi.PipelineResource {
	var resource *pipelineapi.PipelineResource
	if o.GitInfo != nil {
		gitURL := o.GitInfo.HttpsURL()
		if gitURL != "" {
			resource = &pipelineapi.PipelineResource{
				TypeMeta: metav1.TypeMeta{
					APIVersion: syntax.TektonAPIVersion,
					Kind:       "PipelineResource",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Spec: pipelineapi.PipelineResourceSpec{
					Type: pipelineapi.PipelineResourceTypeGit,
					Params: []pipelineapi.Param{
						{
							Name:  "revision",
							Value: o.Revision,
						},
						{
							Name:  "url",
							Value: gitURL,
						},
					},
				},
			}
		}
	}
	return resource
}

func (o *StepCreateTaskOptions) setBuildValues() error {
	labels := map[string]string{}
	if o.GitInfo != nil {
		labels["owner"] = o.GitInfo.Organisation
		labels["repo"] = o.GitInfo.Name
	}
	labels["branch"] = o.Branch
	if o.Context != "" {
		labels["context"] = o.Context
	}
	return o.combineLabels(labels)
}

func (o *StepCreateTaskOptions) combineLabels(labels map[string]string) error {
	// add any custom labels
	for _, customLabel := range o.CustomLabels {
		parts := strings.Split(customLabel, "=")
		if len(parts) != 2 {
			return errors.Errorf("expected 2 parts to label but got %v", len(parts))
		}
		labels[parts[0]] = parts[1]
	}
	o.labels = labels
	return nil
}

func (o *StepCreateTaskOptions) combineEnvVars(projectConfig *jenkinsfile.PipelineConfig) error {
	// add any custom env vars
	envMap := make(map[string]corev1.EnvVar)
	for _, e := range projectConfig.Env {
		envMap[e.Name] = e
	}
	for _, customEnvVar := range o.CustomEnvs {
		parts := strings.Split(customEnvVar, "=")
		if len(parts) != 2 {
			return errors.Errorf("expected 2 parts to env var but got %v", len(parts))
		}
		e := corev1.EnvVar{
			Name:  parts[0],
			Value: parts[1],
		}
		envMap[e.Name] = e
	}
	projectConfig.Env = syntax.EnvMapToSlice(envMap)
	return nil
}

// TODO: Use the same YAML lib here as in buildpipeline/pipeline.go
// TODO: Use interface{} with a helper function to reduce code repetition?
// TODO: Take no arguments and use o.Results internally?
func (o *StepCreateTaskOptions) writeOutput(folder string, pipeline *pipelineapi.Pipeline, tasks []*pipelineapi.Task, pipelineRun *pipelineapi.PipelineRun, resources []*pipelineapi.PipelineResource, structure *v1.PipelineStructure, pipelineActivity *kube.PromoteStepActivityKey) error {
	if err := os.Mkdir(folder, os.ModePerm); err != nil {
		if !os.IsExist(err) {
			return err
		}
	}
	data, err := yaml.Marshal(pipeline)
	if err != nil {
		return errors.Wrapf(err, "failed to marshal Pipeline YAML")
	}
	fileName := filepath.Join(folder, "pipeline.yml")
	err = ioutil.WriteFile(fileName, data, util.DefaultWritePermissions)
	if err != nil {
		return errors.Wrapf(err, "failed to save Pipeline file %s", fileName)
	}
	log.Infof("generated Pipeline at %s\n", util.ColorInfo(fileName))

	data, err = yaml.Marshal(pipelineRun)
	if err != nil {
		return errors.Wrapf(err, "failed to marshal PipelineRun YAML")
	}
	fileName = filepath.Join(folder, "pipeline-run.yml")
	err = ioutil.WriteFile(fileName, data, util.DefaultWritePermissions)
	if err != nil {
		return errors.Wrapf(err, "failed to save PipelineRun file %s", fileName)
	}
	log.Infof("generated PipelineRun at %s\n", util.ColorInfo(fileName))

	if structure != nil {
		data, err = yaml.Marshal(structure)
		if err != nil {
			return errors.Wrapf(err, "failed to marshal PipelineStructure YAML")
		}
		fileName = filepath.Join(folder, "structure.yml")
		err = ioutil.WriteFile(fileName, data, util.DefaultWritePermissions)
		if err != nil {
			return errors.Wrapf(err, "failed to save PipelineStructure file %s", fileName)
		}
		log.Infof("generated PipelineStructure at %s\n", util.ColorInfo(fileName))
	}

	taskList := &pipelineapi.TaskList{}
	for _, task := range tasks {
		taskList.Items = append(taskList.Items, *task)
	}

	resourceList := &pipelineapi.PipelineResourceList{}
	for _, resource := range resources {
		resourceList.Items = append(resourceList.Items, *resource)
	}

	data, err = yaml.Marshal(taskList)
	if err != nil {
		return errors.Wrapf(err, "failed to marshal Task YAML")
	}
	fileName = filepath.Join(folder, "tasks.yml")
	err = ioutil.WriteFile(fileName, data, util.DefaultWritePermissions)
	if err != nil {
		return errors.Wrapf(err, "failed to save Task file %s", fileName)
	}
	log.Infof("generated Tasks at %s\n", util.ColorInfo(fileName))

	data, err = yaml.Marshal(resourceList)
	if err != nil {
		return errors.Wrapf(err, "failed to marshal PipelineResource YAML")
	}
	fileName = filepath.Join(folder, "resources.yml")
	err = ioutil.WriteFile(fileName, data, util.DefaultWritePermissions)
	if err != nil {
		return errors.Wrapf(err, "failed to save PipelineResource file %s", fileName)
	}
	log.Infof("generated PipelineResources at %s\n", util.ColorInfo(fileName))

	data, err = yaml.Marshal(pipelineActivity)
	if err != nil {
		return errors.Wrapf(err, "failed to marshal PipelineActivity YAML")
	}
	fileName = filepath.Join(folder, "pipelineActivity.yml")
	err = ioutil.WriteFile(fileName, data, util.DefaultWritePermissions)
	if err != nil {
		return errors.Wrapf(err, "failed to save PipelineActivity file %s", fileName)
	}
	log.Infof("generated PipelineActivity at %s\n", util.ColorInfo(fileName))

	return nil
}

// Given a Pipeline and its Tasks, applies the Tasks and Pipeline to the cluster
// and creates and applies a PipelineResource for their source repo and a PipelineRun
// to execute them.
func (o *StepCreateTaskOptions) applyPipeline(pipeline *pipelineapi.Pipeline, tasks []*pipelineapi.Task, resources []*pipelineapi.PipelineResource, structure *v1.PipelineStructure, run *pipelineapi.PipelineRun, gitInfo *gits.GitRepository, branch string, activityKey *kube.PromoteStepActivityKey) error {
	_, ns, err := o.KubeClientAndDevNamespace()
	if err != nil {
		return err
	}
	tektonClient, _, err := o.TektonClient()
	if err != nil {
		return err
	}

	info := util.ColorInfo

	var activityOwnerReference *metav1.OwnerReference

	if activityKey != nil {

		jxClient, _, jxErr := o.JXClientAndDevNamespace()
		if jxErr != nil {
			return jxErr
		}
		activity, _, err := activityKey.GetOrCreate(jxClient, pipeline.Namespace)
		if err != nil {
			return err
		}

		activityOwnerReference = &metav1.OwnerReference{
			APIVersion: jenkinsio.GroupAndVersion,
			Kind:       "PipelineActivity",
			Name:       activity.Name,
			UID:        activity.UID,
		}
	}

	for _, resource := range resources {
		_, err := tekton.CreateOrUpdateSourceResource(tektonClient, ns, resource)
		if err != nil {
			return errors.Wrapf(err, "failed to create/update PipelineResource %s in namespace %s", resource.Name, ns)
		}
		if resource.Spec.Type == pipelineapi.PipelineResourceTypeGit {
			gitURL := gitInfo.HttpCloneURL()
			log.Infof("upserted PipelineResource %s for the git repository %s and branch %s\n", info(resource.Name), info(gitURL), info(branch))
		} else {
			log.Infof("upserted PipelineResource %s\n", info(resource.Name))
		}
	}

	for _, task := range tasks {
		if activityOwnerReference != nil {
			task.OwnerReferences = []metav1.OwnerReference{*activityOwnerReference}
		}
		_, err = tekton.CreateOrUpdateTask(tektonClient, ns, task)
		if err != nil {
			return errors.Wrapf(err, "failed to create/update the task %s in namespace %s", task.Name, ns)
		}
		log.Infof("upserted Task %s\n", info(task.Name))
	}

	if activityOwnerReference != nil {
		pipeline.OwnerReferences = []metav1.OwnerReference{*activityOwnerReference}
	}

	pipeline, err = tekton.CreateOrUpdatePipeline(tektonClient, ns, pipeline)
	if err != nil {
		return errors.Wrapf(err, "failed to create/update the Pipeline in namespace %s", ns)
	}
	log.Infof("upserted Pipeline %s\n", info(pipeline.Name))

	pipelineOwnerReference := metav1.OwnerReference{
		APIVersion: syntax.TektonAPIVersion,
		Kind:       "Pipeline",
		Name:       pipeline.Name,
		UID:        pipeline.UID,
	}

	structure.OwnerReferences = []metav1.OwnerReference{pipelineOwnerReference}
	run.OwnerReferences = []metav1.OwnerReference{pipelineOwnerReference}

	_, err = tekton.CreatePipelineRun(tektonClient, ns, run)
	if err != nil {
		return errors.Wrapf(err, "failed to create the PipelineRun in namespace %s", ns)
	}
	log.Infof("created PipelineRun %s\n", info(run.Name))

	if structure != nil {
		structure.PipelineRunRef = &run.Name

		jxClient, _, err := o.JXClientAndDevNamespace()
		if err != nil {
			return err
		}
		structuresClient := jxClient.JenkinsV1().PipelineStructures(ns)

		// Reset the structure name to be the run's name and set the PipelineRef and PipelineRunRef
		if structure.PipelineRef == nil {
			structure.PipelineRef = &pipeline.Name
		}
		structure.Name = run.Name
		structure.PipelineRunRef = &run.Name

		if _, structErr := structuresClient.Create(structure); structErr != nil {
			return errors.Wrapf(structErr, "failed to create the PipelineStructure in namespace %s", ns)
		}
		log.Infof("created PipelineStructure %s\n", info(structure.Name))
	}

	return nil
}

func (o *StepCreateTaskOptions) createSteps(languageName string, projectConfig *config.ProjectConfig, pipelineConfig *jenkinsfile.PipelineConfig, templateKind string, step *syntax.Step, containerName string, dir string, prefixPath string) []syntax.Step {
	steps := []syntax.Step{}

	if step.GetImage() != "" {
		containerName = step.GetImage()
	} else {
		containerName = pipelineConfig.Agent.GetImage()
	}

	if step.Dir != "" {
		dir = step.Dir
	}
	dir = strings.Replace(dir, "/home/jenkins/go/src/REPLACE_ME_GIT_PROVIDER/REPLACE_ME_ORG/REPLACE_ME_APP_NAME", o.getWorkspaceDir(), -1)

	gitInfo := o.GitInfo
	if gitInfo != nil {
		gitProviderHost := gitInfo.Host
		dir = strings.Replace(dir, PlaceHolderAppName, gitInfo.Name, -1)
		dir = strings.Replace(dir, PlaceHolderOrg, gitInfo.Organisation, -1)
		dir = strings.Replace(dir, PlaceHolderGitProvider, gitProviderHost, -1)
		dir = strings.Replace(dir, PlaceHolderDockerRegistryOrg, strings.ToLower(o.GetDockerRegistryOrg(projectConfig, gitInfo)), -1)
	} else {
		log.Warnf("No GitInfo available!\n")
	}

	if step.GetCommand() != "" {
		if containerName == "" {
			containerName = o.DefaultImage
			log.Warnf("No 'agent.container' specified in the pipeline configuration so defaulting to use: %s\n", containerName)
		}

		s := syntax.Step{}
		o.stepCounter++
		prefix := prefixPath
		if prefix != "" {
			prefix += "-"
		}
		stepName := step.Name
		if stepName == "" {
			stepName = "step" + strconv.Itoa(1+o.stepCounter)
		}
		s.Name = prefix + stepName
		s.Command = o.replaceCommandText(step)
		if o.CustomImage != "" {
			s.Image = o.CustomImage
		} else {
			s.Image = containerName
		}

		workspaceDir := o.getWorkspaceDir()
		if strings.HasPrefix(dir, "./") {
			dir = workspaceDir + strings.TrimPrefix(dir, ".")
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(workspaceDir, dir)
		}
		s.Dir = dir

		modifyStep := o.modifyStep(projectConfig, s, gitInfo, pipelineConfig, templateKind, step, containerName, dir)

		// let allow the docker images to have no actual version which is replaced via the version stream
		image, err := o.VersionResolver.ResolveDockerImage(modifyStep.Image)
		if err != nil {
			log.Warnf("failed to resolve docker image version: %s due to %s\n", modifyStep.Image, err.Error())
		} else {
			modifyStep.Image = image
		}

		steps = append(steps, modifyStep)
	} else if step.Loop != nil {
		// Just copy in the loop step without altering it.
		// TODO: We don't get magic around image resolution etc, but we avoid naming collisions that result otherwise.
		steps = append(steps, *step)
	}
	for _, s := range step.Steps {
		// TODO add child prefix?
		childPrefixPath := prefixPath
		steps = append(steps, o.createSteps(languageName, projectConfig, pipelineConfig, templateKind, s, containerName, dir, childPrefixPath)...)
	}
	return steps
}

// replaceCommandText lets remove any escaped "\$" stuff in the pipeline library
// and replace any use of the VERSION file with using the VERSION env var
func (o *StepCreateTaskOptions) replaceCommandText(step *syntax.Step) string {
	answer := strings.Replace(step.GetFullCommand(), "\\$", "$", -1)

	// lets replace the old way of setting versions
	answer = strings.Replace(answer, "export VERSION=`cat VERSION` && ", "", 1)
	answer = strings.Replace(answer, "export VERSION=$PREVIEW_VERSION && ", "", 1)

	for _, text := range []string{"$(cat VERSION)", "$(cat ../VERSION)", "$(cat ../../VERSION)"} {
		answer = strings.Replace(answer, text, "${VERSION}", -1)
	}
	return answer
}

func (o *StepCreateTaskOptions) getWorkspaceDir() string {
	return filepath.Join("/workspace", o.SourceName)
}

func (o *StepCreateTaskOptions) modifyEnvVars(container *corev1.Container, globalEnv []corev1.EnvVar) {
	envVars := []corev1.EnvVar{}
	for _, e := range container.Env {
		name := e.Name
		if name != "JENKINS_URL" {
			envVars = append(envVars, e)
		}
	}
	if kube.GetSliceEnvVar(envVars, "DOCKER_REGISTRY") == nil {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "DOCKER_REGISTRY",
			Value: o.DockerRegistry,
		})
	}
	if kube.GetSliceEnvVar(envVars, "BUILD_NUMBER") == nil {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "BUILD_NUMBER",
			Value: o.BuildNumber,
		})
	}
	if o.PipelineKind != "" && kube.GetSliceEnvVar(envVars, "PIPELINE_KIND") == nil {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "PIPELINE_KIND",
			Value: o.PipelineKind,
		})
	}
	if o.Context != "" && kube.GetSliceEnvVar(envVars, "PIPELINE_CONTEXT") == nil {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "PIPELINE_CONTEXT",
			Value: o.Context,
		})
	}
	gitInfo := o.GitInfo
	branch := o.Branch
	if gitInfo != nil {
		u := gitInfo.CloneURL
		if u != "" && kube.GetSliceEnvVar(envVars, "SOURCE_URL") == nil {
			envVars = append(envVars, corev1.EnvVar{
				Name:  "SOURCE_URL",
				Value: u,
			})
		}
		repo := gitInfo.Name
		owner := gitInfo.Organisation
		if owner != "" && kube.GetSliceEnvVar(envVars, "REPO_OWNER") == nil {
			envVars = append(envVars, corev1.EnvVar{
				Name:  "REPO_OWNER",
				Value: owner,
			})
		}
		if repo != "" && kube.GetSliceEnvVar(envVars, "REPO_NAME") == nil {
			envVars = append(envVars, corev1.EnvVar{
				Name:  "REPO_NAME",
				Value: repo,
			})
		}
		if owner != "" && repo != "" && branch != "" {
			jobName := fmt.Sprintf("%s/%s/%s", owner, repo, branch)
			if kube.GetSliceEnvVar(envVars, "JOB_NAME") == nil {
				envVars = append(envVars, corev1.EnvVar{
					Name:  "JOB_NAME",
					Value: jobName,
				})
			}
		}

		// lets keep the APP_NAME environment variable we need for previews
		if repo != "" && kube.GetSliceEnvVar(envVars, "APP_NAME") == nil {
			envVars = append(envVars, corev1.EnvVar{
				Name:  "APP_NAME",
				Value: repo,
			})
		}
	}
	if branch != "" {
		if kube.GetSliceEnvVar(envVars, "BRANCH_NAME") == nil {
			envVars = append(envVars, corev1.EnvVar{
				Name:  "BRANCH_NAME",
				Value: branch,
			})
		}
	}
	if kube.GetSliceEnvVar(envVars, "JX_BATCH_MODE") == nil {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "JX_BATCH_MODE",
			Value: "true",
		})
	}

	for _, param := range o.Results.PipelineParams {
		name := strings.ToUpper(param.Name)
		if kube.GetSliceEnvVar(envVars, name) == nil {
			envVars = append(envVars, corev1.EnvVar{
				Name:  name,
				Value: "${inputs.params." + param.Name + "}",
			})
		}
	}

	for _, e := range globalEnv {
		if kube.GetSliceEnvVar(envVars, e.Name) == nil && e.ValueFrom != nil {
			envVars = append(envVars, e)
		}
	}

	for i := range envVars {
		if envVars[i].Name == "XDG_CONFIG_HOME" {
			envVars[i].Value = "/workspace/xdg_config"
		}
	}

	if container.Name == "build-container-build" && !o.NoKaniko {
		if kube.GetSliceEnvVar(envVars, "GOOGLE_APPLICATION_CREDENTIALS") == nil {
			envVars = append(envVars, corev1.EnvVar{
				Name:  "GOOGLE_APPLICATION_CREDENTIALS",
				Value: o.KanikoSecretMount,
			})
		}
	}
	if kube.GetSliceEnvVar(envVars, "PREVIEW_VERSION") == nil && kube.GetSliceEnvVar(envVars, "VERSION") != nil {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "PREVIEW_VERSION",
			Value: "${inputs.params.version}",
		})
	}
	container.Env = envVars
}

func (o *StepCreateTaskOptions) modifyVolumes(container *corev1.Container, volumes []corev1.Volume) []corev1.Volume {
	answer := volumes

	if container.Name == "build-container-build" && !o.NoKaniko {
		kubeClient, ns, err := o.KubeClientAndDevNamespace()
		if err != nil {
			log.Warnf("failed to find kaniko secret: %s\n", err)
		} else {
			if o.KanikoSecret == "" {
				o.KanikoSecret = kanikoSecretName
			}
			if o.KanikoSecretKey == "" {
				o.KanikoSecretKey = kanikoSecretKey
			}
			secretName := o.KanikoSecret
			key := o.KanikoSecretKey
			secret, err := kubeClient.CoreV1().Secrets(ns).Get(secretName, metav1.GetOptions{})
			if err != nil {
				log.Warnf("failed to find secret %s in namespace %s: %s\n", secretName, ns, err)
			} else if secret != nil && secret.Data != nil && secret.Data[key] != nil {
				// lets mount the kaniko secret
				volumeName := "kaniko-secret"
				_, fileName := filepath.Split(o.KanikoSecretMount)

				volume := corev1.Volume{
					Name: volumeName,
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: secretName,
							Items: []corev1.KeyToPath{
								{
									Key:  key,
									Path: fileName,
								},
							},
						},
					},
				}
				if !kube.ContainsVolume(answer, volume) {
					answer = append(answer, volume)
				}

				mountDir, _ := filepath.Split(o.KanikoSecretMount)
				mountDir = strings.TrimSuffix(mountDir, "/")
				volumeMount := corev1.VolumeMount{
					Name:      volumeName,
					MountPath: mountDir,
					ReadOnly:  true,
				}
				if !kube.ContainsVolumeMount(container.VolumeMounts, volumeMount) {
					container.VolumeMounts = append(container.VolumeMounts, volumeMount)
				}
			}
		}
	}

	podInfoName := "podinfo"
	volume := corev1.Volume{
		Name: podInfoName,
		VolumeSource: corev1.VolumeSource{
			DownwardAPI: &corev1.DownwardAPIVolumeSource{
				Items: []corev1.DownwardAPIVolumeFile{
					{
						Path: "labels",
						FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: "metadata.labels",
						},
					},
				},
			},
		},
	}
	if !kube.ContainsVolume(volumes, volume) {
		answer = append(answer, volume)
	}
	volumeMount := corev1.VolumeMount{
		Name:      podInfoName,
		MountPath: "/etc/podinfo",
		ReadOnly:  true,
	}
	if !kube.ContainsVolumeMount(container.VolumeMounts, volumeMount) {
		container.VolumeMounts = append(container.VolumeMounts, volumeMount)
	}
	return answer
}

func (o *StepCreateTaskOptions) cloneGitRepositoryToTempDir(gitURL string) error {
	var err error
	o.Dir, err = ioutil.TempDir("", "git")
	if err != nil {
		return err
	}
	log.Infof("my jx log : shallow cloning repository %s to temp dir %s\n", gitURL, o.Dir)
	log.Infof("shallow cloning repository %s to temp dir %s\n", gitURL, o.Dir)
	err = o.Git().Init(o.Dir)
	if err != nil {
		return errors.Wrapf(err, "failed to init a new git repository in directory %s", o.Dir)
	}
	if o.Verbose {
		log.Infof("ran git init in %s", o.Dir)
	}
	err = o.Git().AddRemote(o.Dir, "origin", gitURL)
	if err != nil {
		return errors.Wrapf(err, "failed to add remote origin with url %s in directory %s", gitURL, o.Dir)
	}
	if o.Verbose {
		log.Infof("ran git add remote origin %s in %s", gitURL, o.Dir)
	}
	commitish := make([]string, 0)
	if o.PullRequestNumber != "" {
		pr := fmt.Sprintf("pull/%s/head:%s", o.PullRequestNumber, o.Branch)
		if o.Verbose {
			log.Infof("will fetch %s for %s in dir %s\n", pr, gitURL, o.Dir)
		}
		commitish = append(commitish, pr)
	}
	if o.Revision != "" {
		if o.Verbose {
			log.Infof("will fetch %s for %s in dir %s\n", o.Revision, gitURL, o.Dir)
		}
		commitish = append(commitish, o.Revision)
	} else {
		commitish = append(commitish, "master")
	}
	err = o.Git().FetchBranchShallow(o.Dir, "origin", commitish...)
	if err != nil {
		return errors.Wrapf(err, "failed to fetch %s from %s in directory %s", commitish, gitURL, o.Dir)
	}
	if o.Revision != "" {
		err = o.Git().Checkout(o.Dir, o.Revision)
		if err != nil {
			return errors.Wrapf(err, "failed to checkout revision %s", o.Revision)
		}
	} else {
		err = o.Git().Checkout(o.Dir, "master")
		if err != nil {
			return errors.Wrapf(err, "failed to checkout revision master")
		}
	}
	return nil
}

func (o *StepCreateTaskOptions) deleteTempDir() {
	log.Infof("removing the temp directory %s\n", o.Dir)
	err := os.RemoveAll(o.Dir)
	if err != nil {
		log.Warnf("failed to delete dir %s: %s\n", o.Dir, err.Error())
	}
}

func (o *StepCreateTaskOptions) viewSteps(tasks ...*pipelineapi.Task) error {
	table := o.CreateTable()
	showTaskName := len(tasks) > 1
	if showTaskName {
		table.AddRow("TASK", "NAME", "COMMAND", "IMAGE")
	} else {
		table.AddRow("NAME", "COMMAND", "IMAGE")
	}
	for _, task := range tasks {
		for _, step := range task.Spec.Steps {
			command := append([]string{}, step.Command...)
			command = append(command, step.Args...)
			commands := strings.Join(command, " ")
			if showTaskName {
				table.AddRow(task.Name, step.Name, commands, step.Image)
			} else {
				table.AddRow(step.Name, commands, step.Image)
			}
		}
	}
	table.Render()
	return nil
}

func getVersionFromFile(dir string) (string, error) {
	var version string
	versionFile := filepath.Join(dir, "VERSION")
	exist, err := util.FileExists(versionFile)
	if err != nil {
		return "", err
	}
	if exist {
		data, err := ioutil.ReadFile(versionFile)
		if err != nil {
			return "", errors.Wrapf(err, "failed to read file %s", versionFile)
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			log.Warnf("versions file %s is empty!\n", versionFile)
		} else {
			version = text
			if version != "" {
				return version, nil
			}
		}
	}
	return "", errors.New("failed to read file " + versionFile)
}

func (o *StepCreateTaskOptions) setVersionOnReleasePipelines(pipelineConfig *jenkinsfile.PipelineConfig) error {
	if o.NoReleasePrepare || o.ViewSteps {
		return nil
	}
	version := ""

	if o.DryRun {
		version, err := getVersionFromFile(o.Dir)
		if err != nil {
			log.Warn("No version file or incorrect content; using 0.0.1 as version")
			version = "0.0.1"
		}
		o.version = version
		o.Revision = "v" + version
		o.Results.PipelineParams = append(o.Results.PipelineParams, pipelineapi.Param{
			Name:  "version",
			Value: o.version,
		})
		log.Infof("Version used: '%s'", util.ColorInfo(version))

		return nil
	} else if o.PipelineKind == jenkinsfile.PipelineKindRelease {
		release := pipelineConfig.Pipelines.Release
		if release == nil {
			return fmt.Errorf("no Release pipeline available")
		}
		sv := release.SetVersion
		if sv == nil {
			// lets create a default set version pipeline
			sv = &jenkinsfile.PipelineLifecycle{
				Steps: []*syntax.Step{
					{
						Command: "jx step next-version --use-git-tag-only --tag",
						Name:    "next-version",
						Comment: "tags git with the next version",
					},
				},
			}
		}
		steps := sv.Steps
		err := o.invokeSteps(steps)
		if err != nil {
			return err
		}
		version, err = getVersionFromFile(o.Dir)
		if err != nil {
			return err
		}
		o.Revision = "v" + version
	} else {
		// lets use the branch name if we can find it for the version number
		branch := o.Branch
		if branch == "" {
			branch = o.Revision
		}
		buildNumber := o.BuildNumber
		o.previewVersionPrefix = "0.0.0-SNAPSHOT-" + branch + "-"
		version = o.previewVersionPrefix + buildNumber
	}
	if version != "" {
		if !hasParam(o.Results.PipelineParams, "version") {
			o.Results.PipelineParams = append(o.Results.PipelineParams, pipelineapi.Param{
				Name:  "version",
				Value: version,
			})
		}
	}
	o.version = version
	if o.BuildNumber != "" {
		if !hasParam(o.Results.PipelineParams, "build_id") {
			o.Results.PipelineParams = append(o.Results.PipelineParams, pipelineapi.Param{
				Name:  "build_id",
				Value: o.BuildNumber,
			})
		}
	}
	return nil
}

func hasParam(params []pipelineapi.Param, name string) bool {
	for _, param := range params {
		if param.Name == name {
			return true
		}
	}
	return false
}

func hasPipelineParam(params []pipelineapi.Param, name string) bool {
	for _, param := range params {
		if param.Name == name {
			return true
		}
	}
	return false
}

func (o *StepCreateTaskOptions) runStepCommand(step *syntax.Step) error {
	c := step.GetFullCommand()
	if c == "" {
		return nil
	}
	log.Infof("running command: %s\n", util.ColorInfo(c))

	commandText := strings.Replace(step.GetFullCommand(), "\\$", "$", -1)

	cmd := util.Command{
		Name: "/bin/sh",
		Args: []string{"-c", commandText},
		Out:  o.Out,
		Err:  o.Err,
		Dir:  o.Dir,
	}
	result, err := cmd.RunWithoutRetry()
	if err != nil {
		return err
	}
	log.Infof("%s\n", result)
	return nil
}

func (o *StepCreateTaskOptions) invokeSteps(steps []*syntax.Step) error {
	for _, s := range steps {
		if s == nil {
			continue
		}
		if len(s.Steps) > 0 {
			err := o.invokeSteps(s.Steps)
			if err != nil {
				return err
			}
		}
		when := strings.TrimSpace(s.When)
		if when == "!prow" || s.GetCommand() == "" {
			continue
		}
		err := o.runStepCommand(s)
		if err != nil {
			return err
		}
	}
	return nil
}

// modifyStep allows a container step to be modified to do something different
func (o *StepCreateTaskOptions) modifyStep(projectConfig *config.ProjectConfig, parsedStep syntax.Step, gitInfo *gits.GitRepository, pipelineConfig *jenkinsfile.PipelineConfig, templateKind string, step *syntax.Step, containerName string, dir string) syntax.Step {

	if !o.NoKaniko {
		if strings.HasPrefix(parsedStep.Command, "skaffold build") ||
			(len(parsedStep.Arguments) > 0 && strings.HasPrefix(strings.Join(parsedStep.Arguments[1:], " "), "skaffold build")) {
			sourceDir := o.getWorkspaceDir()
			dockerfile := filepath.Join(sourceDir, "Dockerfile")
			localRepo := o.getDockerRegistry(projectConfig)
			destination := o.dockerImage(projectConfig, gitInfo)

			args := []string{"--cache=true", "--cache-dir=/workspace",
				"--context=" + sourceDir,
				"--dockerfile=" + dockerfile,
				"--destination=" + destination + ":${inputs.params.version}",
				"--cache-repo=" + localRepo + "/" + o.ProjectID + "/cache",
			}
			if localRepo != "gcr.io" {
				args = append(args, "--skip-tls-verify-registry="+localRepo)
			}

			if ipAddressRegistryRegex.MatchString(localRepo) {
				args = append(args, "--insecure")
			}

			parsedStep.Command = "/kaniko/executor"
			parsedStep.Arguments = args

			if o.KanikoImage == "" {
				o.KanikoImage = kanikoDockerImage
			}
			parsedStep.Image = o.KanikoImage
		}
	}
	return parsedStep
}

func (o *StepCreateTaskOptions) dockerImage(projectConfig *config.ProjectConfig, gitInfo *gits.GitRepository) string {
	dockerRegistry := o.getDockerRegistry(projectConfig)

	dockeerRegistryOrg := o.DockerRegistryOrg
	if dockeerRegistryOrg == "" {
		dockeerRegistryOrg = o.GetDockerRegistryOrg(projectConfig, gitInfo)
	}
	appName := gitInfo.Name
	return dockerRegistry + "/" + dockeerRegistryOrg + "/" + appName
}

func (o *StepCreateTaskOptions) getDockerRegistry(projectConfig *config.ProjectConfig) string {
	dockerRegistry := o.DockerRegistry
	if dockerRegistry == "" {
		dockerRegistry = o.GetDockerRegistry(projectConfig)
	}
	return dockerRegistry
}

// ObjectReferences creates a list of object references created
func (r *StepCreateTaskResults) ObjectReferences() []kube.ObjectReference {
	resources := []kube.ObjectReference{}
	for _, task := range r.Tasks {
		if task.ObjectMeta.Name == "" {
			log.Warnf("created Task has no name: %#v\n", task)

		} else {
			resources = append(resources, kube.CreateObjectReference(task.TypeMeta, task.ObjectMeta))
		}
	}
	if r.Pipeline != nil {
		if r.Pipeline.ObjectMeta.Name == "" {
			log.Warnf("created Pipeline has no name: %#v\n", r.Pipeline)

		} else {
			resources = append(resources, kube.CreateObjectReference(r.Pipeline.TypeMeta, r.Pipeline.ObjectMeta))
		}
	}
	if r.PipelineRun != nil {
		if r.PipelineRun.ObjectMeta.Name == "" {
			log.Warnf("created PipelineRun has no name: %#v\n", r.PipelineRun)
		} else {
			resources = append(resources, kube.CreateObjectReference(r.PipelineRun.TypeMeta, r.PipelineRun.ObjectMeta))
		}
	}
	if len(resources) == 0 {
		log.Warnf("no Tasks, Pipeline or PipelineRuns created\n")
	}
	return resources
}
