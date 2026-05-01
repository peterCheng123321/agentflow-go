package server

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agentflow-go/internal/config"
	"agentflow-go/internal/rag"
)

// writeTestPNG drops a small solid-color PNG at path so the handler has
// something concrete to thumbnail.
func writeTestPNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 0xff, G: 0x80, B: 0x40, A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write test png: %v", err)
	}
}

// newTestServerWithDoc spins up a minimal Server with a rag manager that
// already knows about a single PNG document on disk.
func newTestServerWithDoc(t *testing.T, filename string) (*Server, string) {
	t.Helper()
	dataDir := t.TempDir()

	docsDir := filepath.Join(dataDir, "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	docPath := filepath.Join(docsDir, filename)
	writeTestPNG(t, docPath, 200, 100)

	persistDir := filepath.Join(dataDir, "vector_store")
	if err := os.MkdirAll(persistDir, 0o755); err != nil {
		t.Fatalf("mkdir vec: %v", err)
	}
	mgr := rag.NewManager(persistDir)
	if err := mgr.IngestFile(docPath, "fixture content", map[string]interface{}{"filename": filename}); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	s := &Server{
		cfg: &config.Config{DataDir: dataDir},
		rag: mgr,
		mux: http.NewServeMux(),
	}
	s.mux.HandleFunc("/v1/documents/", s.handleDocuments)
	return s, dataDir
}

func TestThumbnailHandler_ReturnsPNGAndCaches(t *testing.T) {
	const filename = "fixture.png"
	s, dataDir := newTestServerWithDoc(t, filename)

	req := httptest.NewRequest(http.MethodGet, "/v1/documents/"+filename+"/thumbnail?size=128", nil)
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", got)
	}
	// Confirm we got a real PNG (8-byte signature).
	body := rr.Body.Bytes()
	if len(body) < 8 || !bytes.Equal(body[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}) {
		t.Fatalf("body is not a PNG (len=%d)", len(body))
	}

	// Decoding should succeed and width should equal the requested size
	// (200×100 source → fits to 128×64).
	img, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("decode response png: %v", err)
	}
	if w := img.Bounds().Dx(); w != 128 {
		t.Errorf("thumbnail width = %d, want 128", w)
	}

	// Cache file must now exist on disk.
	cacheDir := filepath.Join(dataDir, "cache", "thumbs")
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatalf("read cache dir: %v", err)
	}
	pngCount := 0
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".png" {
			pngCount++
		}
	}
	if pngCount != 1 {
		t.Errorf("cache png count = %d, want 1", pngCount)
	}
}

func TestThumbnailHandler_ServesFromCacheOnSecondHit(t *testing.T) {
	const filename = "fixture2.png"
	s, dataDir := newTestServerWithDoc(t, filename)

	// Pin the source mtime so the cache key is stable across the two hits.
	docPath := filepath.Join(dataDir, "docs", filename)
	pinned := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(docPath, pinned, pinned); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	hit := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/v1/documents/"+filename+"/thumbnail?size=64", nil)
		rr := httptest.NewRecorder()
		s.mux.ServeHTTP(rr, req)
		return rr
	}

	first := hit()
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d", first.Code)
	}

	// Mark the cache file with sentinel bytes — if the second hit re-renders
	// instead of serving from cache, those bytes would be overwritten with a
	// real PNG.
	cacheDir := filepath.Join(dataDir, "cache", "thumbs")
	entries, err := os.ReadDir(cacheDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("cache empty after first hit: %v", err)
	}
	cacheFile := filepath.Join(cacheDir, entries[0].Name())
	sentinel := []byte("CACHED")
	if err := os.WriteFile(cacheFile, sentinel, 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	second := hit()
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d (body=%s)", second.Code, second.Body.String())
	}
	if !bytes.Equal(second.Body.Bytes(), sentinel) {
		t.Errorf("second hit did not serve from cache: got %d bytes, want sentinel %q", len(second.Body.Bytes()), sentinel)
	}
}

func TestParseThumbnailSize_Clamps(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 128},
		{"64", 64},
		{"512", 512},
		{"5", 32},     // below min → clamp up
		{"9999", 512}, // above max → clamp down
		{"abc", 128},  // unparseable → default
	}
	for _, c := range cases {
		if got := parseThumbnailSize(c.in); got != c.want {
			t.Errorf("parseThumbnailSize(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
