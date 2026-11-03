package voice

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"time"

	"omaterm/internal/atomicfile"
)

// Progress is told how much of a download has arrived, and of how much: 0
// while the size isn't known.
type Progress func(done, total int64)

var (
	// ErrBusy is a download of the same file running already, in this
	// process or another.
	ErrBusy = errors.New("already downloading")
	// ErrChecksum is a download whose bytes aren't the ones pinned.
	ErrChecksum = errors.New("the download doesn't match its checksum")
	// ErrUnpinned is a download with no checksum to check it against.
	ErrUnpinned = errors.New("no checksum is pinned for this download")
)

// httpClient fetches downloads; tests swap it. No overall timeout: a
// model is hundreds of MB, and the context ends a download.
var httpClient = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	ResponseHeaderTimeout: 30 * time.Second,
	TLSHandshakeTimeout:   15 * time.Second,
}}

// progressEvery is how often a download tells its progress, at most.
var progressEvery = 100 * time.Millisecond

// Download fetches url into dest, checking it against sum, a hex SHA-256.
// The bytes go to dest.part first and are renamed into place once they
// check out; a failed or cancelled download leaves nothing behind. size,
// when known, caps what is read. A second download of dest, from this
// process or another, gets ErrBusy.
func Download(ctx context.Context, url, sum, dest string, size int64, progress Progress) error {
	if sum == "" {
		return ErrUnpinned
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	unlock, ok := tryLock(dest + ".lock")
	if !ok {
		return ErrBusy
	}
	defer unlock()
	part := dest + ".part"
	err := fetch(ctx, url, sum, part, size, progress)
	if err == nil {
		err = atomicfile.Rename(part, dest)
	}
	if err != nil {
		os.Remove(part)
	}
	return err
}

// fetch downloads url into part and checks it.
func fetch(ctx context.Context, url, sum, part string, size int64, progress Progress) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("download: %s", res.Status)
	}
	total := res.ContentLength
	if total <= 0 {
		total = size
	}
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	body := io.Reader(res.Body)
	if size > 0 {
		// One byte over says the file isn't the one pinned, rather than
		// filling the disk.
		body = io.LimitReader(body, size+1)
	}
	pw := &progressWriter{total: total, fn: progress}
	_, err = io.Copy(io.MultiWriter(f, h, pw), body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	pw.report(true)
	if size > 0 && pw.done > size {
		return ErrChecksum
	}
	if hex.EncodeToString(h.Sum(nil)) != sum {
		return ErrChecksum
	}
	return nil
}

// progressWriter counts what passes, telling fn now and then.
type progressWriter struct {
	done, total int64
	fn          Progress
	at          time.Time
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	p.report(false)
	return len(b), nil
}

func (p *progressWriter) report(last bool) {
	if p.fn == nil {
		return
	}
	if now := time.Now(); last || now.Sub(p.at) >= progressEvery {
		p.at = now
		p.fn(p.done, p.total)
	}
}

// maxEngineBytes caps the executable unpacked from an engine build.
const maxEngineBytes = 256 << 20

// InstallEngine downloads the build e into dir and unpacks its
// whisper-server to EnginePath(dir).
func InstallEngine(ctx context.Context, e Engine, dir string, progress Progress) error {
	archive := filepath.Join(dir, e.Asset())
	if err := Download(ctx, e.URL(), e.SHA256, archive, 0, progress); err != nil {
		return err
	}
	defer os.Remove(archive)
	return unpackEngine(archive, EnginePath(dir))
}

// unpackEngine takes the whisper-server out of a build's tar.gz, into
// dest.
func unpackEngine(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return errors.New("the download has no whisper-server")
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg || path.Base(hdr.Name) != "whisper-server" {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return err
		}
		tmp := dest + ".tmp"
		out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
		if err != nil {
			return err
		}
		n, err := io.Copy(out, io.LimitReader(tr, maxEngineBytes+1))
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err == nil && n > maxEngineBytes {
			err = errors.New("whisper-server in the download is too large")
		}
		if err == nil {
			err = atomicfile.Rename(tmp, dest)
		}
		if err != nil {
			os.Remove(tmp)
		}
		return err
	}
}
