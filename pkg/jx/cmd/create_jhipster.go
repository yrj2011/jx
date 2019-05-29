package cmd

import (
	"fmt"
	"github.com/jenkins-x/jx/pkg/jx/cmd/helper"
	"os"

	"github.com/spf13/cobra"

	"github.com/jenkins-x/jx/pkg/jx/cmd/opts"
	"github.com/jenkins-x/jx/pkg/jx/cmd/templates"
	"github.com/jenkins-x/jx/pkg/log"
	"github.com/jenkins-x/jx/pkg/util"
)

var (
	createJHipsterLong = templates.LongDesc(`
		Creates a new JHipster application and then optionally setups CI/CD pipelines and GitOps promotion.

		JHipster is an application generator for gRPC services in Go with a set of tools/libraries.

		This command is expected to be run within your '$GOHOME' directory. e.g. at '$GOHOME/src/github.com/myOrgOrUser/'

		For more documentation about JHipster see: [http://www.jhipster.tech/](http://www.jhipster.tech/)

` + opts.SeeAlsoText("jx create project"))

	createJHipsterExample = templates.Examples(`
		# Create a JHipster application and be prompted for the folder name
		jx create jhipster 

		# Create a JHipster application in the myname sub-folder folder
		jx create jhipster myname
	`)
)

// CreateJHipsterOptions the options for the create spring command
type CreateJHipsterOptions struct {
	CreateProjectOptions
}

// NewCmdCreateJHipster creates a command object for the "create" command
func NewCmdCreateJHipster(commonOpts *opts.CommonOptions) *cobra.Command {
	options := &CreateJHipsterOptions{
		CreateProjectOptions: CreateProjectOptions{
			ImportOptions: ImportOptions{
				CommonOptions: commonOpts,
			},
		},
	}

	cmd := &cobra.Command{
		Use:     "jhipster",
		Short:   "Create a new JHipster based application and import the generated code into Git and Jenkins for CI/CD",
		Long:    createJHipsterLong,
		Example: createJHipsterExample,
		Run: func(cmd *cobra.Command, args []string) {
			options.Cmd = cmd
			options.Args = args
			err := options.Run()
			helper.CheckErr(err)
		},
	}
	options.addCreateAppFlags(cmd)
	return cmd
}

// checkJHipsterInstalled lazily install JHipster if its not installed already
func (o CreateJHipsterOptions) checkJHipsterInstalled() error {
	_, err := o.GetCommandOutput("", "jhipster", "--version")
	if err != nil {
		log.Info("Installing JHipster..")
		_, err = o.GetCommandOutput("", "rimraf", "--version")
		if err != nil {
			log.Info("Installing rimraf..")
			_, err = o.GetCommandOutput("", "npm", "install", "-g", "rimraf")
			if err != nil {
				return err
			}
		}
		err = o.RunCommand("yarn", "global", "add", "generator-jhipster")
		if err != nil {
			return err
		}
		log.Info("Installed JHipster")
	}
	return err
}

// GenerateJHipster creates a fresh JHipster project by running jhipster on local shell
func (o CreateJHipsterOptions) GenerateJHipster(dir string) error {
	err := os.MkdirAll(dir, util.DefaultWritePermissions)
	if err != nil {
		return err
	}
	return o.RunCommandInteractiveInDir(!o.BatchMode, dir, "jhipster")
}

// Run implements the command
func (o *CreateJHipsterOptions) Run() error {
	err := o.checkJHipsterInstalled()
	if err != nil {
		return err
	}

	dir := ""
	args := o.Args
	if len(args) > 0 {
		dir = args[0]
	}
	if dir == "" {
		if o.BatchMode {
			return util.MissingOption(optionOutputDir)
		}
		dir, err = util.PickValue("Pick the name of the new project:", "myhipster", true, "", o.In, o.Out, o.Err)
		if err != nil {
			return err
		}
		if dir == "" || dir == "." {
			return fmt.Errorf("Invalid project name: %s", dir)
		}
	}
	log.Blank()

	err = o.GenerateJHipster(dir)
	if err != nil {
		return err
	}

	log.Infof("Created JHipster project at %s\n\n", util.ColorInfo(dir))
	return o.ImportCreatedProject(dir)
}
