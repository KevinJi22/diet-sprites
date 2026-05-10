package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	runcoptions "github.com/containerd/containerd/runtime/v2/runc/options"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// safeBuffer is a bytes.Buffer safe for concurrent writes from containerd's
// stdout and stderr IO goroutines.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

const (
	setupTimeout = 60 * time.Second
	execTimeout  = 10 * time.Second
	maxCodeBytes = 64 * 1024
	memoryLimit  = uint64(512 * 1024 * 1024)
	cpuQuota     = int64(100_000)  // microseconds of CPU per period
	cpuPeriod    = uint64(100_000) // 100ms window — quota/period = fraction of 1 CPU
	// Internal labels — resolved to (containerd runtime, shim options) by
	// resolveRuntime. Both labels currently map to the runc.v2 shim; gVisor is
	// selected by passing the runsc binary path through the shim's options.
	// The native io.containerd.runsc.v1 shim deadlocks against containerd 2.x
	// (TTRPC API drift), so we route through the runc shim which containerd is
	// built and tested against.
	gvisorRuntime   = "gvisor"
	runcRuntime     = "runc"
	runscBinary     = "/usr/bin/runsc"
	shimRuntime     = "io.containerd.runc.v2"
	benchmarkWarmup = 2
	containerdSock  = "/run/containerd/containerd.sock"
	ctrNamespace    = "runner"
)

type RunRequest struct {
	Language string `json:"language"`
	Code     string `json:"code"`
}

type Spans struct {
	ImageLookupMS     int64 `json:"image_lookup_ms"`
	ContainerCreateMS int64 `json:"container_create_ms"`
	TaskCreateMS      int64 `json:"task_create_ms"`
	TaskStartMS       int64 `json:"task_start_ms"`
	ExecMS            int64 `json:"exec_ms"`
}

type RunResult struct {
	Output     string `json:"output"`
	DurationMS int64  `json:"duration_ms"`
	TimedOut   bool   `json:"timed_out,omitempty"`
	Spans      *Spans `json:"spans,omitempty"`
}

type langSpec struct {
	image   string
	ext     string
	command []string
}

var languages = map[string]langSpec{
	"python": {
		image:   "docker.io/library/python:3.11-slim",
		ext:     ".py",
		command: []string{"python3"},
	},
	"node": {
		image:   "docker.io/library/node:20-slim",
		ext:     ".js",
		command: []string{"node"},
	},
	"go": {
		image:   "docker.io/library/golang:1.22-alpine",
		ext:     ".go",
		command: []string{"go", "run"},
	},
}

func (r RunRequest) validate() error {
	if _, ok := languages[r.Language]; !ok {
		return fmt.Errorf("unsupported language %q", r.Language)
	}
	if len(r.Code) == 0 {
		return fmt.Errorf("code must not be empty")
	}
	if len(r.Code) > maxCodeBytes {
		return fmt.Errorf("code exceeds %dKB limit", maxCodeBytes/1024)
	}
	return nil
}

func execute(ctx context.Context, req RunRequest, runtime string) (*RunResult, error) {
	spec, ok := languages[req.Language]
	if !ok {
		return nil, fmt.Errorf("language is not supported: %s", req.Language)
	}

	f, err := os.CreateTemp("", "runner-*"+spec.ext)
	if err != nil {
		return nil, fmt.Errorf("creating temp file: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(req.Code); err != nil {
		return nil, fmt.Errorf("writing code: %w", err)
	}
	f.Close()

	setupCtx, setupCancel := context.WithTimeout(ctx, setupTimeout)
	defer setupCancel()
	setupCtx = namespaces.WithNamespace(setupCtx, ctrNamespace)

	var spans = &Spans{}

	ctrClient, err := getContainerdClient()
	if err != nil {
		return nil, fmt.Errorf("connecting to containerd: %w", err)
	}

	start := time.Now()
	image, err := ctrClient.GetImage(setupCtx, spec.image)
	spans.ImageLookupMS = time.Since(start).Milliseconds()
	if err != nil {
		return nil, fmt.Errorf("image %q not found (pull it first): %w", spec.image, err)
	}

	id := fmt.Sprintf("runner-%d", time.Now().UnixNano())

	specOpts, err := buildSpec(req, image, f.Name())
	if err != nil {
		return nil, fmt.Errorf("failed to build spec: %w", err)
	}

	var shimOpts *runcoptions.Options
	if runtime == gvisorRuntime {
		shimOpts = &runcoptions.Options{BinaryName: runscBinary}
	}

	start = time.Now()
	container, err := ctrClient.NewContainer(setupCtx, id,
		containerd.WithRuntime(shimRuntime, shimOpts),
		containerd.WithNewSnapshot(id, image),
		containerd.WithNewSpec(specOpts...),
	)
	spans.ContainerCreateMS = time.Since(start).Milliseconds()
	if err != nil {
		return nil, fmt.Errorf("creating container: %w", err)
	}
	defer container.Delete(context.Background(), containerd.WithSnapshotCleanup)

	var buf safeBuffer

	// Capture the cio.IO so we can call Wait() after process exit. exitCh fires
	// when the process exits (write end of FIFOs closes), but the containerd IO
	// goroutines that drain those FIFOs into buf may still be running. Wait()
	// blocks until they finish, preventing a race between reading buf and the drain.
	var taskIO cio.IO
	ioCreator := func(id string) (cio.IO, error) {
		io, err := cio.NewCreator(cio.WithStreams(nil, &buf, &buf))(id)
		if err != nil {
			return nil, err
		}
		taskIO = io
		return io, nil
	}

	start = time.Now()
	task, err := container.NewTask(setupCtx, ioCreator)
	spans.TaskCreateMS = time.Since(start).Milliseconds()
	if err != nil {
		return nil, fmt.Errorf("creating task: %w", err)
	}
	defer task.Delete(context.Background())

	// Wait must be registered before Start — see package doc.
	exitCh, err := task.Wait(context.Background())
	if err != nil {
		return nil, fmt.Errorf("registering wait: %w", err)
	}

	start = time.Now()
	err = task.Start(setupCtx)
	spans.TaskStartMS = time.Since(start).Milliseconds()
	if err != nil {
		return nil, fmt.Errorf("starting task: %w", err)
	}
	execStart := time.Now()

	// Switch to a fresh exec-only deadline so user code gets the full window.
	execCtx, execCancel := context.WithTimeout(context.Background(), execTimeout)
	defer execCancel()

	timedOut := false
	select {
	case <-exitCh:
	case <-execCtx.Done():
		timedOut = true
		_ = task.Kill(context.Background(), syscall.SIGKILL)
		<-exitCh
	}
	elapsed := time.Since(execStart)
	spans.ExecMS = elapsed.Milliseconds()

	// Drain IO: process exit closes the FIFO write ends, but the containerd
	// goroutines copying from those FIFOs into buf may not have finished yet.
	// Wait() blocks until they do.
	taskIO.Wait()

	return &RunResult{
		Output:     buf.String(),
		DurationMS: elapsed.Milliseconds(),
		TimedOut:   timedOut,
		Spans:      spans,
	}, nil
}

// benchmarkCode is a tiny program per language: enough to exercise startup + one print.
var benchmarkCode = map[string]string{
	"python": `import sys; print("ok")`,
	"node":   `console.log("ok")`,
	"go": `package main
import "fmt"
func main() { fmt.Println("ok") }`,
}

type runtimeStats struct {
	Samples int
	MinMS   int64
	MeanMS  int64
	MaxMS   int64
	P99MS   int64
}

type langResult struct {
	Language string
	Runc     runtimeStats
	Gvisor   runtimeStats
}

type BenchmarkReport struct {
	Iterations int
	Results    []langResult
}

func mean(samples []int64) float64 {
	if len(samples) == 0 {
		return 0.0
	}
	sum := int64(0)

	for _, val := range samples {
		sum += val
	}

	return float64(sum) / float64(len(samples))
}

func benchmark(ctx context.Context, iterations int) (*BenchmarkReport, error) {
	report := &BenchmarkReport{Iterations: iterations}
	for language, program := range benchmarkCode {
		langResult := langResult{Language: language}
		req := RunRequest{Language: language, Code: program}
		for _, runtime := range []string{runcRuntime, gvisorRuntime} {
			for range benchmarkWarmup {
				_, _ = execute(ctx, req, runtime)
			}

			samples := make([]int64, 0, iterations)
			for i := range iterations {
				result, err := execute(ctx, req, runtime)
				if err != nil {
					fmt.Printf("failed to execute code on iteration %d, skipping\n", i)
					continue
				}
				samples = append(samples, result.DurationMS)
			}

			if len(samples) == 0 {
				continue
			}
			slices.Sort(samples)
			stats := runtimeStats{
				Samples: len(samples),
				MinMS:   samples[0],
				MeanMS:  int64(mean(samples)),
				MaxMS:   samples[len(samples)-1],
				P99MS:   samples[len(samples)*99/100],
			}
			switch runtime {
			case runcRuntime:
				langResult.Runc = stats
			case gvisorRuntime:
				langResult.Gvisor = stats
			}
		}
		report.Results = append(report.Results, langResult)
	}
	slices.SortFunc(report.Results, func(a, b langResult) int {
		return strings.Compare(a.Language, b.Language)
	})
	return report, nil
}

func buildSpec(req RunRequest, image containerd.Image, codeFile string) ([]oci.SpecOpts, error) {
	spec, ok := languages[req.Language]
	if !ok {
		return nil, fmt.Errorf("language is not supported: %s", req.Language)
	}

	return []oci.SpecOpts{
		oci.WithImageConfig(image),
		oci.WithProcessArgs(append(spec.command, "/code/script"+spec.ext)...),
		oci.WithMemoryLimit(memoryLimit),
		oci.WithCPUCFS(cpuQuota, cpuPeriod),
		oci.WithMounts([]specs.Mount{
			{
				Type:        "bind",
				Source:      codeFile,
				Destination: "/code/script" + spec.ext,
				Options:     []string{"rbind", "ro"},
			},
		}),
		oci.WithLinuxNamespace(specs.LinuxNamespace{Type: specs.NetworkNamespace}),
	}, nil
}
