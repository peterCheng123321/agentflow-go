package server

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func unescapeURLPathSegment(s string) string {
	if u, err := url.PathUnescape(s); err == nil {
		return u
	}
	return s
}

// handleDocuments dispatches /v1/documents/{name} and /v1/documents/{name}/view.
func (s *Server) handleDocuments(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/documents/")
	if rest == "" {
		s.handleListDocuments(w, r)
		return
	}

	parts := strings.Split(rest, "/")
	name := unescapeURLPathSegment(parts[0])

	if len(parts) >= 2 && parts[1] == "view" {
		s.serveDocumentFile(w, r, name)
		return
	}
	if len(parts) >= 2 && parts[1] == "text" {
		s.serveDocumentText(w, r, name)
		return
	}

	switch r.Method {
	case http.MethodDelete:
		s.deleteDocumentFile(w, name)
	case http.MethodGet:
		s.serveDocumentFile(w, r, name)
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleListDocuments returns every filename present under DataDir/docs.
func (s *Server) handleListDocuments(w http.ResponseWriter, r *http.Request) {
	dir := filepath.Join(s.cfg.DataDir, "docs")
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"documents": names})
}

func (s *Server) serveDocumentFile(w http.ResponseWriter, r *http.Request, name string) {
	path, ok := s.resolveDocDiskPath(name)
	if !ok {
		s.writeError(w, http.StatusNotFound, fmt.Sprintf("document %q not found", name))
		return
	}
	http.ServeFile(w, r, path)
}

// serveDocumentText extracts and returns plain text for a document — used by
// the SwiftUI viewer to preview .docx (and any other format the OCR engine
// can read text from). The OCR cache is consulted first so repeat opens are
// instant.
//
// Response shape: {"filename": "...", "text": "...", "source": "cache|extract"}
func (s *Server) serveDocumentText(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	path, ok := s.resolveDocDiskPath(name)
	if !ok {
		s.writeError(w, http.StatusNotFound, fmt.Sprintf("document %q not found", name))
		return
	}

	source := "extract"
	if s.ocrCache != nil {
		if cached, ok := s.ocrCache.Get(path); ok {
			s.writeJSON(w, http.StatusOK, map[string]string{
				"filename": name,
				"text":     cached,
				"source":   "cache",
			})
			return
		}
	}
	text, err := s.ocr.ScanFile(path)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("extract failed: %v", err))
		return
	}
	if s.ocrCache != nil && text != "" {
		s.ocrCache.Set(path, text)
	}
	s.writeJSON(w, http.StatusOK, map[string]string{
		"filename": name,
		"text":     text,
		"source":   source,
	})
}

func (s *Server) deleteDocumentFile(w http.ResponseWriter, name string) {
	path, ok := s.resolveDocDiskPath(name)
	if !ok {
		s.writeError(w, http.StatusNotFound, fmt.Sprintf("document %q not found", name))
		return
	}
	if err := os.Remove(path); err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.rag.DeleteDocument(name)
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "filename": name})
}

// resolveDocDiskPath locates a document on disk.
//
// Storage layouts in play:
//   1. `data/docs/<filename>`                — legacy single-upload endpoint
//   2. `data/docs/<nanos>-<filename>`        — single-upload with timestamp prefix
//   3. `data/docs/<case_id>/<filename>`      — folder-intake commit (NEW)
//
// We try (1) and (2) under the docsDir root, then walk every case-id subdir
// looking for an exact filename match. Walk depth is capped to two levels so
// it stays cheap even with hundreds of cases.
func (s *Server) resolveDocDiskPath(name string) (string, bool) {
	base := filepath.Base(name)
	dataDir := s.cfg.DataDir
	if !filepath.IsAbs(dataDir) {
		if abs, err := filepath.Abs(dataDir); err == nil {
			dataDir = abs
		}
	}
	docsDir := filepath.Join(dataDir, "docs")

	// 1. Exact-path candidates at the docsDir root (legacy single upload).
	for _, c := range []string{
		filepath.Join(docsDir, base),
		filepath.Join(docsDir, name),
	} {
		if info, err := os.Stat(filepath.Clean(c)); err == nil && !info.IsDir() {
			return filepath.Clean(c), true
		}
	}

	// 2. Read the docs dir once; we need both the legacy "<nanos>-<base>"
	//    sweep AND the new "case_id/" subdir sweep.
	entries, err := os.ReadDir(docsDir)
	if err != nil {
		return "", false
	}

	// 2a. Legacy convention: pick the newest "<nanos>-<base>" at the root.
	suffix := "-" + base
	var best string
	var bestPrefix int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasSuffix(n, suffix) {
			continue
		}
		prefixStr := strings.TrimSuffix(n, suffix)
		p, err := strconv.ParseInt(prefixStr, 10, 64)
		if err != nil {
			continue
		}
		if p >= bestPrefix {
			bestPrefix = p
			best = n
		}
	}
	if best != "" {
		return filepath.Join(docsDir, best), true
	}

	// 2b. Case subdirectory sweep — folder-intake stores under data/docs/<case_id>/.
	//     We look for an exact filename match in any case subdir.
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		candidate := filepath.Join(docsDir, e.Name(), base)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
	}

	return "", false
}

// caseDocumentMeta is the per-file payload returned from
// GET /v1/cases/{id}/documents/list.
type caseDocumentMeta struct {
	Filename   string `json:"filename"`
	Doctype    string `json:"doctype"`
	OCRIndexed bool   `json:"ocr_indexed"`
	RAGIndexed bool   `json:"rag_indexed"`
	SizeBytes  int64  `json:"size_bytes"`
	ModifiedAt string `json:"modified_at"`
}

// handleListCaseDocuments returns the file-attachment metadata table for a case.
// Failures stat'ing an individual file are non-fatal — the entry is still
// returned with size_bytes=0 and an empty modified_at so the UI can flag the
// missing file rather than failing the whole listing.
func (s *Server) handleListCaseDocuments(w http.ResponseWriter, r *http.Request, caseID string) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	c, ok := s.workflow.GetCaseSnapshot(caseID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "Case not found")
		return
	}

	doctypes := make(map[string]string, len(c.AIFileSummaries))
	for _, row := range c.AIFileSummaries {
		fn, _ := row["filename"].(string)
		if fn == "" {
			continue
		}
		dt, _ := row["doctype"].(string)
		doctypes[fn] = dt
	}

	out := make([]caseDocumentMeta, 0, len(c.UploadedDocuments))
	for _, filename := range c.UploadedDocuments {
		entry := caseDocumentMeta{
			Filename: filename,
			Doctype:  doctypes[filename],
		}

		if path, found := s.resolveDocDiskPath(filename); found {
			if info, err := os.Stat(path); err == nil {
				entry.SizeBytes = info.Size()
				entry.ModifiedAt = info.ModTime().UTC().Format(time.RFC3339)
			}
			if s.ocrCache != nil {
				if _, hit := s.ocrCache.Get(path); hit {
					entry.OCRIndexed = true
				}
			}
		}

		if doc, ok := s.rag.GetDocumentFlex(filename); ok && len(doc.Chunks) > 0 {
			entry.RAGIndexed = true
		}

		out = append(out, entry)
	}

	s.writeJSON(w, http.StatusOK, out)
}
