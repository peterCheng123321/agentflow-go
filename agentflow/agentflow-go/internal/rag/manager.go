package rag

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"agentflow-go/internal/model"
	"agentflow-go/internal/vec"

	"golang.org/x/text/unicode/norm"
)

type Manager struct {
	mu              sync.RWMutex
	documents       []model.DocumentRecord
	tokenizedCorpus [][]string
	embedder        embedder
	chunkEmbeddings [][]float32
	bm25IDF         map[string]float64
	bm25DocFreq     map[string]int
	bm25AvgDocLen   float64
	bm25K1          float64
	bm25B           float64
	persistDir      string
	searchCache     map[string][]model.SearchResult
	cacheMu         sync.RWMutex
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
	m.chunkEmbeddings = m.loadEmbeddings()
	m.reconcileEmbeddingsLocked()
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

	if m.bm25IDF == nil {
		m.bm25IDF = make(map[string]float64)
	}
	if m.bm25DocFreq == nil {
		m.bm25DocFreq = make(map[string]int)
	}

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

	data, err := json.Marshal(ps)
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
		m.bm25IDF[token] = math.Log((float64(nChunks)-float64(df)+0.5)/(float64(df)+0.5) + 1.0)
	}
}

func (m *Manager) IngestFile(filePath string, text string, metadata map[string]interface{}) error {
	m.mu.Lock()

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
	needBackfill := len(chunks) > 0 && m.embedder != nil

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
		m.chunkEmbeddings = append(m.chunkEmbeddings, nil)

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
			m.bm25IDF[token] = math.Log((float64(nDocs)-float64(df)+0.5)/(float64(df)+0.5) + 1.0)
		}
	}

	// Clear search cache on new ingestion
	m.cacheMu.Lock()
	m.searchCache = make(map[string][]model.SearchResult)
	m.cacheMu.Unlock()

	m.saveStore()
	m.saveEmbeddings()
	m.mu.Unlock()
	if needBackfill {
		go m.backfillEmbeddings()
	}
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
		m.chunkEmbeddings = make([][]float32, len(m.tokenizedCorpus))
		m.saveStore()
		m.saveEmbeddings()

		// Clear cache
		m.cacheMu.Lock()
		m.searchCache = make(map[string][]model.SearchResult)
		m.cacheMu.Unlock()
		needBackfill := m.embedder != nil && len(m.tokenizedCorpus) > 0
		m.mu.Unlock()
		if needBackfill {
			go m.backfillEmbeddings()
		}
		return
	}
	m.mu.Unlock()
}

func (m *Manager) Search(query string, k int) []model.SearchResult {
	m.cacheMu.RLock()
	if cached, ok := m.searchCache[query]; ok && len(cached) >= k {
		m.cacheMu.RUnlock()
		return cached[:k]
	}
	m.cacheMu.RUnlock()

	// Snapshot the embedder under a brief read lock, then do the HTTP
	// embed *without* holding m.mu — otherwise concurrent ingest/delete
	// blocks for the duration of the network call.
	m.mu.RLock()
	emb := m.embedder
	chunksEmbedded := len(m.chunkEmbeddings) > 0 && len(m.chunkEmbeddings) == len(m.tokenizedCorpus)
	m.mu.RUnlock()

	var queryVec []float32
	if emb != nil && chunksEmbedded {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		vecs, err := emb.Embed(ctx, []string{query})
		cancel()
		if err != nil {
			log.Printf("[rag] dense embed failed: %v (falling back to BM25)", err)
		} else if len(vecs) == 1 {
			queryVec = vec.Normalize(vecs[0])
		}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	queryTokens := tokenize(query)
	results := m.scoreAndRank(queryTokens, queryVec, k)

	m.cacheMu.Lock()
	m.searchCache[query] = results
	if len(m.searchCache) > 256 {
		for key := range m.searchCache {
			delete(m.searchCache, key)
			break
		}
	}
	m.cacheMu.Unlock()

	return results
}

// scoreAndRank assumes m.mu is held. queryVec may be nil when dense is
// unavailable; in that case it falls back to BM25-only ranking.
func (m *Manager) scoreAndRank(queryTokens []string, queryVec []float32, k int) []model.SearchResult {
	bm25Scores := m.bm25Scores(queryTokens)
	if len(bm25Scores) == 0 {
		return nil
	}

	var denseScores []float64
	if queryVec != nil && len(m.chunkEmbeddings) == len(m.tokenizedCorpus) {
		denseScores = make([]float64, len(m.chunkEmbeddings))
		for i, v := range m.chunkEmbeddings {
			if len(v) != len(queryVec) {
				denseScores[i] = -1
				continue
			}
			denseScores[i] = vec.Dot(queryVec, v)
		}
	}

	if len(denseScores) == 0 {
		return m.resultsFromScores(bm25Scores, k, "bm25")
	}

	scored := rrfFuse(bm25Scores, denseScores, k)
	if len(scored) > k {
		scored = scored[:k]
	}
	results := make([]model.SearchResult, 0, len(scored))
	for _, sc := range scored {
		docIdx, chunkIdx, ok := m.chunkLocation(sc.globalChunkIdx)
		if !ok {
			continue
		}
		results = append(results, model.SearchResult{
			Filename:  m.documents[docIdx].Filename,
			Chunk:     m.documents[docIdx].Chunks[chunkIdx],
			Score:     math.Round(sc.score*10000) / 10000,
			MatchMode: "hybrid",
		})
	}
	return results
}

func (m *Manager) bm25Scores(queryTokens []string) []float64 {
	if len(m.tokenizedCorpus) == 0 {
		return nil
	}
	scores := make([]float64, len(m.tokenizedCorpus))
	for i := range m.tokenizedCorpus {
		scores[i] = m.bm25Score(queryTokens, i)
	}
	return scores
}

func (m *Manager) resultsFromScores(scores []float64, k int, mode string) []model.SearchResult {
	ranks := topRanks(scores, k)
	results := make([]model.SearchResult, 0, len(ranks))
	for _, globalIdx := range ranks {
		docIdx, chunkIdx, ok := m.chunkLocation(globalIdx)
		if !ok {
			continue
		}
		results = append(results, model.SearchResult{
			Filename:  m.documents[docIdx].Filename,
			Chunk:     m.documents[docIdx].Chunks[chunkIdx],
			Score:     math.Round(scores[globalIdx]*10000) / 10000,
			MatchMode: mode,
		})
	}
	return results
}

func (m *Manager) bm25Search(queryTokens []string, k int) []model.SearchResult {
	if len(m.tokenizedCorpus) == 0 {
		return nil
	}

	type scoredChunk struct {
		docIdx   int
		chunkIdx int
		score    float64
	}

	var scored []scoredChunk

	chunkIdx := 0
	for docIdx, doc := range m.documents {
		for cIdx := range doc.Chunks {
			score := m.bm25Score(queryTokens, chunkIdx)
			if score > 0 {
				scored = append(scored, scoredChunk{
					docIdx:   docIdx,
					chunkIdx: cIdx,
					score:    score,
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
	chunksWithDense := m.chunksWithDenseLocked(totalChunks)
	backendMode := "bm25"
	if m.embedder != nil {
		backendMode = "hybrid"
	}

	return map[string]interface{}{
		"document_count":    len(m.documents),
		"total_chunks":      totalChunks,
		"chunks_with_dense": chunksWithDense,
		"backend_mode":      backendMode,
	}
}

func (m *Manager) chunksWithDenseLocked(totalChunks int) int {
	n := len(m.chunkEmbeddings)
	if n > totalChunks {
		n = totalChunks
	}
	count := 0
	for i := 0; i < n; i++ {
		if len(m.chunkEmbeddings[i]) > 0 {
			count++
		}
	}
	return count
}

func (m *Manager) reconcileEmbeddingsLocked() {
	totalChunks := 0
	for _, doc := range m.documents {
		totalChunks += len(doc.Chunks)
	}
	if totalChunks == 0 {
		m.chunkEmbeddings = nil
		return
	}
	if len(m.chunkEmbeddings) > totalChunks {
		m.chunkEmbeddings = m.chunkEmbeddings[:totalChunks]
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
