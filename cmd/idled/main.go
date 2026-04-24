package main

import (
	"context"
	"log"
	"os/exec"
	"time"
)

const (
	cpuThresholdPct               = 10.0
	idleNetThreshold              = 50 * 1024
	checkInterval                 = 30 * time.Second
	consecutiveIdleCheckThreshold = 10
)

func main() {
	log.Printf("idled starting, waiting 2m grace period before monitoring")
	time.Sleep(2 * time.Minute)

	log.Printf("starting idle monitor (interval=%s, cpu_threshold=%.1f%%, net_threshold=%d bytes)", checkInterval, cpuThresholdPct, idleNetThreshold)
	if err := checkSystemActivity(context.Background(), checkInterval); err != nil {
		log.Fatal(err)
	}
}

func checkSystemActivity(ctx context.Context, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	monitor := Monitor{}
	consecutiveIdleCount := 0
	firstSample := true

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			cpuUsage, networkBytes, err := monitor.Sample()
			if err != nil {
				log.Printf("sample error (skipping): %v", err)
				continue
			}
			if firstSample {
				firstSample = false
				continue
			}
			if isIdle(cpuUsage, networkBytes) {
				consecutiveIdleCount++
				log.Printf("idle tick %d/%d (cpu=%.1f%%, net=%d bytes)", consecutiveIdleCount, consecutiveIdleCheckThreshold, cpuUsage, networkBytes)
				if consecutiveIdleCount >= consecutiveIdleCheckThreshold {
					log.Printf("idle threshold reached, shutting down")
					cmd := exec.Command("poweroff")
					if err := cmd.Run(); err != nil {
						log.Printf("poweroff failed: %v", err)
					}
					return nil
				}
			} else {
				if consecutiveIdleCount > 0 {
					log.Printf("activity detected, resetting idle count (cpu=%.1f%%, net=%d bytes)", cpuUsage, networkBytes)
				}
				consecutiveIdleCount = 0
			}
		}
	}
}

func isIdle(cpuUsage float64, networkBytes uint64) bool {
	return cpuUsage < cpuThresholdPct && networkBytes < idleNetThreshold
}
