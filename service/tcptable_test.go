package service

import (
	"encoding/binary"
	"testing"
)

func TestParseTCPTable(t *testing.T) {
	v4 := make([]byte, 4+2*24)
	binary.LittleEndian.PutUint32(v4, 2)
	binary.BigEndian.PutUint16(v4[4+8:], 5173)
	binary.LittleEndian.PutUint32(v4[4+20:], 1200)
	binary.BigEndian.PutUint16(v4[4+24+8:], 445)
	binary.LittleEndian.PutUint32(v4[4+24+20:], 4)
	v6 := make([]byte, 4+56)
	binary.LittleEndian.PutUint32(v6, 1)
	binary.BigEndian.PutUint16(v6[4+20:], 5173)
	binary.LittleEndian.PutUint32(v6[4+52:], 1200)

	ls := append(parseTCPTable(v4, false), parseTCPTable(v6, true)...)
	if len(ls) != 3 || ls[0] != (tcpListener{5173, 1200}) || ls[1] != (tcpListener{445, 4}) || ls[2] != (tcpListener{5173, 1200}) {
		t.Fatalf("parsed %v", ls)
	}
	ports := portsOf(ls, map[int]bool{1200: true}, map[int]string{1200: "node"})
	if len(ports) != 1 || ports[0].Port != 5173 || ports[0].Name != "node" {
		t.Errorf("ports = %+v: the project's own, each once", ports)
	}
	if got := parseTCPTable(v4[:30], false); len(got) != 1 {
		t.Errorf("a cut table gives what it holds: %v", got)
	}
}
