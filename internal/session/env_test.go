package session

import "testing"

func TestSameKeyOnWindows(t *testing.T) {
	if !sameKey("Path", "PATH", "windows") || sameKey("Path", "PATH", "linux") || !sameKey("PATH", "PATH", "linux") {
		t.Error("Windows' variable names ignore case, others' don't")
	}
	for shell, want := range map[string]bool{`C:\bin\pwsh.exe`: true, `C:\Windows\system32\cmd.exe`: true, "/bin/zsh": false, `C:\Program Files\Git\bin\bash.exe`: false} {
		if windowsShell(shell) != want {
			t.Errorf("windowsShell(%q) = %v", shell, !want)
		}
	}
}
