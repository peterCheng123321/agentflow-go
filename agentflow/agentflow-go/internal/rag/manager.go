package rag

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"agentflow-go/internal/model"

	"golang.org/x/text/unicode/norm"
)

type Manager struct {
	mu               sync.RWMutex
	documents        []model.DocumentRecord
	tokenizedCorpus  [][]string
	bm25IDF          map[string]float64
	bm25DocFreq      map[string]int
	bm25AvgDocLen    float64
	bm25K1           float64
	bm25B            float64
	persistDir       string
	searchCache      map[string][]model.SearchResult
	cacheMu          sync.RWMutex
}

type persistedStore struct {
	Documents     []model.DocumentRecord `json:"documents"`
	BM25IDF       map[string]float64     `json:"bm25_idf"`
	BM25DocFreq   map[string]int         `json:"bm25_doc_freq"`
	BM25AvgDocLen float64                `json:"bm25_avg_doc_len"`
}

func NewManager(persistDir string) *Manager {
	m := &Manager{
		persistDir:  persistDir,
		bm25K1:      1.5,
		bm25B:       0.75,
		bm25IDF:     make(map[string]float64),
		bm25DocFreq: make(map[string]int),
		searchCache: make(map[string][]model.SearchResult),
	}
	m.loadStore()
	return m
}

func (m *Manager) loadStore() {
	storePath := filepath.Join(m.persistDir, "rag_store.json")
	data, err := os.ReadFile(storePath)
	if err != nil {
		return
	}
	
	var ps persistedStore
	if err := json.Unmarshal(data, &ps); err != nil {
		return
	}
	
	m.documents = ps.Documents
	m.bm25IDF = ps.BM25IDF
	m.bm25DocFreq = ps.BM25DocFreq
	m.bm25AvgDocLen = ps.BM25AvgDocLen

	if m.bm25IDF == nil { m.bm25IDF = make(map[string]float64) }
	if m.bm25DocFreq == nil { m.bm25DocFreq = make(map[string]int) }

	if len(m.bm25IDF) == 0 && len(m.documents) > 0 {
		m.rebuildIndex()
	} else {
		// Just fill tokenizedCorpus for current session search
		for _, doc := range m.documents {
			for _, chunk := range doc.Chunks {
				m.tokenizedCorpus = append(m.tokenizedCorpus, tokenize(chunk))
			}
		}
	}
}

func (m *Manager) saveStore() {
	os.MkdirAll(m.persistDir, 0755)
	storePath := filepath.Join(m.persistDir, "rag_store.json")
	
	ps := persistedStore{
		Documents:     m.documents,
		BM25IDF:       m.bm25IDF,
		BM25DocFreq:   m.bm25DocFreq,
		BM25AvgDocLen: m.bm25AvgDocLen,
	}
	
	data, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return
	}

	tmpPath := storePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return
	}
	if err := os.Rename(tmpPath, storePath); err != nil {
		os.Remove(tmpPath)
	}
}

func (m *Manager) rebuildIndex() {
	m.tokenizedCorpus = nil
	m.bm25DocFreq = make(map[string]int)
	
	totalChunks := 0
	for _, doc := range m.documents {
		totalChunks += len(doc.Chunks)
	}

	if totalChunks == 0 {
		return
	}
	
	totalDocLen := 0
	for _, doc := range m.documents {
		for _, chunk := range doc.Chunks {
			tokens := tokenize(chunk)
			m.tokenizedCorpus = append(m.tokenizedCorpus, tokens)
			totalDocLen += len(tokens)
			
			seen := make(map[string]bool)
			for _, token := range tokens {
				if !seen[token] {
					m.bm25DocFreq[token]++
					seen[token] = true
				}
			}
		}
	}
	
	nChunks := len(m.tokenizedCorpus)
	m.bm25AvgDocLen = float64(totalDocLen) / float64(nChunks)
	
	// Calculate IDF based on number of chunks as 'documents'
	for token, df := range m.bm25DocFreq {
		m.bm25IDF[token] = math.Log((float64(nChunks) - float64(df) + 0.5) / (float64(df) + 0.5) + 1.0)
	}
}

func (m *Manager) IngestFile(filePath string, text string, metadata map[string]interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	filename := filepath.Base(filePath)
	if metadata != nil {
		if v, ok := metadata["filename"].(string); ok {
			v = filepath.Base(v)
			if v != "" {
				filename = v
			}
		}
	}
	filename = NormalizeLogicalName(filename)
	
	// Chunk the text
	chunks := chunkText(text, 512)
	
	doc := model.DocumentRecord{
		Filename:      filename,
		Path:          filePath,
		FileType:      detectFileType(filePath),
		FileSizeBytes: getFileSize(filePath),
		Chunks:        chunks,
		AIMetadata:    metadata,
		IngestedAt:    time.Now(),
	}
	
	m.documents = append(m.documents, doc)
	
	// Incremental Index Update
	for _, chunk := range chunks {
		tokens := tokenize(chunk)
		m.tokenizedCorpus = append(m.tokenizedCorpus, tokens)
		
		seen := make(map[string]bool)
		for _, token := range tokens {
			if !seen[token] {
				m.bm25DocFreq[token]++
				seen[token] = true
			}
		}
	}
	
	// Re-calculate averages and IDF
	nDocs := len(m.tokenizedCorpus)
	if nDocs > 0 {
		totalLen := 0
		for _, tokens := range m.tokenizedCorpus {
			totalLen += len(tokens)
		}
		m.bm25AvgDocLen = float64(totalLen) / float64(nDocs)
		
		// Update IDF for all tokens (could be more incremental but this is O(UniqueTokens) which is fine)
		for token, df := range m.bm25DocFreq {
			m.bm25IDF[token] = math.Log((float64(nDocs) - float64(df) + 0.5) / (float64(df) + 0.5) + 1.0)
		}
	}
	
	// Clear search cache on new ingestion
	m.cacheMu.Lock()
	m.searchCache = make(map[string][]model.SearchResult)
	m.cacheMu.Unlock()
	
	m.saveStore()
	return nil
}

// NormalizeLogicalName returns NFC-normalized basename so RAG keys match case attachments and URLs across macOS/Windows.
func NormalizeLogicalName(s string) string {
	if s == "" {
		return ""
	}
	return norm.NFC.String(filepath.Base(s))
}

func (m *Manager) DeleteDocument(filename string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	want := NormalizeLogicalName(filename)
	newDocs := make([]model.DocumentRecord, 0, len(m.documents))
	for _, doc := range m.documents {
		if NormalizeLogicalName(doc.Filename) != want {
			newDocs = append(newDocs, doc)
		}
	}

	if len(newDocs) != len(m.documents) {
		m.documents = newDocs
		m.rebuildIndex()
		m.saveStore()
		
		// Clear cache
		m.cacheMu.Lock()
		m.searchCache = make(map[string][]model.SearchResult)
		m.cacheMu.Unlock()
	}
}

func (m *Manager) Search(query string, k int) []model.SearchResult {
	m.cacheMu.RLock()
	if cached, ok := m.searchCache[query]; ok && len(cached) >= k {
		m.cacheMu.RUnlock()
		return cached[:k]
	}
	m.cacheMu.RUnlock()
	
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	queryTokens := tokenize(query)
	results := m.bm25Search(queryTokens, k)
	
	// Cache results
	m.cacheMu.Lock()
	m.searchCache[query] = results
	if len(m.searchCache) > 256 {
		// Evict oldest
		for key := range m.searchCache {
			delete(m.searchCache, key)
			break
		}
	}
	m.cacheMu.Unlock()
	
	return results
}

func (m *Manager) bm25Search(queryTokens []string, k int) []model.SearchResult {
	if len(m.tokenizedCorpus) == 0 {
		return nil
	}
	
	type scoredChunk struct {
		docIdx  int
		chunkIdx int
		score   float64
	}
	
	var scored []scoredChunk
	
	chunkIdx := 0
	for docIdx, doc := range m.documents {
		for cIdx := range doc.Chunks {
			score := m.bm25Score(queryTokens, chunkIdx)
			if score > 0 {
				scored = append(scored, scoredChunk{
					docIdx:  docIdx,
					chunkIdx: cIdx,
					score:   score,
				})
			}
			chunkIdx++
		}
	}
	
	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})
	
	// Take top k
	if len(scored) > k {
		scored = scored[:k]
	}
	
	results := make([]model.SearchResult, len(scored))
	for i, sc := range scored {
		results[i] = model.SearchResult{
			Filename:  m.documents[sc.docIdx].Filename,
			Chunk:     m.documents[sc.docIdx].Chunks[sc.chunkIdx],
			Score:     math.Round(sc.score*10000) / 10000,
			MatchMode: "bm25",
		}
	}
	
	return results
}

func (m *Manager) bm25Score(queryTokens []string, docIdx int) float64 {
	if docIdx >= len(m.tokenizedCorpus) {
		return 0
	}
	
	docTokens := m.tokenizedCorpus[docIdx]
	docLen := float64(len(docTokens))
	
	// Count term frequencies in document
	tf := make(map[string]int)
	for _, token := range docTokens {
		tf[token]++
	}
	
	score := 0.0
	for _, queryToken := range queryTokens {
		idf, ok := m.bm25IDF[queryToken]
		if !ok {
			continue
		}
		
		tfVal := float64(tf[queryToken])
		numerator := tfVal * (m.bm25K1 + 1)
		denominator := tfVal + m.bm25K1*(1-m.bm25B+m.bm25B*docLen/m.bm25AvgDocLen)
		
		score += idf * numerator / denominator
	}
	
	return score
}

func (m *Manager) GetDocument(filename string) (model.DocumentRecord, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	want := NormalizeLogicalName(filename)
	for i := range m.documents {
		if NormalizeLogicalName(m.documents[i].Filename) == want {
			d := m.documents[i]
			d.Chunks = append([]string(nil), d.Chunks...)
			return d, true
		}
	}
	return model.DocumentRecord{}, false
}

func copyDocumentRecord(d *model.DocumentRecord) model.DocumentRecord {
	out := *d
	out.Chunks = append([]string(nil), d.Chunks...)
	return out
}

// GetDocumentFlex matches stored docs when path encoding, basename, or NFC form differs from the UI string.
func (m *Manager) GetDocumentFlex(name string) (model.DocumentRecord, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return model.DocumentRecord{}, false
	}
	wantN := NormalizeLogicalName(name)
	baseWant := filepath.Base(name)

	m.mu.RLock()
	defer m.mu.RUnlock()

	for i := range m.documents {
		fn := m.documents[i].Filename
		if NormalizeLogicalName(fn) == wantN {
			return copyDocumentRecord(&m.documents[i]), true
		}
		if fn == name || strings.EqualFold(fn, name) {
			return copyDocumentRecord(&m.documents[i]), true
		}
		if filepath.Base(fn) == baseWant {
			return copyDocumentRecord(&m.documents[i]), true
		}
	}
	return model.DocumentRecord{}, false
}

func (m *Manager) GetSummary() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	totalChunks := 0
	for _, doc := range m.documents {
		totalChunks += len(doc.Chunks)
	}
	
	return map[string]interface{}{
		"document_count": len(m.documents),
		"total_chunks":   totalChunks,
		"backend_mode":   "bm25",
	}
}

// Helper functions
func tokenize(text string) []string {
	var tokens []string
	var current strings.Builder
	
	for _, r := range text {
		// Chinese characters: treat each as a separate token (unigram)
		if r >= 0x4e00 && r <= 0x9fff {
			// If we were building an alphanumeric token, flush it
			if current.Len() > 0 {
				tokens = append(tokens, strings.ToLower(current.String()))
				current.Reset()
			}
			tokens = append(tokens, string(r))
			continue
		}

		// Alphanumeric: group together
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			current.WriteRune(r)
		} else {
			if current.Len() > 0 {
				tokens = append(tokens, strings.ToLower(current.String()))
				current.Reset()
			}
		}
	}
	
	if current.Len() > 0 {
		tokens = append(tokens, strings.ToLower(current.String()))
	}
	
	return tokens
}

func chunkText(text string, maxChunkSize int) []string {
	if len(text) == 0 {
		return nil
	}
	
	var chunks []string
	for len(text) > maxChunkSize {
		// Find a good split point (sentence boundary)
		splitIdx := maxChunkSize
		for i := maxChunkSize; i > maxChunkSize/2; i-- {
			if i < len(text) && (text[i] == '.' || text[i] == ';' || text[i] == '\n') {
				splitIdx = i + 1
				break
			}
		}
		chunks = append(chunks, text[:splitIdx])
		text = text[splitIdx:]
	}
	
	if len(text) > 0 {
		chunks = append(chunks, text)
	}
	
	return chunks
}

func detectFileType(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".pdf":
		return "pdf"
	case ".docx":
		return "docx"
	case ".txt", ".md":
		return "txt"
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".tiff":
		return "image"
	default:
		return "unknown"
	}
}

func getFileSize(filePath string) int64 {
	info, err := os.Stat(filePath)
	if err != nil {
		return 0
	}
	return info.Size()
}
