package cmd

import "github.com/spf13/cobra"

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Manage server snapshots",
}

func init() {
	rootCmd.AddCommand(snapshotCmd)
}
