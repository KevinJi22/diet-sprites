package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

var serverDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a server by name",
	Long: `Delete a Hetzner server by name and wait for it to be removed.

Example:

  sandbox server delete my-box

This is permanent — all data on the server will be lost.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		name := args[0]

		server, _, err := client.Server.GetByName(ctx, name)
		if err != nil {
			return fmt.Errorf("looking up server %q: %w", name, err)
		}
		if server == nil {
			return fmt.Errorf("server %q not found", name)
		}

		result, _, err := client.Server.DeleteWithResult(ctx, server)
		if err != nil {
			return fmt.Errorf("deleting server %q: %w", name, err)
		}

		if err := client.Action.WaitFor(ctx, result.Action); err != nil {
			return fmt.Errorf("waiting for server deletion: %w", err)
		}

		fmt.Printf("Server %q deleted successfully\n", name)
		return nil
	},
}

func init() {
	serverCmd.AddCommand(serverDeleteCmd)
}
