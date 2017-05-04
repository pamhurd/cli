package common

import (
	"os"

	"code.cloudfoundry.org/cli/actor/pluginaction"
	oldCmd "code.cloudfoundry.org/cli/cf/cmd"
	"code.cloudfoundry.org/cli/command"
	"code.cloudfoundry.org/cli/command/flag"
	"code.cloudfoundry.org/cli/command/plugin/shared"
	"code.cloudfoundry.org/cli/util"
	"code.cloudfoundry.org/cli/util/configv3"
)

//go:generate counterfeiter . InstallPluginActor

type InstallPluginActor interface {
	CreateExecutableCopy(path string) (string, error)
	FetchPluginFromURL(url string) (string, error)
	FileExists(path string) bool
	GetAndValidatePlugin(metadata pluginaction.PluginMetadata, commands pluginaction.CommandList, path string) (configv3.Plugin, error)
	InstallPluginFromPath(path string, plugin configv3.Plugin) error
	IsPluginInstalled(pluginName string) bool
	UninstallPlugin(uninstaller pluginaction.PluginUninstaller, name string) error
}

type InstallPluginCommand struct {
	OptionalArgs         flag.InstallPluginArgs `positional-args:"yes"`
	Force                bool                   `short:"f" description:"Force install of plugin without confirmation"`
	RegisteredRepository string                 `short:"r" description:"Name of a registered repository where the specified plugin is located"`
	usage                interface{}            `usage:"CF_NAME install-plugin (LOCAL-PATH/TO/PLUGIN | URL | -r REPO_NAME PLUGIN_NAME) [-f]\n\nEXAMPLES:\n   CF_NAME install-plugin ~/Downloads/plugin-foobar\n   CF_NAME install-plugin https://example.com/plugin-foobar_linux_amd64\n   CF_NAME install-plugin -r My-Repo plugin-echo"`
	relatedCommands      interface{}            `related_commands:"add-plugin-repo, list-plugin-repos, plugins"`

	UI     command.UI
	Config command.Config
	Actor  InstallPluginActor
}

func (cmd *InstallPluginCommand) Setup(config command.Config, ui command.UI) error {
	cmd.UI = ui
	cmd.Config = config
	cmd.Actor = pluginaction.NewActor(config, nil)
	return nil
}

func (cmd InstallPluginCommand) Execute(_ []string) error {
	if !cmd.Config.Experimental() {
		oldCmd.Main(os.Getenv("CF_TRACE"), os.Args)
		return nil
	}

	pluginPath := string(cmd.OptionalArgs.PathURLOrPluginName)

	if pluginPath == "" {
		return command.RequiredArgumentError{ArgumentName: "PATH_URL_PLUGIN_NAME"}
	}

	tempPluginPath, err := cmd.preparePluginForInstallation(pluginPath)

	defer os.Remove(tempPluginPath)
	if err != nil {
		return err
	}

	rpcService, err := shared.NewRPCService(cmd.Config, cmd.UI)
	if err != nil {
		return err
	}

	plugin, err := cmd.Actor.GetAndValidatePlugin(rpcService, Commands, tempPluginPath)
	if err != nil {
		// change plugin path in error to the original and not the temporary copy
		if _, isInvalid := err.(pluginaction.PluginInvalidError); isInvalid {
			err = pluginaction.PluginInvalidError{Path: pluginPath}
		}
		return shared.HandleError(err)
	}

	if cmd.Actor.IsPluginInstalled(plugin.Name) {
		if !cmd.Force {
			return shared.PluginAlreadyInstalledError{
				Name:    plugin.Name,
				Version: plugin.Version.String(),
				Path:    pluginPath,
			}
		}

		err = cmd.uninstallPlugin(plugin, rpcService)
		if err != nil {
			return err
		}
	}

	return cmd.installPlugin(plugin, tempPluginPath)
}

func (cmd InstallPluginCommand) installPlugin(plugin configv3.Plugin, pluginPath string) error {
	cmd.UI.DisplayTextWithFlavor("Installing plugin {{.Name}}...", map[string]interface{}{
		"Name": plugin.Name,
	})

	installErr := cmd.Actor.InstallPluginFromPath(pluginPath, plugin)
	if installErr != nil {
		return installErr
	}

	cmd.UI.DisplayOK()
	cmd.UI.DisplayTextWithFlavor("Plugin {{.Name}} {{.Version}} successfully installed.", map[string]interface{}{
		"Name":    plugin.Name,
		"Version": plugin.Version.String(),
	})
	return nil
}

func (cmd InstallPluginCommand) uninstallPlugin(plugin configv3.Plugin, rpcService *shared.RPCService) error {
	cmd.UI.DisplayText("Plugin {{.Name}} {{.Version}} is already installed. Uninstalling existing plugin...", map[string]interface{}{
		"Name":    plugin.Name,
		"Version": plugin.Version.String(),
	})

	uninstallErr := cmd.Actor.UninstallPlugin(rpcService, plugin.Name)
	if uninstallErr != nil {
		return uninstallErr
	}

	cmd.UI.DisplayOK()
	cmd.UI.DisplayText("Plugin {{.Name}} successfully uninstalled.", map[string]interface{}{
		"Name": plugin.Name,
	})

	return nil
}

func (cmd InstallPluginCommand) preparePluginForInstallation(pathURLOrPluginName string) (string, error) {
	pathType := util.DeterminePathType(pathURLOrPluginName)

	switch {
	case pathType == util.PluginFilePath:
		if !cmd.Actor.FileExists(pathURLOrPluginName) {
			return "", shared.FileNotFoundError{Path: pathURLOrPluginName}
		}

		err := cmd.promptForInstallPlugin("Do you want to install the plugin {{.Path}}?", pathURLOrPluginName)
		if err != nil {
			return "", err
		}

		// copy plugin binary to a temporary location and make it executable
		return cmd.Actor.CreateExecutableCopy(pathURLOrPluginName)

	case pathType == util.PluginHTTPPath:
		err := cmd.promptForInstallPlugin("Do you want to install the plugin from {{.Path}}?", pathURLOrPluginName)
		if err != nil {
			return "", err
		}

		cmd.UI.DisplayText("Starting download of plugin binary from URL...")

		downloadedPath, _ := cmd.Actor.FetchPluginFromURL(pathURLOrPluginName)
		stat, _ := os.Stat(downloadedPath)
		cmd.UI.DisplayText("{{.Bytes}} bytes downloaded...", map[string]interface{}{
			"Bytes": stat.Size(),
		})

		return downloadedPath, nil
	}

	return "", command.UnsupportedURLSchemeError{UnsupportedURL: pathURLOrPluginName}
}

func (cmd InstallPluginCommand) promptForInstallPlugin(prompt string, path string) error {
	cmd.UI.DisplayHeader("Attention: Plugins are binaries written by potentially untrusted authors.")
	cmd.UI.DisplayHeader("Install and use plugins at your own risk.")

	if !cmd.Force {
		really, promptErr := cmd.UI.DisplayBoolPrompt(false, prompt, map[string]interface{}{
			"Path": path,
		})
		if promptErr != nil {
			return promptErr
		}
		if !really {
			return shared.PluginInstallationCancelled{}
		}
	}

	return nil
}
