package cmd

import (
	"context"
	"fmt"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/kevin/diet_sprites/internal/config"
	"github.com/spf13/cobra"
)

var snapshotListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available snapshots",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		images, err := client.Image.AllWithOpts(ctx, hcloud.ImageListOpts{
			Type: []hcloud.ImageType{hcloud.ImageTypeSnapshot},
		})
		if err != nil {
			return fmt.Errorf("listing snapshots: %w", err)
		}

		cfg, err := config.Load()
		if err != nil {
			return err
		}

		if len(images) == 0 {
			fmt.Println("No snapshots found.")
			return nil
		}

		fmt.Printf("%-12s  %-32s  %-8s\n", "ID", "DESCRIPTION", "SIZE")
		for _, img := range images {
			marker := ""
			if img.ID == cfg.DefaultSnapshotID {
				marker = " [default]"
			}
			fmt.Printf("%-12d  %-32s  %.1fGB%s\n", img.ID, img.Description, img.ImageSize, marker)
		}
		return nil
	},
}

func init() {
	snapshotCmd.AddCommand(snapshotListCmd)
}
