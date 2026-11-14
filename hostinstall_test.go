package main

import "testing"

func TestParseProbe(t *testing.T) {
	p := parseProbe("U=Linux\nA=x86_64\nV=0.14.5\nB=0.14.5+abc123\n")
	if p.os != "linux" || p.arch != "amd64" || p.version != "0.14.5" || p.build != "0.14.5+abc123" || !p.found {
		t.Fatalf("probe: %+v", p)
	}
	// A login banner's lines are not settings.
	p = parseProbe("Welcome to devbox\nU=Darwin\nA=arm64\nV=\nB=\n")
	if p.os != "darwin" || p.arch != "arm64" || p.found {
		t.Fatalf("probe: %+v", p)
	}
}

func TestRemoteArch(t *testing.T) {
	for in, want := range map[string]string{
		"x86_64": "amd64", "amd64": "amd64",
		"aarch64": "arm64", "arm64": "arm64",
		"armv7l": "arm", "i686": "386",
	} {
		if got := remoteArch(in); got != want {
			t.Errorf("remoteArch(%q) = %q, want %q", in, got, want)
		}
	}
}
