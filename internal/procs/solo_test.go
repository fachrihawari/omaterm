package procs

import (
	"reflect"
	"testing"
)

func TestImportSolo(t *testing.T) {
	got := ImportSolo("testdata")
	want := []Def{
		{Name: "web", Command: "npm run dev", Dir: "web", Env: map[string]string{"PORT": "3000"}, Autostart: true, Restart: "always", Watch: []string{"src/**"}},
		{Name: "worker", Command: "go run ./cmd/worker"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestImportSoloAbsent(t *testing.T) {
	if defs := ImportSolo(t.TempDir()); defs != nil {
		t.Errorf("got %v for no solo.yml", defs)
	}
}
