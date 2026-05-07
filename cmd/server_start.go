package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

var serverStartCmd = &cobra.Command{
	Use:   "start <name>",
	Short: "Power on a server by name",
	Args:  cobra.ExactArgs(1),
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

		fmt.Printf("Starting server %q...\n", name)
		action, _, err := client.Server.Poweron(ctx, server)
		if err != nil {
			return fmt.Errorf("powering on server %q: %w", name, err)
		}

		if err := client.Action.WaitFor(ctx, action); err != nil {
			return fmt.Errorf("waiting for server start: %w", err)
		}

		ip := server.PublicNet.IPv4.IP.String()
		fmt.Printf("Server %q is running (%s)\n", name, ip)
		return nil
	},
}

func init() {
	serverCmd.AddCommand(serverStartCmd)
}
