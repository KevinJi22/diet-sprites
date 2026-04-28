# sandbox

CLI for spinning Hetzner cloud servers up and down.

## Prerequisites

- Go 1.22+
- `HCLOUD_TOKEN` environment variable set to a Hetzner API token
- An SSH key uploaded to Hetzner (for `server create --ssh-key` and `server bootstrap`)

```bash
go build -o sandbox .
export HCLOUD_TOKEN=<your token>
```

## Golden image workflow

Run this once to create a snapshot with containerd, gVisor, and the idle-shutdown daemon pre-installed. Every future `server create` boots from it in ~10s.

```bash
# 1. Boot a fresh server
sandbox server create --name setup-box --image ubuntu-24.04 --ssh-key <key-name> --wait

# 2. Install containerd, gVisor, pre-pull language images, and the idle-shutdown daemon
sandbox server bootstrap setup-box --identity ~/.ssh/id_ed25519

# 3. Snapshot and set as default boot image
sandbox snapshot create setup-box --power-off --set-default

# 4. Clean up the setup server
sandbox server delete setup-box
```

From here, `sandbox server create --name <name>` boots the golden image automatically.

## Commands

### server

```
sandbox server create    --name <name> [--type cx23] [--location nbg1] [--ssh-key <name>] [--wait]
sandbox server delete    <name>
sandbox server wait      <name>        # probe SSH with exponential backoff, print latency histogram
sandbox server bootstrap <name> --identity <key-path> [--user root] [--arch amd64]
```

### snapshot

```
sandbox snapshot list
sandbox snapshot create <server-name> [--description golden-image] [--power-off] [--set-default]
sandbox snapshot delete <id>
```

### key

```
sandbox key upload <name> --public-key-file <path>
```

## idled

The idle-shutdown daemon (`cmd/idled`) runs on the VM as a systemd service. It monitors CPU usage and network I/O every 30 seconds and calls `poweroff` after 5 consecutive idle minutes (10 × 30s checks below 10% CPU and 50KB/s network). A 2-minute grace period on startup prevents it from shutting down a server before work begins.

`sandbox server bootstrap` installs it. The systemd unit is embedded in the binary at build time so no extra files are needed.
