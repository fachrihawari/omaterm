package proto

import (
	"bytes"
	"encoding/binary"
	"runtime"
	"testing"
)

// A frame read from a message in hand is no longer than the message: a
// 5-byte message claiming 16 MB had that much made each time.
func TestFrameNoLongerThanMessage(t *testing.T) {
	msg := []byte{FrameInput, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(msg[1:], 16<<20-1)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if _, _, err := ReadFrame(bytes.NewReader(msg)); err == nil {
		t.Fatal("a frame longer than its message was read")
	}
	runtime.ReadMemStats(&after)
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<20 {
		t.Errorf("reading it took %d bytes", grew)
	}
	ok := AppendFrame(nil, FrameInput, []byte("hi"))
	if _, p, err := ReadFrame(bytes.NewReader(ok)); err != nil || string(p) != "hi" {
		t.Fatalf("a whole frame: %q %v", p, err)
	}
}
