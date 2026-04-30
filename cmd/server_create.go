package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/kevin/diet_sprites/internal/config"
	"github.com/kevin/diet_sprites/internal/sshprobe"
	"github.com/spf13/cobra"
)

var createFlags struct {
	name        string
	serverType  string
	image       string
	location    string
	sshKey      string
	wait        bool
	runnerToken string
	identity    string
	user        string
}

var serverCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new server",
	Long: `Create a new Hetzner cloud server and wait for it to start.

Without a default snapshot set, boots ubuntu-24.04. Once you've built a golden
image with "server bootstrap" and "snapshot create --set-default", it boots that
instead — containerd, gVisor, and the runner are already installed.

Examples:

  # Boot from golden snapshot and start the runner in one step
  sandbox server create --name my-runner --ssh-key my-key --wait \
    --identity ~/.ssh/id_ed25519 --runner-token $(openssl rand -hex 16)

  # Boot a raw Ubuntu server for initial golden image setup
  sandbox server create --name setup-box --image ubuntu-24.04 --ssh-key my-key --wait

  # Custom type and location
  sandbox server create --name my-box --type cx32 --location fsn1 --ssh-key my-key

Server types: cx23, cx33, cx43, cx53 (shared Intel); cax11, cax21, ... (shared ARM)
Locations:    nbg1 (Nuremberg), fsn1 (Falkenstein), hel1 (Helsinki), ash (Ashburn), hil (Hillsboro)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if createFlags.runnerToken != "" && !createFlags.wait {
			return fmt.Errorf("--runner-token requires --wait")
		}

		ctx := context.Background()

		// Resolve image: explicit --image > default snapshot in config > ubuntu-24.04.
		var imageRef *hcloud.Image
		if createFlags.image != "" {
			imageRef = &hcloud.Image{Name: createFlags.image}
		} else {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if cfg.DefaultSnapshotID != 0 {
				fmt.Printf("Using default snapshot %d\n", cfg.DefaultSnapshotID)
				imageRef = &hcloud.Image{ID: cfg.DefaultSnapshotID}
			} else {
				imageRef = &hcloud.Image{Name: "ubuntu-24.04"}
			}
		}

		opts := hcloud.ServerCreateOpts{
			Name:             createFlags.name,
			ServerType:       &hcloud.ServerType{Name: createFlags.serverType},
			Image:            imageRef,
			Location:         &hcloud.Location{Name: createFlags.location},
			StartAfterCreate: hcloud.Ptr(true),
		}

		if createFlags.sshKey != "" {
			key, _, err := client.SSHKey.GetByName(ctx, createFlags.sshKey)
			if err != nil {
				return fmt.Errorf("looking up SSH key: %w", err)
			}
			if key == nil {
				return fmt.Errorf("SSH key %q not found", createFlags.sshKey)
			}
			opts.SSHKeys = []*hcloud.SSHKey{key}
		}

		result, _, err := client.Server.Create(ctx, opts)
		if err != nil {
			return fmt.Errorf("creating server: %w", err)
		}

		fmt.Printf("Creating server %q (id: %d)...\n", result.Server.Name, result.Server.ID)

		if err := client.Action.WaitFor(ctx, result.Action); err != nil {
			return fmt.Errorf("waiting for server creation: %w", err)
		}

		server, _, err := client.Server.GetByID(ctx, result.Server.ID)
		if err != nil {
			return fmt.Errorf("fetching server details: %w", err)
		}

		ip := server.PublicNet.IPv4.IP.String()
		fmt.Printf("Server %q ready (id: %d, ip: %s)\n", server.Name, server.ID, ip)
		if result.RootPassword != "" {
			fmt.Printf("Root password: %s\n", result.RootPassword)
		}

		if createFlags.wait {
			fmt.Printf("\nProbing SSH on %s:22...\n", ip)
			cfg := sshprobe.DefaultConfig(ip)
			start := time.Now()
			results, probeErr := sshprobe.Probe(ctx, cfg)
			sshprobe.PrintResults(results, time.Since(start))
			if probeErr != nil {
				return fmt.Errorf("SSH probe: %w", probeErr)
			}

			if createFlags.runnerToken != "" {
				if createFlags.identity == "" {
					return fmt.Errorf("--runner-token requires --identity")
				}
				fmt.Println("Writing runner token and starting service...")
				dropIn := fmt.Sprintf("[Service]\nEnvironment=RUNNER_TOKEN=%s\n", createFlags.runnerToken)
				writeDropIn := fmt.Sprintf(
					`mkdir -p /etc/systemd/system/runner.service.d && printf %%s %q > /etc/systemd/system/runner.service.d/token.conf && systemctl daemon-reload && systemctl start runner`,
					dropIn,
				)
				if err := sshRun(ip, createFlags.user, createFlags.identity, writeDropIn); err != nil {
					return fmt.Errorf("starting runner: %w", err)
				}
				fmt.Printf("Runner listening on http://%s:8080\n", ip)
			}
		}

		return nil
	},
}

func init() {
	serverCmd.AddCommand(serverCreateCmd)

	serverCreateCmd.Flags().StringVarP(&createFlags.name, "name", "n", "", "Server name (required)")
	serverCreateCmd.Flags().StringVarP(&createFlags.serverType, "type", "t", "cx23", "Server type (e.g. cx23, cax11, cx33)")
	serverCreateCmd.Flags().StringVarP(&createFlags.image, "image", "i", "", "OS image name (default: use snapshot from config, else ubuntu-24.04)")
	serverCreateCmd.Flags().StringVarP(&createFlags.location, "location", "l", "nbg1", "Datacenter location (e.g. nbg1, fsn1, hel1)")
	serverCreateCmd.Flags().StringVarP(&createFlags.sshKey, "ssh-key", "k", "", "SSH key name to inject")
	serverCreateCmd.Flags().BoolVarP(&createFlags.wait, "wait", "w", false, "Wait for SSH to become ready")
	serverCreateCmd.Flags().StringVar(&createFlags.runnerToken, "runner-token", "", "Secret token for the runner HTTP API (requires --wait and --identity)")
	serverCreateCmd.Flags().StringVar(&createFlags.identity, "identity", "", "SSH private key path (required with --runner-token)")
	serverCreateCmd.Flags().StringVarP(&createFlags.user, "user", "u", "root", "SSH login user")

	_ = serverCreateCmd.MarkFlagRequired("name")
}
