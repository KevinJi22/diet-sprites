package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

const (
	execTimeout    = 10 * time.Second
	maxCodeBytes   = 64 * 1024
	memoryLimit    = uint64(512 * 1024 * 1024)
	cpuQuota       = int64(100_000)  // microseconds of CPU per period
	cpuPeriod      = uint64(100_000) // 100ms window — quota/period = fraction of 1 CPU
	gvisorRuntime  = "io.containerd.runsc.v1"
	containerdSock = "/run/containerd/containerd.sock"
	ctrNamespace   = "runner"
)

type RunRequest struct {
	Language string `json:"language"`
	Code     string `json:"code"`
}

type RunResult struct {
	Output     string `json:"output"`
	DurationMS int64  `json:"duration_ms"`
	TimedOut   bool   `json:"timed_out,omitempty"`
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

func execute(ctx context.Context, req RunRequest) (*RunResult, error) {
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

	ctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	ctx = namespaces.WithNamespace(ctx, ctrNamespace)

	image, err := ctrClient.GetImage(ctx, spec.image)
	if err != nil {
		return nil, fmt.Errorf("image %q not found (pull it first): %w", spec.image, err)
	}

	id := fmt.Sprintf("runner-%d", time.Now().UnixNano())

	specOpts, err := buildSpec(req, image, f.Name())
	if err != nil {
		return nil, fmt.Errorf("failed to build spec: %w", err)
	}

	container, err := ctrClient.NewContainer(ctx, id,
		containerd.WithRuntime(gvisorRuntime, nil),
		containerd.WithNewSnapshot(id, image),
		containerd.WithNewSpec(specOpts...),
	)
	if err != nil {
		return nil, fmt.Errorf("creating container: %w", err)
	}
	defer container.Delete(context.Background(), containerd.WithSnapshotCleanup)

	var buf bytes.Buffer
	task, err := container.NewTask(ctx, cio.NewCreator(cio.WithStreams(nil, &buf, &buf)))
	if err != nil {
		return nil, fmt.Errorf("creating task: %w", err)
	}
	defer task.Delete(context.Background())

	// Wait must be registered before Start — see package doc.
	exitCh, err := task.Wait(context.Background())
	if err != nil {
		return nil, fmt.Errorf("registering wait: %w", err)
	}

	start := time.Now()
	if err := task.Start(ctx); err != nil {
		return nil, fmt.Errorf("starting task: %w", err)
	}

	timedOut := false
	select {
	case <-exitCh:
	case <-ctx.Done():
		timedOut = true
		_ = task.Kill(context.Background(), syscall.SIGKILL)
		<-exitCh
	}
	elapsed := time.Since(start)

	return &RunResult{
		Output:     buf.String(),
		DurationMS: elapsed.Milliseconds(),
		TimedOut:   timedOut,
	}, nil
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
