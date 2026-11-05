package fsview

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func folder(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, text := range files {
		write(t, filepath.Join(root, name), text)
	}
	return root
}

// A path that leads out of the folder, or into its git directory, is
// refused, whether by .., by being absolute, or through a link.
func TestLocal(t *testing.T) {
	root := folder(t, map[string]string{"a.txt": "a\n", "sub/b.txt": "b\n", ".git/config": "[core]\n"})
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret"), "no\n")
	os.Symlink(outside, filepath.Join(root, "out"))
	os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "secret"))
	os.Symlink("sub/b.txt", filepath.Join(root, "in"))
	for _, p := range []string{"../x", "a/../../x", "sub/../a.txt", "/etc/passwd", ".git/config", ".GIT", "sub/.git/x", "out/secret", "secret"} {
		if _, err := Local(root, p); err == nil {
			t.Errorf("Local(%q) let it through", p)
		}
	}
	for _, p := range []string{"", ".", "a.txt", "sub", "sub/b.txt", "in"} {
		if _, err := Local(root, p); err != nil {
			t.Errorf("Local(%q): %v", p, err)
		}
	}
	if _, err := Local(root, "gone.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing file: %v, want it not found", err)
	}
}

// A folder lists its folders first, each by name, without .git, and mutes
// node_modules and what git ignores.
func TestList(t *testing.T) {
	root := folder(t, map[string]string{
		"b.txt": "b\n", "a.txt": "a\n", "zdir/x": "x\n", "adir/y": "y\n",
		"node_modules/m/index.js": "m\n", "dist/out.js": "o\n", ".gitignore": "dist/\n",
	})
	if _, err := exec.LookPath("git"); err == nil {
		cmd := exec.Command("git", "init", "-q")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
	}
	d, err := List(root, "")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	muted := map[string]bool{}
	for _, e := range d.Entries {
		names = append(names, e.Name)
		muted[e.Name] = e.Muted
	}
	want := []string{"adir", "dist", "node_modules", "zdir", ".gitignore", "a.txt", "b.txt"}
	if !slices.Equal(names, want) {
		t.Errorf("entries %v, want %v", names, want)
	}
	if !muted["node_modules"] || muted["adir"] || muted["a.txt"] {
		t.Errorf("muted %v: want node_modules alone among those", muted)
	}
	if _, err := exec.LookPath("git"); err == nil && !muted["dist"] {
		t.Error("dist is ignored by git but not muted")
	}
	in, err := List(root, "node_modules/m")
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Entries) != 1 || !in.Entries[0].Muted {
		t.Errorf("inside node_modules: %+v, want its file muted", in.Entries)
	}
	if d.Entries[5].Size != 2 {
		t.Errorf("a.txt is %d bytes, want 2", d.Entries[5].Size)
	}
}

// A file reads as text, or is named as a picture, a binary or a large
// file, with no text.
func TestRead(t *testing.T) {
	root := folder(t, map[string]string{
		"a.go": "package a\n", "logo.svg": "<svg/>", "bin": "a\x00b",
		"big.txt": strings.Repeat("x", MaxText+1),
	})
	for path, kind := range map[string]string{"a.go": "text", "logo.svg": "image", "bin": "binary", "big.txt": "large"} {
		f, err := Read(root, path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if f.Kind != kind {
			t.Errorf("%s is %q, want %q", path, f.Kind, kind)
		}
		if (f.Text != "") != (kind == "text") {
			t.Errorf("%s: text %q", path, f.Text)
		}
	}
	if _, err := Read(root, ""); err == nil {
		t.Error("read the folder itself as a file")
	}
}
