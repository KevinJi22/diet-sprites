package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	_ "embed"
)

//go:embed assets/idled.service
var idledServiceUnit []byte

//go:embed assets/runner.service
var runnerServiceUnit []byte

// bootstrapInstallScript is the canonical server setup script. Both
// "image build" and "server bootstrap" run this to stay in sync.
//
// Key details baked in here (see notes.md "Debugging diary"):
//   - runsc wrapper: routes through io.containerd.runc.v2 with BinaryName
//     instead of the dedicated runsc shim, which deadlocks against containerd 2.x
//   - --platform=ptrace: systrap stalls on Hetzner VMs that lack /dev/kvm
//   - --ignore-cgroups: containerd already sets the cgroup; runsc re-doing it deadlocks
//   - apparmor_restrict_unprivileged_userns=0: Ubuntu 24.04 default blocks gVisor's gofer/sentry
//   - smoke test via runc.v2 + --runc-binary: validates the production path, not the broken shim
const bootstrapInstallScript = `
	set -euo pipefail

	apt-get update
	apt-get install -y containerd curl gpg

	mkdir -p /etc/containerd
	containerd config default > /etc/containerd/config.toml
	systemctl enable --now containerd

	curl -fsSL https://gvisor.dev/archive.key | gpg --dearmor -o /usr/share/keyrings/gvisor-archive-keyring.gpg
	echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/gvisor-archive-keyring.gpg] https://storage.googleapis.com/gvisor/releases release main" > /etc/apt/sources.list.d/gvisor.list
	apt-get update
	apt-get install -y runsc

	# Ubuntu 24.04 disables unprivileged user namespaces by default; gVisor's
	# gofer and sentry both need them or sandbox start hangs until timeout.
	echo 'kernel.apparmor_restrict_unprivileged_userns=0' > /etc/sysctl.d/99-gvisor.conf
	sysctl --system

	# Wrap runsc so every invocation gets debug logs + ptrace platform +
	# ignore-cgroups (cgroup v2 setup inside runsc stalls otherwise). The
	# runner invokes runsc through the runc shim with BinaryName, so this
	# wrapper is the single place runsc flags can be set globally.
	if [ ! -f /usr/bin/runsc.real ]; then
		mv /usr/bin/runsc /usr/bin/runsc.real
	fi
	cat > /usr/bin/runsc <<'WRAP'
#!/bin/sh
exec /usr/bin/runsc.real \
  --debug --debug-log=/var/log/runsc/runsc.%COMMAND%.log \
  --platform=ptrace --ignore-cgroups "$@"
WRAP
	chmod +x /usr/bin/runsc
	mkdir -p /var/log/runsc

	systemctl restart containerd

	# Pre-pull language images.
	ctr -n runner image pull docker.io/library/python:3.11-slim
	ctr -n runner image pull docker.io/library/node:20-slim
	ctr -n runner image pull docker.io/library/golang:1.22-alpine

	# Smoke-test gVisor end-to-end via the runc shim with runsc as the
	# binary — same path the runner uses. The dedicated runsc.v1 shim
	# deadlocks against containerd 2.x, which is why we route through
	# runc.v2 with BinaryName=/usr/bin/runsc.
	ctr -n runner image pull docker.io/library/hello-world:latest
	ctr -n runner run --rm \
		--runtime io.containerd.runc.v2 \
		--runc-binary /usr/bin/runsc \
		docker.io/library/hello-world:latest gvisor-smoke-test
`

type binaryDef struct {
	pkg       string
	name      string
	unit      []byte
	dst       string
	enableCmd string
}

var serverBinaries = []binaryDef{
	{
		pkg:       "./cmd/idled",
		name:      "idled",
		unit:      nil, // set in init
		dst:       "/usr/local/bin/idled",
		enableCmd: "chmod +x /usr/local/bin/idled && systemctl daemon-reload && systemctl enable --now idled",
	},
	{
		pkg:       "./cmd/runner",
		name:      "runner",
		unit:      nil, // set in init
		dst:       "/usr/local/bin/runner",
		enableCmd: "chmod +x /usr/local/bin/runner && systemctl daemon-reload && systemctl enable runner",
	},
}

func init() {
	serverBinaries[0].unit = idledServiceUnit
	serverBinaries[1].unit = runnerServiceUnit
}

// provisionServer installs containerd/gVisor and deploys runner/idled to the server at ip.
func provisionServer(ip, user, identity, arch string) error {
	fmt.Println("Installing containerd and gVisor...")
	if err := sshRun(ip, user, identity, bootstrapInstallScript); err != nil {
		return fmt.Errorf("installing containerd/gVisor: %w", err)
	}
	return deployBinaries(ip, user, identity, arch)
}

// deployBinaries cross-compiles idled and runner, copies them to ip, and enables their services.
func deployBinaries(ip, user, identity, arch string) error {
	tmp, err := os.MkdirTemp("", "sandbox-deploy-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	for _, bin := range serverBinaries {
		fmt.Printf("Building %s (linux/%s)...\n", bin.name, arch)
		binPath := filepath.Join(tmp, bin.name)
		buildCmd := exec.Command("go", "build", "-o", binPath, bin.pkg)
		buildCmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0")
		buildCmd.Stdout = os.Stdout
		buildCmd.Stderr = os.Stderr
		if err := buildCmd.Run(); err != nil {
			return fmt.Errorf("building %s: %w", bin.name, err)
		}

		unitPath := filepath.Join(tmp, bin.name+".service")
		if err := os.WriteFile(unitPath, bin.unit, 0644); err != nil {
			return fmt.Errorf("writing %s unit: %w", bin.name, err)
		}

		if err := scpFile(ip, user, identity, binPath, bin.dst); err != nil {
			return fmt.Errorf("copying %s binary: %w", bin.name, err)
		}
		if err := scpFile(ip, user, identity, unitPath, "/etc/systemd/system/"+bin.name+".service"); err != nil {
			return fmt.Errorf("copying %s unit: %w", bin.name, err)
		}

		fmt.Printf("Enabling %s service...\n", bin.name)
		if err := sshRun(ip, user, identity, bin.enableCmd); err != nil {
			return fmt.Errorf("enabling %s: %w", bin.name, err)
		}
	}
	return nil
}
