package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type CPUStat struct {
	user, nice, system, idle, iowait, irq, softirq uint64
}

type Monitor struct {
	prevCPU      CPUStat
	prevNetBytes uint64
}

func (m *Monitor) Sample() (cpuPct float64, netDelta uint64, err error) {
	cpu, err := readCPUStat()
	if err != nil {
		return 0, 0, err
	}
	net, err := readNetDev()
	if err != nil {
		return 0, 0, err
	}

	cpuPct = getCpuPercent(m.prevCPU, cpu)
	netDelta = net - m.prevNetBytes

	m.prevCPU = cpu
	m.prevNetBytes = net
	return cpuPct, netDelta, nil
}

func readCPUStat() (CPUStat, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return CPUStat{}, err
	}

	var s CPUStat
	fmt.Sscanf(strings.SplitN(string(data), "\n", 2)[0], "cpu %d %d %d %d %d %d %d", &s.user, &s.nice, &s.system, &s.idle, &s.iowait, &s.irq, &s.softirq)
	return s, nil
}

func getCpuPercent(prev, cur CPUStat) float64 {
	prevTotal := prev.user + prev.nice + prev.system + prev.idle + prev.iowait + prev.irq + prev.softirq
	curTotal := cur.user + cur.nice + cur.system + cur.idle + cur.iowait + cur.irq + cur.softirq

	totalDelta := curTotal - prevTotal
	idleDelta := cur.idle - prev.idle
	return float64(totalDelta-idleDelta) / float64(totalDelta) * 100
}

func readNetDev() (uint64, error) {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, err
	}

	var totalBytes uint64
	for _, line := range strings.Split(string(data), "\n")[2:] { // skip 2 header lines
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		iface := strings.TrimSuffix(fields[0], ":")
		if iface == "lo" {
			continue // skip loopback
		}
		recv, _ := strconv.ParseUint(fields[1], 10, 64)
		sent, _ := strconv.ParseUint(fields[9], 10, 64)
		totalBytes += recv + sent
	}

	return totalBytes, nil
}
