package procs

import (
	"path/filepath"
	"testing"
)

func TestTrust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust.json")
	trust := OpenTrust(path)
	if trust.Trusted("/work/app", "v1") {
		t.Fatal("trusted before Add")
	}
	if err := trust.Add("/work/app", "v1"); err != nil {
		t.Fatal(err)
	}
	if !trust.Trusted("/work/app", "v1") {
		t.Fatal("not trusted after Add")
	}
	if !OpenTrust(path).Trusted("/work/app", "v1") {
		t.Error("trust did not survive reopening")
	}
	if trust.Trusted("/work/other", "v1") {
		t.Error("another folder is trusted")
	}
	if trust.Trusted("/work/app", "v2") {
		t.Error("another variant is trusted")
	}
}
