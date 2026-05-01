package server

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"
)

// ocrCache is a content-addressed disk cache for OCR results.
//
// Why: vision-OCR is the slowest hop in the intake pipeline (3–8s per image
// against the cloud model). The same evidence file is re-analyzed every time
// the user re-runs intake on a folder, or experiments with different
// classifications. Hashing the bytes once and stashing the text means a
// re-run on the same folder skips OCR entirely.
//
// Layout: <DataDir>/ocr_cache/<sha256>.txt
//
// The cache is best-effort — any read or write error degrades silently into
// a re-OCR. We never want a cache bug to block intake.
type ocrCache struct {
	dir   string
	hits  atomic.Uint64
	miss  atomic.Uint64
	saves atomic.Uint64
}

func newOCRCache(dataDir string) *ocrCache {
	d := filepath.Join(dataDir, "ocr_cache")
	_ = os.MkdirAll(d, 0755)
	return &ocrCache{dir: d}
}

// hashFile streams the file through sha256.
func (c *ocrCache) hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Get returns the cached OCR text for the file at path, or "" + false if
// nothing is cached. Any error is treated as cache miss.
func (c *ocrCache) Get(path string) (string, bool) {
	hash, err := c.hashFile(path)
	if err != nil {
		c.miss.Add(1)
		return "", false
	}
	cachedPath := filepath.Join(c.dir, hash+".txt")
	b, err := os.ReadFile(cachedPath)
	if err != nil {
		c.miss.Add(1)
		return "", false
	}
	c.hits.Add(1)
	return string(b), true
}

// Set writes the OCR text for the file at path to the cache. Errors are logged
// but never returned — caching is best-effort.
func (c *ocrCache) Set(path, text string) {
	hash, err := c.hashFile(path)
	if err != nil {
		return
	}
	cachedPath := filepath.Join(c.dir, hash+".txt")
	if err := os.WriteFile(cachedPath, []byte(text), 0644); err != nil {
		log.Printf("[ocr-cache] write %s: %v", cachedPath, err)
		return
	}
	c.saves.Add(1)
}

func (c *ocrCache) Stats() (hits, miss, saves uint64) {
	return c.hits.Load(), c.miss.Load(), c.saves.Load()
}
