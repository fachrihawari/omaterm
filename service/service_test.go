package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFuzzyFilter(t *testing.T) {
	items := []string{
		"src/components/App.tsx",
		"src/components/GitPanel.tsx",
		"src/lib/terminal-layout.ts",
		"package.json",
		"README.md",
	}

	// Empty query returns everything.
	if got := FuzzyFilter(items, "", 0); len(got) != len(items) {
		t.Fatalf("empty query: %d results", len(got))
	}

	// Subsequence match.
	got := FuzzyFilter(items, "gitp", 0)
	if len(got) != 1 || got[0].Value != "src/components/GitPanel.tsx" {
		t.Fatalf("gitp: %+v", got)
	}

	// Case-insensitive.
	got = FuzzyFilter(items, "APP", 0)
	if len(got) == 0 || got[0].Value != "src/components/App.tsx" {
		t.Fatalf("APP: %+v", got)
	}

	// No match.
	if got := FuzzyFilter(items, "zzzzz", 0); len(got) != 0 {
		t.Fatalf("zzzzz: %+v", got)
	}

	// Indices cover the query bytes.
	got = FuzzyFilter(items, "tsx", 0)
	if len(got) == 0 {
		t.Fatal("tsx: no results")
	}
	for _, m := range got {
		if len(m.Indices) != 3 {
			t.Errorf("%s: indices = %v", m.Value, m.Indices)
		}
	}

	// Limit.
	if got := FuzzyFilter(items, "s", 2); len(got) != 2 {
		t.Fatalf("limit: %d results", len(got))
	}

	// Basename bonus: "app" should rank App.tsx above others containing
	// the letters in directories.
	got = FuzzyFilter([]string{"src/app/readme.md", "src/App.tsx"}, "app", 0)
	if got[0].Value != "src/App.tsx" {
		t.Fatalf("basename ranking: %+v", got)
	}
}

func TestGitIgnore(t *testing.T) {
	ig := &gitIgnore{patterns: []gitIgnorePattern{
		{pat: "node_modules", dirOnly: true},
		{pat: "*.log"},
		{pat: "build", dirOnly: true, anchored: true},
		{pat: "dist"},
	}}
	cases := []struct {
		path string
		want bool
	}{
		{"node_modules/", true},
		{"node_modules/x/", true},
		{"src/app.log", true},
		{"app.log", true},
		{"build/", true},
		{"src/build/", false}, // anchored
		{"dist", true},
		{"src/dist", true}, // unanchored matches trailing
		{"src/main.go", false},
	}
	for _, tc := range cases {
		if got := ig.matches(tc.path); got != tc.want {
			t.Errorf("matches(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestDiscoverRepos(t *testing.T) {
	// Build a fake workspace: root not a repo, one child repo.
	root := t.TempDir()
	child := filepath.Join(root, "childproj")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init", "-q", "-b", "main")
	cmd.Dir = child
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s\n%s", err, out)
	}

	repos := DiscoverRepos(root, 1)
	if len(repos) != 1 || repos[0].Name != "childproj" {
		t.Fatalf("repos = %+v", repos)
	}
	if repos[0].RelPath != "childproj" {
		t.Errorf("RelPath = %q", repos[0].RelPath)
	}

	// The root becomes a repo too: it comes first.
	cmd = exec.Command("git", "init", "-q", "-b", "main")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init root: %s\n%s", err, out)
	}
	repos = DiscoverRepos(root, 1)
	if len(repos) != 2 {
		t.Fatalf("repos = %d, want 2", len(repos))
	}
	if repos[0].RelPath != "." {
		t.Errorf("root repo should come first, RelPath = %q", repos[0].RelPath)
	}
}
