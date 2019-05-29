package cmd

import (
	"fmt"
	"github.com/jenkins-x/jx/pkg/jx/cmd/helper"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	v1 "github.com/jenkins-x/jx/pkg/apis/jenkins.io/v1"
	"github.com/jenkins-x/jx/pkg/config"
	"github.com/jenkins-x/jx/pkg/gits"
	configio "github.com/jenkins-x/jx/pkg/io"
	"github.com/jenkins-x/jx/pkg/jx/cmd/bdd"
	"github.com/jenkins-x/jx/pkg/jx/cmd/opts"
	"github.com/jenkins-x/jx/pkg/jx/cmd/templates"
	"github.com/jenkins-x/jx/pkg/kube"
	"github.com/jenkins-x/jx/pkg/log"
	"github.com/jenkins-x/jx/pkg/util"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	optionDefaultAdminPassword = "default-admin-password"
)

// StepBDDOptions contains the command line arguments for this command
type StepBDDOptions struct {
	StepOptions

	InstallOptions InstallOptions
	Flags          StepBDDFlags
}

type StepBDDFlags struct {
	GoPath              string
	GitProvider         string
	GitOwner            string
	ReportsOutputDir    string
	UseCurrentTeam      bool
	DeleteTeam          bool
	DisableDeleteApp    bool
	DisableDeleteRepo   bool
	IgnoreTestFailure   bool
	Parallel            bool
	VersionsDir         string
	VersionsRepository  string
	VersionsGitRef      string
	ConfigFile          string
	TestRepoGitCloneUrl string
	SkipRepoGitClone    bool
	UseRevision         bool
	TestGitBranch       string
	TestGitPrNumber     string
	JxBinary            string
	TestCases           []string
	VersionsRepoPr      bool
}

var (
	stepBDDLong = templates.LongDesc(`
		This pipeline step lets you run the BDD tests in the current team in a current cluster or create a new cluster/team run tests there then tear things down again.

`)

	stepBDDExample = templates.Examples(`
		# run the BDD tests in the current team
		jx step bdd --use-current-team --git-provider-url=http://my.git.server.com

        # create a new team for the tests, run the tests then tear everything down again 
		jx step bdd -b --provider=gke --git-provider=ghe --git-provider-url=http://my.git.server.com --default-admin-password=myadminpwd --git-username myuser --git-api-token mygittoken
`)
)

func NewCmdStepBDD(commonOpts *opts.CommonOptions) *cobra.Command {
	options := StepBDDOptions{
		StepOptions: StepOptions{
			CommonOptions: commonOpts,
		},
		InstallOptions: CreateInstallOptions(commonOpts),
	}
	cmd := &cobra.Command{
		Use:     "bdd",
		Short:   "Performs the BDD tests on the current cluster, new clusters or teams",
		Long:    stepBDDLong,
		Example: stepBDDExample,
		Run: func(cmd *cobra.Command, args []string) {
			options.Cmd = cmd
			options.Args = args
			err := options.Run()
			helper.CheckErr(err)
		},
	}
	installOptions := &options.InstallOptions
	installOptions.addInstallFlags(cmd, true)

	cmd.Flags().StringVarP(&options.Flags.ConfigFile, "config", "c", "", "the config YAML file containing the clusters to create")
	cmd.Flags().StringVarP(&options.Flags.GoPath, "gopath", "", "", "the GOPATH directory where the BDD test git repository will be cloned")
	cmd.Flags().StringVarP(&options.Flags.GitProvider, "git-provider", "g", "", "the git provider kind")
	cmd.Flags().StringVarP(&options.Flags.GitOwner, "git-owner", "", "", "the git owner of new git repositories created by the tests")
	cmd.Flags().StringVarP(&options.Flags.ReportsOutputDir, "reports-dir", "", "reports", "the directory used to copy in any generated report files")
	cmd.Flags().StringVarP(&options.Flags.TestRepoGitCloneUrl, "test-git-repo", "r", "http://github.com/jenkins-x/bdd-jx.git", "the git repository to clone for the BDD tests")
	cmd.Flags().BoolVarP(&options.Flags.SkipRepoGitClone, "skip-test-git-repo-clone", "", false, "Skip cloning the bdd test git repo")
	cmd.Flags().StringVarP(&options.Flags.JxBinary, "binary", "", "jx", "the binary location of the 'jx' executable for creating clusters")
	cmd.Flags().StringVarP(&options.Flags.TestGitBranch, "test-git-branch", "", "master", "the git repository branch to use for the BDD tests")
	cmd.Flags().StringVarP(&options.Flags.TestGitPrNumber, "test-git-pr-number", "", "", "the Pull Request number to fetch from the repository for the BDD tests")
	cmd.Flags().StringArrayVarP(&options.Flags.TestCases, "tests", "t", []string{"test-quickstart-node-http"}, "the list of the test cases to run")
	cmd.Flags().StringVarP(&options.Flags.VersionsDir, "dir", "", "", "the git clone of the jenkins-x/jenkins-x-versions git repository. Used to default the version of jenkins-x-platform when creating clusters if no --version option is supplied")
	cmd.Flags().BoolVarP(&options.Flags.DeleteTeam, "delete-team", "", true, "Whether we should delete the Team we create for each Git Provider")
	cmd.Flags().BoolVarP(&options.Flags.DisableDeleteApp, "no-delete-app", "", false, "Disables deleting the created app after the test")
	cmd.Flags().BoolVarP(&options.Flags.DisableDeleteRepo, "no-delete-repo", "", false, "Disables deleting the created repository after the test")
	cmd.Flags().BoolVarP(&options.Flags.UseCurrentTeam, "use-current-team", "", false, "If enabled lets use the current Team to run the tests")
	cmd.Flags().BoolVarP(&options.Flags.IgnoreTestFailure, "ignore-fail", "i", false, "Ignores test failures so that a BDD test run can capture the output and report on the test passes/failures")
	cmd.Flags().BoolVarP(&options.Flags.IgnoreTestFailure, "parallel", "", false, "Should we process each cluster configuration in parallel")
	cmd.Flags().BoolVarP(&options.Flags.UseRevision, "use-revision", "", true, "Use the git revision from the current git clone instead of the Pull Request branch")
	cmd.Flags().BoolVarP(&options.Flags.VersionsRepoPr, "version-repo-pr", "", false, "For use with jenkins-x-versions PR. Indicates the git revision of the PR should be used to clone the jenkins-x-versions")

	cmd.Flags().StringVarP(&installOptions.Flags.Provider, "provider", "", "", "Cloud service providing the Kubernetes cluster.  Supported providers: "+KubernetesProviderOptions())

	return cmd
}

func (o *StepBDDOptions) Run() error {
	flags := &o.Flags

	var err error
	if o.Flags.GoPath == "" {
		o.Flags.GoPath = os.Getenv("GOPATH")
		if o.Flags.GoPath == "" {
			o.Flags.GoPath, err = os.Getwd()
			if err != nil {
				return err
			}
		}
	}

	if o.InstallOptions.Flags.VersionsRepository == "" {
		o.InstallOptions.Flags.VersionsRepository = opts.DefaultVersionsURL
	}

	gitProviderUrl := o.gitProviderUrl()
	if gitProviderUrl == "" {
		return util.MissingOption("git-provider-url")
	}

	fileName := flags.ConfigFile
	if fileName == "" {
		return o.runOnCurrentCluster()
	}

	config, err := bdd.LoadBddClusters(fileName)
	if err != nil {
		return err
	}
	if len(config.Clusters) == 0 {
		return fmt.Errorf("No clusters specified in configuration file %s", fileName)
	}

	// TODO handle parallel...
	errors := []error{}
	for _, cluster := range config.Clusters {
		err := o.createCluster(cluster)
		if err != nil {
			return err
		}

		defer o.deleteCluster(cluster)

		err = o.runTests(o.Flags.GoPath)
		if err != nil {
			log.Warnf("Failed to perform tests on cluster %s: %s\n", cluster.Name, err)
			errors = append(errors, err)
		}
	}
	return util.CombineErrors(errors...)
}

// runOnCurrentCluster runs the tests on the current cluster
func (o *StepBDDOptions) runOnCurrentCluster() error {
	var err error

	gitProviderName := o.Flags.GitProvider
	if gitProviderName != "" && !o.Flags.UseCurrentTeam {
		gitUser := o.InstallOptions.GitRepositoryOptions.Username
		if gitUser == "" {
			return util.MissingOption("git-username")
		}
		gitToken := o.InstallOptions.GitRepositoryOptions.ApiToken
		if gitToken == "" {
			return util.MissingOption("git-api-token")
		}

		defaultAdminPassword := o.InstallOptions.AdminSecretsService.Flags.DefaultAdminPassword
		if defaultAdminPassword == "" {
			return util.MissingOption(optionDefaultAdminPassword)
		}
		defaultOptions := o.createDefaultCommonOptions()

		gitProviderUrl := o.gitProviderUrl()

		teamPrefix := "bdd-"
		if o.InstallOptions.Flags.Tekton {
			teamPrefix += "tekton-"
		}
		team := kube.ToValidName(teamPrefix + gitProviderName + "-" + o.teamNameSuffix())
		log.Infof("Creating team %s\n", util.ColorInfo(team))

		installOptions := o.InstallOptions
		installOptions.CommonOptions = defaultOptions
		installOptions.InitOptions.CommonOptions = defaultOptions
		installOptions.SkipAuthSecretsMerge = true
		installOptions.BatchMode = true

		installOptions.InitOptions.Flags.NoTiller = true
		installOptions.InitOptions.Flags.HelmClient = true
		installOptions.InitOptions.Flags.SkipTiller = true
		installOptions.Flags.Namespace = team
		installOptions.Flags.NoDefaultEnvironments = true
		installOptions.Flags.DefaultEnvironmentPrefix = team
		installOptions.AdminSecretsService.Flags.DefaultAdminPassword = defaultAdminPassword

		err = installOptions.Run()
		if err != nil {
			return errors.Wrapf(err, "Failed to install team %s", team)
		}

		defer o.deleteTeam(team)

		defaultOptions.SetDevNamespace(team)

		// now lets setup the git server
		createGitServer := &CreateGitServerOptions{
			CreateOptions: CreateOptions{
				CommonOptions: defaultOptions,
			},
			Kind: gitProviderName,
			Name: gitProviderName,
			URL:  gitProviderUrl,
		}
		err = o.Retry(10, time.Second*10, func() error {
			err = createGitServer.Run()
			if err != nil {
				return errors.Wrapf(err, "Failed to create git server with kind %s at url %s in team %s", gitProviderName, gitProviderUrl, team)
			}
			return nil
		})
		if err != nil {
			return err
		}

		createGitToken := &CreateGitTokenOptions{
			CreateOptions: CreateOptions{
				CommonOptions: defaultOptions,
			},
			ServerFlags: opts.ServerFlags{
				ServerURL: gitProviderUrl,
			},
			Username: gitUser,
			ApiToken: gitToken,
		}
		err = createGitToken.Run()
		if err != nil {
			return errors.Wrapf(err, "Failed to create git user token for user %s at url %s in team %s", gitProviderName, gitProviderUrl, team)
		}

		// now lets create an environment...
		createEnv := &CreateEnvOptions{
			CreateOptions: CreateOptions{
				CommonOptions: defaultOptions,
			},
			HelmValuesConfig: config.HelmValuesConfig{
				ExposeController: &config.ExposeController{},
			},
			Options: v1.Environment{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: v1.EnvironmentSpec{
					PromotionStrategy: v1.PromotionStrategyTypeAutomatic,
					Order:             100,
				},
			},
			PromotionStrategy:      string(v1.PromotionStrategyTypeAutomatic),
			ForkEnvironmentGitRepo: kube.DefaultEnvironmentGitRepoURL,
			Prefix:                 team,
		}

		createEnv.BatchMode = true
		createEnv.Options.Name = "staging"
		createEnv.Options.Spec.Label = "Staging"
		createEnv.GitRepositoryOptions.ServerURL = gitProviderUrl
		gitOwner := o.Flags.GitOwner
		if gitOwner == "" && gitUser != "" {
			// lets avoid loading the git owner from the current cluster
			gitOwner = gitUser
		}
		if gitOwner != "" {
			createEnv.GitRepositoryOptions.Owner = gitOwner
		}
		if gitUser != "" {
			createEnv.GitRepositoryOptions.Username = gitUser
		}
		log.Infof("using environment git owner: %s\n", util.ColorInfo(gitOwner))
		log.Infof("using environment git user: %s\n", util.ColorInfo(gitUser))

		err = createEnv.Run()
		if err != nil {
			return err
		}
	} else {
		log.Infof("Using the default git provider for the tests\n")

	}
	return o.runTests(o.Flags.GoPath)
}

func (o *StepBDDOptions) deleteTeam(team string) error {
	if !o.Flags.DeleteTeam {
		log.Infof("Disabling the deletion of team: %s\n", util.ColorInfo(team))
		return nil
	}

	log.Infof("Deleting team %s\n", util.ColorInfo(team))
	deleteTeam := &DeleteTeamOptions{
		CommonOptions: o.createDefaultCommonOptions(),
		Confirm:       true,
	}
	deleteTeam.Args = []string{team}
	err := deleteTeam.Run()
	if err != nil {
		return errors.Wrapf(err, "Failed to delete team %s", team)
	}
	return nil

}

func (o *StepBDDOptions) createDefaultCommonOptions() *opts.CommonOptions {
	defaultOptions := o.CommonOptions
	defaultOptions.BatchMode = true
	defaultOptions.Args = nil
	return defaultOptions
}

func (o *StepBDDOptions) gitProviderUrl() string {
	return o.InstallOptions.GitRepositoryOptions.ServerURL
}

// teamNameSuffix returns a team name suffix using the current branch +
func (o *StepBDDOptions) teamNameSuffix() string {
	repo := os.Getenv("REPO_NAME")
	branch := os.Getenv("BRANCH_NAME")
	buildNumber := o.GetBuildNumber()
	if buildNumber == "" {
		buildNumber = "1"
	}
	return strings.Join([]string{repo, branch, buildNumber}, "-")
}

func (o *StepBDDOptions) runTests(gopath string) error {
	gitURL := o.Flags.TestRepoGitCloneUrl
	gitRepository, err := gits.ParseGitURL(gitURL)
	if err != nil {
		return errors.Wrapf(err, "Failed to parse git url %s", gitURL)
	}

	testDir := filepath.Join(gopath, gitRepository.Organisation, gitRepository.Name)
	if !o.Flags.SkipRepoGitClone {

		log.Infof("cloning BDD test repository to: %s\n", util.ColorInfo(testDir))

		err = os.MkdirAll(testDir, util.DefaultWritePermissions)
		if err != nil {
			return errors.Wrapf(err, "Failed to create dir %s", testDir)
		}

		log.Infof("Cloning git repository %s to dir %s\n", util.ColorInfo(gitURL), util.ColorInfo(testDir))
		err = o.Git().CloneOrPull(gitURL, testDir)
		if err != nil {
			return errors.Wrapf(err, "Failed to clone repo %s to %s", gitURL, testDir)
		}

		branchName := o.Flags.TestGitBranch
		pullRequestNumber := o.Flags.TestGitPrNumber
		log.Infof("Checking out repository branch %s to dir %s\n", util.ColorInfo(branchName), util.ColorInfo(testDir))
		if pullRequestNumber != "" {
			err = o.Git().FetchBranch(testDir, "origin", fmt.Sprintf("pull/%s/head:%s", pullRequestNumber, branchName))
			if err != nil {
				return errors.Wrapf(err, "Failed to fetch Pull request number %s", pullRequestNumber)
			}
		}

		err = o.Git().Checkout(testDir, branchName)
		if err != nil {
			return errors.Wrapf(err, "Failed to checkout branch %s", branchName)
		}
	}

	env := map[string]string{
		"GIT_PROVIDER_URL": o.gitProviderUrl(),
	}
	gitOwner := o.Flags.GitOwner
	if gitOwner != "" {
		env["GIT_ORGANISATION"] = gitOwner
	}
	if o.Flags.DisableDeleteApp {
		env["JX_DISABLE_DELETE_APP"] = "true"
	}
	if o.Flags.DisableDeleteRepo {
		env["JX_DISABLE_DELETE_REPO"] = "true"
	}

	c := &util.Command{
		Dir:  testDir,
		Name: "make",
		Args: o.Flags.TestCases,
		Env:  env,
		Out:  os.Stdout,
		Err:  os.Stdout,
	}
	_, err = c.RunWithoutRetry()

	err = o.reportStatus(testDir, err)

	o.copyReports(testDir, err)

	if o.Flags.IgnoreTestFailure && err != nil {
		log.Infof("Ignoring test failure %s\n", err)
		return nil
	}
	return err
}

// reportStatus runs a bunch of commands to report on the status of the cluster
func (o *StepBDDOptions) reportStatus(testDir string, err error) error {
	errs := []error{}
	if err != nil {
		errs = append(errs, err)
	}

	commands := []util.Command{
		{
			Name: "kubectl",
			Args: []string{"get", "pods"},
		},
		{
			Name: "kubectl",
			Args: []string{"get", "env", "dev", "-oyaml"},
		},
		{
			Name: "jx",
			Args: []string{"status", "-b"},
		},
		{
			Name: "jx",
			Args: []string{"version", "-b"},
		},
		{
			Name: "jx",
			Args: []string{"get", "env", "-b"},
		},
		{
			Name: "jx",
			Args: []string{"get", "activities", "-b"},
		},
		{
			Name: "jx",
			Args: []string{"get", "application", "-b"},
		},
		{
			Name: "jx",
			Args: []string{"get", "preview", "-b"},
		},
		{
			Name: "jx",
			Args: []string{"open"},
		},
	}

	for _, cmd := range commands {
		cmd.Dir = testDir
		cmd.Out = os.Stdout
		cmd.Err = os.Stdout

		_, err = cmd.RunWithoutRetry()
		if err != nil {
			errs = append(errs, err)
		}
	}
	return util.CombineErrors(errs...)
}

func (o *StepBDDOptions) copyReports(testDir string, err error) error {
	reportsDir := filepath.Join(testDir, "reports")
	if _, err := os.Stat(reportsDir); os.IsNotExist(err) {
		return nil
	}
	reportsOutputDir := o.Flags.ReportsOutputDir
	if reportsOutputDir == "" {
		reportsOutputDir = "reports"
	}
	err = os.MkdirAll(reportsOutputDir, util.DefaultWritePermissions)
	if err != nil {
		log.Warnf("failed to make reports output dir: %s : %s\n", reportsOutputDir, err)
		return err
	}
	err = util.CopyDir(reportsDir, reportsOutputDir, true)
	if err != nil {
		log.Warnf("failed to copy reports dir: %s directory to: %s : %s\n", reportsDir, reportsOutputDir, err)
	}
	return err
}

func (o *StepBDDOptions) createCluster(cluster *bdd.CreateCluster) error {
	buildNum := o.GetBuildNumber()
	if buildNum == "" {
		log.Warnf("No build number could be found from the environment variable $BUILD_NUMBER!\n")
	}
	baseClusterName := kube.ToValidName(cluster.Name)
	revision := os.Getenv("PULL_PULL_SHA")
	branch := o.GetBranchName(o.Flags.VersionsDir)
	if branch == "" {
		branch = "x"
	}
	log.Infof("found git revision %s: branch %s\n", revision, branch)

	if o.Flags.VersionsRepoPr && o.InstallOptions.Flags.VersionsGitRef == "" {
		if revision != "" && (branch == "" || o.Flags.UseRevision) {
			o.InstallOptions.Flags.VersionsGitRef = revision
		} else {
			o.InstallOptions.Flags.VersionsGitRef = branch
		}
	} else {
		o.InstallOptions.Flags.VersionsGitRef = "master"
	}

	log.Infof("using versions git repo %s and ref %s\n", o.InstallOptions.Flags.VersionsRepository, o.InstallOptions.Flags.VersionsGitRef)

	cluster.Name = kube.ToValidName(branch + "-" + buildNum + "-" + cluster.Name)
	log.Infof("\nCreating cluster %s\n", util.ColorInfo(cluster.Name))
	binary := o.Flags.JxBinary
	args := cluster.Args
	args = append(args, "--cluster-name", cluster.Name)

	if util.StringArrayIndex(args, "-b") < 0 && util.StringArrayIndex(args, "--batch-mode") < 0 {
		args = append(args, "--batch-mode")
	}

	if util.StringArrayIndex(args, "--version") < 0 && util.StringArrayHasPrefixIndex(args, "--version=") < 0 {
		version, err := o.getVersion()
		if err != nil {
			return err
		}
		if version != "" {
			args = append(args, "--version", version)
		}
	}

	if !cluster.NoLabels {
		cluster.Labels = addLabel(cluster.Labels, "cluster", baseClusterName)
		cluster.Labels = addLabel(cluster.Labels, "branch", branch)

		args = append(args, "--labels", cluster.Labels)
	}

	gitProviderURL := o.gitProviderUrl()
	if gitProviderURL != "" {
		args = append(args, "--git-provider-url", gitProviderURL)
	}

	if o.InstallOptions.Flags.VersionsRepository != "" {
		args = append(args, "--versions-repo", o.InstallOptions.Flags.VersionsRepository)
	}
	if o.InstallOptions.Flags.VersionsGitRef != "" {
		args = append(args, "--versions-ref", o.InstallOptions.Flags.VersionsGitRef)
	}
	gitUsername := o.InstallOptions.GitRepositoryOptions.Username
	if gitUsername != "" {
		args = append(args, "--git-username", gitUsername)
	}
	gitOwner := o.Flags.GitOwner
	if gitOwner != "" {
		args = append(args, "--environment-git-owner", gitOwner)
	}
	gitKind := o.InstallOptions.GitRepositoryOptions.ServerKind
	if gitKind != "" {
		args = append(args, "--git-provider-kind ", gitKind)
	}
	if o.CommonOptions.InstallDependencies {
		args = append(args, "--install-dependencies")
	}

	// expand any environment variables
	for i, arg := range args {
		args[i] = os.ExpandEnv(arg)
	}

	safeArgs := append([]string{}, args...)

	gitToken := o.InstallOptions.GitRepositoryOptions.ApiToken
	if gitToken != "" {
		args = append(args, "--git-api-token", gitToken)
		safeArgs = append(safeArgs, "--git-api-token", "**************¬")
	}
	adminPwd := o.InstallOptions.AdminSecretsService.Flags.DefaultAdminPassword
	if adminPwd != "" {
		args = append(args, "--default-admin-password", adminPwd)
		safeArgs = append(safeArgs, "--default-admin-password", "**************¬")
	}

	log.Infof("running command: %s\n", util.ColorInfo(fmt.Sprintf("%s %s", binary, strings.Join(safeArgs, " "))))

	// lets not log any sensitive command line arguments
	e := exec.Command(binary, args...)
	e.Stdout = o.Out
	e.Stderr = o.Err
	os.Setenv("PATH", util.PathWithBinary())

	// work around for helm apply with GitOps using a k8s local Service URL
	os.Setenv("CHART_REPOSITORY", kube.DefaultChartMuseumURL)
	err := e.Run()
	if err != nil {
		log.Errorf("Error: Command failed  %s %s\n", binary, strings.Join(safeArgs, " "))
	}
	return err
}

func (o *StepBDDOptions) deleteCluster(cluster *bdd.CreateCluster) error {
	return nil
}

// getVersion returns the jenkins-x-platform version to use for the cluster or empty string if no specific version can be found
func (o *StepBDDOptions) getVersion() (string, error) {
	version := o.InstallOptions.Flags.Version
	if version != "" {
		return version, nil
	}

	// lets try detect a local `Makefile` to find the version
	dir := o.Flags.VersionsDir
	version, err := LoadVersionFromCloudEnvironmentsDir(dir, configio.NewFileStore())
	if err != nil {
		return version, errors.Wrapf(err, "failed to load jenkins-x-platform version from dir %s", dir)
	}
	log.Infof("loaded version %s from Makefile in directory %s\n\n", util.ColorInfo(version), util.ColorInfo(dir))
	return version, nil
}
