package devurl

import (
	"os"
	"path/filepath"
)

// NSSDB is a certificate database a browser keeps.
type NSSDB struct {
	Dir   string
	Label string
}

// NSSDBs are the browsers' certificate databases that exist: Chrome and
// Chromium's on Linux, and each Firefox profile's.
func NSSDBs(e Env) []NSSDB {
	if e.Home == "" {
		return nil
	}
	var dbs []NSSDB
	add := func(dir, label string) {
		if _, err := os.Stat(filepath.Join(dir, "cert9.db")); err == nil {
			dbs = append(dbs, NSSDB{Dir: dir, Label: label})
		}
	}
	if e.OS == "linux" {
		add(filepath.Join(e.Home, ".pki", "nssdb"), "Chrome and Chromium")
	}
	for _, glob := range []string{
		".mozilla/firefox/*",
		".config/mozilla/firefox/*",
		"snap/firefox/common/.mozilla/firefox/*",
		".var/app/org.mozilla.firefox/.mozilla/firefox/*",
		"Library/Application Support/Firefox/Profiles/*",
	} {
		dirs, _ := filepath.Glob(filepath.Join(e.Home, glob))
		for _, dir := range dirs {
			add(dir, "Firefox profile "+filepath.Base(dir))
		}
	}
	return dbs
}

// has tells whether the database has the authority.
func (db NSSDB) has(e Env) bool {
	return run("certutil", "-d", "sql:"+db.Dir, "-L", "-n", nssName) == nil
}

func nssAdd(e Env, dir string) error {
	return run("certutil", "-d", "sql:"+dir, "-A", "-n", nssName, "-t", nssTrust, "-i", e.CAPath())
}

func nssRemove(dir string) error {
	return run("certutil", "-d", "sql:"+dir, "-D", "-n", nssName)
}
