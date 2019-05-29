package cmd_test_helpers

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/helm/pkg/proto/hapi/chart"

	"github.com/pkg/errors"
	"sigs.k8s.io/yaml"

	resources_test "github.com/jenkins-x/jx/pkg/kube/resources/mocks"

	jenkinsv1 "github.com/jenkins-x/jx/pkg/apis/jenkins.io/v1"
	"github.com/jenkins-x/jx/pkg/gits"
	"github.com/jenkins-x/jx/pkg/helm"
	helm_test "github.com/jenkins-x/jx/pkg/helm/mocks"
	"github.com/jenkins-x/jx/pkg/io/secrets"
	"github.com/jenkins-x/jx/pkg/jx/cmd"
	cmd_test "github.com/jenkins-x/jx/pkg/jx/cmd/clients/mocks"
	"github.com/jenkins-x/jx/pkg/jx/cmd/opts"
	"github.com/jenkins-x/jx/pkg/kube"
	vault_test "github.com/jenkins-x/jx/pkg/vault/mocks"
	"github.com/petergtz/pegomock"
	uuid "github.com/satori/go.uuid"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
)

// Helpers for various app tests

// AppTestOptions contains all useful data from the test environment initialized by `prepareInitialPromotionEnv`
type AppTestOptions struct {
	ConfigureGitFn  gits.ConfigureGitFn
	CommonOptions   *opts.CommonOptions
	FakeGitProvider *gits.FakeProvider
	DevRepo         *gits.FakeRepository
	DevEnvRepo      *gits.FakeRepository
	OrgName         string
	DevEnvRepoInfo  *gits.GitRepository
	DevEnv          *jenkinsv1.Environment
	MockHelmer      *helm_test.MockHelmer
	MockFactory     *cmd_test.MockFactory
	MockVaultClient *vault_test.MockClient
	OriginalJxHome  string
	OriginalKubeCfg string
	TempJxHome      string
	TempKubeCfg     string
}

// GetFullDevEnvDir returns a dev environment including org name and env
func (o *AppTestOptions) GetFullDevEnvDir(envDir string) (name string) {
	return filepath.Join(envDir, o.OrgName, o.DevEnvRepoInfo.Name)

}

// DirectlyAddApp adds a dummy app using helm
func (o *AppTestOptions) AddApp(values map[string]interface{}, prefix string) (string, string, string, error) {
	// Can't run in parallel

	nameUUID, err := uuid.NewV4()
	if err != nil {
		return "", "", "", errors.WithStack(err)
	}
	name := fmt.Sprintf("%s%s", prefix, nameUUID.String())
	alias := fmt.Sprintf("%s-alias", name)
	version := "0.0.1"
	installOpts := &cmd.AddAppOptions{
		AddOptions: cmd.AddOptions{
			CommonOptions: o.CommonOptions,
		},
		Version:              version,
		Repo:                 kube.DefaultChartMuseumURL,
		GitOps:               false,
		DevEnv:               o.DevEnv,
		HelmUpdate:           true, // Flag default when run on CLI
		ConfigureGitCallback: o.ConfigureGitFn,
	}
	helm_test.StubFetchChart(name, "", kube.DefaultChartMuseumURL, &chart.Chart{
		Metadata: &chart.Metadata{
			Name:        name,
			Version:     version,
			Description: "My test chart description",
		},
	}, o.MockHelmer)
	installOpts.Args = []string{name}
	err = installOpts.Run()
	if err != nil {
		return "", "", "", errors.WithStack(err)
	}
	return name, alias, version, nil
}

// DirectlyAddAppToGitOps modifies the environment git repo directly to add a dummy app
func (o *AppTestOptions) DirectlyAddAppToGitOps(appName string, values map[string]interface{}, prefix string) (name string,
	alias string, version string, err error) {
	envDir, err := o.CommonOptions.EnvironmentsDir()
	if err != nil {
		return "", "", "", err
	}
	devEnvDir := o.GetFullDevEnvDir(envDir)
	err = os.MkdirAll(devEnvDir, 0700)
	if err != nil {
		return "", "", "", err
	}

	// Update the requirements
	fileName := filepath.Join(devEnvDir, helm.RequirementsFileName)
	requirements := helm.Requirements{}
	if _, err := os.Stat(fileName); err == nil {
		data, err := ioutil.ReadFile(fileName)
		if err != nil {
			return "", "", "", err
		}

		err = yaml.Unmarshal(data, &requirements)
		if err != nil {
			return "", "", "", err
		}
	}
	// Put some commits on a branch
	name = appName
	if name == "" {
		nameUUID, err := uuid.NewV4()
		if err != nil {
			return "", "", "", err
		}
		name = nameUUID.String()
	}
	alias = fmt.Sprintf("%s-alias", name)
	version = "0.0.1"
	name = fmt.Sprintf("%s-%s", prefix, name)
	requirements.Dependencies = append(requirements.Dependencies, &helm.Dependency{
		Name:       name,
		Alias:      alias,
		Version:    version,
		Repository: helm.FakeChartmusuem,
	})
	data, err := yaml.Marshal(requirements)
	if err != nil {
		return "", "", "", err
	}
	err = ioutil.WriteFile(fileName, data, 0600)
	if err != nil {
		return "", "", "", err
	}

	// Add the values.yaml
	if values != nil {
		appDir := filepath.Join(devEnvDir, name)
		fileName := filepath.Join(appDir, helm.ValuesFileName)
		err := os.MkdirAll(appDir, 0700)
		if err != nil {
			return "", "", "", err
		}
		data, err = yaml.Marshal(values)
		if err != nil {
			return "", "", "", errors.Wrapf(err, "marshaling %vs\n", values)
		}
		err = ioutil.WriteFile(fileName, data, 0600)
		if err != nil {
			return "", "", "", errors.Wrapf(err, "writing %s\n%s\n", fileName, string(data))
		}
	}

	return name, alias, version, nil
}

// Cleanup must be run in a defer statement whenever CreateAppTestOptions is run
func (o *AppTestOptions) Cleanup() error {
	err := cmd.CleanupTestEnvironmentDir(o.CommonOptions)
	if err != nil {
		return err
	}
	err = cmd.CleanupTestKubeConfigDir(o.OriginalKubeCfg, o.TempKubeCfg)
	if err != nil {
		return err
	}
	err = cmd.CleanupTestJxHomeDir(o.OriginalJxHome, o.TempJxHome)
	if err != nil {
		return err
	}
	return nil
}

// CreateAppTestOptions configures the mock environment for running apps related tests
// If you use this function, then don't use t.Parallel
func CreateAppTestOptions(gitOps bool, appName string, t *testing.T) *AppTestOptions {
	mockFactory := cmd_test.NewMockFactory()
	commonOpts := opts.NewCommonOptionsWithFactory(mockFactory)
	o := AppTestOptions{
		CommonOptions: &commonOpts,
		MockFactory:   mockFactory,
	}
	originalJxHome, tempJxHome, err := cmd.CreateTestJxHomeDir()
	assert.NoError(t, err)
	o.OriginalJxHome = originalJxHome
	o.TempJxHome = tempJxHome
	originalKubeCfg, tempKubeCfg, err := cmd.CreateTestKubeConfigDir()
	assert.NoError(t, err)
	o.OriginalKubeCfg = originalKubeCfg
	o.TempKubeCfg = tempKubeCfg

	testOrgNameUUID, err := uuid.NewV4()
	assert.NoError(t, err)
	testOrgName := testOrgNameUUID.String()
	testRepoNameUUID, err := uuid.NewV4()
	assert.NoError(t, err)
	testRepoName := testRepoNameUUID.String()
	devEnvRepoName := fmt.Sprintf("environment-%s-%s-dev", testOrgName, testRepoName)
	fakeRepo := gits.NewFakeRepository(testOrgName, testRepoName)
	devEnvRepo := gits.NewFakeRepository(testOrgName, devEnvRepoName)

	fakeGitProvider := gits.NewFakeProvider(fakeRepo, devEnvRepo)
	fakeGitProvider.User.Username = testOrgName

	var devEnv *jenkinsv1.Environment
	if gitOps {
		devEnv = kube.NewPermanentEnvironmentWithGit("dev", fmt.Sprintf("http://fake.git/%s/%s.git", testOrgName,
			devEnvRepoName))
		devEnv.Spec.Source.URL = devEnvRepo.GitRepo.CloneURL
		devEnv.Spec.Source.Ref = "master"
		o.MockVaultClient = vault_test.NewMockClient()
		pegomock.When(mockFactory.SecretsLocation()).ThenReturn(pegomock.ReturnValue(secrets.VaultLocationKind))
		pegomock.When(mockFactory.CreateSystemVaultClient(pegomock.AnyString())).ThenReturn(pegomock.ReturnValue(o.
			MockVaultClient), pegomock.ReturnValue(nil))
	} else {
		devEnv = kube.NewPermanentEnvironment("dev")
	}
	o.MockHelmer = helm_test.NewMockHelmer()
	installerMock := resources_test.NewMockInstaller()
	cmd.ConfigureTestOptionsWithResources(o.CommonOptions,
		[]runtime.Object{},
		[]runtime.Object{
			devEnv,
		},
		gits.NewGitLocal(),
		fakeGitProvider,
		o.MockHelmer,
		installerMock,
	)

	err = cmd.CreateTestEnvironmentDir(o.CommonOptions)
	assert.NoError(t, err)
	o.ConfigureGitFn = func(dir string, gitInfo *gits.GitRepository, gitter gits.Gitter) error {
		err := gitter.Init(dir)
		if err != nil {
			return err
		}
		// Really we should have a dummy environment chart but for now let's just mock it out as needed
		err = os.MkdirAll(filepath.Join(dir, "templates"), 0700)
		if err != nil {
			return err
		}
		data, err := json.Marshal(devEnv)
		if err != nil {
			return err
		}
		err = ioutil.WriteFile(filepath.Join(dir, "templates", "dev-env.yaml"), data, 0755)
		if err != nil {
			return err
		}
		if appName != "" {
			appName = fmt.Sprintf("%s-%s", "jx-app", appName)
			err = os.MkdirAll(filepath.Join(dir, appName, "templates"), 0700)
			if err != nil {
				return err
			}
			app := jenkinsv1.App{
				ObjectMeta: v1.ObjectMeta{
					Name: appName,
					Labels: map[string]string{
						helm.LabelAppName:    appName,
						helm.LabelAppVersion: "0.0.1",
					},
					Annotations: map[string]string{
						helm.AnnotationAppDescription: "Description",
						helm.AnnotationAppRepository:  "Repository",
					},
				},
			}

			data, err = yaml.Marshal(app)
			if err != nil {
				return err
			}
			err = ioutil.WriteFile(filepath.Join(dir, appName, "templates", "app.yaml"), data, 0755)
			if err != nil {
				return err
			}
		}

		return gitter.AddCommit(dir, "Initial Commit")
	}
	o.FakeGitProvider = fakeGitProvider
	o.DevRepo = fakeRepo
	o.DevEnvRepo = devEnvRepo
	o.OrgName = testOrgName
	o.DevEnv = devEnv
	o.DevEnvRepoInfo = &gits.GitRepository{
		Name: devEnvRepoName,
	}
	return &o
}
