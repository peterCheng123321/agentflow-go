package server

import (
	"io"
	"log"
	"os"
	"sync"
	"time"

	agentflow_io "agentflow-go/internal/io"
)

// Apple-optimized upload buffer sizes
var (
	// Use Apple M-series optimal buffer sizes
	uploadBufferSize = agentflow_io.ParallelChunkSize()
)

// uploadBuffer is a reusable buffer pool for file uploads
var uploadBuffer = sync.Pool{
	New: func() any {
		b := make([]byte, uploadBufferSize) // Use optimal chunk size
		return &b
	},
}

// fastCopy copies from src to dst using Apple-optimized buffers
func fastCopy(dst *os.File, src io.Reader) (int64, error) {
	// Use the Apple-optimized buffer pool
	buf := agentflow_io.PooledBuffer()
	defer agentflow_io.ReleaseBuffer(buf)

	var written int64
	for {
		n, err := io.ReadFull(src, buf)
		if n > 0 {
			nw, werr := dst.Write(buf[:n])
			written += int64(nw)
			if werr != nil {
				return written, werr
			}
		}
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return written, nil
			}
			return written, err
		}
	}
}

// saveFileResult represents the result of a parallel file save operation
type saveFileResult struct {
	path    string
	success bool
	size    int64
}

// saveFilesParallel saves multiple files in parallel for better performance
func saveFilesParallel(destDir string, fileInfos []multipartFile) []string {
	const maxSavers = 8
	sem := make(chan struct{}, maxSavers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var savedPaths []string
	
	for _, fi := range fileInfos {
		wg.Add(1)
		go func(info multipartFile) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			
			outPath := info.destPath
			dst, err := os.Create(outPath)
			if err != nil {
				log.Printf("[Upload] Failed to create %s: %v", info.filename, err)
				return
			}
			defer dst.Close()
			
			if _, err := fastCopy(dst, info.src); err != nil {
				log.Printf("[Upload] Failed to write %s: %v", info.filename, err)
				os.Remove(outPath)
				return
			}
			info.src.Close()
			
			mu.Lock()
			savedPaths = append(savedPaths, outPath)
			mu.Unlock()
		}(fi)
	}
	
	wg.Wait()
	return savedPaths
}

// multipartFile represents a file to be saved
type multipartFile struct {
	src      io.ReadCloser
	filename string
	destPath string
}

// broadcastDebouncer debounces WebSocket broadcasts to avoid flooding clients
type broadcastDebouncer struct {
	mu       sync.Mutex
	timer    *time.Timer
	server   *Server
	interval time.Duration
}

func newBroadcastDebouncer(s *Server, interval time.Duration) *broadcastDebouncer {
	return &broadcastDebouncer{
		server:   s,
		interval: interval,
	}
}

func (b *broadcastDebouncer) trigger() {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	if b.timer != nil {
		b.timer.Stop()
	}
	
	b.timer = time.AfterFunc(b.interval, func() {
		b.server.broadcastStatus()
	})
}

// parallelRAGIngest ingests multiple files in parallel for better performance
func (s *Server) parallelRAGIngest(files []ragFile) {
	if len(files) == 0 {
		return
	}
	
	const maxConcurrency = 8
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	
	for _, f := range files {
		wg.Add(1)
		go func(rf ragFile) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			
			meta := map[string]interface{}{
				"filename": rf.filename,
			}
			if rf.classification != nil {
				meta["classification"] = rf.classification
			}
			
			if err := s.rag.IngestFile(rf.filePath, rf.text, meta); err != nil {
				log.Printf("[RAG] Ingest failed for %s: %v", rf.filename, err)
			}
		}(f)
	}
	
	wg.Wait()
}

type ragFile struct {
	filePath       string
	filename       string
	text           string
	classification map[string]interface{}
}
