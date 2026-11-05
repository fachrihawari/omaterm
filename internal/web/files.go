package web

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"omaterm/internal/fsview"
	"omaterm/internal/gitsnap"
	"omaterm/internal/proto"
)

// A session's folder and its git changes, for the page to read as the
// desktop's Files and Git tabs show them. Reading is all: the page
// changes nothing here, as it starts and stops nothing (its shell, in the
// pane, can).

// maxRaw caps a picture the page loads whole.
const maxRaw = 16 << 20

// maxPatch caps a file's diff: past it, the page says it is too large.
const maxPatch = 1 << 20

// paneFolder is what the page reads of a pane's session: the folder it
// works in, its worktree's or else its project's; base, the branch a
// worktree's changes are since, "" for none; and the pane itself.
type paneFolder struct {
	root string
	base string
	pane proto.Pane
}

// folder finds the pane id's session in the state, as the daemon has it
// or the app forwards it.
func (s *Server) folder(ctx context.Context, id string) (paneFolder, bool) {
	v, err := s.d.Handle(ctx, "state", nil)
	if err != nil {
		return paneFolder{}, false
	}
	raw, _ := json.Marshal(v)
	var st proto.State
	if json.Unmarshal(raw, &st) != nil {
		return paneFolder{}, false
	}
	for _, sess := range st.Sessions {
		for _, p := range sess.Panes {
			if p.ID != id {
				continue
			}
			f := paneFolder{root: p.WorkDir(), pane: p}
			if w := sess.Worktree; w != nil && w.Dir != "" {
				f.root, f.base = w.Dir, w.Base
				return f, true
			}
			for _, pr := range st.Projects {
				if pr.ID == sess.Project && pr.Dir != "" {
					f.root = pr.Dir
				}
			}
			return f, f.root != ""
		}
	}
	return paneFolder{}, false
}

// paneFolderOr404 is the request's pane's folder, or a 404 answered.
func (s *Server) paneFolderOr404(w http.ResponseWriter, r *http.Request) (paneFolder, bool) {
	f, ok := s.folder(r.Context(), r.PathValue("id"))
	if !ok {
		http.Error(w, "no such pane", http.StatusNotFound)
	}
	return f, ok
}

// fileError answers a read that failed: a path that leaves the folder is
// the page's mistake, one that is gone a 404.
func fileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, fsview.ErrBadPath):
		http.Error(w, "not a path in the folder", http.StatusBadRequest)
	case errors.Is(err, fs.ErrNotExist):
		http.Error(w, "not found", http.StatusNotFound)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// files lists a folder of the pane's session: ?path= under its root.
func (s *Server) files(w http.ResponseWriter, r *http.Request) {
	f, ok := s.paneFolderOr404(w, r)
	if !ok {
		return
	}
	d, err := fsview.List(f.root, r.URL.Query().Get("path"))
	if err != nil {
		fileError(w, err)
		return
	}
	writeJSON(w, struct {
		Root string `json:"root"`
		fsview.Dir
	}{filepath.Base(f.root), d})
}

// file is one of its files, as fsview reads it.
func (s *Server) file(w http.ResponseWriter, r *http.Request) {
	f, ok := s.paneFolderOr404(w, r)
	if !ok {
		return
	}
	file, err := fsview.Read(f.root, r.URL.Query().Get("path"))
	if err != nil {
		fileError(w, err)
		return
	}
	writeJSON(w, file)
}

// raw is a picture of its folder, which the page shows in an <img>: only
// a picture, so that nothing else of the folder comes as a page of this
// origin, and sandboxed, so that an SVG opened by itself runs nothing.
func (s *Server) raw(w http.ResponseWriter, r *http.Request) {
	f, ok := s.paneFolderOr404(w, r)
	if !ok {
		return
	}
	full, err := fsview.Local(f.root, r.URL.Query().Get("path"))
	if err != nil {
		fileError(w, err)
		return
	}
	if !fsview.IsImage(full) {
		http.Error(w, "not a picture", http.StatusNotFound)
		return
	}
	fh, err := os.Open(full)
	if err != nil {
		fileError(w, err)
		return
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil || st.IsDir() {
		http.Error(w, "not a picture", http.StatusNotFound)
		return
	}
	if st.Size() > maxRaw {
		http.Error(w, "too large", http.StatusRequestEntityTooLarge)
		return
	}
	h := w.Header()
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'")
	if strings.EqualFold(filepath.Ext(full), ".svg") {
		h.Set("Content-Type", "image/svg+xml")
	}
	http.ServeContent(w, r, filepath.Base(full), st.ModTime(), fh)
}

// change is a changed file as the page reads it.
type change struct {
	Path    string `json:"path"`
	OldPath string `json:"oldPath,omitempty"`
	Status  string `json:"status"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Binary  bool   `json:"binary,omitempty"`
}

// git asks the daemon a git method of the pane, woken first: with the app
// closed, the daemon answers only for a pane that runs. A worktree's
// changes are since its base branch, else since HEAD.
func (s *Server) git(ctx context.Context, f paneFolder, method string, paths ...string) (any, error) {
	wake, _ := json.Marshal(map[string]string{"id": f.pane.ID})
	if _, err := s.d.Handle(ctx, "pane.wake", wake); err != nil {
		return nil, err
	}
	p := proto.GitParams{Pane: f.pane.ID, Base: gitsnap.BaseHead, Paths: paths}
	if f.base != "" {
		p.Base = gitsnap.BaseBranch
	}
	raw, _ := json.Marshal(p)
	return s.d.Handle(ctx, method, raw)
}

// gitError answers a git method that failed: a pane outside a repository
// is a 404 the page says as such.
func gitError(w http.ResponseWriter, err error) {
	if proto.CodeOf(err) == proto.CodeNotFound {
		http.Error(w, "Not a git repository", http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), http.StatusBadGateway)
}

// changes are the files the pane's session changed: in a worktree, since
// its base branch; else since HEAD, what is not committed.
func (s *Server) changes(w http.ResponseWriter, r *http.Request) {
	f, ok := s.paneFolderOr404(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	v, err := s.git(ctx, f, "git.changes")
	if err != nil {
		gitError(w, err)
		return
	}
	raw, _ := json.Marshal(v)
	var files []gitsnap.File
	json.Unmarshal(raw, &files)
	out := make([]change, 0, len(files))
	for _, c := range files {
		out = append(out, change{c.Path, c.OldPath, c.Status, c.Added, c.Removed, c.Binary})
	}
	writeJSON(w, map[string]any{"base": f.base, "files": out})
}

// diff is one changed file's patch, ?path= as changes names it: from the
// root of the pane's repository.
func (s *Server) diff(w http.ResponseWriter, r *http.Request) {
	f, ok := s.paneFolderOr404(w, r)
	if !ok {
		return
	}
	rel, err := fsview.Clean(r.URL.Query().Get("path"))
	if err != nil || rel == "" {
		http.Error(w, "not a path in the folder", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	// The repository the daemon opens for the pane, whose root the
	// changes' paths are from.
	repo, err := gitsnap.Open(ctx, f.pane.WorkDir())
	if err != nil {
		http.Error(w, "Not a git repository", http.StatusNotFound)
		return
	}
	v, err := s.git(ctx, f, "git.diff", filepath.Join(repo.Root, filepath.FromSlash(rel)))
	if err != nil {
		gitError(w, err)
		return
	}
	raw, _ := json.Marshal(v)
	var res proto.ReadResult
	json.Unmarshal(raw, &res)
	if len(res.Text) > maxPatch {
		writeJSON(w, map[string]bool{"large": true})
		return
	}
	writeJSON(w, res)
}
