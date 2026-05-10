package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

var bootstrapFlags struct {
	identity string
	user     string
	arch     string
}

var serverBootstrapCmd = &cobra.Command{
	Use:   "bootstrap <server-name>",
	Short: "Install containerd, gVisor, and idled on a server",
	Long: `Prepares a fresh server to become the golden snapshot image.

Installs containerd and gVisor (runsc) via apt, wires runsc into containerd
as a runtime shim, then cross-compiles and installs idled as a systemd service.

Run this once on a fresh server, then snapshot the result.

Example:
  sandbox server bootstrap my-box --identity ~/.ssh/id_ed25519`,
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

		if err := provisionServer(ip, bootstrapFlags.user, bootstrapFlags.identity, bootstrapFlags.arch); err != nil {
			return err
		}

		fmt.Printf("Bootstrap complete on %s\n", name)
		return nil
	},
}

func sshRun(ip, user, identity, command string) error {
	cmd := exec.Command("ssh",
		"-i", identity,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "BatchMode=yes",
		fmt.Sprintf("%s@%s", user, ip),
		command,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// startRunnerWithToken writes a systemd drop-in containing RUNNER_TOKEN and
// restarts the runner service. Uses a single SSH command to avoid extra round trips.
// reset-failed clears any start-limit state accumulated from token-less boot crashes.
func startRunnerWithToken(ip, user, identity, token string) error {
	cmd := fmt.Sprintf(
		`mkdir -p /etc/systemd/system/runner.service.d && printf '[Service]\nEnvironment=RUNNER_TOKEN=%%s\n' %q > /etc/systemd/system/runner.service.d/token.conf && systemctl daemon-reload && systemctl reset-failed runner && systemctl restart runner`,
		token,
	)
	return sshRun(ip, user, identity, cmd)
}

func scpFile(ip, user, identity, src, dst string) error {
	cmd := exec.Command("scp",
		"-i", identity,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "BatchMode=yes",
		src,
		fmt.Sprintf("%s@%s:%s", user, ip, dst),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func init() {
	serverCmd.AddCommand(serverBootstrapCmd)

	serverBootstrapCmd.Flags().StringVarP(&bootstrapFlags.identity, "identity", "i", "", "Local SSH private key path (required)")
	serverBootstrapCmd.Flags().StringVarP(&bootstrapFlags.user, "user", "u", "root", "SSH login user")
	serverBootstrapCmd.Flags().StringVar(&bootstrapFlags.arch, "arch", "amd64", "Target architecture: amd64 or arm64")
	_ = serverBootstrapCmd.MarkFlagRequired("identity")
}
