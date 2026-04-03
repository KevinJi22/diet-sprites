package cmd

import (
	"context"
	"fmt"
	"strconv"

	"github.com/kevin/diet_sprites/internal/config"
	"github.com/spf13/cobra"
)

var snapshotDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a snapshot by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid snapshot ID %q: %w", args[0], err)
		}

		img, _, err := client.Image.GetByID(ctx, id)
		if err != nil {
			return fmt.Errorf("looking up snapshot %d: %w", id, err)
		}
		if img == nil {
			return fmt.Errorf("snapshot %d not found", id)
		}

		if _, err := client.Image.Delete(ctx, img); err != nil {
			return fmt.Errorf("deleting snapshot: %w", err)
		}

		fmt.Printf("Snapshot %d (%q) deleted.\n", id, img.Description)

		// Clear default if this was it.
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if cfg.DefaultSnapshotID == id {
			cfg.DefaultSnapshotID = 0
			if err := config.Save(cfg); err != nil {
				return fmt.Errorf("clearing default snapshot: %w", err)
			}
			fmt.Println("Cleared as default snapshot.")
		}
		return nil
	},
}

func init() {
	snapshotCmd.AddCommand(snapshotDeleteCmd)
}
