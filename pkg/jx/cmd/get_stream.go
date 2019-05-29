package cmd

import (
	"github.com/jenkins-x/jx/pkg/jx/cmd/helper"
	"strings"

	"github.com/jenkins-x/jx/pkg/log"
	"github.com/jenkins-x/jx/pkg/util"
	"github.com/jenkins-x/jx/pkg/version"
	"github.com/pkg/errors"

	"github.com/spf13/cobra"

	"github.com/jenkins-x/jx/pkg/jx/cmd/opts"
	"github.com/jenkins-x/jx/pkg/jx/cmd/templates"
)

// GetStreamOptions the command line options
type GetStreamOptions struct {
	GetOptions

	Kind               string
	VersionsRepository string
	VersionsGitRef     string
}

var (
	getStreamLong = templates.LongDesc(`
		Displays the version of a chart, package or docker image from the Version Stream

		For more information see: [http://jenkins-x.io/architecture/version-stream/](http://jenkins-x.io/architecture/version-stream/)

`)

	getStreamExample = templates.Examples(`
		# List the version of a docker image
		jx get stream -k docker gcr.io/jenkinsxio/builder-jx

		# List the version of a chart
		jx get stream -k charts jenkins-x/tekton
	`)
)

// NewCmdGetStream creates the command
func NewCmdGetStream(commonOpts *opts.CommonOptions) *cobra.Command {
	options := &GetStreamOptions{
		GetOptions: GetOptions{
			CommonOptions: commonOpts,
		},
	}

	cmd := &cobra.Command{
		Use:     "stream",
		Short:   "Displays the version of a chart, package or docker image from the Version Stream",
		Long:    getStreamLong,
		Example: getStreamExample,
		Aliases: []string{"url"},
		Run: func(cmd *cobra.Command, args []string) {
			options.Cmd = cmd
			options.Args = args
			err := options.Run()
			helper.CheckErr(err)
		},
	}
	cmd.Flags().StringVarP(&options.Kind, "kind", "k", "docker", "The kind of version. Possible values: "+strings.Join(version.KindStrings, ", "))
	cmd.Flags().StringVarP(&options.VersionsRepository, "repo", "r", opts.DefaultVersionsURL, "Jenkins X versions Git repo")
	cmd.Flags().StringVarP(&options.VersionsGitRef, "versions-ref", "", "", "Jenkins X versions Git repository reference (tag, branch, sha etc)")
	return cmd
}

// Run implements this command
func (o *GetStreamOptions) Run() error {
	resolver, err := o.CreateVersionResolver(o.VersionsRepository, o.VersionsGitRef)
	if err != nil {
		return errors.Wrap(err, "failed to create the VersionResolver")
	}
	args := o.Args
	if len(args) == 0 {
		return util.MissingArgument("name")
	}
	name := args[0]

	kind := version.VersionKind(o.Kind)
	if kind == version.KindDocker {
		result, err := resolver.ResolveDockerImage(name)
		if err != nil {
			return errors.Wrapf(err, "failed to resolve docker image %s", name)
		}
		log.Infof("resolved image %s to %s\n", util.ColorInfo(name), util.ColorInfo(result))
		return nil
	}

	n, err := resolver.StableVersionNumber(kind, name)
	if err != nil {
		return errors.Wrapf(err, "failed to resolve %s version of %s", o.Kind, name)
	}

	log.Infof("resolved %s %s to version: %s\n", util.ColorInfo(name), util.ColorInfo(o.Kind), util.ColorInfo(n))
	return nil
}
