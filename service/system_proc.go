//go:build !darwin && !windows

package service

// Off macOS the samples come from /proc: where there is none, they say so.

func cpuTimes() (total, idle uint64, ok bool) { return readCPUTimes() }

func memUsage() (used, total uint64, ok bool) { return readMemInfo() }
