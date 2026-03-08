package cmd

import (
	"fmt"
	"os"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

var client *hcloud.Client

var rootCmd = &cobra.Command{
	Use:   "sandbox",
	Short: "Manage ephemeral Hetzner servers",
	Long: `sandbox — ephemeral Hetzner server manager

Requires a Hetzner API token set in the environment:

  export HETZNER_API_KEY=your_token_here

Or place it in a .env file in the current directory:

  echo 'HETZNER_API_KEY=your_token_here' > .env

Get a token at: https://console.hetzner.cloud → project → Security → API Tokens`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		_ = godotenv.Load()
		token := os.Getenv("HETZNER_API_KEY")
		if token == "" {
			return fmt.Errorf("HETZNER_API_KEY is not set")
		}
		client = hcloud.NewClient(hcloud.WithToken(token))
		return nil
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
