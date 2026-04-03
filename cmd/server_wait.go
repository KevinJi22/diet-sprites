package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/kevin/diet_sprites/internal/sshprobe"
	"github.com/spf13/cobra"
)

var serverWaitCmd = &cobra.Command{
	Use:   "wait <name>",
	Short: "Wait for a server's SSH to become ready",
	Long: `Probe a server's SSH port with exponential backoff until it responds.

Useful after creating a server or restoring from a snapshot to know
when you can SSH in.

Example:

  sandbox server wait my-box`,
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

		ip := server.PublicNet.IPv4.IP.String()
		fmt.Printf("Probing SSH on %s (%s:22)...\n", name, ip)

		cfg := sshprobe.DefaultConfig(ip)
		start := time.Now()
		results, err := sshprobe.Probe(ctx, cfg)
		sshprobe.PrintResults(results, time.Since(start))

		if err != nil {
			return fmt.Errorf("SSH probe: %w", err)
		}
		return nil
	},
}

func init() {
	serverCmd.AddCommand(serverWaitCmd)
}
