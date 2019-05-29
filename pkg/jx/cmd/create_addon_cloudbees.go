package cmd

import (
	"fmt"
	"github.com/jenkins-x/jx/pkg/jx/cmd/helper"
	"strings"
	"time"

	survey "gopkg.in/AlecAivazis/survey.v1"

	"github.com/jenkins-x/jx/pkg/util"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/jenkins-x/jx/pkg/jx/cmd/opts"
	"github.com/jenkins-x/jx/pkg/jx/cmd/templates"
	"github.com/jenkins-x/jx/pkg/kube"
	"github.com/jenkins-x/jx/pkg/kube/pki"
	"github.com/jenkins-x/jx/pkg/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	defaultCloudBeesReleaseName = "cb"
	defaultCloudBeesNamespace   = "jx"
	coreRepoName                = "cb"
	cbServiceName               = "cb-core"
	coreRepoUrl                 = "http://chartmuseum.jx.charts-demo.cloudbees.com"
	defaultCloudBeesVersion     = ""
)

var (
	CreateAddonCloudBeesLong = templates.LongDesc(`
		Creates the CloudBees app for Kubernetes addon

		CloudBees app for Kubernetes provides unified Continuous Delivery Environment console to make it easier to do CI/CD and Environments across a number of microservices and teams

		For more information please see [http://www.cloudbees.com/blog/want-help-build-cloudbees-kubernetes-jenkins-x](http://www.cloudbees.com/blog/want-help-build-cloudbees-kubernetes-jenkins-x)
`)

	CreateAddonCloudBeesExample = templates.Examples(`
		# Create the cloudbees addon 
		jx create addon cloudbees
	`)
)

// CreateAddonCloudBeesOptions the options for the create spring command
type CreateAddonCloudBeesOptions struct {
	CreateAddonOptions
	Sso         bool
	DefaultRole string
	Basic       bool
	Password    string
}

// NewCmdCreateAddonCloudBees creates a command object for the "create" command
func NewCmdCreateAddonCloudBees(commonOpts *opts.CommonOptions) *cobra.Command {
	options := &CreateAddonCloudBeesOptions{
		CreateAddonOptions: CreateAddonOptions{
			CreateOptions: CreateOptions{
				CommonOptions: commonOpts,
			},
		},
	}

	cmd := &cobra.Command{
		Use:     "cloudbees",
		Short:   "Create the CloudBees app for Kubernetes (a web console for working with CI/CD, Environments and GitOps)",
		Aliases: []string{"cloudbee", "cb", "core", "kubecd"},
		Long:    CreateAddonCloudBeesLong,
		Example: CreateAddonCloudBeesExample,
		Run: func(cmd *cobra.Command, args []string) {
			options.Cmd = cmd
			options.Args = args
			err := options.Run()
			helper.CheckErr(err)
		},
	}

	cmd.Flags().BoolVarP(&options.Sso, "sso", "", false, "Enable single sign-on")
	cmd.Flags().StringVarP(&options.DefaultRole, "default-role", "", "", "The default role to apply to new users. Defaults to no role and applies to the admin namespace only")
	cmd.Flags().BoolVarP(&options.Basic, "basic", "", false, "Enable basic auth")
	cmd.Flags().StringVarP(&options.Password, "password", "p", "", "Password to access UI when using basic auth.  Defaults to default Jenkins X admin password.")
	options.addFlags(cmd, defaultCloudBeesNamespace, defaultCloudBeesReleaseName, defaultCloudBeesVersion)
	return cmd
}

// Run implements the command
func (o *CreateAddonCloudBeesOptions) Run() error {

	if o.Sso == false && o.Basic == false {
		return fmt.Errorf("please use --sso or --basic flag")
	}
	if o.Sso == false && o.DefaultRole != "" {
		return fmt.Errorf("--default-role can not be used in conjunction with --basic flag")
	}

	surveyOpts := survey.WithStdio(o.In, o.Out, o.Err)
	client, err := o.KubeClient()
	if err != nil {
		return err
	}

	// check if Helm repo is missing, the repo is authenticated and includes username/password so check with dummy values
	// first as we wont need to prompt for username password if the host part of the URL matches an existing repo
	missing, _, err := o.Helm().IsRepoMissing(coreRepoUrl)
	if err != nil {
		return err
	}

	if missing {
		log.Infof(`
You will need your username and password to install this addon while it is in preview.
To register to get your username/password to to: %s

`, util.ColorInfo("http://pages.cloudbees.com/K8s"))

		username := ""
		prompt := &survey.Input{
			Message: "CloudBees Preview username",
			Help:    "CloudBees is in private preview which requires a username / password for installation",
		}
		survey.AskOne(prompt, &username, nil, surveyOpts)

		password := ""
		passPrompt := &survey.Password{
			Message: "CloudBees Preview password",
			Help:    "CloudBees is in private preview which requires a username / password for installation",
		}
		survey.AskOne(passPrompt, &password, nil, surveyOpts)

		_, err := o.AddHelmBinaryRepoIfMissing(coreRepoUrl, coreRepoName, username, password)
		if err != nil {
			return err
		}
	}

	if o.Sso {
		log.Infof("Configuring %s...\n", util.ColorInfo("single sign-on"))
		_, devNamespace, err := o.KubeClientAndDevNamespace()
		if err != nil {
			return errors.Wrap(err, "getting the dev namespace")
		}
		ingressConfig, err := kube.GetIngressConfig(client, devNamespace)
		if err != nil {
			return errors.Wrap(err, "retrieving existing ingress configuration")
		}
		domain, err := util.PickValue("Domain:", ingressConfig.Domain, true, "", o.In, o.Out, o.Err)
		if err != nil {
			return errors.Wrap(err, "reading domain")
		}

		dexURL, err := util.PickValue("Dex URL:", fmt.Sprintf("http://dex.sso.%s", ingressConfig.Domain), true, "", o.In, o.Out, o.Err)
		if err != nil {
			return errors.Wrap(err, "reading dex URL")
		}

		// Strip the trailing slash automatically
		dexURL = strings.TrimSuffix(dexURL, "/")

		err = o.EnsureCertManager()
		if err != nil {
			return errors.Wrap(err, "ensuring cert-manager is installed")
		}

		certClient, err := o.CertManagerClient()
		if err != nil {
			return errors.Wrap(err, "creating cert-manager client")
		}
		ingressConfig.TLS = true
		ingressConfig.Issuer = pki.CertManagerIssuerProd
		err = pki.CreateCertManagerResources(certClient, o.Namespace, ingressConfig)
		if err != nil {
			return errors.Wrap(err, "creating cert-manager issuer")
		}

		values := []string{
			"sso.create=true",
			"sso.oidcIssuerUrl=" + dexURL,
			"sso.domain=" + domain,
			"sso.certIssuerName=" + pki.CertManagerIssuerProd}

		if len(o.SetValues) > 0 {
			o.SetValues = o.SetValues + "," + strings.Join(values, ",")
		} else {
			o.SetValues = strings.Join(values, ",")
		}
		if o.DefaultRole != "" {
			if len(o.SetValues) > 0 {
				o.SetValues = o.SetValues + "," + "defaultRole=" + o.DefaultRole
			} else {
				o.SetValues = "defaultRole=" + o.DefaultRole
			}
		}
	} else {
		// Disable SSO for basic auth
		o.SetValues = strings.Join([]string{"sso.create=false"}, ",")
	}

	err = o.CreateAddon("cb")
	if err != nil {
		return err
	}

	if o.Sso {
		// wait for cert to be issued
		certName := pki.CertSecretPrefix + "core"
		log.Infof("Waiting for cert: %s...\n", util.ColorInfo(certName))
		certMngrClient, err := o.CertManagerClient()
		if err != nil {
			return errors.Wrap(err, "creating the cert-manager client")
		}
		err = pki.WaitCertificateIssuedReady(certMngrClient, certName, o.Namespace, 3*time.Minute)
		if err != nil {
			return err // this is already wrapped by the previous call
		}
		log.Infof("Ready Cert: %s\n", util.ColorInfo(certName))
	}

	if o.Basic {
		_, devNamespace, err := o.KubeClientAndDevNamespace()
		if err != nil {
			return errors.Wrap(err, "getting the team's dev namespace")
		}

		if o.Password == "" {
			o.Password, err = o.GetDefaultAdminPassword(devNamespace)
			if err != nil {
				return err
			}
		}

		_, currentNamespace, err := o.KubeClientAndNamespace()
		if err != nil {
			return errors.Wrap(err, "getting the current namesapce")
		}

		svc, err := client.CoreV1().Services(currentNamespace).Get(cbServiceName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		annotationsUpdated := false
		if svc.Annotations[kube.AnnotationExpose] == "" {
			svc.Annotations[kube.AnnotationExpose] = "true"
			annotationsUpdated = true
		}
		if svc.Annotations[kube.AnnotationIngress] == "" {
			svc.Annotations[kube.AnnotationIngress] = "nginx.ingress.kubernetes.io/auth-type: basic\nnginx.ingress.kubernetes.io/auth-secret: jx-basic-auth"
			annotationsUpdated = true
		}
		if annotationsUpdated {
			svc, err = client.CoreV1().Services(o.Namespace).Update(svc)
			if err != nil {
				return fmt.Errorf("failed to update service %s/%s", o.Namespace, cbServiceName)
			}
		}

		log.Infof("using exposecontroller config from dev namespace %s\n", devNamespace)
		log.Infof("target namespace %s\n", o.Namespace)

		// create the ingress rule
		err = o.Expose(devNamespace, o.Namespace, o.Password)
		if err != nil {
			return err
		}
	}

	log.Infof("Addon installed successfully.\n\n  %s Open the app in a browser\n\n", util.ColorInfo("jx cloudbees"))

	return nil
}
