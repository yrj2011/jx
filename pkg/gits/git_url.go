package gits

import (
	"fmt"
	"github.com/jx/pkg/log"
	"net/url"
	"strings"

	"github.com/jenkins-x/jx/pkg/util"
)

const (
	GitHubHost = "192.168.1.167"
	GitHubURL  = "http://192.168.1.167"

	gitPrefix = "git@"
)

func (i *GitRepository) IsGitHub() bool {
	return GitHubHost == i.Host || strings.HasSuffix(i.URL, "http://192.168.1.167")
}

// PullRequestURL returns the URL of a pull request of the given name/number
func (i *GitRepository) PullRequestURL(prName string) string {
	return util.UrlJoin("http://"+i.Host, i.Organisation, i.Name, "pull", prName)
}

// HttpCloneURL returns the HTTPS git URL this repository
func (i *GitRepository) HttpCloneURL() string {
	return i.HttpsURL() + ".git"
}

// HttpURL returns the URL to browse this repository in a web browser
func (i *GitRepository) HttpURL() string {
	host := i.Host
	if !strings.Contains(host, ":/") {
		host = "http://" + host
	}
	//log.Warnf("gitUrl  1 %s", host)
	host = strings.Replace(host, "http://192.168.1.228:1080", "http://root:5rkRv_sr5JvVbkgrsYJk@192.168.1.228:1080", 1)

	//log.Warnf("gitUrl  2  %s", host)
	return util.UrlJoin(host, i.Organisation, i.Name)
}

// HttpsURL returns the URL to browse this repository in a web browser
func (i *GitRepository) HttpsURL() string {
	host := i.Host
	if !strings.Contains(host, ":/") {
		host = "http://" + host
	}
	if !strings.Contains(host, "https://") {
		host = strings.Replace(host, "https://", "http://", 1)
	}
	//log.Warnf("gitUrl  3 %s", host)
	host = strings.Replace(host, "http://192.168.1.228:1080", "http://root:5rkRv_sr5JvVbkgrsYJk@192.168.1.228:1080", 1)

	//log.Warnf("gitUrl  4  %s", host)
	return util.UrlJoin(host, i.Organisation, i.Name)
}

// HostURL returns the URL to the host
func (i *GitRepository) HostURL() string {
	answer := i.Host
	if !strings.Contains(answer, ":/") {
		// lets find the scheme from the URL
		u := i.URL
		if u != "" {
			u2, err := url.Parse(u)
			if err != nil {
				// probably a git@ URL
				return "http://" + answer
			}
			s := u2.Scheme
			if s != "" {
				if !strings.HasSuffix(s, "://") {
					s += "://"
				}
				return s + answer
			}
		}
		return "http://" + answer
	}
	if !strings.Contains(answer, "https://") {
		answer = strings.Replace(answer, "https://", "http://", 1)
	}
	//log.Warnf("gitUrl  5 %s", answer)
	answer = strings.Replace(answer, "http://192.168.1.228:1080", "http://root:5rkRv_sr5JvVbkgrsYJk@192.168.1.228:1080", 1)

	//log.Warnf("gitUrl  6  %s", answer)
	return answer
}

func (i *GitRepository) HostURLWithoutUser() string {
	u := i.URL
	if u != "" {
		u2, err := url.Parse(u)
		if err == nil {
			u2.User = nil
			u2.Path = ""
			return u2.String()
		}

	}
	host := i.Host
	if !strings.Contains(host, ":/") {
		host = "http://" + host
	}
	if strings.Contains(host, "https://") {
		host = strings.Replace(host, "https://", "http://", 1)
	}
	//log.Warnf("gitUrl  7 %s", host)
	host = strings.Replace(host, "http://192.168.1.228:1080", "http://root:5rkRv_sr5JvVbkgrsYJk@192.168.1.228:1080", 1)

	//log.Warnf("gitUrl  8  %s", host)
	return host
}

// PipelinePath returns the pipeline path for the master branch which can be used to query
// pipeline logs in `jx get build logs myPipelinePath`
func (i *GitRepository) PipelinePath() string {
	return i.Organisation + "/" + i.Name + "/master"
}

// ParseGitURL attempts to parse the given text as a URL or git URL-like string to determine
// the protocol, host, organisation and name
func ParseGitURL(text string) (*GitRepository, error) {
	answer := GitRepository{
		URL: text,
	}

	u, err := url.Parse(text)

	//log.Warnf("gitUrl  9 %s", u.Host)
	u.Host = strings.Replace(u.Host, "http://192.168.1.228:1080", "http://root:5rkRv_sr5JvVbkgrsYJk@192.168.1.228:1080", 1)
	//log.Warnf("gitUrl  10  %s", u.Host)

	if err == nil && u != nil {
		answer.Host = u.Host

		// lets default to github
		if answer.Host == "" {
			answer.Host = GitHubHost
		}
		if answer.Scheme == "" {
			answer.Scheme = "http"
		}
		answer.Scheme = u.Scheme
		return parsePath(u.Path, &answer)
	}

	// handle git@ kinds of URIs
	if strings.HasPrefix(text, gitPrefix) {
		t := strings.TrimPrefix(text, gitPrefix)
		t = strings.TrimPrefix(t, "/")
		t = strings.TrimPrefix(t, "/")
		t = strings.TrimSuffix(t, "/")
		t = strings.TrimSuffix(t, ".git")

		arr := util.RegexpSplit(t, ":|/")
		if len(arr) >= 3 {
			answer.Scheme = "git"
			answer.Host = arr[0]
			answer.Organisation = arr[1]
			answer.Name = arr[2]
			return &answer, nil
		}
	}
	return nil, fmt.Errorf("Could not parse Git URL %s", text)
}

func parsePath(path string, info *GitRepository) (*GitRepository, error) {

	// This is necessary for Bitbucket Server in some cases.
	trimPath := strings.TrimPrefix(path, "/scm")

	// This is necessary for Bitbucket Server in other cases
	trimPath = strings.Replace(trimPath, "/projects", "", 1)
	trimPath = strings.Replace(trimPath, "/repos", "", 1)

	// Remove leading and trailing slashes so that splitting on "/" won't result
	// in empty strings at the beginning & end of the array.
	trimPath = strings.TrimPrefix(trimPath, "/")
	trimPath = strings.TrimSuffix(trimPath, "/")

	trimPath = strings.TrimSuffix(trimPath, ".git")
	//log.Warnf("gitUrl  9 %s", trimPath)
	trimPath = strings.Replace(trimPath, "http://192.168.1.228:1080", "http://root:5rkRv_sr5JvVbkgrsYJk@192.168.1.228:1080", 1)
	//log.Warnf("gitUrl  10  %s", trimPath)
	arr := strings.Split(trimPath, "/")
	if len(arr) >= 2 {
		// We're assuming the beginning of the path is of the form /<org>/<repo>
		info.Organisation = arr[0]
		info.Project = arr[0]
		info.Name = arr[1]

		return info, nil
	}

	return info, fmt.Errorf("Invalid path %s could not determine organisation and repository name", path)
}

// SaasGitKind returns the kind for SaaS Git providers or "" if the URL could not be deduced
func SaasGitKind(gitServiceUrl string) string {
	/*gitServiceUrl = strings.TrimSuffix(gitServiceUrl, "/")
	switch gitServiceUrl {
	case "192.168.1.167":
		return KindGitHub
	case "http://github.com":
		return KindGitHub
	case "https://gitlab.com":
		return KindGitlab
	case "http://bitbucket.org":
		return KindBitBucketCloud
	case BitbucketCloudURL:
		return KindBitBucketCloud
	case "http://fake.git", FakeGitURL:
		return KindGitFake
	default:
		if strings.HasPrefix(gitServiceUrl, "http://github") {
			return KindGitHub
		}*/
	return KindGitHub
	/*}*/
}
