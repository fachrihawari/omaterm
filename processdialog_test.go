package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/procs"
)

func TestProcessDialogSavesSharedAndLocalDefs(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.useLocalProcHost()
	defer a.procHost.Close()

	tr := ui.NewTester(a.view, 1440, 900)
	runProcessCommand(t, a, "process.add")
	tr.Frame()
	fillProcessDialog(t, tr, "web", "sh -c 'echo up'")
	if err := tr.Click("Save"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	defs, err := procs.LoadFile(a.workspace)
	if err != nil || len(defs) != 1 || defs[0].Name != "web" {
		t.Fatalf("shared definitions = %+v, err=%v", defs, err)
	}
	if len(a.procList[a.projects[a.active].ID]) != 1 || !a.procList[a.projects[a.active].ID][0].Trusted {
		t.Fatalf("saved process was not trusted and listed: %+v", a.procList[a.projects[a.active].ID])
	}

	a.openProcessDialog(a.active, nil)
	tr.Frame()
	if err := tr.Click("proc-shared"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	fillProcessDialog(t, tr, "local", "echo local")
	if err := tr.Click("Save"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	state := a.state()
	if len(state.Projects) != 1 || len(state.Projects[0].Processes) != 1 || state.Projects[0].Processes[0].Name != "local" {
		t.Fatalf("local process state = %+v", state.Projects)
	}
	defs, err = procs.LoadFile(a.workspace)
	if err != nil || len(defs) != 1 || defs[0].Name != "web" {
		t.Fatalf("local save changed shared file: defs=%+v err=%v", defs, err)
	}
}

func TestProcessDialogSuggestionAndDirectoryValidation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.useLocalProcHost()
	defer a.procHost.Close()
	if err := os.WriteFile(filepath.Join(a.workspace, "package.json"), []byte(`{"scripts":{"dev":"vite --port 5173"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	tr := ui.NewTester(a.view, 1440, 900)
	a.openProcessDialog(a.active, nil)
	tr.Frame()
	if err := tr.Click("npm run dev"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	st := a.procDialog
	if st == nil {
		t.Fatalf("dialog closed after suggestion click: %v", tr.Texts())
	}
	if st.name != "dev" || st.command != "npm run dev" || st.port != "5173" {
		t.Fatalf("suggestion did not fill the form: %+v", st)
	}
	st.dir = "../outside"
	tr.Frame()
	if !tr.HasText("the dir leaves the project") {
		t.Fatalf("directory error note is missing: %v", tr.Texts())
	}
	if err := tr.Click("Save"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if !st.open || len(a.procList[a.projects[a.active].ID]) != 0 {
		t.Fatal("invalid directory was saved")
	}
	st.open = false
	tr.Frame()
}

func TestProcessDialogEditRenamesDef(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.useLocalProcHost()
	defer a.procHost.Close()
	if err := procs.SaveFile(a.workspace, []procs.Def{{Name: "old", Command: "echo old"}}); err != nil {
		t.Fatal(err)
	}
	a.procHost.Sync()
	defs, err := procs.LoadFile(a.workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.procHost.Trust(a.projects[a.active].ID, "old"); err != nil {
		t.Fatal(err)
	}
	a.procHost.Sync()
	old := defs[0]
	a.openProcessDialog(a.active, &old)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	st := a.procDialog
	if st.name != "old" || st.command != "echo old" || st.oldName != "old" {
		t.Fatalf("edit form was not filled: %+v", st)
	}
	st.name = "new"
	if err := tr.Click("Save"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	defs, err = procs.LoadFile(a.workspace)
	if err != nil || len(defs) != 1 || defs[0].Name != "new" {
		t.Fatalf("renamed definitions = %+v, err=%v", defs, err)
	}
}

func fillProcessDialog(t *testing.T, tr *ui.Tester, name, command string) {
	t.Helper()
	if err := tr.Click("Process name"); err != nil {
		t.Fatal(err)
	}
	tr.Type(name)
	if err := tr.Click("Command input"); err != nil {
		t.Fatal(err)
	}
	tr.Type(command)
	tr.Frame()
}

func runProcessCommand(t *testing.T, a *appState, id string) {
	t.Helper()
	for _, cmd := range commandRegistry {
		if cmd.ID == id {
			cmd.Run(a, nil)
			return
		}
	}
	t.Fatalf("command %q is missing", id)
}
