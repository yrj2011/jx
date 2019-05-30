package cmd

import (
	"fmt"
	"github.com/jenkins-x/jx/pkg/jx/cmd/helper"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/jenkins-x/jx/pkg/jx/cmd/opts"
	"github.com/jenkins-x/jx/pkg/jx/cmd/templates"
	"github.com/jenkins-x/jx/pkg/util"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

const (
	gendocFrontmatterTemplate = `---
date: %s
title: "%s"
slug: %s
url: %s
---
`
)

var (
	create_docs_long = templates.LongDesc(`
		Creates the documentation markdown files
`)

	create_docs_example = templates.Examples(`
		# Create the documentation files
		jx create docs
	`)
)

// CreateDocsOptions the options for the create spring command
type CreateDocsOptions struct {
	CreateOptions

	Dir string
}

// NewCmdCreateDocs creates a command object for the "create" command
func NewCmdCreateDocs(commonOpts *opts.CommonOptions) *cobra.Command {
	options := &CreateDocsOptions{
		CreateOptions: CreateOptions{
			CommonOptions: commonOpts,
		},
	}

	cmd := &cobra.Command{
		Use:     "docs",
		Short:   "Creates the documentation files",
		Aliases: []string{"doc"},
		Long:    create_docs_long,
		Example: create_docs_example,
		Run: func(cmd *cobra.Command, args []string) {
			options.Cmd = cmd
			options.Args = args
			err := options.Run()
			helper.CheckErr(err)
		},
	}

	cmd.Flags().StringVarP(&options.Dir, "dir", "d", ".", "the directory to generate the docs into")

	return cmd
}

// Run implements the command
func (o *CreateDocsOptions) Run() error {
	jxcommand := NewJXCommand(o.GetFactory(), o.In, o.Out, o.Err, nil)
	dir := o.Dir

	exists, _ := util.FileExists(dir)
	if !exists {
		err := os.Mkdir(dir, util.DefaultWritePermissions)
		if err != nil {
			return fmt.Errorf("Failed to create %s: %s", dir, err)
		}
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	prepender := func(filename string) string {
		name := filepath.Base(filename)
		base := strings.TrimSuffix(name, path.Ext(name))
		url := "/commands/" + strings.ToLower(base) + "/"
		return fmt.Sprintf(gendocFrontmatterTemplate, now, strings.Replace(base, "_", " ", -1), base, url)
	}

	linkHandler := func(name string) string {
		base := strings.TrimSuffix(name, path.Ext(name))
		return "/commands/" + strings.ToLower(base) + "/"
	}

	//jww.FEEDBACK.Println("Generating Hugo command-line documentation in", gendocdir, "...")
	doc.GenMarkdownTreeCustom(jxcommand, dir, prepender, linkHandler)
	//jww.FEEDBACK.Println("Done.")

	return nil
}
