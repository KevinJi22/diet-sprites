package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/kevin/diet_sprites/internal/config"
	"github.com/kevin/diet_sprites/internal/sshprobe"
	"github.com/spf13/cobra"
)

var traceFlagVars struct {
	ip         string
	name       string
	sshKey     string
	token      string
	language   string
	code       string
	identity   string
	user       string
	coldStart  bool
	n          int
	keepServer bool
}

// traceRunResult mirrors the runner's RunResult for JSON decoding.
type traceRunResult struct {
	Output     string      `json:"output"`
	DurationMS int64       `json:"duration_ms"`
	TimedOut   bool        `json:"timed_out,omitempty"`
	Spans      *traceSpans `json:"spans,omitempty"`
}

type traceSpans struct {
	ImageLookupMS     int64 `json:"image_lookup_ms"`
	ContainerCreateMS int64 `json:"container_create_ms"`
	TaskCreateMS      int64 `json:"task_create_ms"`
	TaskStartMS       int64 `json:"task_start_ms"`
	ExecMS            int64 `json:"exec_ms"`
}

type spanSamples struct {
	httpMS            []int64
	imageLookupMS     []int64
	containerCreateMS []int64
	taskCreateMS      []int64
	taskStartMS       []int64
	execMS            []int64
}

var traceCmd = &cobra.Command{
	Use:   "trace",
	Short: "Measure per-stage latency of a sandbox run",
	Long: `Instruments every stage of a sandbox execution and prints a latency breakdown.

Hot path (existing runner):
  sandbox trace --ip 1.2.3.4 --runner-token mytoken --language python --code 'print("hi")'
  sandbox trace --ip 1.2.3.4 --runner-token mytoken --language python --code 'print("hi")' --n 20

Cold-start path (creates a new VM, measures all stages, then deletes it):
  sandbox trace --cold-start --name trace-box --ssh-key my-key \
    --runner-token mytoken --identity ~/.ssh/id_ed25519 \
    --language python --code 'print("hi")'`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if traceFlagVars.coldStart {
			return runColdStartTrace(cmd.Context())
		}
		if traceFlagVars.ip == "" {
			return fmt.Errorf("--ip is required (or use --cold-start to create a VM)")
		}
		return runHotTrace(cmd.Context(), traceFlagVars.ip)
	},
}

// runColdStartTrace creates a VM, measures every infrastructure stage, runs one
// code execution, prints a full breakdown, then deletes the server.
func runColdStartTrace(ctx context.Context) error {
	if traceFlagVars.name == "" {
		return fmt.Errorf("--name is required for --cold-start")
	}
	if traceFlagVars.token == "" {
		return fmt.Errorf("--runner-token is required for --cold-start")
	}
	if traceFlagVars.identity == "" {
		return fmt.Errorf("--identity is required for --cold-start")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	var imageRef *hcloud.Image
	if cfg.DefaultSnapshotID != 0 {
		fmt.Printf("Using default snapshot %d\n", cfg.DefaultSnapshotID)
		imageRef = &hcloud.Image{ID: cfg.DefaultSnapshotID}
	} else {
		imageRef = &hcloud.Image{Name: "ubuntu-24.04"}
	}

	opts := hcloud.ServerCreateOpts{
		Name:             traceFlagVars.name,
		ServerType:       &hcloud.ServerType{Name: "cx23"},
		Image:            imageRef,
		Location:         &hcloud.Location{Name: "nbg1"},
		StartAfterCreate: hcloud.Ptr(true),
	}
	if traceFlagVars.sshKey != "" {
		key, _, err := client.SSHKey.GetByName(ctx, traceFlagVars.sshKey)
		if err != nil {
			return fmt.Errorf("looking up SSH key: %w", err)
		}
		if key == nil {
			return fmt.Errorf("SSH key %q not found", traceFlagVars.sshKey)
		}
		opts.SSHKeys = []*hcloud.SSHKey{key}
	}

	epoch := time.Now()

	fmt.Printf("Creating server %q...\n", traceFlagVars.name)
	createResult, _, err := client.Server.Create(ctx, opts)
	if err != nil {
		return fmt.Errorf("creating server: %w", err)
	}
	tAPICall := time.Since(epoch)

	if err := client.Action.WaitFor(ctx, createResult.Action); err != nil {
		return fmt.Errorf("waiting for server: %w", err)
	}
	tVMReady := time.Since(epoch)

	server, _, err := client.Server.GetByID(ctx, createResult.Server.ID)
	if err != nil {
		return fmt.Errorf("fetching server: %w", err)
	}
	ip := server.PublicNet.IPv4.IP.String()

	if !traceFlagVars.keepServer {
		defer func() {
			fmt.Printf("\nCleaning up: deleting %q...\n", traceFlagVars.name)
			_, _, _ = client.Server.DeleteWithResult(context.Background(), server)
		}()
	}

	fmt.Printf("Server ready at %s — probing SSH...\n", ip)
	if _, err := sshprobe.Probe(ctx, sshprobe.DefaultConfig(ip)); err != nil {
		return fmt.Errorf("SSH probe: %w", err)
	}
	tSSHReady := time.Since(epoch)

	fmt.Println("Starting runner...")
	dropIn := fmt.Sprintf("[Service]\nEnvironment=RUNNER_TOKEN=%s\n", traceFlagVars.token)
	startRunner := fmt.Sprintf(
		`mkdir -p /etc/systemd/system/runner.service.d && printf %%s %q > /etc/systemd/system/runner.service.d/token.conf && systemctl daemon-reload && systemctl start runner`,
		dropIn,
	)
	if err := sshRun(ip, traceFlagVars.user, traceFlagVars.identity, startRunner); err != nil {
		return fmt.Errorf("starting runner: %w", err)
	}

	fmt.Println("Waiting for runner health check...")
	if err := waitForHealth(ctx, ip); err != nil {
		return fmt.Errorf("runner health: %w", err)
	}
	tRunnerReady := time.Since(epoch)

	fmt.Println("Submitting code...")
	tHTTPStart := time.Now()
	result, err := postRun(ctx, ip, traceFlagVars.token, traceFlagVars.language, traceFlagVars.code)
	tHTTP := time.Since(tHTTPStart)
	if err != nil {
		return fmt.Errorf("run request: %w", err)
	}
	tTotal := time.Since(epoch)

	fmt.Println()
	printColdStartTable(
		tAPICall,
		tVMReady-tAPICall,
		tSSHReady-tVMReady,
		tRunnerReady-tSSHReady,
		tHTTP,
		tTotal,
		result,
	)
	if result.Output != "" {
		fmt.Printf("\nOutput: %s", result.Output)
	}
	return nil
}

// runHotTrace submits N runs to an already-running runner and prints per-stage stats.
func runHotTrace(ctx context.Context, ip string) error {
	n := traceFlagVars.n
	if n < 1 {
		n = 1
	}

	ss := &spanSamples{}
	fmt.Printf("Running %d iteration(s) against %s...\n", n, ip)
	for i := range n {
		t0 := time.Now()
		result, err := postRun(ctx, ip, traceFlagVars.token, traceFlagVars.language, traceFlagVars.code)
		httpMS := time.Since(t0).Milliseconds()
		if err != nil {
			fmt.Printf("  run %d failed: %v\n", i+1, err)
			continue
		}
		ss.httpMS = append(ss.httpMS, httpMS)
		if result.Spans != nil {
			ss.imageLookupMS = append(ss.imageLookupMS, result.Spans.ImageLookupMS)
			ss.containerCreateMS = append(ss.containerCreateMS, result.Spans.ContainerCreateMS)
			ss.taskCreateMS = append(ss.taskCreateMS, result.Spans.TaskCreateMS)
			ss.taskStartMS = append(ss.taskStartMS, result.Spans.TaskStartMS)
			ss.execMS = append(ss.execMS, result.Spans.ExecMS)
		}
	}

	fmt.Println()
	printHotTable(ss, n)
	return nil
}

// waitForHealth polls /health until the runner responds 200 or 30s elapse.
func waitForHealth(ctx context.Context, ip string) error {
	url := fmt.Sprintf("http://%s:8080/health", ip)
	hc := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := hc.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return nil
		}
		if resp != nil {
			resp.Body.Close()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("runner not healthy after 30s")
}

// postRun sends a POST /run request and returns the decoded result.
func postRun(ctx context.Context, ip, token, language, code string) (*traceRunResult, error) {
	body, err := json.Marshal(map[string]string{"language": language, "code": code})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("http://%s:8080/run", ip), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("runner returned HTTP %d", resp.StatusCode)
	}

	var result traceRunResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &result, nil
}

const colWidth = 24

func printColdStartTable(apiCall, vmBoot, sshReady, runnerStart, httpRT, total time.Duration, result *traceRunResult) {
	sep := strings.Repeat("─", colWidth+2+10)
	fmt.Printf("%-*s  %s\n", colWidth, "Stage", "Duration")
	fmt.Println(sep)
	fmt.Printf("%-*s  %s\n", colWidth, "Hetzner API call", fmtDur(apiCall))
	fmt.Printf("%-*s  %s\n", colWidth, "VM boot", fmtDur(vmBoot))
	fmt.Printf("%-*s  %s\n", colWidth, "SSH ready", fmtDur(sshReady))
	fmt.Printf("%-*s  %s\n", colWidth, "Runner startup", fmtDur(runnerStart))
	fmt.Println(sep)
	fmt.Printf("%-*s  %s\n", colWidth, "HTTP round-trip", fmtDur(httpRT))
	if result.Spans != nil {
		fmt.Printf("  %-*s  %s\n", colWidth-2, "image_lookup", fmtMS(result.Spans.ImageLookupMS))
		fmt.Printf("  %-*s  %s\n", colWidth-2, "container_create", fmtMS(result.Spans.ContainerCreateMS))
		fmt.Printf("  %-*s  %s\n", colWidth-2, "task_create", fmtMS(result.Spans.TaskCreateMS))
		fmt.Printf("  %-*s  %s\n", colWidth-2, "task_start", fmtMS(result.Spans.TaskStartMS))
		fmt.Printf("  %-*s  %s\n", colWidth-2, "exec", fmtMS(result.Spans.ExecMS))
	} else {
		fmt.Printf("  (no spans — implement TODO(human) in execute() to see breakdown)\n")
	}
	fmt.Println(sep)
	fmt.Printf("%-*s  %s\n", colWidth, "Total (cold start)", fmtDur(total))
}

func printHotTable(ss *spanSamples, n int) {
	type row struct {
		label string
		vals  []int64
	}
	rows := []row{
		{"HTTP round-trip", ss.httpMS},
		{"  image_lookup", ss.imageLookupMS},
		{"  container_create", ss.containerCreateMS},
		{"  task_create", ss.taskCreateMS},
		{"  task_start", ss.taskStartMS},
		{"  exec", ss.execMS},
	}

	header := fmt.Sprintf("%-*s  %6s  %6s  %6s  %6s  %6s", colWidth, "Stage", "P50", "P95", "P99", "Min", "Max")
	sep := strings.Repeat("─", len(header))
	fmt.Printf("Completed %d/%d runs\n\n", len(ss.httpMS), n)
	fmt.Println(header)
	fmt.Println(sep)
	for _, r := range rows {
		if len(r.vals) == 0 {
			continue
		}
		sorted := make([]int64, len(r.vals))
		copy(sorted, r.vals)
		sort.Slice(sorted, func(a, b int) bool { return sorted[a] < sorted[b] })
		fmt.Printf("%-*s  %6s  %6s  %6s  %6s  %6s\n",
			colWidth, r.label,
			fmtMS(pct(sorted, 0.50)),
			fmtMS(pct(sorted, 0.95)),
			fmtMS(pct(sorted, 0.99)),
			fmtMS(sorted[0]),
			fmtMS(sorted[len(sorted)-1]),
		)
	}
}

func pct(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[int(float64(len(sorted)-1)*p)]
}

func fmtDur(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func fmtMS(ms int64) string {
	if ms == 0 {
		return "-"
	}
	return fmt.Sprintf("%dms", ms)
}

func init() {
	rootCmd.AddCommand(traceCmd)

	traceCmd.Flags().StringVar(&traceFlagVars.ip, "ip", "", "IP of a running sandbox server")
	traceCmd.Flags().StringVar(&traceFlagVars.token, "runner-token", "", "Bearer token for the runner HTTP API")
	traceCmd.Flags().StringVar(&traceFlagVars.language, "language", "python", "Language to run (python, node, go)")
	traceCmd.Flags().StringVar(&traceFlagVars.code, "code", `import sys; print("hello")`, "Code to execute")
	traceCmd.Flags().IntVar(&traceFlagVars.n, "n", 1, "Number of hot-path runs (percentile stats printed when n > 1)")

	traceCmd.Flags().BoolVar(&traceFlagVars.coldStart, "cold-start", false, "Create a new VM, measure all stages, then delete it")
	traceCmd.Flags().StringVar(&traceFlagVars.name, "name", "", "Server name for cold-start (required with --cold-start)")
	traceCmd.Flags().StringVar(&traceFlagVars.sshKey, "ssh-key", "", "SSH key name to inject (optional for cold-start)")
	traceCmd.Flags().StringVar(&traceFlagVars.identity, "identity", "", "SSH private key path (required with --cold-start)")
	traceCmd.Flags().StringVar(&traceFlagVars.user, "user", "root", "SSH login user")
	traceCmd.Flags().BoolVar(&traceFlagVars.keepServer, "keep-server", false, "Skip deleting the VM after cold-start trace")
}
