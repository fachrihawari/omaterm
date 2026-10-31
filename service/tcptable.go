package service

import (
	"encoding/binary"
	"sort"
)

// tcpListener is a row of Windows' TCP table of listeners.
type tcpListener struct{ port, pid int }

// parseTCPTable reads what GetExtendedTcpTable gives for listeners with
// their owning process: a count, then rows of MIB_TCPROW_OWNER_PID (24
// bytes) or MIB_TCP6ROW_OWNER_PID (56). A port is in network order in
// the low 16 bits of its 32.
func parseTCPTable(b []byte, v6 bool) []tcpListener {
	if len(b) < 4 {
		return nil
	}
	rowSize, portAt, pidAt := 24, 8, 20
	if v6 {
		rowSize, portAt, pidAt = 56, 20, 52
	}
	n := int(binary.LittleEndian.Uint32(b))
	var ls []tcpListener
	for i := 0; i < n; i++ {
		row := b[4+i*rowSize:]
		if len(row) < rowSize {
			break
		}
		port := int(binary.BigEndian.Uint16(row[portAt:]))
		ls = append(ls, tcpListener{port: port, pid: int(binary.LittleEndian.Uint32(row[pidAt:]))})
	}
	return ls
}

// portsOf is the ports the processes in pidSet listen on, each once.
func portsOf(ls []tcpListener, pidSet map[int]bool, procName map[int]string) []Port {
	var ports []Port
	seen := map[int]bool{}
	for _, l := range ls {
		if !pidSet[l.pid] || seen[l.port] {
			continue
		}
		seen[l.port] = true
		name := procName[l.pid]
		if name == "" {
			name = "unknown"
		}
		ports = append(ports, Port{Port: l.port, Name: portName(l.port, name), Open: true, Pid: l.pid, Process: name})
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i].Port < ports[j].Port })
	return ports
}
