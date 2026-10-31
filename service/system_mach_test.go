package service

import (
	"encoding/binary"
	"math"
	"testing"
)

// Ticks that wrap past 32 bits keep counting up.
func TestMachTicksWrap(t *testing.T) {
	var m machTicks
	total0, idle0 := m.add([4]uint32{100, 50, math.MaxUint32 - 49, 50})
	if idle0 != math.MaxUint32-49 || total0 != idle0+200 {
		t.Fatalf("first: %d %d", total0, idle0)
	}
	// 200 ticks later, 150 of them idle, and idle wrapped.
	total, idle := m.add([4]uint32{120, 80, 100, 50})
	if idle != idle0+150 || total != total0+200 {
		t.Fatalf("after the wrap: total +%d, idle +%d", total-total0, idle-idle0)
	}
}

// The memory in use is app memory, wired and compressed, in pages of the
// kernel's size.
func TestMachMemUsed(t *testing.T) {
	vm := make([]byte, vmStatsSize)
	put := func(off int, v uint32) { binary.NativeEndian.PutUint32(vm[off:], v) }
	put(0, 9999)               // free_count: not in use
	put(vmWireCount, 100)      // wired
	put(vmPurgeableCount, 50)  // purgeable, out of the internal pages
	put(vmCompressorPages, 25) // compressed
	put(vmInternalPageCount, 1050)
	put(136, 7777) // external_page_count: file cache, not in use
	used, ok := machMemUsed(vm, 16384)
	if !ok || used != (1000+100+25)*16384 {
		t.Fatalf("used %d, %v", used, ok)
	}
	// More purgeable than internal pages (they are read apart) is no app
	// memory, not a huge number.
	put(vmPurgeableCount, 2000)
	if used, _ := machMemUsed(vm, 4096); used != (100+25)*4096 {
		t.Fatalf("purgeable over internal: %d", used)
	}
	if _, ok := machMemUsed(vm[:100], 4096); ok {
		t.Fatal("a short struct read")
	}
}
