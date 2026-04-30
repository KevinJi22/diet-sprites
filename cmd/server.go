package cmd

import "github.com/spf13/cobra"

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Manage servers",
	Long: `Create and manage Hetzner cloud servers.

First-time setup — build the golden image (run once):

  sandbox server create --name setup-box --image ubuntu-24.04 --ssh-key my-key --wait
  sandbox server bootstrap setup-box --identity ~/.ssh/id_ed25519
  sandbox snapshot create setup-box --power-off --set-default
  sandbox server delete setup-box

Spin up a runner from the golden image:

  sandbox server create --name my-runner --ssh-key my-key --wait \
    --identity ~/.ssh/id_ed25519 --runner-token <secret>

The runner is then live at http://<ip>:8080/run. The server auto-powers-off
after 5 minutes idle (idled daemon, baked into the golden image).`,
}

func init() {
	rootCmd.AddCommand(serverCmd)
}
