package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	// Register decoders so image.Decode handles JPEG and GIF inputs.
	_ "image/gif"
	_ "image/jpeg"
)

// thumbnailDefaultSize is the px size used when no `size` query param is given.
const thumbnailDefaultSize = 128

// thumbnailMinSize / thumbnailMaxSize bound the requested size to keep
// renders cheap and prevent resource abuse.
const (
	thumbnailMinSize = 32
	thumbnailMaxSize = 512
)

// handleDocumentThumbnailByID serves a PNG thumbnail for a document.
//
// Behaviour:
//   - PDFs       → first-page PNG via `qlmanage`
//   - rasters    → decoded + downscaled with stdlib image/draw (NN)
//   - everything → a small solid-color placeholder PNG
//
// Generated PNGs are cached on disk at data/cache/thumbs/<sha>.png keyed by
// (filename, mtime, size). Subsequent hits are served straight from that file.
func (s *Server) handleDocumentThumbnailByID(w http.ResponseWriter, r *http.Request, filename string) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	filename = unescapeURLPathSegment(filename)
	doc, ok := s.rag.GetDocumentFlex(filename)
	if !ok {
		s.writeError(w, http.StatusNotFound, "Document not found")
		return
	}

	diskPath, found := s.resolveDocDiskPath(doc)
	if !found {
		s.writeError(w, http.StatusNotFound, "File not found on disk")
		return
	}

	size := parseThumbnailSize(r.URL.Query().Get("size"))

	st, err := os.Stat(diskPath)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Stat failed")
		return
	}

	cacheDir := filepath.Join(s.cfg.DataDir, "cache", "thumbs")
	cachePath := filepath.Join(cacheDir, thumbnailCacheKey(doc.Filename, st.ModTime(), size)+".png")

	// Cache hit → serve directly.
	if _, err := os.Stat(cachePath); err == nil {
		w.Header().Set("Content-Type", "image/png")
		http.ServeFile(w, r, cachePath)
		return
	}

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to create cache dir")
		return
	}

	pngBytes, err := renderThumbnail(diskPath, doc.Filename, size)
	if err != nil {
		// Fall back to a placeholder rather than 500ing — a missing thumbnail
		// shouldn't break the document grid in the UI.
		pngBytes = placeholderPNG(size, doc.Filename)
	}

	// Best-effort cache write: a failure here doesn't fail the response.
	_ = os.WriteFile(cachePath, pngBytes, 0o644)

	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(pngBytes)
}

// parseThumbnailSize clamps the `size` query param to [min, max] with a default.
func parseThumbnailSize(raw string) int {
	if raw == "" {
		return thumbnailDefaultSize
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return thumbnailDefaultSize
	}
	if n < thumbnailMinSize {
		return thumbnailMinSize
	}
	if n > thumbnailMaxSize {
		return thumbnailMaxSize
	}
	return n
}

// thumbnailCacheKey is sha256(filename + "|" + mtime-unix-ns + "|" + size).
// mtime is included so a re-uploaded document with the same name busts the cache.
func thumbnailCacheKey(filename string, mtime time.Time, size int) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%d", filename, mtime.UnixNano(), size)
	return hex.EncodeToString(h.Sum(nil))
}

// renderThumbnail picks the right rendering strategy based on extension.
// Returns a PNG byte slice no larger than `size` on its longest edge.
func renderThumbnail(diskPath, logicalName string, size int) ([]byte, error) {
	ext := strings.ToLower(filepath.Ext(logicalName))
	if ext == "" {
		ext = strings.ToLower(filepath.Ext(diskPath))
	}

	switch ext {
	case ".pdf":
		return renderPDFThumbnail(diskPath, size)
	case ".png", ".jpg", ".jpeg", ".gif":
		return renderRasterThumbnail(diskPath, size)
	case ".heic", ".webp":
		return renderRasterViaSips(diskPath, size)
	default:
		return nil, fmt.Errorf("no native thumbnailer for %s", ext)
	}
}

// renderPDFThumbnail uses macOS `qlmanage` to render the first page to PNG.
func renderPDFThumbnail(pdfPath string, size int) ([]byte, error) {
	tmp, err := os.MkdirTemp("", "thumb-pdf-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// qlmanage -t -s <size> -o <dir> <file>
	cmd := exec.CommandContext(ctx, "qlmanage", "-t", "-s", strconv.Itoa(size), "-o", tmp, pdfPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("qlmanage: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	// qlmanage names the output "<basename>.png".
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".png") {
			return os.ReadFile(filepath.Join(tmp, e.Name()))
		}
	}
	return nil, fmt.Errorf("qlmanage produced no PNG")
}

// renderRasterThumbnail decodes a PNG/JPEG/GIF and downscales with stdlib.
func renderRasterThumbnail(path string, size int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	src, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	return scaleAndEncode(src, size), nil
}

// renderRasterViaSips converts unsupported formats (HEIC/WebP) to PNG with
// `sips`, then downscales via the stdlib path.
func renderRasterViaSips(path string, size int) ([]byte, error) {
	tmp, err := os.MkdirTemp("", "thumb-sips-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	out := filepath.Join(tmp, "converted.png")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sips", "-s", "format", "png", "-Z", strconv.Itoa(size), path, "--out", out)
	if msg, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("sips: %w (%s)", err, strings.TrimSpace(string(msg)))
	}
	return os.ReadFile(out)
}

// scaleAndEncode fits src inside a (size×size) box preserving aspect ratio
// and returns PNG bytes. Uses NN scaling — fine for thumbnails, stdlib only.
func scaleAndEncode(src image.Image, size int) []byte {
	b := src.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	dstW, dstH := fitInBox(srcW, srcH, size)

	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	for y := 0; y < dstH; y++ {
		sy := b.Min.Y + y*srcH/dstH
		for x := 0; x < dstW; x++ {
			sx := b.Min.X + x*srcW/dstW
			dst.Set(x, y, src.At(sx, sy))
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, dst)
	return buf.Bytes()
}

// fitInBox returns the largest (w', h') with the same aspect ratio as (w,h)
// such that both dimensions are ≤ box.
func fitInBox(w, h, box int) (int, int) {
	if w <= 0 || h <= 0 {
		return box, box
	}
	if w <= box && h <= box {
		return w, h
	}
	if w >= h {
		return box, int(float64(h) * float64(box) / float64(w))
	}
	return int(float64(w) * float64(box) / float64(h)), box
}

// placeholderPNG draws a flat colored square with a 2-px border. The fill
// color is derived from the file extension so different doc types are
// visually distinguishable in a grid.
func placeholderPNG(size int, filename string) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	fill := extensionColor(filename)
	border := color.RGBA{R: 0x33, G: 0x33, B: 0x33, A: 0xff}

	draw.Draw(img, img.Bounds(), &image.Uniform{C: fill}, image.Point{}, draw.Src)
	// 2-px border
	for x := 0; x < size; x++ {
		img.Set(x, 0, border)
		img.Set(x, 1, border)
		img.Set(x, size-1, border)
		img.Set(x, size-2, border)
	}
	for y := 0; y < size; y++ {
		img.Set(0, y, border)
		img.Set(1, y, border)
		img.Set(size-1, y, border)
		img.Set(size-2, y, border)
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// extensionColor maps file extensions to a stable RGB so the UI can visually
// group placeholder thumbnails by doctype at a glance.
func extensionColor(filename string) color.RGBA {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".docx", ".doc":
		return color.RGBA{R: 0x2b, G: 0x57, B: 0x9a, A: 0xff} // word blue
	case ".xlsx", ".xls", ".csv":
		return color.RGBA{R: 0x1f, G: 0x6f, B: 0x3d, A: 0xff} // excel green
	case ".pptx", ".ppt":
		return color.RGBA{R: 0xc0, G: 0x4a, B: 0x1d, A: 0xff} // pptx orange
	case ".zip", ".tar", ".gz":
		return color.RGBA{R: 0x6e, G: 0x60, B: 0x3a, A: 0xff} // archive tan
	case ".txt", ".md":
		return color.RGBA{R: 0x55, G: 0x55, B: 0x55, A: 0xff} // text grey
	default:
		return color.RGBA{R: 0x44, G: 0x44, B: 0x88, A: 0xff} // generic
	}
}

