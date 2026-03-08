package cmd

import (
	"context"
	"fmt"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/spf13/cobra"
)

var createFlags struct {
	name       string
	serverType string
	image      string
	location   string
	sshKey     string
}

var serverCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new server",
	Long: `Create a new Hetzner cloud server and wait for it to start.

Examples:

  # Minimal (uses defaults: cx22, ubuntu-24.04, nbg1)
  sandbox server create --name my-box

  # With SSH key so you can log in
  sandbox server create --name my-box --ssh-key my-key

  # Custom type and location
  sandbox server create --name my-box --type cx32 --location fsn1 --ssh-key my-key

Server types: cx23, cx33, cx43, cx53 (shared Intel); cax11, cax21, ... (shared ARM)
Locations:    nbg1 (Nuremberg), fsn1 (Falkenstein), hel1 (Helsinki), ash (Ashburn), hil (Hillsboro)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		opts := hcloud.ServerCreateOpts{
			Name:             createFlags.name,
			ServerType:       &hcloud.ServerType{Name: createFlags.serverType},
			Image:            &hcloud.Image{Name: createFlags.image},
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

		fmt.Printf("Server %q created successfully (id: %d)\n", result.Server.Name, result.Server.ID)
		if result.RootPassword != "" {
			fmt.Printf("Root password: %s\n", result.RootPassword)
		}
		return nil
	},
}

func init() {
	serverCmd.AddCommand(serverCreateCmd)

	serverCreateCmd.Flags().StringVarP(&createFlags.name, "name", "n", "", "Server name (required)")
	serverCreateCmd.Flags().StringVarP(&createFlags.serverType, "type", "t", "cx23", "Server type (e.g. cx23, cax11, cx33)")
	serverCreateCmd.Flags().StringVarP(&createFlags.image, "image", "i", "ubuntu-24.04", "OS image name")
	serverCreateCmd.Flags().StringVarP(&createFlags.location, "location", "l", "nbg1", "Datacenter location (e.g. nbg1, fsn1, hel1)")
	serverCreateCmd.Flags().StringVarP(&createFlags.sshKey, "ssh-key", "k", "", "SSH key name to inject")

	_ = serverCreateCmd.MarkFlagRequired("name")
}
