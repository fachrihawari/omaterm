package main

// Installing omaterm on a remote host, as VS Code installs its server: the
// app copies the build it is running to a computer of the same kind, and
// updates it when the build changes. The bridge then starts the daemon
// there.

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"omaterm/internal/daemon"
	"omaterm/internal/paths"
	"omaterm/internal/sshconn"
)

// hostProbe is what a host says about the omaterm it runs.
type hostProbe struct {
	os, arch string
	version  string
	build    string
	found    bool
}

// prepareHost makes the host run this app's build: the bridge is new code
// too, so an older omaterm there would open its window instead. A host of
// another architecture cannot take this binary; it must have been built
// for that computer once.
func (a *appState) prepareHost(h RemoteHost, build string) error {
	p, err := probeHost(h.SSH)
	if err != nil {
		return err
	}
	if p.os == "" || p.arch == "" {
		return fmt.Errorf("%s didn't say what system it runs", h.Name)
	}
	same := p.os == runtime.GOOS && p.arch == runtime.GOARCH
	if !same {
		if p.found && p.version == daemon.VersionOf(build) {
			// Built for that computer and installed by hand: its bridge
			// writes the marker on its first connection.
			return nil
		}
		return fmt.Errorf("%s runs %s/%s; this omaterm is %s/%s. Build omaterm for that computer (mygo build -platform %s/%s) and install it there once",
			h.Name, p.os, p.arch, runtime.GOOS, runtime.GOARCH, p.os, p.arch)
	}
	if p.found && p.build == build {
		return nil
	}
	a.post(func() {
		if info := a.hostInfos[h.ID]; info != nil {
			info.Status, info.Err = "installing", ""
		}
	})
	if err := installHost(h, build); err != nil {
		return err
	}
	return nil
}

// probeHost asks a host what it runs, and whether it already has this
// build: the marker beside its state, which the bridge writes too.
func probeHost(target string) (hostProbe, error) {
	name := paths.Name()
	script := `u=$(uname -s 2>/dev/null); a=$(uname -m 2>/dev/null); ` +
		`p=; for c in "$HOME/.local/bin/omaterm" "$HOME/.local/omaterm.app/omaterm" "$HOME/.config/omaterm/bin/omaterm"; do [ -x "$c" ] && { p=$c; break; }; done; ` +
		`[ -n "$p" ] || p=$(command -v omaterm 2>/dev/null); ` +
		`v=; [ -n "$p" ] && v=$("$p" version 2>/dev/null); v=${v##* }; ` +
		`s="${XDG_STATE_HOME:-$HOME/.local/state}/` + name + `/build"; b=; [ -f "$s" ] && b=$(cat "$s"); ` +
		`printf "U=%s\nA=%s\nV=%s\nB=%s\n" "$u" "$a" "$v" "$b"`
	out, err := sshconn.Run(target, script)
	if err != nil {
		return hostProbe{}, err
	}
	return parseProbe(out), nil
}

// parseProbe reads the lines probeHost prints, ignoring a login banner's.
func parseProbe(out string) hostProbe {
	var p hostProbe
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "U":
			p.os = remoteOS(v)
		case "A":
			p.arch = remoteArch(v)
		case "V":
			if v != "" {
				p.version, p.found = v, true
			}
		case "B":
			p.build = v
		}
	}
	return p
}

func remoteOS(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "linux", "darwin":
		return strings.ToLower(strings.TrimSpace(v))
	}
	return strings.ToLower(strings.TrimSpace(v))
}

func remoteArch(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	case "armv7l", "armv6l", "arm":
		return "arm"
	case "i386", "i686", "386":
		return "386"
	}
	return strings.ToLower(strings.TrimSpace(v))
}

// installHost copies this executable, and the terminal library beside it,
// to the host and links the command panes use.
func installHost(h RemoteHost, build string) error {
	if strings.ContainsAny(build, "\"'`$\\ \t\n") {
		return fmt.Errorf("bad build name %q", build)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := addTarFile(tw, exe, "omaterm"); err != nil {
		return err
	}
	if so := terminalLibrary(exe); so != "" {
		if err := addTarFile(tw, so, "libghostty-vt.so"); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	want := daemon.VersionOf(build)
	script := `set -e; ` +
		`mkdir -p "$HOME/.local/omaterm.app" "$HOME/.local/bin"; ` +
		`systemctl --user stop omaterm >/dev/null 2>&1 || true; ` +
		`systemctl --user stop omaterm-dev >/dev/null 2>&1 || true; ` +
		`"$HOME/.local/bin/omaterm" daemon stop >/dev/null 2>&1 || true; ` +
		`pkill -f "$HOME/.local/omaterm.app/omaterm __daemon" >/dev/null 2>&1 || true; ` +
		`tar -xf - -C "$HOME/.local/omaterm.app"; ` +
		`chmod 755 "$HOME/.local/omaterm.app/omaterm"; ` +
		`if [ -f "$HOME/.local/omaterm.app/libghostty-vt.so" ]; then chmod 755 "$HOME/.local/omaterm.app/libghostty-vt.so"; fi; ` +
		`ln -sf "$HOME/.local/omaterm.app/omaterm" "$HOME/.local/bin/omaterm"; ` +
		`s="${XDG_STATE_HOME:-$HOME/.local/state}/` + paths.Name() + `"; mkdir -p "$s"; ` +
		`v=$("$HOME/.local/bin/omaterm" version 2>/dev/null); v=${v##* }; ` +
		`if [ "$v" != "` + want + `" ]; then echo "omaterm $v is not ` + want + `" >&2; exit 1; fi; ` +
		`printf "%s" "` + build + `" > "$s/build"; ` +
		`echo "omaterm $v"`
	if _, err := sshconn.Stream(h.SSH, script, &buf); err != nil {
		return fmt.Errorf("install omaterm on %s: %w", h.Name, err)
	}
	return nil
}

// terminalLibrary is the terminal library this build loads: beside the
// executable in a packaged app, or in mygo's cache for a go build.
func terminalLibrary(exe string) string {
	beside := filepath.Join(filepath.Dir(exe), "libghostty-vt.so")
	if _, err := os.Stat(beside); err == nil {
		return beside
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(cache, "mygo", "natives", "*", "libghostty-vt.so"))
	var best string
	var bestTime time.Time
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && info.ModTime().After(bestTime) {
			best, bestTime = m, info.ModTime()
		}
	}
	return best
}

func addTarFile(tw *tar.Writer, path, name string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	hdr := &tar.Header{Name: name, Mode: 0o755, Size: info.Size(), ModTime: info.ModTime(), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}
