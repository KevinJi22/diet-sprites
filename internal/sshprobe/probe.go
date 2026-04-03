package sshprobe

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"strings"
	"time"
)

// Result tracks the outcome of a single probe attempt.
type Result struct {
	Attempt  int
	Duration time.Duration
	Err      error
}

// Config configures the SSH readiness probe.
type Config struct {
	Host           string
	Port           int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	MaxAttempts    int
	DialTimeout    time.Duration
}

// DefaultConfig returns a Config with sensible defaults for the given host.
func DefaultConfig(host string) Config {
	return Config{
		Host:           host,
		Port:           22,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		MaxAttempts:    20,
		DialTimeout:    5 * time.Second,
	}
}

// Backoff calculates the wait duration before retry number `attempt` (0-indexed).
// Must satisfy: non-negative, grows with attempt number, capped at cfg.MaxBackoff.
func Backoff(attempt int, cfg Config) time.Duration {
	if attempt == 0 {
		return cfg.InitialBackoff
	}
	backoff := min(cfg.InitialBackoff*(1<<attempt), cfg.MaxBackoff)

	// full jitter
	return rand.N(backoff)
}

// Probe retries an SSH banner check with exponential backoff until it succeeds,
// the context is cancelled, or MaxAttempts is exhausted.
func Probe(ctx context.Context, cfg Config) ([]Result, error) {
	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))
	var results []Result

	for i := range cfg.MaxAttempts {
		if i > 0 {
			wait := Backoff(i, cfg)
			select {
			case <-ctx.Done():
				return results, ctx.Err()
			case <-time.After(wait):
			}
		}

		start := time.Now()
		err := checkSSH(ctx, addr, cfg.DialTimeout)
		results = append(results, Result{
			Attempt:  i + 1,
			Duration: time.Since(start),
			Err:      err,
		})

		if err == nil {
			return results, nil
		}
	}

	return results, fmt.Errorf("SSH not ready after %d attempts", cfg.MaxAttempts)
}

// checkSSH dials the address and verifies the remote sends an SSH protocol banner.
func checkSSH(ctx context.Context, addr string, timeout time.Duration) error {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return fmt.Errorf("setting read deadline: %w", err)
	}

	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil {
		return fmt.Errorf("reading SSH banner: %w", err)
	}

	if !strings.HasPrefix(string(buf[:n]), "SSH-") {
		return fmt.Errorf("unexpected banner: %q", string(buf[:n]))
	}

	return nil
}

// PrintResults prints a summary with a latency histogram to stdout.
func PrintResults(results []Result, elapsed time.Duration) {
	if len(results) == 0 {
		fmt.Println("No probe results.")
		return
	}

	fmt.Println("\n--- SSH Readiness Probe ---")

	var maxDur time.Duration
	for _, r := range results {
		if r.Duration > maxDur {
			maxDur = r.Duration
		}
	}

	const barWidth = 40
	for _, r := range results {
		width := int(float64(barWidth) * float64(r.Duration) / float64(maxDur))
		if width < 1 && r.Duration > 0 {
			width = 1
		}
		bar := strings.Repeat("█", width)
		status := "✓"
		if r.Err != nil {
			status = "✗"
		}
		fmt.Printf("  #%-2d %s %-*s %s\n", r.Attempt, status, barWidth, bar, r.Duration.Round(time.Millisecond))
	}

	last := results[len(results)-1]
	fmt.Printf("\nAttempts:   %d\n", len(results))
	fmt.Printf("Total time: %s\n", elapsed.Round(time.Millisecond))
	if last.Err == nil {
		fmt.Println("Status:     ready")
	} else {
		fmt.Printf("Status:     not ready (%s)\n", last.Err)
	}
}
