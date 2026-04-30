package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

var serverListCmd = &cobra.Command{
	Use:   "list",
	Short: "List servers in your Hetzner project",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		servers, err := client.Server.All(ctx)
		if err != nil {
			return fmt.Errorf("listing servers: %w", err)
		}

		if len(servers) == 0 {
			fmt.Println("No servers found.")
			return nil
		}

		fmt.Printf("%-20s  %-16s  %-10s  %s\n", "NAME", "IP", "STATUS", "TYPE")
		for _, s := range servers {
			ip := "-"
			if s.PublicNet.IPv4.IP != nil {
				ip = s.PublicNet.IPv4.IP.String()
			}
			fmt.Printf("%-20s  %-16s  %-10s  %s\n", s.Name, ip, s.Status, s.ServerType.Name)
		}
		return nil
	},
}

func init() {
	serverCmd.AddCommand(serverListCmd)
}
