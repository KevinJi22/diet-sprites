package cmd

import "github.com/spf13/cobra"

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Manage servers",
	Long: `Create and delete Hetzner cloud servers.

Typical workflow:

  # 1. Upload your SSH key once
  sandbox key upload --name my-key

  # 2. Create a server
  sandbox server create --name my-box --ssh-key my-key

  # 3. SSH in
  ssh root@<ip shown after create>

  # 4. Destroy when done
  sandbox server delete my-box`,
}

func init() {
	rootCmd.AddCommand(serverCmd)
}
