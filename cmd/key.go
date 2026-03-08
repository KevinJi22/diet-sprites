package cmd

import "github.com/spf13/cobra"

var keyCmd = &cobra.Command{
	Use:   "key",
	Short: "Manage SSH keys",
	Long: `Manage SSH keys stored in your Hetzner account.

Keys uploaded here can be injected into servers at creation time via
the --ssh-key flag on "sandbox server create".`,
}

func init() {
	rootCmd.AddCommand(keyCmd)
}
