//go:build linux || darwin

package web_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omaterm/internal/web"
)

func picture() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 8, 6))
	for x := 0; x < 8; x++ {
		img.Set(x, 3, color.RGBA{200, 40, 40, 255})
	}
	return img
}

// photoWithEXIF is a JPEG as a phone takes it: an EXIF segment, with
// where it was taken, before the picture.
func photoWithEXIF(t *testing.T) []byte {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, picture(), nil); err != nil {
		t.Fatal(err)
	}
	payload := []byte("Exif\x00\x00GPSLatitude=51.5007N;GPSLongitude=0.1246W")
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	out := append([]byte{}, b.Bytes()[:2]...)
	out = append(out, seg...)
	out = append(out, payload...)
	return append(out, b.Bytes()[2:]...)
}

// pngClaiming is a small PNG whose header says it is w×h.
func pngClaiming(t *testing.T, w, h uint32) []byte {
	var b bytes.Buffer
	png.Encode(&b, image.NewGray(image.Rect(0, 0, 1, 1)))
	p := b.Bytes()
	binary.BigEndian.PutUint32(p[16:], w)
	binary.BigEndian.PutUint32(p[20:], h)
	binary.BigEndian.PutUint32(p[29:], crc32.ChecksumIEEE(p[12:29]))
	return p
}

func (f *fixture) upload(t *testing.T, c *http.Client, pane string, body []byte, origin string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", f.base+"/api/panes/"+pane+"/upload", bytes.NewReader(body))
	req.Header.Set("Content-Type", "image/jpeg")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Path string }
	if res.StatusCode == 200 {
		json.NewDecoder(res.Body).Decode(&got)
	}
	res.Body.Close()
	return res, got.Path
}

// A photo is kept in the state folder's uploads, the user's alone, under
// a name of the server's; what is kept is the picture without its EXIF.
func TestUploadKeepsThePictureAlone(t *testing.T) {
	f, c := filesFixture(t)
	res, path := f.upload(t, c, "a1", photoWithEXIF(t), f.base)
	if res.StatusCode != 200 {
		t.Fatalf("upload: %s", res.Status)
	}
	dir := filepath.Join(f.dir, "uploads")
	if filepath.Dir(path) != dir || !strings.HasPrefix(filepath.Base(path), "phone-") || filepath.Ext(path) != ".jpg" ||
		strings.ContainsAny(path, " '\"$;&|") {
		t.Fatalf("path %q", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("Exif")) || bytes.Contains(b, []byte("GPS")) {
		t.Fatal("the EXIF was kept")
	}
	if img, err := jpeg.Decode(bytes.NewReader(b)); err != nil || img.Bounds().Dx() != 8 {
		t.Fatalf("kept %v %v", img, err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v", fi.Mode())
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("folder mode %v", fi.Mode())
	}

	// A screenshot's PNG stays a PNG.
	var p bytes.Buffer
	png.Encode(&p, picture())
	if res, path := f.upload(t, c, "a1", p.Bytes(), f.base); res.StatusCode != 200 || filepath.Ext(path) != ".png" {
		t.Fatalf("png: %s %q", res.Status, path)
	}
}

// What isn't a picture, claims too many pixels or is too big, is turned
// away, and nothing is kept.
func TestUploadRefuses(t *testing.T) {
	f, c := filesFixture(t)
	for name, tc := range map[string]struct {
		body []byte
		want int
	}{
		"text":     {[]byte("#!/bin/sh\nrm -rf ~\n"), http.StatusBadRequest},
		"bomb":     {pngClaiming(t, 100000, 100000), http.StatusRequestEntityTooLarge},
		"too big":  {append(photoWithEXIF(t), make([]byte, 10<<20)...), http.StatusRequestEntityTooLarge},
		"no width": {pngClaiming(t, 0, 10), http.StatusBadRequest},
	} {
		if res, _ := f.upload(t, c, "a1", tc.body, f.base); res.StatusCode != tc.want {
			t.Errorf("%s: %s, want %d", name, res.Status, tc.want)
		}
	}
	if res, _ := f.upload(t, c, "nope", photoWithEXIF(t), f.base); res.StatusCode != http.StatusNotFound {
		t.Errorf("no such pane: %s", res.Status)
	}
	if res, _ := f.upload(t, c, "a1", photoWithEXIF(t), "https://evil.example"); res.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site: %s", res.Status)
	}
	if res, _ := f.upload(t, f.client(t), "a1", photoWithEXIF(t), f.base); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("unpaired: %s", res.Status)
	}
	// GET has no such route: it changes nothing.
	if res, err := c.Get(f.base + "/api/panes/a1/upload"); err != nil || res.StatusCode != http.StatusNotFound {
		t.Errorf("GET: %v %v", res.Status, err)
	}
	if entries, _ := os.ReadDir(filepath.Join(f.dir, "uploads")); len(entries) != 0 {
		t.Fatalf("kept %d", len(entries))
	}
}

// A device sends so many photos a minute.
func TestUploadLimit(t *testing.T) {
	f, c := filesFixture(t)
	photo := photoWithEXIF(t)
	for i := 0; i < 10; i++ {
		if res, _ := f.upload(t, c, "a1", photo, f.base); res.StatusCode != 200 {
			t.Fatalf("photo %d: %s", i, res.Status)
		}
	}
	if res, _ := f.upload(t, c, "a1", photo, f.base); res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("one too many: %s", res.Status)
	}
	// Another device has its own.
	if res, _ := f.upload(t, f.paired(t), "a1", photo, f.base); res.StatusCode != 200 {
		t.Fatalf("another device: %s", res.Status)
	}
}

// A day on, a photo goes; nothing else in the folder does.
func TestUploadSweep(t *testing.T) {
	f, c := filesFixture(t)
	dir := filepath.Join(f.dir, "uploads")
	os.MkdirAll(dir, 0o700)
	old, other := filepath.Join(dir, "phone-0123.jpg"), filepath.Join(dir, "notes.txt")
	for _, p := range []string{old, other} {
		os.WriteFile(p, []byte("x"), 0o600)
		day := time.Now().Add(-25 * time.Hour)
		os.Chtimes(p, day, day)
	}
	res, path := f.upload(t, c, "a1", photoWithEXIF(t), f.base)
	if res.StatusCode != 200 {
		t.Fatal(res.Status)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("the day-old photo stays")
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("another file went")
	}
	fi, _ := os.Stat(path)
	if kept := web.SweepUploads(dir, time.Now()); kept != fi.Size() {
		t.Fatalf("kept %d bytes, want %d", kept, fi.Size())
	}
	if web.SweepUploads(dir, time.Now().Add(25*time.Hour)) != 0 {
		t.Fatal("the new photo outlasts its day")
	}
}
