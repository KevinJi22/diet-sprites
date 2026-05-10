# sandbox

Ephemeral Hetzner cloud sandbox — spin up isolated code-execution VMs, run untrusted code in gVisor containers, and measure cold-start latency end-to-end.

## Setup

**Prerequisites:** Go 1.22+, a Hetzner API token, and an SSH key uploaded to Hetzner.

```bash
go build -o sandbox .

# Either export directly:
export HETZNER_API_KEY=<your token>

# Or drop into a .env file (loaded automatically):
echo 'HETZNER_API_KEY=<your token>' > .env
```

---

## Getting started

### Build the golden image (once)

Creates a Hetzner snapshot with containerd, gVisor, the code runner, and the idle-shutdown daemon pre-installed. Every future server boots from it in ~10s instead of going through a full install.

```bash
sandbox image build --ssh-key <key-name> --identity ~/.ssh/id_ed25519
```

This boots a temporary Ubuntu server, installs everything, snapshots it, and deletes the setup server. Takes a few minutes the first time; saves it ever after.

| Flag | Default | Description |
|------|---------|-------------|
| `--name` | `setup-box` | Temporary server name |
| `--type` | `cx23` | Hetzner server type |
| `--location` | `nbg1` | Datacenter location |
| `--arch` | `amd64` | `amd64` or `arm64` |
| `--description` | `golden-image` | Snapshot description |

### Start a runner server

```bash
sandbox server create \
  --name my-runner \
  --ssh-key <key-name> \
  --identity ~/.ssh/id_ed25519 \
  --runner-token <secret> \
  --wait
```

`--runner-token` is a secret you choose (not your Hetzner API key). Generate one with `openssl rand -hex 16`. Once the command returns, the server is up and the runner is live.

---

## Running code

Run an inline snippet against a running server:

```bash
sandbox run --ip <ip> --language python --code 'print("hello")'
sandbox run --ip <ip> --language node   --code 'console.log("hello")'
sandbox run --ip <ip> --language go     --code 'package main
import "fmt"
func main() { fmt.Println("hello") }'
```

Run a file directly:

```bash
sandbox run --ip <ip> --language python --file script.py
sandbox run --ip <ip> --language python --file examples/longrunning/slow_import.py
```

`--runner-token` can be omitted if you created the server with `--runner-token` — the token is stored in config and looked up automatically by IP.

Each run is isolated in a gVisor container: 512MB RAM limit, 10s CPU timeout, no network access. Supported languages: `python`, `node`, `go`.

---

## Benchmarking

Compare gVisor vs runc overhead across all supported languages:

```bash
# 10 iterations per language per runtime, returns a JSON report
curl -s "http://<ip>:8080/benchmark?n=10" \
  -H "Authorization: Bearer <secret>" | jq .
```

Returns min/mean/max/P99 for each language under both runtimes. Useful for quantifying the syscall-interception overhead gVisor adds vs the security it provides.

---

## Tracing latency

`sandbox trace` breaks down where time actually goes, stage by stage.

### Hot path — runs against an already-running server

```bash
# Single run with a full span breakdown
sandbox trace --ip <ip> --runner-token <secret> --language python --code 'print("hello")'

# 20 runs with P50/P95/P99 per stage
sandbox trace --ip <ip> --runner-token <secret> --language python \
  --code 'print("hello")' --n 20
```

Example output (20 runs):
```
Completed 20/20 runs (cache hits: 0)

Stage               P50     P95     P99     Min     Max
──────────────────────────────────────────────────────────
HTTP round-trip     145ms   178ms   201ms   132ms   245ms
  image_lookup       12ms    15ms    18ms     9ms    22ms
  container_create   45ms    52ms    61ms    40ms    74ms
  task_create        82ms    91ms   104ms    78ms   112ms
  task_start         15ms    18ms    22ms    12ms    28ms
  exec              234ms   289ms   312ms   220ms   345ms
```

Once checkpoint/restore is active, `restore` and `checkpoint` stages appear alongside a cache-hit count.

### Cold-start path — creates a VM, measures all stages, deletes it

```bash
sandbox trace \
  --cold-start \
  --name trace-box \
  --ssh-key <key-name> \
  --identity ~/.ssh/id_ed25519 \
  --runner-token <secret> \
  --language python \
  --code 'print("hello")'
```

Example output:
```
Stage                     Duration
────────────────────────────────────
Hetzner API call            1.2s
VM boot                    11.4s
SSH ready                   3.2s
Runner startup              0.8s
────────────────────────────────────
HTTP round-trip             145ms
  image_lookup               12ms
  container_create           45ms
  task_create                82ms
  task_start                 15ms
  exec                      234ms
────────────────────────────────────
Total (cold start)         16.7s
```

Add `--keep-server` to skip VM deletion (useful for debugging).

### Testing checkpoint/restore payoff

`examples/longrunning/` has two programs designed to make the before/after measurable:

- **`slow_import.py`** — heavy stdlib imports followed by exit. Cold start is dominated by import time (~150–400ms). Use this to measure cold-start amortization once checkpointing is active.
- **`long_compute.py`** — quick startup, long iterative loop (stand-in for training/simulation). Use this to test mid-run pause/resume.

```bash
# Measure cold start cost of the import-heavy program
sandbox trace --ip <ip> --runner-token <secret> \
  --language python --file examples/longrunning/slow_import.py --n 20
```

---

## Managing servers and snapshots

```bash
sandbox server list              # list running servers
sandbox server delete --name my-runner

sandbox snapshot list            # list snapshots, shows which is the default
sandbox snapshot delete <id>     # clean up old snapshots (Hetzner charges per GB)
```

---

## idled — auto-shutdown daemon

Every server bootstrapped from the golden image runs `idled`, a daemon that monitors CPU and network I/O every 30 seconds. After 5 consecutive idle minutes (below 10% CPU and 50KB/s network), it calls `poweroff`. A 2-minute grace period prevents early shutdown on fresh boots.

This means servers self-destruct when idle — no runaway billing from forgotten instances.

---

## Token rotation

To update the runner token on a live server without rebooting:

```bash
ssh root@<ip> "printf '[Service]\nEnvironment=RUNNER_TOKEN=<new-secret>\n' \
  > /etc/systemd/system/runner.service.d/token.conf \
  && systemctl daemon-reload && systemctl restart runner"
```
