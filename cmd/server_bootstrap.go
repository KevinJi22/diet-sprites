package cmd

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

//go:embed assets/idled.service
var idledServiceUnit []byte

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

		fmt.Println("Installing containerd and gVisor...")
		// TODO(human): write the shell script that installs containerd + gVisor
		// and wires runsc into /etc/containerd/config.toml.
		installScript := `
			apt-get update && \
			apt-get install -y containerd && \
			mkdir -p /etc/containerd && \
			containerd config default > /etc/containerd/config.toml && \
			systemctl enable --now containerd && \
			curl -fsSL https://gvisor.dev/archive.key | gpg --dearmor -o /usr/share/keyrings/gvisor-archive-keyring.gpg && \
			echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/gvisor-archive-keyring.gpg] https://storage.googleapis.com/gvisor/releases release main" | tee /etc/apt/sources.list.d/gvisor.list && \
			apt-get update && \
			apt-get install -y runsc && \
			cat >> /etc/containerd/config.toml <<'EOF'

			[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc]
			runtime_type = "io.containerd.runsc.v1"
			EOF
			systemctl restart containerd && \
			ctr image pull docker.io/library/python:3.11-slim && \
			ctr image pull docker.io/library/node:20-slim
		`
		if err := sshRun(ip, bootstrapFlags.user, bootstrapFlags.identity, installScript); err != nil {
			return fmt.Errorf("installing containerd/gVisor: %w", err)
		}

		tmp, err := os.MkdirTemp("", "sandbox-bootstrap-*")
		if err != nil {
			return fmt.Errorf("creating temp dir: %w", err)
		}
		defer os.RemoveAll(tmp)

		fmt.Printf("Building idled (linux/%s)...\n", bootstrapFlags.arch)
		binaryPath := filepath.Join(tmp, "idled")
		buildCmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/idled")
		buildCmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+bootstrapFlags.arch, "CGO_ENABLED=0")
		buildCmd.Stdout = os.Stdout
		buildCmd.Stderr = os.Stderr
		if err := buildCmd.Run(); err != nil {
			return fmt.Errorf("building idled: %w", err)
		}

		unitPath := filepath.Join(tmp, "idled.service")
		if err := os.WriteFile(unitPath, idledServiceUnit, 0644); err != nil {
			return fmt.Errorf("writing unit file: %w", err)
		}

		fmt.Printf("Copying files to %s (%s)...\n", name, ip)
		if err := scpFile(ip, bootstrapFlags.user, bootstrapFlags.identity, binaryPath, "/usr/local/bin/idled"); err != nil {
			return fmt.Errorf("copying binary: %w", err)
		}
		if err := scpFile(ip, bootstrapFlags.user, bootstrapFlags.identity, unitPath, "/etc/systemd/system/idled.service"); err != nil {
			return fmt.Errorf("copying unit file: %w", err)
		}

		fmt.Println("Enabling idled service...")
		if err := sshRun(ip, bootstrapFlags.user, bootstrapFlags.identity,
			"chmod +x /usr/local/bin/idled && systemctl daemon-reload && systemctl enable --now idled"); err != nil {
			return fmt.Errorf("enabling service: %w", err)
		}

		fmt.Printf("Bootstrap complete on %s\n", name)
		return nil
	},
}

func sshRun(ip, user, identity, command string) error {
	cmd := exec.Command("ssh",
		"-i", identity,
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "BatchMode=yes",
		fmt.Sprintf("%s@%s", user, ip),
		command,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func scpFile(ip, user, identity, src, dst string) error {
	cmd := exec.Command("scp",
		"-i", identity,
		"-o", "StrictHostKeyChecking=accept-new",
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
