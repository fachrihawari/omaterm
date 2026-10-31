// Package atomicfile replaces a file's contents through a temporary file
// beside it, so a crash or a full disk leaves the old file or the new one,
// never half of one. See Rename.
package atomicfile

// Rename is os.Rename, which replaces the destination's file where Unix
// renames it at once; on Windows it retries for about a second while
// another program still holds the file open, as Defender scanning a fresh
// file, an editor, or the search indexer does.
func Rename(oldpath, newpath string) error {
	return rename(oldpath, newpath)
}
