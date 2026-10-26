package paths

import (
	"os"
	"path/filepath"
	"strconv"
)

// privateTempDir is a folder in the temporary one that only this user
// can open, for sockets whose path is too long where they belong. In /tmp
// itself, another user could make the socket first: the daemon then
// couldn't start, or the omaterm command talked to theirs.
func privateTempDir() string {
	base := filepath.Join(os.TempDir(), Name()+"-"+strconv.Itoa(os.Getuid()))
	for i := 0; i < 8; i++ {
		dir := base
		if i > 0 {
			dir += "-" + strconv.Itoa(i) // the name was taken by someone else
		}
		os.Mkdir(dir, 0o700)
		if ownPrivateDir(dir) {
			return dir
		}
	}
	return base // the client still refuses another user's socket
}
