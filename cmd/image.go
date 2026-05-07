package cmd

import "github.com/spf13/cobra"

var imageCmd = &cobra.Command{
	Use:   "image",
	Short: "Manage golden images",
	Long: `Build and manage golden snapshot images.

Build a golden image (runs once, takes ~5-10 minutes):

  sandbox image build --ssh-key my-key --identity ~/.ssh/id_ed25519

This creates a fresh server, installs containerd/gVisor/runner/idled,
snapshots it as the default boot image, then deletes the setup server.`,
}

func init() {
	rootCmd.AddCommand(imageCmd)
}
