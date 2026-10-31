package service

import "encoding/binary"

// What macOS's mach calls give is read here, apart from the calls
// (system_darwin.go), so that the reading is tested on any system.

// machTicks turns host_cpu_load_info's tick counts (user, system, idle,
// nice), 32 bits each that wrap within weeks on a machine with many cores,
// into running totals.
type machTicks struct {
	last [4]uint32
	sum  [4]uint64
	seen bool
}

// add takes a sample's ticks and returns the totals so far: all of them,
// and the idle ones.
func (m *machTicks) add(ticks [4]uint32) (total, idle uint64) {
	for i, t := range ticks {
		if m.seen {
			m.sum[i] += uint64(t - m.last[i]) // the difference survives a wrap
		} else {
			m.sum[i] = uint64(t)
		}
		m.last[i] = t
	}
	m.seen = true
	return m.sum[0] + m.sum[1] + m.sum[2] + m.sum[3], m.sum[2]
}

// Offsets in struct vm_statistics64 (mach/vm_statistics.h), 152 bytes:
//
//	  0 free_count            uint32
//	  4 active_count          uint32
//	  8 inactive_count        uint32
//	 12 wire_count            uint32
//	 16 zero_fill_count       uint64, then reactivations, pageins,
//	                          pageouts, faults, cow_faults, lookups,
//	                          hits, purges (uint64 each, to 88)
//	 88 purgeable_count       uint32
//	 92 speculative_count     uint32
//	 96 decompressions        uint64, then compressions, swapins,
//	                          swapouts (uint64 each, to 128)
//	128 compressor_page_count uint32
//	132 throttled_count       uint32
//	136 external_page_count   uint32
//	140 internal_page_count   uint32
//	144 total_uncompressed_pages_in_compressor uint64
const (
	vmStatsSize         = 152
	vmWireCount         = 12
	vmPurgeableCount    = 88
	vmCompressorPages   = 128
	vmInternalPageCount = 140
)

// machMemUsed is the memory in use as Activity Monitor counts it: app
// memory (internal pages that can't be purged), wired and compressed.
func machMemUsed(vm []byte, pageSize uint64) (uint64, bool) {
	if len(vm) < vmStatsSize || pageSize == 0 {
		return 0, false
	}
	u32 := func(off int) uint64 { return uint64(binary.NativeEndian.Uint32(vm[off:])) }
	app := u32(vmInternalPageCount)
	if p := u32(vmPurgeableCount); p < app {
		app -= p
	} else {
		app = 0
	}
	return (app + u32(vmWireCount) + u32(vmCompressorPages)) * pageSize, true
}
