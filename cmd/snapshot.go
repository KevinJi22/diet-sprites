package cmd

import "github.com/spf13/cobra"

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Manage server snapshots",
	Long: `Manage Hetzner snapshots of server disks.

Snapshots are how you build the golden image: configure a server once
(containerd, gVisor, runner, idled), snapshot it, then boot every future
server from that snapshot in ~10s instead of re-installing everything.

  sandbox snapshot create setup-box --power-off --set-default
  sandbox snapshot list
  sandbox snapshot delete <id>`,
}

func init() {
	rootCmd.AddCommand(snapshotCmd)
}
