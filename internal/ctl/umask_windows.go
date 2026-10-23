package ctl

// umask does nothing on Windows, whose sockets are kept by their ACLs.
func umask(m int) int { return 0 }
