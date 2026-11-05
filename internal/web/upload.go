package web

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"image"
	_ "image/gif" // decoded, then sent on as a JPEG
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A photo or screenshot from the phone, for an agent to look at: the page
// sends it, scaled down, and pastes the path it is answered with into the
// pane, as its own paste. It is kept outside every project, in the
// daemon's state folder, under a name of the server's, and for a day.
// The server decodes it and encodes it again: what reaches the agent is
// the picture alone, without the EXIF that tells where a phone took it.

// The upload's limits: the bytes that come, the pixels they may claim to
// be (a small file can say it is a huge picture, which decoding would
// fill memory with), and how many a device sends a minute.
const (
	maxUploadBytes  = 10 << 20
	maxUploadPixels = 16 << 20
	uploadLimit     = 10
)

// uploadsKept is how long a photo is kept, and uploadsQuota how many bytes
// of them at most: past it, a new one waits for the old to go.
const (
	uploadsKept  = 24 * time.Hour
	uploadsQuota = 200 << 20
)

// uploadDirer is a daemon that keeps the phone's photos, as the session
// daemon does, in its state folder.
type uploadDirer interface{ UploadDir() string }

// uploadDir is where the photos go, "" when the daemon keeps none.
func (s *Server) uploadDir() string {
	if u, ok := s.d.(uploadDirer); ok {
		return u.UploadDir()
	}
	return ""
}

// allowUpload tells whether the device may send another photo now.
func (s *Server) allowUpload(device string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.uploads == nil {
		s.uploads = map[string][]time.Time{}
	}
	for d, times := range s.uploads {
		if times = recent(times, now); len(times) == 0 {
			delete(s.uploads, d)
		} else {
			s.uploads[d] = times
		}
	}
	if len(s.uploads[device]) >= uploadLimit {
		return false
	}
	s.uploads[device] = append(s.uploads[device], now)
	return true
}

var (
	errNotPicture  = errors.New("that isn't a PNG, JPEG or GIF picture")
	errTooManyPx   = errors.New("the picture is too large: send it smaller")
	errUploadsFull = errors.New("the photos sent today fill omaterm's room for them: try again once older ones go, in a day")
)

// decoding lets two pictures be decoded and encoded again at once: each
// may take some 64 MB while it is.
var decoding = make(chan struct{}, 2)

// upload keeps the photo the page sends for the pane, and answers its
// path.
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	dir := s.uploadDir()
	if dir == "" {
		http.NotFound(w, r)
		return
	}
	if _, ok := s.paneFolderOr404(w, r); !ok {
		return
	}
	d := r.Context().Value(deviceKey{}).(Device)
	if !s.allowUpload(d.ID) {
		http.Error(w, "too many photos: wait a minute", http.StatusTooManyRequests)
		return
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxUploadBytes))
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		http.Error(w, "the photo is over 10 MB", http.StatusRequestEntityTooLarge)
		return
	}
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	decoding <- struct{}{}
	defer func() { <-decoding }()
	img, format, err := decodeUpload(b)
	switch {
	case errors.Is(err, errTooManyPx):
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	path, err := saveUpload(dir, img, format, time.Now())
	switch {
	case errors.Is(err, errUploadsFull):
		http.Error(w, err.Error(), http.StatusInsufficientStorage)
		return
	case err != nil:
		log.Printf("web: upload: %v", err)
		http.Error(w, "the photo couldn't be kept", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"path": path})
}

// decodeUpload reads a picture, its size checked from its header before
// any of it is decoded.
func decodeUpload(b []byte) (image.Image, string, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, "", errNotPicture
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxUploadPixels {
		return nil, "", errTooManyPx
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, "", errNotPicture
	}
	return img, format, nil
}

// saveUpload writes the picture into dir under a new name, read and
// written by the user alone: a PNG as a PNG, a screenshot's text kept
// sharp, else a JPEG. The day's old photos go first.
func saveUpload(dir string, img image.Image, format string, now time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	if sweepUploads(dir, now) >= uploadsQuota {
		return "", errUploadsFull
	}
	var buf bytes.Buffer
	ext := ".jpg"
	if format == "png" {
		ext = ".png"
		if err := png.Encode(&buf, img); err != nil {
			return "", err
		}
	} else if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return "", err
	}
	var id [8]byte
	rand.Read(id[:])
	path := filepath.Join(dir, "phone-"+hex.EncodeToString(id[:])+ext)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// sweepUploads removes the photos in dir older than uploadsKept, and
// tells how many bytes the rest take.
func sweepUploads(dir string, now time.Time) int64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var kept int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || !strings.HasPrefix(e.Name(), "phone-") {
			continue
		}
		if now.Sub(info.ModTime()) > uploadsKept {
			os.Remove(filepath.Join(dir, e.Name()))
			continue
		}
		kept += info.Size()
	}
	return kept
}

// keepUploads sweeps the photos now, and every hour while the server
// runs, so that none outlasts its day by much.
func (s *Server) keepUploads() {
	dir := s.uploadDir()
	if dir == "" {
		return
	}
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		sweepUploads(dir, time.Now())
		select {
		case <-s.done:
			return
		case <-t.C:
		}
	}
}
