# sandbox

Ephemeral Hetzner cloud sandbox — spin up isolated code-execution VMs, run untrusted code in gVisor containers, and measure cold-start latency end-to-end.

## Prerequisites

- Go 1.22+
- A Hetzner API token set in the environment or a `.env` file in the project root
- An SSH key uploaded to Hetzner (used for `server create` and `server bootstrap`)

```bash
go build -o sandbox .

# Either export directly:
export HETZNER_API_KEY=<your token>

# Or create a .env file (loaded automatically):
echo 'HETZNER_API_KEY=<your token>' > .env
```

---

## Flows

### 1. Build the golden image (run once)

Creates a Hetzner snapshot with containerd, gVisor, the code runner, and the idle-shutdown daemon pre-installed. Every future `server create` boots from it in ~10s.

```bash
# Boot a fresh Ubuntu server
sandbox server create --name setup-box --image ubuntu-24.04 --ssh-key <key-name> --wait

# Install containerd, gVisor, pre-pull language images, code runner, and idle daemon
sandbox server bootstrap setup-box --identity ~/.ssh/id_ed25519

# Snapshot the configured server and mark it as the default boot image
sandbox snapshot create setup-box --power-off --set-default

# Delete the setup server
sandbox server delete setup-box
```

The runner service is installed but not started during bootstrap — it requires a secret token injected per-server at create time (see below).

---

### 2. Spin up a runner server

Boot a server from the golden snapshot and start the code-runner HTTP API in one command:

```bash
sandbox server create \
  --name my-runner \
  --ssh-key <key-name> \
  --wait \
  --identity ~/.ssh/id_ed25519 \
  --runner-token <secret>
```

`--runner-token` writes the token into a systemd drop-in and starts the runner. Once the command returns, the API is live at `http://<ip>:8080`.

The runner token is a secret you choose — any random string. It is not your Hetzner API key. Generate one with:

```bash
openssl rand -hex 16
```

---

### 3. Run code

```bash
# Health check (no auth required)
curl http://<ip>:8080/health

# Run a Python snippet
curl -s -X POST http://<ip>:8080/run \
  -H "Authorization: Bearer <secret>" \
  -H "Content-Type: application/json" \
  -d '{"language":"python","code":"print(\"hello\")"}'
# → {"output":"hello\n","duration_ms":312}

# Run Node
curl -s -X POST http://<ip>:8080/run \
  -H "Authorization: Bearer <secret>" \
  -H "Content-Type: application/json" \
  -d '{"language":"node","code":"console.log(\"hello\")"}'

# Run Go
curl -s -X POST http://<ip>:8080/run \
  -H "Authorization: Bearer <secret>" \
  -H "Content-Type: application/json" \
  -d '{"language":"go","code":"package main\nimport \"fmt\"\nfunc main(){fmt.Println(\"hello\")}"}'
```

Supported languages: `python`, `node`, `go`. Each run is isolated in a gVisor container with 512MB RAM and a 10s CPU timeout.

---

### 4. Benchmark runtimes (runc vs gVisor)

```bash
# 10 iterations per language per runtime
curl -s "http://<ip>:8080/benchmark?n=10" \
  -H "Authorization: Bearer <secret>" | jq .
```

Returns min/mean/max/P99 for each language under both `runc` and `gVisor (runsc)`.

---

### 5. Measure cold-start latency (Phase 3)

`sandbox trace` instruments every stage of a run end-to-end and prints a latency breakdown.

**Hot path** — send runs to an already-running server and get per-stage stats:

```bash
# Single run with span breakdown
sandbox trace \
  --ip <ip> \
  --runner-token <secret> \
  --language python \
  --code 'print("hello")'

# 20 runs with P50/P95/P99 per stage
sandbox trace \
  --ip <ip> \
  --runner-token <secret> \
  --language python \
  --code 'import time; time.sleep(0.1); print("done")' \
  --n 20
```

Example output (single run):
```
Stage                     Duration
────────────────────────────────────
HTTP round-trip           145ms
  image_lookup             12ms
  container_create         45ms
  task_create              82ms
  task_start               15ms
  exec                    234ms
```

Example output (20 runs):
```
Stage               P50     P95     P99     Min     Max
──────────────────────────────────────────────────────────
HTTP round-trip     145ms   178ms   201ms   132ms   245ms
  image_lookup       12ms    15ms    18ms     9ms    22ms
  container_create   45ms    52ms    61ms    40ms    74ms
  task_create        82ms    91ms   104ms    78ms   112ms
  task_start         15ms    18ms    22ms    12ms    28ms
  exec              234ms   289ms   312ms   220ms   345ms
```

**Cold-start path** — create a VM, measure all infrastructure stages, run code, delete the VM:

```bash
sandbox trace \
  --cold-start \
  --name trace-box \
  --ssh-key <key-name> \
  --runner-token <secret> \
  --identity ~/.ssh/id_ed25519 \
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

Add `--keep-server` to skip VM deletion (useful when you want to inspect the server afterwards).

---

## idled

The idle-shutdown daemon (`cmd/idled`) runs on the VM as a systemd service. It monitors CPU usage and network I/O every 30 seconds and calls `poweroff` after 5 consecutive idle minutes (10 × 30s checks below 10% CPU and 50KB/s network). A 2-minute grace period on startup prevents shutdown before work begins.

`sandbox server bootstrap` installs it. The systemd unit is embedded in the binary at build time so no extra files are needed.

---

## Token rotation

To update the runner token on a live server without rebooting:

```bash
ssh root@<ip> "printf '[Service]\nEnvironment=RUNNER_TOKEN=<new-secret>\n' \
  > /etc/systemd/system/runner.service.d/token.conf \
  && systemctl daemon-reload && systemctl restart runner"
```
