// Package embedrouter classifies short user messages into intent labels
// by cosine similarity against pre-embedded reference utterances. This is
// the canonical 2025/26 industry pattern for "skip-tools-on-chitchat"
// routing; see ../../docs/router-research.md for the comparison against
// the LLM-classifier approach in cmd/routereval.
//
// The router is stateless after Init: a fixed set of labeled reference
// utterances is embedded once at startup, queries are embedded on demand,
// and Classify returns the label whose best-matching utterance has the
// highest cosine similarity. A confidence margin (top1 − top2) is also
// returned so callers can fall through to a safer path when uncertain.
package embedrouter

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"agentflow-go/internal/vec"
)

// Embedder is the interface this package needs from any embedding model.
// Implementations live in this package (ollama.go) or can be supplied by
// callers — keeping the interface narrow makes the router runtime-agnostic.
type Embedder interface {
	// Embed returns one vector per input string, in order. All returned
	// vectors must share dimensionality.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Reference is a single labeled utterance + its precomputed embedding.
type Reference struct {
	Label     string
	Utterance string
	Vec       []float32
}

// Router classifies a query by cosine similarity over a fixed pool of
// embedded reference utterances. Concurrency-safe for read-only Classify
// calls after Init returns.
type Router struct {
	embedder Embedder
	refs     []Reference
}

// Result is what Classify returns. Score is the top cosine similarity in
// [-1, 1]; Margin is top1 − top2 across distinct labels and is the value
// callers should threshold for confidence.
type Result struct {
	Label   string
	Score   float64
	Margin  float64
	Best    Reference // the reference utterance that won
	Runner  Reference // best utterance from the runner-up label
	Latency int64     // milliseconds spent embedding the query
}

// New constructs a Router given an Embedder. Init must be called before
// Classify.
func New(e Embedder) *Router {
	return &Router{embedder: e}
}

// Init embeds every utterance in the corpus and stores the vectors. It
// batches the request to the embedder to amortize HTTP overhead.
func (r *Router) Init(ctx context.Context, corpus []LabeledUtterance) error {
	if len(corpus) == 0 {
		return fmt.Errorf("empty corpus")
	}
	texts := make([]string, len(corpus))
	for i, u := range corpus {
		texts[i] = u.Utterance
	}
	vecs, err := r.embedder.Embed(ctx, texts)
	if err != nil {
		return fmt.Errorf("embed corpus: %w", err)
	}
	if len(vecs) != len(corpus) {
		return fmt.Errorf("embedder returned %d vectors for %d utterances", len(vecs), len(corpus))
	}
	dim := len(vecs[0])
	refs := make([]Reference, len(corpus))
	for i, u := range corpus {
		if len(vecs[i]) != dim {
			return fmt.Errorf("vector %d has dim %d, expected %d", i, len(vecs[i]), dim)
		}
		refs[i] = Reference{Label: u.Label, Utterance: u.Utterance, Vec: vec.Normalize(vecs[i])}
	}
	r.refs = refs
	return nil
}

// Classify embeds the query and returns the best matching label plus a
// confidence margin. Returns ("", 0, ...) on embedder error so callers
// can fall through to a safer path.
func (r *Router) Classify(ctx context.Context, query string) (Result, error) {
	if len(r.refs) == 0 {
		return Result{}, fmt.Errorf("router not initialized")
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return Result{}, fmt.Errorf("empty query")
	}
	vecs, err := r.embedder.Embed(ctx, []string{q})
	if err != nil {
		return Result{}, fmt.Errorf("embed query: %w", err)
	}
	if len(vecs) != 1 {
		return Result{}, fmt.Errorf("embedder returned %d vectors for 1 query", len(vecs))
	}
	qv := vec.Normalize(vecs[0])

	// Score every reference. Vectors are L2-normalized so dot product
	// equals cosine similarity.
	type scored struct {
		ref   Reference
		score float64
	}
	all := make([]scored, len(r.refs))
	for i, ref := range r.refs {
		all[i] = scored{ref: ref, score: vec.Dot(qv, ref.Vec)}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].score > all[j].score })

	best := all[0]
	// Margin: top1 minus the best score among references with a *different*
	// label. This mirrors how Aurelio Semantic Router computes confidence.
	var runnerUp scored
	for _, s := range all[1:] {
		if s.ref.Label != best.ref.Label {
			runnerUp = s
			break
		}
	}
	margin := best.score - runnerUp.score

	return Result{
		Label:  best.ref.Label,
		Score:  best.score,
		Margin: margin,
		Best:   best.ref,
		Runner: runnerUp.ref,
	}, nil
}

