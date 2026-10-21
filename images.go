package main

import (
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
)

// Pictures open in an image viewer instead of the text editor, and diffs
// of pictures show the two versions side by side instead of a patch that
// says "Binary files differ".

// imageSide is one version of a picture in a diff: what to draw, and the
// facts the caption shows.
type imageSide struct {
	Pic     ui.ImageSource
	W, H    float32
	Size    int64
	Vector  bool // a vector can scale up cleanly, a bitmap cannot
	Missing bool // the file does not exist in this version
}

// imageExts are the formats DecodeBitmap and ParseSVG can read.
var imageExts = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".gif":  true,
	".webp": true,
	".bmp":  true,
	".svg":  true,
}

// isImagePath reports whether path names a picture this app can show.
func isImagePath(path string) bool {
	return imageExts[lowerExt(path)]
}

// isRasterImagePath reports whether path is a picture whose patch is only
// "Binary files differ". An SVG is text, so its diff keeps the lines.
func isRasterImagePath(path string) bool {
	return isImagePath(path) && lowerExt(path) != ".svg"
}

// lowerExt is the path's extension in lower case, for the format checks.
func lowerExt(path string) string {
	return strings.ToLower(filepath.Ext(path))
}

// decodeImage parses data as the picture its path names. An empty data
// means the file is missing from that version.
func decodeImage(path string, data []byte) imageSide {
	side := imageSide{Size: int64(len(data))}
	if len(data) == 0 {
		side.Missing = true
		return side
	}
	if lowerExt(path) == ".svg" {
		if svg, err := ui.ParseSVG(data); err == nil {
			w, h := svg.Size()
			side.Pic, side.W, side.H = svg, w, h
			side.Vector = true
		}
		return side
	}
	if bmp, err := ui.DecodeBitmap(data); err == nil {
		w, h := bmp.Size()
		side.Pic, side.W, side.H = bmp, float32(w), float32(h)
	}
	return side
}

// imageFormat names the picture's format for the status line.
func imageFormat(path string) string {
	switch lowerExt(path) {
	case ".png":
		return "PNG"
	case ".jpg", ".jpeg":
		return "JPEG"
	case ".gif":
		return "GIF"
	case ".webp":
		return "WebP"
	case ".bmp":
		return "BMP"
	case ".svg":
		return "SVG"
	}
	return ""
}
