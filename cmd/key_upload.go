package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/spf13/cobra"
)

var uploadFlags struct {
	name    string
	keyPath string
}

var keyUploadCmd = &cobra.Command{
	Use:   "upload",
	Short: "Upload a local public key to Hetzner",
	Long: `Upload a public SSH key to your Hetzner account.

If you don't have an SSH key yet, generate one with:

  ssh-keygen -t ed25519 -C "your@email.com"

This creates ~/.ssh/id_ed25519 (private) and ~/.ssh/id_ed25519.pub (public).
Only the public key is uploaded to Hetzner.

Example:

  sandbox key upload --name my-key
  sandbox key upload --name my-key --path ~/.ssh/id_rsa.pub

Once uploaded, reference the key by name when creating a server:

  sandbox server create --name my-box --ssh-key my-key`,
	RunE: func(cmd *cobra.Command, args []string) error {
		pubKey, err := os.ReadFile(uploadFlags.keyPath)
		if err != nil {
			return fmt.Errorf("reading public key %q: %w", uploadFlags.keyPath, err)
		}

		ctx := context.Background()
		key, _, err := client.SSHKey.Create(ctx, hcloud.SSHKeyCreateOpts{
			Name:      uploadFlags.name,
			PublicKey: string(pubKey),
		})
		if err != nil {
			return fmt.Errorf("uploading key: %w", err)
		}

		fmt.Printf("SSH key %q uploaded (id: %d, fingerprint: %s)\n", key.Name, key.ID, key.Fingerprint)
		return nil
	},
}

func init() {
	keyCmd.AddCommand(keyUploadCmd)

	home, _ := os.UserHomeDir()
	keyUploadCmd.Flags().StringVarP(&uploadFlags.name, "name", "n", "", "Name for the key in Hetzner (required)")
	keyUploadCmd.Flags().StringVarP(&uploadFlags.keyPath, "path", "p", home+"/.ssh/id_ed25519.pub", "Path to public key file")

	_ = keyUploadCmd.MarkFlagRequired("name")
}
