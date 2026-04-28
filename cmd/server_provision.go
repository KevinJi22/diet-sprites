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

var provisionFlags struct {
	identity string
	user     string
	arch     string
}

var serverProvisionCmd = &cobra.Command{
	Use:   "provision <server-name>",
	Short: "Build and install idled on a server",
	Long: `Cross-compile idled for the target server, copy it and the systemd
unit file over SSH, and enable the service.

Run this before snapshotting to bake the idle-shutdown daemon into the
golden image.

Example:
  sandbox server provision my-box --identity ~/.ssh/id_ed25519`,
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

		tmp, err := os.MkdirTemp("", "sandbox-provision-*")
		if err != nil {
			return fmt.Errorf("creating temp dir: %w", err)
		}
		defer os.RemoveAll(tmp)

		fmt.Printf("Building idled (linux/%s)...\n", provisionFlags.arch)
		binaryPath := filepath.Join(tmp, "idled")
		buildCmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/idled")
		buildCmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+provisionFlags.arch, "CGO_ENABLED=0")
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
		if err := scpFile(ip, provisionFlags.user, provisionFlags.identity, binaryPath, "/usr/local/bin/idled"); err != nil {
			return fmt.Errorf("copying binary: %w", err)
		}
		if err := scpFile(ip, provisionFlags.user, provisionFlags.identity, unitPath, "/etc/systemd/system/idled.service"); err != nil {
			return fmt.Errorf("copying unit file: %w", err)
		}

		fmt.Println("Enabling idled service...")
		if err := sshRun(ip, provisionFlags.user, provisionFlags.identity,
			"chmod +x /usr/local/bin/idled && systemctl daemon-reload && systemctl enable --now idled"); err != nil {
			return fmt.Errorf("enabling service: %w", err)
		}

		fmt.Printf("idled installed and enabled on %s\n", name)
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
	serverCmd.AddCommand(serverProvisionCmd)

	serverProvisionCmd.Flags().StringVarP(&provisionFlags.identity, "identity", "i", "", "Local SSH private key path (required)")
	serverProvisionCmd.Flags().StringVarP(&provisionFlags.user, "user", "u", "root", "SSH login user")
	serverProvisionCmd.Flags().StringVar(&provisionFlags.arch, "arch", "amd64", "Target architecture: amd64 or arm64")
	_ = serverProvisionCmd.MarkFlagRequired("identity")
}
