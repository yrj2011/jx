package opts

import (
	"fmt"
	"net/url"
	"time"

	"github.com/jenkins-x/jx/pkg/auth"
	"github.com/jenkins-x/jx/pkg/gits"
	"github.com/jenkins-x/jx/pkg/jenkins"
	"github.com/jenkins-x/jx/pkg/kube"
	"github.com/jenkins-x/jx/pkg/log"
	"github.com/jenkins-x/jx/pkg/util"
)

// ImportProject imports a MultiBranchProject into Jenkins for the given git URL
func (o *CommonOptions) ImportProject(gitURL string, dir string, jenkinsfile string, branchPattern, credentials string, failIfExists bool, gitProvider gits.GitProvider, authConfigSvc auth.ConfigService, isEnvironment bool, batchMode bool) error {
	jenk, err := o.JenkinsClient()
	if err != nil {
		return err
	}

	secrets, err := o.LoadPipelineSecrets(kube.ValueKindGit, "")
	if err != nil {
		return err
	}

	if gitURL == "" {
		return fmt.Errorf("No Git repository URL found!")
	}

	gitInfo, err := gits.ParseGitURL(gitURL)
	if err != nil {
		return fmt.Errorf("Failed to parse Git URL %s due to: %s", gitURL, err)
	}

	if branchPattern == "" {
		patterns, err := o.TeamBranchPatterns()
		if err != nil {
			return err
		}
		branchPattern = patterns.DefaultBranchPattern
	}
	if branchPattern == "" {
		log.Infof("Querying if the repo is a fork at %s with kind %s\n", gitProvider.ServerURL(), gitProvider.Kind())
		fork, err := o.Git().IsFork(dir)
		if err != nil {
			return fmt.Errorf("No branch pattern specified and could not determine if the Git repository is a fork: %s", err)
		}
		if fork {
			// lets figure out which branches to enable for a fork
			branch, err := o.Git().Branch(dir)
			if err != nil {
				return fmt.Errorf("Failed to get current branch in dir %s: %s", dir, err)
			}
			if branch == "" {
				return fmt.Errorf("Failed to get current branch in dir %s", dir)
			}
			// TODO do we need to scape any wacky characters to make it a valid branch pattern?
			branchPattern = branch
			log.Infof("No branch pattern specified and this repository appears to be a fork so defaulting the branch patterns to run CI/CD on to: %s\n", branchPattern)
		} else {
			log.Infof("jenkins log :" + gitProvider.Kind())
			branchPattern = jenkins.BranchPattern(gitProvider.Kind())
		}
	}

	createCredential := true
	if credentials == "" {
		// lets try find the credentials from the secrets
		credentials = FindGitCredentials(gitProvider, secrets)
		if credentials != "" {
			createCredential = false
		}
	}
	if credentials == "" {
		// TODO lets prompt the user to add a new credential for the Git provider...
		config := authConfigSvc.Config()
		u := gitInfo.HostURL()
		server := config.GetOrCreateServer(u)
		if len(server.Users) == 0 {
			// lets check if the host was used in `~/.jx/gitAuth.yaml` instead of URL
			s2 := config.GetOrCreateServer(gitInfo.Host)
			if s2 != nil && len(s2.Users) > 0 {
				server = s2
				u = gitInfo.Host
			}
		}
		user, err := o.PickPipelineUserAuth(config, server)
		if err != nil {
			return err
		}
		if user.Username == "" {
			return fmt.Errorf("Could not find a username for Git server %s", u)
		}

		credentials, err = o.UpdatePipelineGitCredentialsSecret(server, user)
		if err != nil {
			return err
		}

		if credentials == "" {
			return fmt.Errorf("Failed to find the created pipeline secret for the server %s", server.URL)
		} else {
			createCredential = false
		}
	}
	if createCredential {
		_, err = jenk.GetCredential(credentials)
		if err != nil {
			config := authConfigSvc.Config()
			u := gitInfo.HostURL()
			server := config.GetOrCreateServer(u)
			if len(server.Users) == 0 {
				// lets check if the host was used in `~/.jx/gitAuth.yaml` instead of URL
				s2 := config.GetOrCreateServer(gitInfo.Host)
				if s2 != nil && len(s2.Users) > 0 {
					server = s2
					u = gitInfo.Host
				}
			}
			user, err := config.PickServerUserAuth(server, "user name for the Jenkins Pipeline", batchMode, "", o.In, o.Out, o.Err)
			if err != nil {
				return err
			}
			if user.Username == "" {
				return fmt.Errorf("Could not find a username for Git server %s", u)
			}
			err = jenk.CreateCredential(credentials, user.Username, user.ApiToken)

			if err != nil {
				return fmt.Errorf("error creating Jenkins credential %s at %s %v", credentials, jenk.BaseURL(), err)
			}
			log.Infof("Created credential %s for host %s user %s\n", util.ColorInfo(credentials), util.ColorInfo(u), util.ColorInfo(user.Username))
		}
	}
	org := gitInfo.Organisation
	err = o.Retry(10, time.Second*10, func() error {
		folder, err := jenk.GetJob(org)
		if err != nil {
			// could not find folder so lets try create it
			jobUrl := util.UrlJoin(jenk.BaseURL(), jenk.GetJobURLPath(org))
			log.Infof("jenkins log :" + jobUrl + ",org:" + org)
			folderXML := jenkins.CreateFolderXML(jobUrl, org)
			err = jenk.CreateJobWithXML(folderXML, org)
			if err != nil {
				return fmt.Errorf("Failed to create the %s folder in Jenkins: %s", org, err)
			}
		} else {
			c := folder.Class
			if c != "com.cloudbees.hudson.plugins.folder.Folder" {
				log.Warnf("Warning the folder %s is of class %s", org, c)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	err = o.Retry(10, time.Second*10, func() error {

		projectXml := jenkins.CreateMultiBranchProjectXml(gitInfo, gitProvider, credentials, branchPattern, jenkinsfile)
		log.Infof("jenkins log projectXml :" + projectXml)
		jobName := gitInfo.Name
		job, err := jenk.GetJobByPath(org, jobName)
		if err == nil {
			if failIfExists {
				return fmt.Errorf("Job already exists in Jenkins at %s", job.Url)
			} else {
				log.Infof("Job already exists in Jenkins at %s\n", job.Url)
			}
		} else {
			log.Infof("jenkins log projectXml :" + projectXml + ",org:" + org + ",jobName:" + jobName)
			err = jenk.CreateFolderJobWithXML(projectXml, org, jobName)
			if err != nil {
				return fmt.Errorf("Failed to create MultiBranchProject job %s in folder %s due to: %s", jobName, org, err)
			}
			job, err = jenk.GetJobByPath(org, jobName)
			if err != nil {
				return fmt.Errorf("Failed to find the MultiBranchProject job %s in folder %s due to: %s", jobName, org, err)
			}
			log.Infof("Created Jenkins Project: %s\n", util.ColorInfo(job.Url))
			o.LogImportedProject(isEnvironment, gitInfo)

			params := url.Values{}
			log.Infof("jenkins log projectXml 2 :" + projectXml + ",org:" + org + ",jobName:" + jobName)
			err = jenk.Build(job, params)
			if err != nil {
				return fmt.Errorf("Failed to trigger job %s due to %s", job.Url, err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// register the webhook
	suffix := gitProvider.JenkinsWebHookPath(gitURL, "")
	jenkBaseURL := o.ExternalJenkinsBaseURL
	if jenkBaseURL == "" {
		jenkBaseURL = jenk.BaseURL()
	}
	webhookUrl := util.UrlJoin(jenkBaseURL, suffix)
	webhook := &gits.GitWebHookArguments{
		Owner: gitInfo.Organisation,
		Repo:  gitInfo,
		URL:   webhookUrl,
	}
	return gitProvider.CreateWebHook(webhook)
}

// LogImportedProject logs more details for an imported project
func (o *CommonOptions) LogImportedProject(isEnvironment bool, gitInfo *gits.GitRepository) {
	log.Blank()
	if !isEnvironment {
		log.Infof("Watch pipeline activity via:    %s\n", util.ColorInfo(fmt.Sprintf("jx get activity -f %s -w", gitInfo.Name)))
		log.Infof("Browse the pipeline log via:    %s\n", util.ColorInfo(fmt.Sprintf("jx get build logs %s", gitInfo.PipelinePath())))
		log.Infof("Open the Jenkins console via    %s\n", util.ColorInfo("jx console"))
		log.Infof("You can list the pipelines via: %s\n", util.ColorInfo("jx get pipelines"))
		log.Infof("When the pipeline is complete:  %s\n", util.ColorInfo("jx get applications"))
		log.Blank()
		log.Infof("For more help on available commands see: %s\n", util.ColorInfo("http://jenkins-x.io/developing/browsing/"))
		log.Blank()
	}
	log.Info(util.ColorStatus("Note that your first pipeline may take a few minutes to start while the necessary images get downloaded!\n\n"))
}
