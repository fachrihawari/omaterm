package service

import (
	"os"
	"strconv"
	"strings"
)

// SystemStats is the machine's load, as the status bar shows it.
type SystemStats struct {
	CPUPercent float64 // -1 until a second sample gives a delta
	MemUsed    uint64  // bytes
	MemTotal   uint64  // bytes
	OK         bool    // false when the system could not be read
}

// SystemSampler samples system CPU and memory: from /proc on Linux, from
// mach's statistics on macOS. CPU is the busy share between two calls, so
// keep one sampler across samples: the first call only primes it.
type SystemSampler struct {
	prevTotal, prevIdle uint64
	primed              bool
}

// Sample reads /proc/stat and /proc/meminfo, or macOS's counterparts.
// Elsewhere, or when they can't be read, it returns OK false.
func (s *SystemSampler) Sample() SystemStats {
	st := SystemStats{CPUPercent: -1}
	total, idle, ok := cpuTimes()
	if ok {
		if s.primed && total > s.prevTotal {
			busy := (total - s.prevTotal) - (idle - s.prevIdle)
			st.CPUPercent = 100 * float64(busy) / float64(total-s.prevTotal)
			if st.CPUPercent < 0 {
				st.CPUPercent = 0
			} else if st.CPUPercent > 100 {
				st.CPUPercent = 100
			}
		}
		s.prevTotal, s.prevIdle = total, idle
		s.primed = true
	}
	if used, all, ok := memUsage(); ok {
		st.MemUsed, st.MemTotal = used, all
	}
	st.OK = ok
	return st
}

// readCPUTimes returns the aggregate jiffies of /proc/stat and its idle
// (idle + iowait) part.
func readCPUTimes() (total, idle uint64, ok bool) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	line, _, _ := strings.Cut(string(data), "\n")
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	for i, f := range fields[1:] {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		total += v
		if i == 3 || i == 4 { // idle, iowait
			idle += v
		}
	}
	return total, idle, true
}

// readMemInfo returns MemTotal-MemAvailable and MemTotal in bytes.
func readMemInfo() (used, total uint64, ok bool) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	var totalKB, availKB uint64
	var haveTotal, haveAvail bool
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			totalKB, haveTotal = v, true
		case "MemAvailable:":
			availKB, haveAvail = v, true
		}
	}
	if !haveTotal || !haveAvail || totalKB < availKB {
		return 0, 0, false
	}
	return (totalKB - availKB) * 1024, totalKB * 1024, true
}

// FormatBytes renders n as the status bar's compact size, e.g. "1.3 GB".
func FormatBytes(n uint64) string {
	const (
		kb = 1 << 10
		mb = 1 << 20
		gb = 1 << 30
	)
	switch {
	case n >= gb:
		return strconv.FormatFloat(float64(n)/gb, 'f', 1, 64) + " GB"
	case n >= mb:
		return strconv.FormatUint(n/mb, 10) + " MB"
	case n >= kb:
		return strconv.FormatUint(n/kb, 10) + " KB"
	default:
		return strconv.FormatUint(n, 10) + " B"
	}
}
