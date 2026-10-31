package main

import (
	"omaterm/internal/model"
	"os"
	"path/filepath"
	"testing"
)

// The app was called oterm until 0.7, and kept its project list under that
// name. Renaming it must not lose the list.
func TestLegacyStateIsRead(t *testing.T) {
	config := t.TempDir()
	setConfigHome(t, config)

	// A project directory that exists, since loading drops paths that do not.
	projectDir := t.TempDir()

	legacy := filepath.Join(config, "oterm", "state.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"projects":[{"name":"kept","path":"` + projectDir + `","tabs":["dev"]}],"active":0}`
	if err := os.WriteFile(legacy, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	projects, active := loadProjects()
	if len(projects) != 1 {
		t.Fatalf("read %d projects from the old location, want 1", len(projects))
	}
	if projects[0].Name != "kept" || projects[0].Path != projectDir {
		t.Errorf("project = %+v", projects[0])
	}
	if active != 0 {
		t.Errorf("active = %d", active)
	}

	// The new location wins once it exists.
	current := filepath.Join(config, "omaterm", "state.json")
	if err := os.MkdirAll(filepath.Dir(current), 0o755); err != nil {
		t.Fatal(err)
	}
	body = `{"projects":[{"name":"newer","path":"` + projectDir + `","tabs":["dev"]}],"active":0}`
	if err := os.WriteFile(current, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	projects, _ = loadProjects()
	if len(projects) != 1 || projects[0].Name != "newer" {
		t.Fatalf("projects = %+v, want the one from the new location", projects)
	}
}

// loadProjects reads state.json as newApp does, without the rest of an app.
func loadProjects() ([]project, int) {
	a := &appState{saved: map[string][]model.Session{}}
	active := a.loadState()
	return a.projects, active
}
