package ospath

import "testing"

func TestSame(t *testing.T) {
	for _, c := range []struct {
		goos string
		a, b string
		want bool
	}{
		{"linux", "/home/me/proj", "/home/me/proj/", true},
		{"linux", "/home/me/proj", "/home/me/Proj", false},
		{"linux", "/home/me/proj", "/home/me/other", false},
		{"linux", "/home/me/proj", "/home/me/proj/sub", false},
		{"linux", "/a/./b/../c", "/a/c", true},
		{"windows", `C:\proj`, `c:\Proj`, true},
		{"windows", `C:\proj\`, `C:/proj`, true},
		{"windows", `C:\proj`, `D:\proj`, false},
		{"windows", `C:\proj`, `C:\proj\src`, false},
		{"windows", `C:\a\..\b`, `c:\b`, true},
	} {
		if got := Same(c.goos, c.a, c.b); got != c.want {
			t.Errorf("Same(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestContains(t *testing.T) {
	for _, c := range []struct {
		goos string
		dir  string
		path string
		want bool
	}{
		{"linux", "/home/me/proj", "/home/me/proj", true},
		{"linux", "/home/me/proj", "/home/me/proj/src/a.go", true},
		{"linux", "/home/me/proj", "/home/me/project/a.go", false},
		{"linux", "/home/me/proj", "/home/me", false},
		{"linux", "/", "/x", true},
		{"linux", "", "/x", false},
		{"windows", `C:\proj`, `C:\proj\src\a.go`, true},
		{"windows", `c:\Proj`, `C:\proj\src`, true},
		{"windows", `C:\proj`, `C:\projects\a.go`, false},
		{"windows", `C:\proj`, `D:\proj\src`, false},
		{"windows", `C:\`, `D:\app`, false},
		{"windows", `C:\`, `C:\app`, true},
	} {
		if got := Contains(c.goos, c.dir, c.path); got != c.want {
			t.Errorf("Contains(%q, %q) = %v, want %v", c.dir, c.path, got, c.want)
		}
	}
}

func TestShortHome(t *testing.T) {
	for _, c := range []struct {
		goos, home, dir, want string
	}{
		{"linux", "/home/me", "/home/me", "~"},
		{"linux", "/home/me", "/home/me/src", "~/src"},
		{"linux", "/home/me", "/home/me/src/a", "~/src/a"},
		{"linux", "/home/me", "/home/meg", "/home/meg"},
		{"linux", "/home/me", "/srv", "/srv"},
		{"windows", `C:\Users\Me`, `C:\Users\Me`, "~"},
		{"windows", `C:\Users\Me`, `c:\users\me\src`, `~\src`},
		{"windows", `C:\Users\Me`, `C:\Users\MeX`, `C:\Users\MeX`},
		{"windows", `C:\Users\Me`, `D:\src`, `D:\src`},
	} {
		if got := ShortHome(c.goos, c.home, c.dir); got != c.want {
			t.Errorf("ShortHome(%q, %q) = %q, want %q", c.home, c.dir, got, c.want)
		}
	}
}

func TestBase(t *testing.T) {
	for _, c := range []struct {
		goos, p, want string
	}{
		{"linux", "/home/me/src", "src"},
		{"linux", "/", "/"},
		{"linux", "", "."},
		{"windows", `C:\proj\src`, "src"},
		{"windows", `C:\`, "C:"},
		{"windows", `D:/`, "D:"},
		{"windows", `\\host\share`, `\\host\share`},
		{"windows", `\\host\share\src`, "src"},
	} {
		if got := Base(c.goos, c.p); got != c.want {
			t.Errorf("Base(%q) = %q, want %q", c.p, got, c.want)
		}
	}
}

func TestVolumeAndIsAbs(t *testing.T) {
	for _, c := range []struct {
		goos, p string
		vol     string
		abs     bool
	}{
		{"linux", "/home/me", "", true},
		{"linux", "src/a", "", false},
		{"windows", `C:\proj`, "C:", true},
		{"windows", `C:proj`, "C:", false},
		{"windows", `\proj`, "", false},
		{"windows", `\\host\share\src`, `\\host\share`, true},
		{"windows", `\\host\share`, `\\host\share`, false},
	} {
		if got := Volume(c.goos, c.p); got != c.vol {
			t.Errorf("Volume(%q) = %q, want %q", c.p, got, c.vol)
		}
		if got := IsAbs(c.goos, c.p); got != c.abs {
			t.Errorf("IsAbs(%q) = %v, want %v", c.p, got, c.abs)
		}
	}
}
