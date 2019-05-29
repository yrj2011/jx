package cmd

import (
	"fmt"
	"github.com/jenkins-x/jx/pkg/jx/cmd/helper"
	"io/ioutil"
	"strings"
	"time"

	"github.com/jenkins-x/jx/pkg/cloud"
	version2 "github.com/jenkins-x/jx/pkg/version"
	"gopkg.in/AlecAivazis/survey.v1"

	"github.com/jenkins-x/jx/pkg/kube/services"

	"github.com/jenkins-x/jx/pkg/cloud/iks"
	"github.com/jenkins-x/jx/pkg/helm"
	"github.com/jenkins-x/jx/pkg/jx/cmd/opts"
	"github.com/jenkins-x/jx/pkg/jx/cmd/templates"
	"github.com/jenkins-x/jx/pkg/kube"
	"github.com/jenkins-x/jx/pkg/log"
	"github.com/jenkins-x/jx/pkg/util"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	rbacv1 "k8s.io/api/rbac/v1"

	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// InitOptions the options for running init
type InitOptions struct {
	*opts.CommonOptions
	Client clientset.Clientset
	Flags  InitFlags
}

// InitFlags the flags for running init
type InitFlags struct {
	Domain                     string
	Provider                   string
	Namespace                  string
	UserClusterRole            string
	TillerClusterRole          string
	IngressClusterRole         string
	TillerNamespace            string
	IngressNamespace           string
	IngressService             string
	IngressDeployment          string
	ExternalIP                 string
	VersionsRepository         string
	VersionsGitRef             string
	DraftClient                bool
	HelmClient                 bool
	Helm3                      bool
	HelmBin                    string
	RecreateExistingDraftRepos bool
	NoTiller                   bool
	RemoteTiller               bool
	GlobalTiller               bool
	SkipIngress                bool
	SkipTiller                 bool
	SkipClusterRole            bool
	OnPremise                  bool
	Http                       bool
	NoGitValidate              bool
	ExternalDNS                bool
}

const (
	optionUsername        = "username"
	optionNamespace       = "namespace"
	optionTillerNamespace = "tiller-namespace"

	// JenkinsBuildPackURL URL of Draft packs for Jenkins X
	JenkinsBuildPackURL = "http://github.com/jenkins-x/draft-packs.git"

	// defaultIngressNamesapce default namesapce fro ingress controller
	defaultIngressNamesapce = "kube-system"
	// defaultIngressServiceName default name for ingress controller service and deployment
	defaultIngressServiceName = "jxing-nginx-ingress-controller"
)

var (
	initLong = templates.LongDesc(`
		This command initializes the connected Kubernetes cluster for Jenkins X platform installation
`)

	initExample = templates.Examples(`
		jx init
`)
)

// NewCmdInit creates a command object for the generic "init" action, which
// primes a Kubernetes cluster so it's ready for Jenkins X to be installed
func NewCmdInit(commonOpts *opts.CommonOptions) *cobra.Command {
	options := &InitOptions{
		CommonOptions: commonOpts,
	}

	cmd := &cobra.Command{
		Use:     "init",
		Short:   "Init Jenkins X",
		Long:    initLong,
		Example: initExample,
		Run: func(cmd *cobra.Command, args []string) {
			options.Cmd = cmd
			options.Args = args
			err := options.Run()
			helper.CheckErr(err)
		},
	}

	cmd.Flags().StringVarP(&options.Flags.Provider, "provider", "", "", "Cloud service providing the Kubernetes cluster.  Supported providers: "+KubernetesProviderOptions())
	cmd.Flags().StringVarP(&options.Flags.Namespace, optionNamespace, "", "jx", "The namespace the Jenkins X platform should be installed into")
	options.addInitFlags(cmd)
	return cmd
}

func (o *InitOptions) addInitFlags(cmd *cobra.Command) {
	o.addIngressFlags(cmd)

	cmd.Flags().StringVarP(&o.Username, optionUsername, "", "", "The Kubernetes username used to initialise helm. Usually your email address for your Kubernetes account")
	cmd.Flags().StringVarP(&o.Flags.UserClusterRole, "user-cluster-role", "", "cluster-admin", "The cluster role for the current user to be able to administer helm")
	cmd.Flags().StringVarP(&o.Flags.TillerClusterRole, "tiller-cluster-role", "", opts.DefaultTillerRole, "The cluster role for Helm's tiller")
	cmd.Flags().StringVarP(&o.Flags.TillerNamespace, optionTillerNamespace, "", opts.DefaultTillerNamesapce, "The namespace for the Tiller when using a global tiller")
	cmd.Flags().BoolVarP(&o.Flags.DraftClient, "draft-client-only", "", false, "Only install draft client")
	cmd.Flags().BoolVarP(&o.Flags.HelmClient, "helm-client-only", "", opts.DefaultOnlyHelmClient, "Only install helm client")
	cmd.Flags().BoolVarP(&o.Flags.RecreateExistingDraftRepos, "recreate-existing-draft-repos", "", false, "Delete existing helm repos used by Jenkins X under ~/draft/packs")
	cmd.Flags().BoolVarP(&o.Flags.GlobalTiller, "global-tiller", "", opts.DefaultGlobalTiller, "Whether or not to use a cluster global tiller")
	cmd.Flags().BoolVarP(&o.Flags.RemoteTiller, "remote-tiller", "", opts.DefaultRemoteTiller, "If enabled and we are using tiller for helm then run tiller remotely in the kubernetes cluster. Otherwise we run the tiller process locally.")
	cmd.Flags().BoolVarP(&o.Flags.NoTiller, "no-tiller", "", true, "Whether to disable the use of tiller with helm. If disabled we use 'helm template' to generate the YAML from helm charts then we use 'kubectl apply' to install it to avoid using tiller completely.")
	cmd.Flags().BoolVarP(&o.Flags.SkipTiller, "skip-setup-tiller", "", opts.DefaultSkipTiller, "Don't setup the Helm Tiller service - lets use whatever tiller is already setup for us.")
	cmd.Flags().BoolVarP(&o.Flags.SkipClusterRole, "skip-cluster-role", "", opts.DefaultSkipClusterRole, "Don't enable cluster admin role for user")

	cmd.Flags().BoolVarP(&o.Flags.Helm3, "helm3", "", opts.DefaultHelm3, "Use helm3 to install Jenkins X which does not use Tiller")
}

func (o *InitOptions) addIngressFlags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&o.Flags.Domain, "domain", "", "", "Domain to expose ingress endpoints.  Example: jenkinsx.io")
	cmd.Flags().StringVarP(&o.Flags.IngressClusterRole, "ingress-cluster-role", "", "cluster-admin", "The cluster role for the Ingress controller")
	cmd.Flags().StringVarP(&o.Flags.IngressNamespace, "ingress-namespace", "", defaultIngressNamesapce, "The namespace for the Ingress controller")
	cmd.Flags().StringVarP(&o.Flags.IngressService, "ingress-service", "", defaultIngressServiceName, "The name of the Ingress controller Service")
	cmd.Flags().StringVarP(&o.Flags.IngressDeployment, "ingress-deployment", "", defaultIngressServiceName, "The name of the Ingress controller Deployment")
	cmd.Flags().StringVarP(&o.Flags.ExternalIP, "external-ip", "", "", "The external IP used to access ingress endpoints from outside the Kubernetes cluster. For bare metal on premise clusters this is often the IP of the Kubernetes master. For cloud installations this is often the external IP of the ingress LoadBalancer.")
	cmd.Flags().BoolVarP(&o.Flags.SkipIngress, "skip-ingress", "", false, "Skips the installation of ingress controller. Note that a ingress controller must already be installed into the cluster in order for the installation to succeed")
	cmd.Flags().BoolVarP(&o.Flags.OnPremise, "on-premise", "", false, "If installing on an on premise cluster then lets default the 'external-ip' to be the Kubernetes master IP address")
}

func (o *InitOptions) checkOptions() error {
	if o.Flags.Helm3 {
		o.Flags.SkipTiller = true
	}

	if !o.Flags.SkipTiller {
		tillerNamespace := o.Flags.TillerNamespace
		if o.Flags.GlobalTiller {
			if tillerNamespace == "" {
				return util.MissingOption(optionTillerNamespace)
			}
		} else {
			ns := o.Flags.Namespace
			if ns == "" {
				_, curNs, err := o.KubeClientAndNamespace()
				if err != nil {
					return err
				}
				ns = curNs
			}
			if ns == "" {
				return util.MissingOption(optionNamespace)
			}
			o.Flags.Namespace = ns
		}
	}

	if o.Flags.SkipIngress {
		if o.Flags.ExternalIP == "" {
			log.Warnf("Expecting ingress controller to be installed in %s\n",
				util.ColorInfo(fmt.Sprintf("%s/%s", o.Flags.IngressNamespace, o.Flags.IngressDeployment)))
		}
	}

	return nil
}

// Run performs initialization
func (o *InitOptions) Run() error {
	var err error
	if !o.Flags.RemoteTiller || o.Flags.NoTiller {
		o.Flags.HelmClient = true
		o.Flags.SkipTiller = true
		o.Flags.GlobalTiller = false
	}
	o.Flags.Provider, err = o.GetCloudProvider(o.Flags.Provider)
	if err != nil {
		return err
	}

	if !o.Flags.NoGitValidate {
		err = o.validateGit()
		if err != nil {
			return err
		}
	}

	err = o.enableClusterAdminRole()
	if err != nil {
		return err
	}

	// So a user doesn't need to specify ingress options if provider is ICP: we will use ICP's own ingress controller
	// and by default, the tiller namespace "jx"
	if o.Flags.Provider == cloud.ICP {
		o.configureForICP()
	}

	// Needs to be done early as is an ingress availablility is an indicator of cluster readyness
	if o.Flags.Provider == cloud.IKS {
		err = o.initIKSIngress()
		if err != nil {
			return err
		}
	}
	// setup the configuration for helm init
	err = o.checkOptions()
	if err != nil {
		return err
	}
	cfg := opts.InitHelmConfig{
		Namespace:       o.Flags.Namespace,
		OnlyHelmClient:  o.Flags.HelmClient,
		Helm3:           o.Flags.Helm3,
		SkipTiller:      o.Flags.SkipTiller,
		GlobalTiller:    o.Flags.GlobalTiller,
		TillerNamespace: o.Flags.TillerNamespace,
		TillerRole:      o.Flags.TillerClusterRole,
	}
	// helm init, this has been seen to fail intermittently on public clouds, so let's retry a couple of times
	err = o.Retry(3, 2*time.Second, func() (err error) {
		err = o.InitHelm(cfg)
		return
	})

	if err != nil {
		log.Fatalf("helm init failed: %v", err)
		return err
	}

	// draft init
	_, _, err = o.InitBuildPacks()
	if err != nil {
		log.Fatalf("initialise build packs failed: %v", err)
		return err
	}

	// install ingress
	if !o.Flags.SkipIngress {
		err = o.initIngress()
		if err != nil {
			log.Fatalf("ingress init failed: %v", err)
			return err
		}
	}

	return nil
}

func (o *InitOptions) enableClusterAdminRole() error {
	if o.Flags.SkipClusterRole {
		return nil
	}
	client, err := o.KubeClient()
	if err != nil {
		return err
	}

	if o.Username == "" {
		o.Username, err = o.GetClusterUserName()
		if err != nil {
			return err
		}
	}
	if o.Username == "" {
		return util.MissingOption(optionUsername)
	}
	userFormatted := kube.ToValidName(o.Username)

	clusterRoleBindingName := kube.ToValidName(userFormatted + "-" + o.Flags.UserClusterRole + "-binding")

	clusterRoleBindingInterface := client.RbacV1().ClusterRoleBindings()
	clusterRoleBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterRoleBindingName,
		},
		Subjects: []rbacv1.Subject{
			{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "User",
				Name:     o.Username,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     o.Flags.UserClusterRole,
		},
	}

	return o.Retry(3, 10*time.Second, func() (err error) {
		_, err = clusterRoleBindingInterface.Get(clusterRoleBindingName, metav1.GetOptions{})
		if err != nil {
			log.Infof("Trying to create ClusterRoleBinding %s for role: %s for user %s\n %v\n", clusterRoleBindingName, o.Flags.UserClusterRole, o.Username, err)

			//args := []string{"create", "clusterrolebinding", clusterRoleBindingName, "--clusterrole=" + role, "--user=" + user}

			_, err = clusterRoleBindingInterface.Create(clusterRoleBinding)
			if err == nil {
				log.Infof("Created ClusterRoleBinding %s\n", clusterRoleBindingName)
			}
		}
		return err
	})
}

func (o *InitOptions) configureForICP() {
	icpDefaultTillerNS := "default"
	icpDefaultNS := "jx"

	log.Info("")
	log.Info(util.ColorInfo("IBM Cloud Private installation of Jenkins X"))
	log.Info("Configuring Jenkins X options for IBM Cloud Private: ensure your Kubernetes context is already " +
		"configured to point to the cluster jx will be installed into.")
	log.Info("")

	log.Info(util.ColorInfo("Permitting image repositories to be used"))
	log.Info("If you have a clusterimagepolicy, ensure that this policy permits pulling from the following additional repositories: " +
		"the scope of which can be narrowed down once you are sure only images from certain repositories are being used:")
	log.Info("- name: docker.io/* \n" +
		"- name: gcr.io/* \n" +
		"- name: quay.io/* \n" +
		"- name: k8s.gcr.io/* \n" +
		"- name: <your ICP cluster name>:8500/* \n")

	log.Info(util.ColorInfo("IBM Cloud Private defaults"))
	log.Info("By default, with IBM Cloud Private the Tiller namespace for jx will be \"" + icpDefaultTillerNS + "\" and the namespace " +
		"where Jenkins X resources will be installed into is \"" + icpDefaultNS + "\".")
	log.Info("")

	log.Info(util.ColorInfo("Using the IBM Cloud Private Docker registry"))
	log.Info("To use the IBM Cloud Private Docker registry, when environments (namespaces) are created, " +
		"create a Docker registry secret and patch the default service account in the created namespace to use the secret, adding it as an ImagePullSecret. " +
		"This is required so that pods in the created namespace can pull images from the registry.")
	log.Info("")

	o.Flags.IngressNamespace = "kube-system"
	o.Flags.IngressDeployment = "default-backend"
	o.Flags.IngressService = "default-backend"
	o.Flags.TillerNamespace = icpDefaultTillerNS
	o.Flags.Namespace = icpDefaultNS

	surveyOpts := survey.WithStdio(o.In, o.Out, o.Err)
	ICPExternalIP := ""
	ICPDomain := ""

	if !(o.BatchMode) {
		if o.Flags.ExternalIP != "" {
			log.Info("An external IP has already been specified: otherwise you will be prompted for one to use")
			return
		}

		prompt := &survey.Input{
			Message: "Provide the external IP Jenkins X should use: typically your IBM Cloud Private proxy node IP address",
			Default: "", // Would be useful to set this as the public IP automatically
			Help:    "",
		}
		survey.AskOne(prompt, &ICPExternalIP, nil, surveyOpts)

		o.Flags.ExternalIP = ICPExternalIP

		prompt = &survey.Input{
			Message: "Provide the domain Jenkins X should be available at: typically your IBM Cloud Private proxy node IP address but with a domain added to the end",
			Default: ICPExternalIP + ".nip.io",
			Help:    "",
		}

		survey.AskOne(prompt, &ICPDomain, nil, surveyOpts)

		o.Flags.Domain = ICPDomain
	}
}

func (o *InitOptions) initIKSIngress() error {
	log.Info("Wait for Ingress controller to be injected into IBM Kubernetes Service Cluster")
	kubeClient, err := o.KubeClient()
	if err != nil {
		return err
	}

	ingressNamespace := o.Flags.IngressNamespace

	clusterID, err := iks.GetKubeClusterID(kubeClient)
	if err != nil || clusterID == "" {
		clusterID, err = iks.GetClusterID()
		if err != nil {
			return err
		}
	}
	o.Flags.IngressDeployment = "public-cr" + strings.ToLower(clusterID) + "-alb1"
	o.Flags.IngressService = "public-cr" + strings.ToLower(clusterID) + "-alb1"

	return kube.WaitForDeploymentToBeCreatedAndReady(kubeClient, o.Flags.IngressDeployment, ingressNamespace, 30*time.Minute)
}

func (o *InitOptions) initIngress() error {
	surveyOpts := survey.WithStdio(o.In, o.Out, o.Err)
	client, err := o.KubeClient()
	if err != nil {
		return err
	}

	ingressNamespace := o.Flags.IngressNamespace

	err = kube.EnsureNamespaceCreated(client, ingressNamespace, map[string]string{"jenkins.io/kind": "ingress"}, nil)
	if err != nil {
		return fmt.Errorf("Failed to ensure the ingress namespace %s is created: %s\nIs this an RBAC issue on your cluster?", ingressNamespace, err)
	}

	currentContext, err := o.GetCommandOutput("", "kubectl", "config", "current-context")
	if err != nil {
		return err
	}
	if currentContext == "minikube" {
		if o.Flags.Provider == "" {
			o.Flags.Provider = cloud.MINIKUBE
		}
		addons, err := o.GetCommandOutput("", "minikube", "addons", "list")
		if err != nil {
			return err
		}
		if strings.Contains(addons, "- ingress: enabled") {
			log.Success("nginx ingress controller already enabled")
			return nil
		}
		err = o.RunCommand("minikube", "addons", "enable", "ingress")
		if err != nil {
			return err
		}
		log.Success("nginx ingress controller now enabled on Minikube")
		return nil

	}

	if isOpenShiftProvider(o.Flags.Provider) {
		log.Info("Not installing ingress as using OpenShift which uses Route and its own mechanism of ingress")
		return nil
	}

	if o.Flags.Provider == cloud.ALIBABA {
		if o.Flags.IngressDeployment == defaultIngressServiceName {
			o.Flags.IngressDeployment = "nginx-ingress-controller"
		}
		if o.Flags.IngressService == defaultIngressServiceName {
			o.Flags.IngressService = "nginx-ingress-lb"
		}
	}

	podCount, err := kube.DeploymentPodCount(client, o.Flags.IngressDeployment, ingressNamespace)
	if podCount == 0 {
		installIngressController := false
		if o.BatchMode {
			installIngressController = true
		} else {
			prompt := &survey.Confirm{
				Message: "No existing ingress controller found in the " + ingressNamespace + " namespace, shall we install one?",
				Default: true,
				Help:    "An ingress controller works with an external loadbalancer so you can access Jenkins X and your applications",
			}
			survey.AskOne(prompt, &installIngressController, nil, surveyOpts)
		}

		if !installIngressController {
			return nil
		}

		values := []string{"rbac.create=true", fmt.Sprintf("controller.extraArgs.publish-service=%s/%s", ingressNamespace, defaultIngressServiceName) /*,"rbac.serviceAccountName="+ingressServiceAccount*/}
		valuesFiles := []string{}
		valuesFiles, err = helm.AppendMyValues(valuesFiles)
		if err != nil {
			return errors.Wrap(err, "failed to append the myvalues file")
		}
		if o.Flags.Provider == cloud.AWS || o.Flags.Provider == cloud.EKS {
			// For EKS enable both ports for NLBs to be able to use TLS on Nginx ingresses
			// Fix for http://github.com/jenkins-x/jx/issues/3079
			enableHTTP := "true"
			enableHTTPS := "true"

			// For AWS we can only enable one port for NLBs right now?
			if o.Flags.Provider == cloud.AWS {
				enableHTTP = "false"
				enableHTTPS = "true"
				if o.Flags.Http {
					enableHTTP = "true"
					enableHTTPS = "false"
				}
			}
			yamlText := `---
rbac:
 create: true

controller:
 service:
   annotations:
     service.beta.kubernetes.io/aws-load-balancer-type: nlb
   enableHttp: ` + enableHTTP + `
   enableHttps: ` + enableHTTPS + `
`

			f, err := ioutil.TempFile("", "ing-values-")
			if err != nil {
				return err
			}
			fileName := f.Name()
			err = ioutil.WriteFile(fileName, []byte(yamlText), util.DefaultWritePermissions)
			if err != nil {
				return err
			}
			log.Infof("Using helm values file: %s\n", fileName)
			valuesFiles = append(valuesFiles, fileName)
		}
		chartName := "stable/nginx-ingress"

		version, err := o.GetVersionNumber(version2.KindChart, chartName, o.Flags.VersionsRepository, o.Flags.VersionsGitRef)
		if err != nil {
			return errors.Wrapf(err, "failed to load version of chart %s", chartName)
		}

		i := 0
		for {
			log.Infof("Installing using helm binary: %s\n", util.ColorInfo(o.Helm().HelmBinary()))
			helmOptions := helm.InstallChartOptions{
				Chart:       chartName,
				ReleaseName: "jxing",
				Version:     version,
				Ns:          ingressNamespace,
				SetValues:   values,
				ValueFiles:  valuesFiles,
			}
			err = o.InstallChartWithOptions(helmOptions)
			if err != nil {
				if i >= 3 {
					log.Errorf("Failed to install ingress chart: %s", err)
					break
				}
				i++
				time.Sleep(time.Second)
			} else {
				break
			}
		}
		err = kube.WaitForDeploymentToBeReady(client, o.Flags.IngressDeployment, ingressNamespace, 10*time.Minute)
		if err != nil {
			return err
		}

	} else {
		log.Info("existing ingress controller found, no need to install a new one\n")
	}

	if o.Flags.Provider != cloud.MINIKUBE && o.Flags.Provider != cloud.MINISHIFT && o.Flags.Provider != cloud.OPENSHIFT {

		log.Infof("Waiting for external loadbalancer to be created and update the nginx-ingress-controller service in %s namespace\n", ingressNamespace)

		if o.Flags.Provider == cloud.OKE {
			log.Infof("Note: this loadbalancer will fail to be provisioned if you have insufficient quotas, this can happen easily on a OCI free account\n")
		}

		if o.Flags.Provider == cloud.GKE {
			log.Infof("Note: this loadbalancer will fail to be provisioned if you have insufficient quotas, this can happen easily on a GKE free account. To view quotas run: %s\n", util.ColorInfo("gcloud compute project-info describe"))
		}

		externalIP := o.Flags.ExternalIP
		if externalIP == "" && o.Flags.OnPremise {
			// lets find the Kubernetes master IP
			config, _, err := o.Kube().LoadConfig()
			if err != nil {
				return err
			}
			if config == nil {
				return errors.New("empty kubernetes config")
			}
			host := kube.CurrentServer(config)
			if host == "" {
				log.Warnf("No API server host is defined in the local kube config!\n")
			} else {
				externalIP, err = util.UrlHostNameWithoutPort(host)
				if err != nil {
					return fmt.Errorf("Could not parse Kubernetes master URI: %s as got: %s\nTry specifying the external IP address directly via: --external-ip", host, err)
				}
			}
		}

		if externalIP == "" {
			err = services.WaitForExternalIP(client, o.Flags.IngressService, ingressNamespace, 10*time.Minute)
			if err != nil {
				return err
			}
			log.Infof("External loadbalancer created\n")
		} else {
			log.Infof("Using external IP: %s\n", util.ColorInfo(externalIP))
		}

		o.Flags.Domain, err = o.GetDomain(client, o.Flags.Domain, o.Flags.Provider, ingressNamespace, o.Flags.IngressService, externalIP)
		o.CommonOptions.Domain = o.Flags.Domain
		if err != nil {
			return err
		}
	}

	log.Success("nginx ingress controller installed and configured")

	return nil
}

func (o *InitOptions) ingressNamespace() string {
	ingressNamespace := "kube-system"
	if !o.Flags.GlobalTiller {
		ingressNamespace = o.Flags.Namespace
	}
	return ingressNamespace
}

// validateGit validates that git is configured correctly
func (o *InitOptions) validateGit() error {
	// lets ignore errors which indicate no value set
	userName, _ := o.Git().Username("")
	userEmail, _ := o.Git().Email("")
	var err error
	if userName == "" {
		if !o.BatchMode {
			userName, err = util.PickValue("Please enter the name you wish to use with git: ", "", true, "", o.In, o.Out, o.Err)
			if err != nil {
				return err
			}
		}
		if userName == "" {
			return fmt.Errorf("No Git user.name is defined. Please run the command: git config --global --add user.name \"MyName\"")
		}
		err = o.Git().SetUsername("", userName)
		if err != nil {
			return err
		}
	}
	if userEmail == "" {
		if !o.BatchMode {
			userEmail, err = util.PickValue("Please enter the email address you wish to use with git: ", "", true, "", o.In, o.Out, o.Err)
			if err != nil {
				return err
			}
		}
		if userEmail == "" {
			return fmt.Errorf("No Git user.email is defined. Please run the command: git config --global --add user.email \"me@acme.com\"")
		}
		err = o.Git().SetEmail("", userEmail)
		if err != nil {
			return err
		}
	}
	log.Infof("Git configured for user: %s and email %s\n", util.ColorInfo(userName), util.ColorInfo(userEmail))
	return nil
}

// HelmBinary returns name of configured Helm binary
func (o *InitOptions) HelmBinary() string {
	if o.Flags.Helm3 {
		return "helm3"
	}
	testHelmBin := o.Flags.HelmBin
	if testHelmBin != "" {
		return testHelmBin
	}
	return "helm"
}
