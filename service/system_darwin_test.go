//go:build darwin

package service

import (
	"testing"
	"time"
	"unsafe"
)

// On a real Mac the status bar's numbers are there and make sense: memory
// in use within all of it, and a CPU share from the second sample on.
func TestSampleOnThisMac(t *testing.T) {
	var s SystemSampler
	first := s.Sample()
	if !first.OK {
		t.Fatal("the first sample read nothing")
	}
	if first.CPUPercent != -1 {
		t.Errorf("the first sample has a CPU share: %v", first.CPUPercent)
	}
	if first.MemUsed == 0 || first.MemUsed > first.MemTotal {
		t.Fatalf("memory used %d of %d", first.MemUsed, first.MemTotal)
	}
	time.Sleep(300 * time.Millisecond)
	second := s.Sample()
	if !second.OK || second.CPUPercent < 0 || second.CPUPercent > 100 {
		t.Fatalf("second sample: %+v", second)
	}
	var page uintptr
	if hostPageSize(machHost(), unsafe.Pointer(&page)) != 0 || page < 4096 || page&(page-1) != 0 {
		t.Errorf("page size %d", page)
	}
	t.Logf("CPU %.1f%%, memory %s of %s, pages of %d bytes", second.CPUPercent,
		FormatBytes(second.MemUsed), FormatBytes(second.MemTotal), page)
}
