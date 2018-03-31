// Copyright 2016-2018, Pulumi Corporation.  All rights reserved.

package cmd

import (
	"io"
	"os"

	"github.com/blang/semver"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/pulumi/pulumi/pkg/backend/cloud"
	"github.com/pulumi/pulumi/pkg/util/cmdutil"
	"github.com/pulumi/pulumi/pkg/workspace"
)

func newPluginInstallCmd() *cobra.Command {
	var cloudURL string
	var file string
	var reinstall bool
	var cmd = &cobra.Command{
		Use:   "install [KIND NAME VERSION]",
		Args:  cmdutil.MaximumNArgs(3),
		Short: "Install one or more plugins",
		Long: "Install one or more plugins.\n" +
			"\n" +
			"This command is used manually install plugins required by your program.  It may\n" +
			"be run either with a specific KIND, NAME, and VERSION, or by omitting these and\n" +
			"letting Pulumi compute the set of plugins that may be required by the current\n" +
			"project.  VERSION cannot be a range: it must be a specific number.\n" +
			"\n" +
			"If you let Pulumi compute the set to download, it is conservative and may end up\n" +
			"downloading more plugins than is strictly necessary.",
		Run: cmdutil.RunFunc(func(cmd *cobra.Command, args []string) error {
			// Parse the kind, name, and version, if specified.
			var installs []workspace.PluginInfo
			if len(args) > 0 {
				if !workspace.IsPluginKind(args[0]) {
					return errors.Errorf("unrecognized plugin kind: %s", args[0])
				} else if len(args) < 2 {
					return errors.New("missing plugin name argument")
				} else if len(args) < 3 {
					return errors.New("missing plugin version argument")
				}
				version, err := semver.ParseTolerant(args[2])
				if err != nil {
					return errors.Wrap(err, "invalid plugin semver")
				}
				installs = append(installs, workspace.PluginInfo{
					Kind:    workspace.PluginKind(args[0]),
					Name:    args[1],
					Version: &version,
				})
			} else {
				if file != "" {
					return errors.New("--file (-f) is only valid if a specific package is being installed")
				}

				// If a specific plugin wasn't given, compute the set of plugins the current project needs.
				plugins, err := getProjectPlugins()
				if err != nil {
					return err
				}
				for _, plugin := range plugins {
					// Skip language plugins; by definition, we already have one installed.
					// TODO[pulumi/pulumi#956]: eventually we will want to honor and install these in the usual way.
					if plugin.Kind != workspace.LanguagePlugin {
						installs = append(installs, plugin)
					}
				}
			}

			// Target the cloud URL for downloads.
			var releases cloud.Backend
			if len(installs) > 0 && file == "" {
				r, err := cloud.New(cmdutil.Diag(), cloud.ValueOrDefaultURL(cloudURL))
				if err != nil {
					return errors.Wrap(err, "creating API client")
				}
				releases = r
			}

			// Now for each kind, name, version pair, download it from the release website, and install it.
			for _, install := range installs {
				// If the plugin already exists, don't download it unless --reinstall was passed.
				if !reinstall && workspace.HasPlugin(install) {
					continue
				}

				// If we got here, actually try to do the download.
				var source string
				var tarball io.ReadCloser
				var err error
				if file == "" {
					source = releases.CloudURL()
					if tarball, err = releases.DownloadPlugin(install, true); err != nil {
						return errors.Wrapf(err,
							"downloading %s plugin %s from %s", install.Kind, install.String(), source)
					}
				} else {
					source = file
					if tarball, err = os.Open(file); err != nil {
						return errors.Wrapf(err, "opening file %s", source)
					}
				}
				if err = install.Install(tarball); err != nil {
					return errors.Wrapf(err, "installing %s from %s", install.String(), source)
				}
			}

			return nil
		}),
	}

	cmd.PersistentFlags().StringVarP(&cloudURL,
		"cloud-url", "c", "", "A cloud URL to download releases from")
	cmd.PersistentFlags().StringVarP(&file,
		"file", "f", "", "Install a plugin from a tarball file, instead of downloading it")
	cmd.PersistentFlags().BoolVar(&reinstall,
		"reinstall", false, "Reinstall a plugin even if it already exists")

	return cmd
}