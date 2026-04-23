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
// Uploads are stored with a nanosecond timestamp prefix, e.g. "<nanos>-e.txt",
// but the canonical filename tracked in case.uploaded_documents is the original
// logical name ("e.txt"). We try exact matches first (cheap) and then fall back
// to a directory scan matching the "<digits>-<name>" convention, returning the
// newest match. The resolver is robust to cwd changes.
func (s *Server) resolveDocDiskPath(name string) (string, bool) {
	base := filepath.Base(name)
	dataDir := s.cfg.DataDir
	if !filepath.IsAbs(dataDir) {
		if abs, err := filepath.Abs(dataDir); err == nil {
			dataDir = abs
		}
	}
	docsDir := filepath.Join(dataDir, "docs")

	// Exact-path candidates first.
	for _, c := range []string{
		filepath.Join(docsDir, base),
		filepath.Join(docsDir, name),
	} {
		if info, err := os.Stat(filepath.Clean(c)); err == nil && !info.IsDir() {
			return filepath.Clean(c), true
		}
	}

	// Fallback: match the "<nanos>-<base>" upload convention. Pick newest.
	entries, err := os.ReadDir(docsDir)
	if err != nil {
		return "", false
	}
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
	return "", false
}
