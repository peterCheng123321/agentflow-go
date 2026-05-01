package rag

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"agentflow-go/internal/model"
	"agentflow-go/internal/vec"
)

// embedder is the minimal interface this package needs from the global
// embedrouter — kept narrow so we don't import that package and create a
// cycle if it ever needs RAG. Concrete impl lives in embedrouter.OllamaEmbedder.
type embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// SetEmbedder wires an external embedder into the manager. Must be called
// before ingest/search if dense retrieval is desired. nil disables dense.
// Safe to call once at server boot; not thread-safe with concurrent ingest.
func (m *Manager) SetEmbedder(e embedder) {
	m.mu.Lock()
	m.embedder = e
	// If we have chunks but no embeddings yet, kick off a background
	// migration. Idempotent: re-running fills in only the gaps.
	needMigration := false
	totalChunks := 0
	for _, doc := range m.documents {
		totalChunks += len(doc.Chunks)
	}
	if totalChunks > 0 && (len(m.chunkEmbeddings) != totalChunks || m.chunksWithDenseLocked(totalChunks) < totalChunks) {
		needMigration = true
	}
	m.mu.Unlock()
	m.cacheMu.Lock()
	m.searchCache = make(map[string][]model.SearchResult)
	m.cacheMu.Unlock()
	if needMigration && e != nil {
		go m.backfillEmbeddings()
	}
}

// rrfFuse combines a BM25 ranking and a dense ranking via Reciprocal Rank
// Fusion (k=60, the canonical constant from the original RRF paper). RRF
// is robust to score-scale mismatch — no need to normalize BM25 vs cosine.
//
// bm25Scores and denseScores are parallel arrays indexed by chunk number;
// either may be nil to fall back to a single-retriever ranking.
func rrfFuse(bm25Scores, denseScores []float64, k int) []scoredChunk {
	const rrfK = 60.0
	scoreMap := make(map[int]float64)

	if len(bm25Scores) > 0 {
		ranks := topRanks(bm25Scores, k)
		for rank, idx := range ranks {
			scoreMap[idx] += 1.0 / (rrfK + float64(rank+1))
		}
	}
	if len(denseScores) > 0 {
		ranks := topRanks(denseScores, k)
		for rank, idx := range ranks {
			scoreMap[idx] += 1.0 / (rrfK + float64(rank+1))
		}
	}

	out := make([]scoredChunk, 0, len(scoreMap))
	for idx, s := range scoreMap {
		out = append(out, scoredChunk{globalChunkIdx: idx, score: s})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].score > out[j].score })
	return out
}

// topRanks returns the chunk indices sorted by score descending, capped at
// limit. Ignores zero / negative-sentinel scores so they don't fill ranks.
func topRanks(scores []float64, limit int) []int {
	type sc struct {
		idx   int
		score float64
	}
	indexed := make([]sc, 0, len(scores))
	for i, s := range scores {
		if s > 0 {
			indexed = append(indexed, sc{idx: i, score: s})
		}
	}
	sort.Slice(indexed, func(i, j int) bool { return indexed[i].score > indexed[j].score })
	if len(indexed) > limit {
		indexed = indexed[:limit]
	}
	out := make([]int, len(indexed))
	for i, x := range indexed {
		out[i] = x.idx
	}
	return out
}

// scoredChunk is the dense+RRF cousin of the bm25Search struct above.
// We carry a flat global chunk index because RRF mixes both rankings on
// the same index space.
type scoredChunk struct {
	globalChunkIdx int
	score          float64
}

// chunkLocation maps a flat chunk index back to (docIdx, chunkIdxWithinDoc).
func (m *Manager) chunkLocation(globalIdx int) (int, int, bool) {
	cur := 0
	for di, doc := range m.documents {
		if globalIdx < cur+len(doc.Chunks) {
			return di, globalIdx - cur, true
		}
		cur += len(doc.Chunks)
	}
	return 0, 0, false
}

// embedChunksBatched embeds a slice of strings in chunks of `batch` so we
// don't blow up the request body on large ingests. Returns one vector per
// input text. On any error returns the partial result so far, allowing
// callers to fall back gracefully.
func (m *Manager) embedChunksBatched(ctx context.Context, texts []string) ([][]float32, error) {
	if m.embedder == nil {
		return nil, fmt.Errorf("no embedder configured")
	}
	const batch = 32
	out := make([][]float32, 0, len(texts))
	for i := 0; i < len(texts); i += batch {
		end := i + batch
		if end > len(texts) {
			end = len(texts)
		}
		vecs, err := m.embedder.Embed(ctx, texts[i:end])
		if err != nil {
			return out, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// backfillEmbeddings runs in a goroutine after SetEmbedder if the on-disk
// chunk count doesn't match the embedding count. Refills missing slots
// without holding the manager lock during the slow HTTP calls.
func (m *Manager) backfillEmbeddings() {
	// Snapshot under read lock — we don't want to hold the lock while
	// embedding (could take seconds for thousands of chunks).
	m.mu.RLock()
	type chunkRef struct {
		globalIdx int
		text      string
	}
	var pending []chunkRef
	cur := 0
	for _, doc := range m.documents {
		for _, ch := range doc.Chunks {
			if cur >= len(m.chunkEmbeddings) || len(m.chunkEmbeddings[cur]) == 0 {
				pending = append(pending, chunkRef{globalIdx: cur, text: ch})
			}
			cur++
		}
	}
	totalChunks := cur
	m.mu.RUnlock()

	if len(pending) == 0 {
		return
	}
	log.Printf("[rag] backfilling %d embeddings (of %d total chunks)", len(pending), totalChunks)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	texts := make([]string, len(pending))
	for i, p := range pending {
		texts[i] = p.text
	}
	vecs, err := m.embedChunksBatched(ctx, texts)
	if err != nil {
		log.Printf("[rag] backfill embed failed after %d/%d: %v", len(vecs), len(pending), err)
	}

	// Merge under write lock. Be defensive: docs/chunks may have changed
	// during the long embed call, so re-resolve indices by content match.
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.chunkEmbeddings) != totalChunks {
		// Pad/trim so global indices line up. New ingests during backfill
		// either appended (longer) or stale files carried extra rows — align.
		for len(m.chunkEmbeddings) < totalChunks {
			m.chunkEmbeddings = append(m.chunkEmbeddings, nil)
		}
		if len(m.chunkEmbeddings) > totalChunks {
			m.chunkEmbeddings = m.chunkEmbeddings[:totalChunks]
		}
	}
	filled := 0
	for i, p := range pending {
		if i >= len(vecs) {
			break
		}
		if p.globalIdx < len(m.chunkEmbeddings) && len(m.chunkEmbeddings[p.globalIdx]) == 0 {
			m.chunkEmbeddings[p.globalIdx] = vec.Normalize(vecs[i])
			filled++
		}
	}
	log.Printf("[rag] backfill complete: %d new embeddings (total %d)", filled, len(m.chunkEmbeddings))
	m.saveEmbeddings()
	m.cacheMu.Lock()
	m.searchCache = make(map[string][]model.SearchResult)
	m.cacheMu.Unlock()
}

// ---------------- persistence ----------------

const (
	embedFileMagic   = uint32(0xA6E1B3D6) // "RAGE" → AgentFlow rag embed
	embedFileVersion = uint32(1)
)

// embeddingsPath returns the file path for the binary embeddings cache.
func (m *Manager) embeddingsPath() string {
	return filepath.Join(m.persistDir, "rag_embeddings.bin")
}

// loadEmbeddings reads the binary embeddings cache. Missing file or any
// corruption returns nil — caller should treat that as "no dense index"
// and rely on BM25 (with optional async backfill).
func (m *Manager) loadEmbeddings() [][]float32 {
	f, err := os.Open(m.embeddingsPath())
	if err != nil {
		return nil
	}
	defer f.Close()

	var hdr [16]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		log.Printf("[rag] embeddings header read: %v", err)
		return nil
	}
	magic := binary.LittleEndian.Uint32(hdr[0:4])
	version := binary.LittleEndian.Uint32(hdr[4:8])
	nChunks := binary.LittleEndian.Uint32(hdr[8:12])
	dim := binary.LittleEndian.Uint32(hdr[12:16])
	if magic != embedFileMagic || version != embedFileVersion {
		log.Printf("[rag] embeddings header mismatch (magic=%x ver=%d) — discarding", magic, version)
		return nil
	}
	out := make([][]float32, nChunks)
	for i := uint32(0); i < nChunks; i++ {
		// Each chunk row: 4-byte length (0 = missing), then dim float32 LE.
		var lenBuf [4]byte
		if _, err := io.ReadFull(f, lenBuf[:]); err != nil {
			log.Printf("[rag] embeddings row %d header: %v", i, err)
			return nil
		}
		rowLen := binary.LittleEndian.Uint32(lenBuf[:])
		if rowLen == 0 {
			continue
		}
		if rowLen != dim {
			log.Printf("[rag] embeddings row %d dim mismatch %d != %d", i, rowLen, dim)
			return nil
		}
		vec := make([]float32, dim)
		if err := binary.Read(f, binary.LittleEndian, vec); err != nil {
			log.Printf("[rag] embeddings row %d data: %v", i, err)
			return nil
		}
		out[i] = vec
	}
	return out
}

// saveEmbeddings persists chunkEmbeddings to disk atomically. Caller must
// hold m.mu (read or write — we only read m.chunkEmbeddings).
func (m *Manager) saveEmbeddings() {
	if len(m.chunkEmbeddings) == 0 {
		// Nothing to save — remove any stale file.
		_ = os.Remove(m.embeddingsPath())
		return
	}
	dim := 0
	for _, v := range m.chunkEmbeddings {
		if len(v) > 0 {
			dim = len(v)
			break
		}
	}
	if dim == 0 {
		_ = os.Remove(m.embeddingsPath())
		return
	}
	tmpPath := m.embeddingsPath() + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		log.Printf("[rag] saveEmbeddings create: %v", err)
		return
	}
	defer func() {
		f.Close()
		if _, err := os.Stat(tmpPath); err == nil {
			_ = os.Remove(tmpPath)
		}
	}()

	var hdr [16]byte
	binary.LittleEndian.PutUint32(hdr[0:4], embedFileMagic)
	binary.LittleEndian.PutUint32(hdr[4:8], embedFileVersion)
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(len(m.chunkEmbeddings)))
	binary.LittleEndian.PutUint32(hdr[12:16], uint32(dim))
	if _, err := f.Write(hdr[:]); err != nil {
		log.Printf("[rag] saveEmbeddings header: %v", err)
		return
	}
	for _, v := range m.chunkEmbeddings {
		var lenBuf [4]byte
		if len(v) == int(dim) {
			binary.LittleEndian.PutUint32(lenBuf[:], uint32(dim))
			if _, err := f.Write(lenBuf[:]); err != nil {
				return
			}
			if err := binary.Write(f, binary.LittleEndian, v); err != nil {
				return
			}
		} else {
			// Mark row missing — keeps global index alignment intact.
			binary.LittleEndian.PutUint32(lenBuf[:], 0)
			if _, err := f.Write(lenBuf[:]); err != nil {
				return
			}
		}
	}
	if err := f.Close(); err != nil {
		log.Printf("[rag] saveEmbeddings close: %v", err)
		return
	}
	if err := os.Rename(tmpPath, m.embeddingsPath()); err != nil {
		log.Printf("[rag] saveEmbeddings rename: %v", err)
	}
}

