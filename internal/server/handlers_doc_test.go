package server

import (
	"os"
	"path/filepath"
	"testing"

	"agentflow-go/internal/config"
	"agentflow-go/internal/model"
)

func TestResolveDocDiskPath(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	type setup struct {
		// relDocsFiles are filenames created under <DataDir>/docs/.
		relDocsFiles []string
		// absFiles are files created at an absolute path (outside DataDir).
		absFiles []string
	}

	tests := []struct {
		name     string
		setup    setup
		// docPath builds the DocumentRecord.Path from the populated dirs.
		// dataDir is an absolute path to the test's DataDir.
		// absFilePaths maps each absFiles entry name to its absolute path.
		docPath  func(dataDir string, absFilePaths map[string]string) string
		// chdirTo, if non-empty, is a subdir name to create and chdir into
		// before calling resolveDocDiskPath (simulates cwd != DataDir).
		chdirTo  string
		wantOK   bool
		// wantResolved, if non-empty, is the absolute path we expect returned.
		wantResolved func(dataDir string, absFilePaths map[string]string) string
	}{
		{
			name: "absolute path inside DataDir/docs",
			setup: setup{
				relDocsFiles: []string{"report.pdf"},
			},
			docPath: func(dataDir string, _ map[string]string) string {
				return filepath.Join(dataDir, "docs", "report.pdf")
			},
			wantOK: true,
			wantResolved: func(dataDir string, _ map[string]string) string {
				return filepath.Join(dataDir, "docs", "report.pdf")
			},
		},
		{
			name: "relative path resolved via DataDir/docs basename",
			setup: setup{
				relDocsFiles: []string{"note.txt"},
			},
			docPath: func(_ string, _ map[string]string) string {
				return "note.txt"
			},
			wantOK: true,
			wantResolved: func(dataDir string, _ map[string]string) string {
				return filepath.Join(dataDir, "docs", "note.txt")
			},
		},
		{
			name: "caller cwd differs from DataDir",
			setup: setup{
				relDocsFiles: []string{"brief.pdf"},
			},
			docPath: func(_ string, _ map[string]string) string {
				return "brief.pdf"
			},
			chdirTo: "elsewhere",
			wantOK:  true,
			wantResolved: func(dataDir string, _ map[string]string) string {
				return filepath.Join(dataDir, "docs", "brief.pdf")
			},
		},
		{
			name: "unicode filename",
			setup: setup{
				relDocsFiles: []string{"契约-β.pdf"},
			},
			docPath: func(dataDir string, _ map[string]string) string {
				return filepath.Join(dataDir, "docs", "契约-β.pdf")
			},
			wantOK: true,
			wantResolved: func(dataDir string, _ map[string]string) string {
				return filepath.Join(dataDir, "docs", "契约-β.pdf")
			},
		},
		{
			name: "filename with spaces",
			setup: setup{
				relDocsFiles: []string{"my case file.pdf"},
			},
			docPath: func(dataDir string, _ map[string]string) string {
				return filepath.Join(dataDir, "docs", "my case file.pdf")
			},
			wantOK: true,
			wantResolved: func(dataDir string, _ map[string]string) string {
				return filepath.Join(dataDir, "docs", "my case file.pdf")
			},
		},
		{
			name:  "missing file returns ok=false and echoes doc.Path",
			setup: setup{},
			docPath: func(_ string, _ map[string]string) string {
				return "/nonexistent/does-not-exist.pdf"
			},
			wantOK: false,
			wantResolved: func(_ string, _ map[string]string) string {
				return "/nonexistent/does-not-exist.pdf"
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			docsDir := filepath.Join(dataDir, "docs")
			if err := os.MkdirAll(docsDir, 0o755); err != nil {
				t.Fatalf("mkdir docs: %v", err)
			}
			for _, name := range tc.setup.relDocsFiles {
				p := filepath.Join(docsDir, name)
				if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
					t.Fatalf("write %s: %v", p, err)
				}
			}
			absPaths := map[string]string{}
			for _, name := range tc.setup.absFiles {
				p := filepath.Join(t.TempDir(), name)
				if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
					t.Fatalf("write %s: %v", p, err)
				}
				absPaths[name] = p
			}

			// Always chdir to a clean, empty dir so bare-relative candidates
			// (e.g. "note.txt") can't accidentally resolve against an unrelated
			// file under the process's original cwd.
			cwdDir := filepath.Join(t.TempDir(), "cwd")
			if tc.chdirTo != "" {
				cwdDir = filepath.Join(cwdDir, tc.chdirTo)
			}
			if err := os.MkdirAll(cwdDir, 0o755); err != nil {
				t.Fatalf("mkdir cwd: %v", err)
			}
			if err := os.Chdir(cwdDir); err != nil {
				t.Fatalf("chdir: %v", err)
			}

			s := &Server{cfg: &config.Config{DataDir: dataDir}}
			doc := model.DocumentRecord{
				Filename: filepath.Base(tc.docPath(dataDir, absPaths)),
				Path:     tc.docPath(dataDir, absPaths),
			}

			got, ok := s.resolveDocDiskPath(doc)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (got path %q)", ok, tc.wantOK, got)
			}
			want := tc.wantResolved(dataDir, absPaths)
			if got != want {
				t.Errorf("resolved path\n  got:  %q\n  want: %q", got, want)
			}
		})
	}
}
