package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/kevin/diet_sprites/internal/config"
	"github.com/spf13/cobra"
)

var snapshotCreateFlags struct {
	description string
	powerOff    bool
	setDefault  bool
}

var snapshotCreateCmd = &cobra.Command{
	Use:   "create <server-name>",
	Short: "Create a snapshot from a server",
	Long: `Create a snapshot from a running or stopped server.

Examples:

  # Snapshot a server (live, may have inconsistent disk state)
  sandbox snapshot create my-box --description golden-image

  # Power off first for a clean snapshot, then set as default boot image
  sandbox snapshot create my-box --power-off --set-default`,
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

		// if the server is still running and we request to power off before snapshotting, we need to shut down gracefully first
		if snapshotCreateFlags.powerOff && server.Status == hcloud.ServerStatusRunning {
			fmt.Printf("Shutting down %q...\n", name)
			action, _, err := client.Server.Shutdown(ctx, server)
			if err != nil {
				return fmt.Errorf("failed to shut down server %q: %w", name, err)
			}

			if err := client.Action.WaitFor(ctx, action); err != nil {
				return fmt.Errorf("shutdown action failed for server %q: %w", name, err)
			}
		}

		if snapshotCreateFlags.powerOff {
			// WaitFor only confirms the shutdown signal was accepted; poll until the OS has halted.
			for {
				server, _, err = client.Server.GetByID(ctx, server.ID)
				if err != nil {
					return fmt.Errorf("polling server status: %w", err)
				}
				if server.Status == hcloud.ServerStatusOff {
					break
				}
				time.Sleep(2 * time.Second)
			}
		}

		fmt.Println("Creating snapshot...")
		result, _, err := client.Server.CreateImage(ctx, server, &hcloud.ServerCreateImageOpts{
			Type:        hcloud.ImageTypeSnapshot,
			Description: hcloud.Ptr(snapshotCreateFlags.description),
		})

		if err != nil {
			return fmt.Errorf("failed to create snapshot for %q: %w", name, err)
		}

		if err := client.Action.WaitFor(ctx, result.Action); err != nil {
			return fmt.Errorf("snapshot action failed for server %q: %w", name, err)
		}

		fmt.Printf("Created snapshot with id = %d, description = %s\n", result.Image.ID, result.Image.Description)
		if snapshotCreateFlags.setDefault {
			if err := config.Save(&config.Config{DefaultSnapshotID: result.Image.ID}); err != nil {
				return fmt.Errorf("saving default snapshot: %w", err)
			}
		}

		return nil
	},
}

func init() {
	snapshotCmd.AddCommand(snapshotCreateCmd)

	snapshotCreateCmd.Flags().StringVarP(&snapshotCreateFlags.description, "description", "d", "golden-image", "Snapshot description")
	snapshotCreateCmd.Flags().BoolVar(&snapshotCreateFlags.powerOff, "power-off", false, "Power off server before snapshotting (cleaner, avoids dirty disk state)")
	snapshotCreateCmd.Flags().BoolVar(&snapshotCreateFlags.setDefault, "set-default", true, "Save snapshot ID as default in ~/.config/sandbox/config.json")
}
