package main

import (
	"github.com/spf13/cobra"
)

// The command groups are registered by one function each so the group's work
// item can fill in its function without touching the root command. The
// functions are empty until their group is implemented.

// addPairingCommands adds the broker and key commands.
func addPairingCommands(root *cobra.Command, a *app) {
	a.addBrokerAndKeyCommands(root)
}

// addConfigCommands adds the config commands.
func addConfigCommands(root *cobra.Command, a *app) {
	root.AddCommand(a.newConfigCommand())
}

// addServiceCommands adds the service commands.
func addServiceCommands(root *cobra.Command, a *app) {
	root.AddCommand(a.serviceCommand())
}

// addInstallationCommands adds the update and uninstall commands.
func addInstallationCommands(root *cobra.Command, a *app) {}
