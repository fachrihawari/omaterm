//go:build linux || darwin

package web_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"omaterm/internal/model"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "init.defaultBranch=main", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// filesFixture is a daemon whose state has a project in a git repository,
// with one change committed and two not, and a worktree session that
// committed one of its own: pane a1 in the project, w1 in the worktree.
func filesFixture(t *testing.T) (*fixture, *http.Client) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	f := newFixture(t)
	repo := filepath.Join(f.dir, "repo")
	writeFile(t, filepath.Join(repo, "main.go"), "package main\n")
	writeFile(t, filepath.Join(repo, "docs/readme.md"), "# hi\n")
	writeFile(t, filepath.Join(repo, "logo.svg"), `<svg xmlns="http://www.w3.org/2000/svg"/>`)
	gitIn(t, repo, "init", "-q")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "first")
	wt := filepath.Join(f.dir, "wt")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "feat", wt)
	writeFile(t, filepath.Join(wt, "feat.go"), "package main\n\nfunc feat() {}\n")
	gitIn(t, wt, "add", "-A")
	gitIn(t, wt, "commit", "-q", "-m", "feat")
	writeFile(t, filepath.Join(repo, "main.go"), "package main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(repo, "new.txt"), "new\n")
	st := model.State{Version: model.Version, Projects: []model.Project{{ID: "p1", Name: "repo", Dir: repo}},
		Sessions: []model.Session{
			{ID: "s1", Project: "p1", Layout: &model.Layout{Pane: "a1"}, Panes: []model.Pane{{ID: "a1", Dir: repo}}},
			{ID: "s2", Project: "p1", Layout: &model.Layout{Pane: "w1"}, Panes: []model.Pane{{ID: "w1", Dir: wt}},
				Worktree: &model.Worktree{Dir: wt, Branch: "feat", Base: "main"}},
		}}
	if err := model.Save(filepath.Join(f.dir, "state.json"), st); err != nil {
		t.Fatal(err)
	}
	return f, f.paired(t)
}

func getJSON(t *testing.T, c *http.Client, u string, v any) int {
	t.Helper()
	res, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode == 200 && v != nil {
		if err := json.NewDecoder(res.Body).Decode(v); err != nil {
			t.Fatal(err)
		}
	}
	return res.StatusCode
}

// The page lists a session's folder and reads its files, and nothing
// outside it.
func TestWebFiles(t *testing.T) {
	f, c := filesFixture(t)
	api := f.base + "/api/panes/a1/"
	var dir struct {
		Root    string
		Entries []struct {
			Name string
			Dir  bool
		}
	}
	if code := getJSON(t, c, api+"files", &dir); code != 200 {
		t.Fatalf("files: %d", code)
	}
	if dir.Root != "repo" || len(dir.Entries) == 0 || dir.Entries[0].Name != "docs" || !dir.Entries[0].Dir {
		t.Fatalf("files: %+v, want docs first", dir)
	}
	var file struct{ Kind, Text string }
	if code := getJSON(t, c, api+"file?path=main.go", &file); code != 200 || file.Kind != "text" || !strings.Contains(file.Text, "func main") {
		t.Fatalf("file: %d %+v", code, file)
	}
	res, err := c.Get(api + "raw?path=logo.svg")
	if err != nil || res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/svg+xml" ||
		!strings.Contains(res.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("raw picture: %v %v %v", err, res.Status, res.Header)
	}
	for path, want := range map[string]int{
		"file?path=" + url.QueryEscape("../state.json"): 400,
		"file?path=" + url.QueryEscape("/etc/passwd"):   400,
		"files?path=.git":    400,
		"file?path=gone.txt": 404,
		"raw?path=main.go":   404,
	} {
		if code := getJSON(t, c, api+path, nil); code != want {
			t.Errorf("%s: %d, want %d", path, code, want)
		}
	}
	if code := getJSON(t, c, f.base+"/api/panes/nope/files", nil); code != 404 {
		t.Errorf("an unknown pane: %d, want 404", code)
	}
	if code := getJSON(t, f.client(t), api+"files", nil); code != 401 {
		t.Errorf("unpaired: %d, want 401", code)
	}
}

// The page lists what a session changed, and each file's diff: in the
// project, what isn't committed; in a worktree, since its base branch.
func TestWebChanges(t *testing.T) {
	f, c := filesFixture(t)
	type changes struct {
		Base  string
		Files []struct {
			Path, Status   string
			Added, Removed int
		}
	}
	var ch changes
	if code := getJSON(t, c, f.base+"/api/panes/a1/changes", &ch); code != 200 {
		t.Fatalf("changes: %d", code)
	}
	got := map[string]string{}
	for _, file := range ch.Files {
		got[file.Path] = file.Status
		if file.Path == "main.go" && file.Added != 2 {
			t.Errorf("main.go +%d, want +2", file.Added)
		}
	}
	if got["main.go"] != "modified" || got["new.txt"] != "added" || len(got) != 2 || ch.Base != "" {
		t.Fatalf("changes: %+v", ch)
	}
	var d struct {
		Text  string
		Large bool
	}
	if code := getJSON(t, c, f.base+"/api/panes/a1/diff?path=main.go", &d); code != 200 || !strings.Contains(d.Text, "+func main() {}") {
		t.Fatalf("diff: %d %q", code, d.Text)
	}
	if code := getJSON(t, c, f.base+"/api/panes/a1/diff?path="+url.QueryEscape("../x"), nil); code != 400 {
		t.Errorf("diff outside: %d, want 400", code)
	}
	var wch changes
	if code := getJSON(t, c, f.base+"/api/panes/w1/changes", &wch); code != 200 {
		t.Fatalf("worktree changes: %d", code)
	}
	if len(wch.Files) != 1 || wch.Files[0].Path != "feat.go" || wch.Base != "main" {
		t.Fatalf("worktree changes: %+v, want feat.go since main", wch)
	}
}
