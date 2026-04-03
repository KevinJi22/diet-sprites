package sshprobe

import (
	"context"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// --- helpers ---

// startFakeSSH starts a TCP listener that sends banner to each connection.
func startFakeSSH(t *testing.T, banner string) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Write([]byte(banner))
			conn.Close()
		}
	}()
	return ln
}

func listenerPort(t *testing.T, ln net.Listener) int {
	t.Helper()
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return port
}

// --- checkSSH tests ---

func TestCheckSSH_ValidBanner(t *testing.T) {
	ln := startFakeSSH(t, "SSH-2.0-OpenSSH_9.6\r\n")
	defer ln.Close()

	err := checkSSH(context.Background(), ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Errorf("expected success, got: %v", err)
	}
}

func TestCheckSSH_InvalidBanner(t *testing.T) {
	ln := startFakeSSH(t, "HTTP/1.1 200 OK\r\n")
	defer ln.Close()

	err := checkSSH(context.Background(), ln.Addr().String(), 2*time.Second)
	if err == nil {
		t.Error("expected error for non-SSH banner")
	}
}

func TestCheckSSH_ConnectionRefused(t *testing.T) {
	err := checkSSH(context.Background(), "127.0.0.1:1", 1*time.Second)
	if err == nil {
		t.Error("expected error for connection refused")
	}
}

func TestCheckSSH_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := checkSSH(ctx, "127.0.0.1:1", 5*time.Second)
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

// --- Backoff contract tests ---
// These verify the contract the Backoff function must satisfy.
// The placeholder (constant) implementation will fail TestBackoff_GrowsWithAttempt.

func TestBackoff_NonNegative(t *testing.T) {
	cfg := Config{
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     10 * time.Second,
	}
	for attempt := 0; attempt < 15; attempt++ {
		for i := 0; i < 50; i++ {
			if d := Backoff(attempt, cfg); d < 0 {
				t.Fatalf("attempt %d: negative duration %v", attempt, d)
			}
		}
	}
}

func TestBackoff_CappedAtMax(t *testing.T) {
	cfg := Config{
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     1 * time.Second,
	}
	for range 200 {
		if d := Backoff(20, cfg); d > cfg.MaxBackoff {
			t.Fatalf("backoff %v exceeds max %v", d, cfg.MaxBackoff)
		}
	}
}

func TestBackoff_GrowsWithAttempt(t *testing.T) {
	cfg := Config{
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     1 * time.Minute,
	}
	avg := func(attempt int) time.Duration {
		var sum time.Duration
		n := 500
		for range n {
			sum += Backoff(attempt, cfg)
		}
		return sum / time.Duration(n)
	}
	a1 := avg(1)
	a5 := avg(5)
	if a5 <= a1 {
		t.Errorf("backoff should grow: attempt 1 avg=%v, attempt 5 avg=%v", a1, a5)
	}
}

// --- Probe integration tests ---

func TestProbe_SucceedsImmediately(t *testing.T) {
	ln := startFakeSSH(t, "SSH-2.0-OpenSSH_9.6\r\n")
	defer ln.Close()

	cfg := Config{
		Host:           "127.0.0.1",
		Port:           listenerPort(t, ln),
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		MaxAttempts:    5,
		DialTimeout:    2 * time.Second,
	}

	results, err := Probe(context.Background(), cfg)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 attempt, got %d", len(results))
	}
	if results[0].Err != nil {
		t.Errorf("first attempt should succeed: %v", results[0].Err)
	}
}

func TestProbe_RetriesUntilSuccess(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	var attempts atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			n := attempts.Add(1)
			if n <= 2 {
				conn.Close() // close without banner — causes read error
			} else {
				conn.Write([]byte("SSH-2.0-OpenSSH_9.6\r\n"))
				conn.Close()
			}
		}
	}()

	cfg := Config{
		Host:           "127.0.0.1",
		Port:           listenerPort(t, ln),
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
		MaxAttempts:    10,
		DialTimeout:    1 * time.Second,
	}

	results, err := Probe(context.Background(), cfg)
	if err != nil {
		t.Fatalf("expected eventual success, got: %v", err)
	}
	if len(results) < 3 {
		t.Errorf("expected at least 3 attempts, got %d", len(results))
	}
	last := results[len(results)-1]
	if last.Err != nil {
		t.Errorf("last attempt should succeed: %v", last.Err)
	}
}

func TestProbe_ContextCancellation(t *testing.T) {
	// Port 1 refuses immediately; long backoff ensures the context
	// expires during the wait between retries.
	cfg := Config{
		Host:           "127.0.0.1",
		Port:           1,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     1 * time.Second,
		MaxAttempts:    100,
		DialTimeout:    100 * time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := Probe(ctx, cfg)
	elapsed := time.Since(start)

	if err == nil {
		t.Error("expected error from cancelled context")
	}
	if elapsed > 2*time.Second {
		t.Errorf("should have cancelled quickly, took %v", elapsed)
	}
}

func TestProbe_MaxAttemptsExhausted(t *testing.T) {
	cfg := Config{
		Host:           "127.0.0.1",
		Port:           1,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff:     5 * time.Millisecond,
		MaxAttempts:    3,
		DialTimeout:    100 * time.Millisecond,
	}

	results, err := Probe(context.Background(), cfg)
	if err == nil {
		t.Error("expected error after max attempts")
	}
	if len(results) != 3 {
		t.Errorf("expected 3 attempts, got %d", len(results))
	}
}
