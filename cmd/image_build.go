package cmd

import (
	"context"
	"fmt"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/kevin/diet_sprites/internal/config"
	"github.com/kevin/diet_sprites/internal/sshprobe"
	"github.com/spf13/cobra"
)

var imageBuildFlags struct {
	name        string
	serverType  string
	location    string
	sshKey      string
	identity    string
	user        string
	arch        string
	description string
}

var imageBuildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build the golden snapshot image",
	Long: `Create a fresh server, install containerd/gVisor/runner/idled, snapshot it,
and delete the setup server — all in one step.

The snapshot is saved as the default boot image so that future
"sandbox server create" calls use it automatically.

Example:

  sandbox image build --ssh-key my-key --identity ~/.ssh/id_ed25519`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		f := imageBuildFlags

		// ── Step 1: create server ──────────────────────────────────────────
		fmt.Printf("Step 1/4: Creating server %q (ubuntu-24.04)...\n", f.name)

		key, _, err := client.SSHKey.GetByName(ctx, f.sshKey)
		if err != nil {
			return fmt.Errorf("looking up SSH key: %w", err)
		}
		if key == nil {
			return fmt.Errorf("SSH key %q not found", f.sshKey)
		}

		result, _, err := client.Server.Create(ctx, hcloud.ServerCreateOpts{
			Name:             f.name,
			ServerType:       &hcloud.ServerType{Name: f.serverType},
			Image:            &hcloud.Image{Name: "ubuntu-24.04"},
			Location:         &hcloud.Location{Name: f.location},
			StartAfterCreate: hcloud.Ptr(true),
			SSHKeys:          []*hcloud.SSHKey{key},
		})
		if err != nil {
			return fmt.Errorf("creating server: %w", err)
		}
		if err := client.Action.WaitFor(ctx, result.Action); err != nil {
			return fmt.Errorf("waiting for server creation: %w", err)
		}

		server, _, err := client.Server.GetByID(ctx, result.Server.ID)
		if err != nil {
			return fmt.Errorf("fetching server: %w", err)
		}
		ip := server.PublicNet.IPv4.IP.String()
		fmt.Printf("Server %q ready (id: %d, ip: %s)\n", f.name, server.ID, ip)

		fmt.Printf("Probing SSH on %s:22...\n", ip)
		probeResults, probeErr := sshprobe.Probe(ctx, sshprobe.DefaultConfig(ip))
		sshprobe.PrintResults(probeResults, 0)
		if probeErr != nil {
			return fmt.Errorf("SSH probe: %w", probeErr)
		}

		// ── Step 2: bootstrap ──────────────────────────────────────────────
		fmt.Printf("\nStep 2/4: Bootstrapping %q...\n", f.name)

		if err := provisionServer(ip, f.user, f.identity, f.arch); err != nil {
			return err
		}

		// ── Step 3: snapshot ───────────────────────────────────────────────
		fmt.Printf("\nStep 3/4: Snapshotting %q (powering off first)...\n", f.name)

		image, err := shutdownAndSnapshot(ctx, server, f.description, true)
		if err != nil {
			return err
		}
		fmt.Printf("Snapshot created (id: %d, description: %s)\n", image.ID, f.description)

		if err := config.Save(&config.Config{DefaultSnapshotID: image.ID}); err != nil {
			return fmt.Errorf("saving default snapshot: %w", err)
		}
		fmt.Printf("Saved as default boot image\n")

		// ── Step 4: delete setup server ────────────────────────────────────
		fmt.Printf("\nStep 4/4: Deleting setup server %q...\n", f.name)

		delResult, _, err := client.Server.DeleteWithResult(ctx, server)
		if err != nil {
			return fmt.Errorf("deleting server: %w", err)
		}
		if err := client.Action.WaitFor(ctx, delResult.Action); err != nil {
			return fmt.Errorf("waiting for deletion: %w", err)
		}

		fmt.Printf("\nGolden image build complete. Snapshot %d is now the default boot image.\n", image.ID)
		return nil
	},
}

func init() {
	imageCmd.AddCommand(imageBuildCmd)

	imageBuildCmd.Flags().StringVarP(&imageBuildFlags.name, "name", "n", "setup-box", "Temporary server name")
	imageBuildCmd.Flags().StringVarP(&imageBuildFlags.serverType, "type", "t", "cx23", "Server type")
	imageBuildCmd.Flags().StringVarP(&imageBuildFlags.location, "location", "l", "nbg1", "Datacenter location")
	imageBuildCmd.Flags().StringVarP(&imageBuildFlags.sshKey, "ssh-key", "k", "", "SSH key name (required)")
	imageBuildCmd.Flags().StringVarP(&imageBuildFlags.identity, "identity", "i", "", "SSH private key path (required)")
	imageBuildCmd.Flags().StringVarP(&imageBuildFlags.user, "user", "u", "root", "SSH login user")
	imageBuildCmd.Flags().StringVar(&imageBuildFlags.arch, "arch", "amd64", "Target architecture: amd64 or arm64")
	imageBuildCmd.Flags().StringVarP(&imageBuildFlags.description, "description", "d", "golden-image", "Snapshot description")

	_ = imageBuildCmd.MarkFlagRequired("ssh-key")
	_ = imageBuildCmd.MarkFlagRequired("identity")
}
