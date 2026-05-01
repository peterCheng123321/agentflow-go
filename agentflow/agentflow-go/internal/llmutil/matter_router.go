package llmutil

import (
	"context"
	"log"
	"sync"
	"time"

	"agentflow-go/internal/embedrouter"
)

// MatterCorpus is the labeled-exemplar set the embedding router uses to
// classify intake filenames into one of AllowedMatterTypes. Each entry is
// a short Chinese phrase that *exemplifies* a matter type — the embedder
// matches new filenames against these by cosine similarity. Adding a new
// example here is the canonical way to teach the router about a pattern
// production logs surface as misrouted.
var MatterCorpus = []embedrouter.LabeledUtterance{
	{Label: "Civil Litigation", Utterance: "民事起诉状"},
	{Label: "Civil Litigation", Utterance: "起诉状"},
	{Label: "Civil Litigation", Utterance: "民事诉讼"},
	{Label: "Civil Litigation", Utterance: "起诉书"},

	{Label: "Contract Dispute", Utterance: "合同纠纷"},
	{Label: "Contract Dispute", Utterance: "合同履行争议"},
	{Label: "Contract Dispute", Utterance: "合同违约"},

	{Label: "Sales Contract Dispute", Utterance: "买卖合同纠纷"},
	{Label: "Sales Contract Dispute", Utterance: "货物销售合同"},
	{Label: "Sales Contract Dispute", Utterance: "购销合同争议"},

	{Label: "Debt Dispute", Utterance: "欠款追讨"},
	{Label: "Debt Dispute", Utterance: "债务清偿纠纷"},
	{Label: "Debt Dispute", Utterance: "欠款纠纷"},

	{Label: "Loan Dispute", Utterance: "借款合同纠纷"},
	{Label: "Loan Dispute", Utterance: "民间借贷"},
	{Label: "Loan Dispute", Utterance: "借款争议"},

	{Label: "Lease Dispute", Utterance: "房屋租赁合同纠纷"},
	{Label: "Lease Dispute", Utterance: "租金欠付"},
	{Label: "Lease Dispute", Utterance: "住宅租赁争议"},

	{Label: "Commercial Lease Dispute", Utterance: "商铺租赁合同"},
	{Label: "Commercial Lease Dispute", Utterance: "商业租赁纠纷"},
	{Label: "Commercial Lease Dispute", Utterance: "写字楼租赁争议"},

	{Label: "Labor Dispute", Utterance: "劳动合同纠纷"},
	{Label: "Labor Dispute", Utterance: "工资欠付"},
	{Label: "Labor Dispute", Utterance: "劳务争议"},
	{Label: "Labor Dispute", Utterance: "解除劳动关系"},
}

// MatterMargin is the cosine top1 − top2 floor below which the router's
// guess is treated as "uncertain" — we fall through to the keyword hints.
//
// Calibrated empirically: legal matter types overlap semantically much
// more than chat-intent buckets, so margins are tight. Observed correct
// margins on a probe set: 0.015–0.060. 0.01 keeps almost all the right
// answers and only trips on truly out-of-domain inputs (e.g. an image
// filename) where keyword fallback's default (Civil Litigation) is just
// as good a guess as any.
var MatterMargin = 0.01

var (
	matterRouter   *embedrouter.Router
	matterRouterMu sync.RWMutex
	matterReady    bool
	matterReadyMu  sync.Mutex
)

// SetMatterRouter wires an embedding router for matter-type inference.
// Call once at server boot with the same embedder instance used for the
// agent router; the corpus is loaded lazily on first use so server boot
// isn't blocked on the sidecar warming up.
//
// Safe to pass nil to disable embedding routing (caller falls back to
// keyword hints).
func SetMatterRouter(r *embedrouter.Router) {
	matterRouterMu.Lock()
	matterRouter = r
	matterReady = false
	matterRouterMu.Unlock()
}

// ensureMatterCorpus initializes the router's reference set on first call.
// Idempotent and inexpensive after the first success.
func ensureMatterCorpus(ctx context.Context) bool {
	matterRouterMu.RLock()
	r := matterRouter
	matterRouterMu.RUnlock()
	if r == nil {
		return false
	}
	matterReadyMu.Lock()
	defer matterReadyMu.Unlock()
	if matterReady {
		return true
	}
	if err := r.Init(ctx, MatterCorpus); err != nil {
		log.Printf("[matter-router] corpus init failed: %v", err)
		return false
	}
	matterReady = true
	log.Printf("[matter-router] corpus ready (%d utterances)", len(MatterCorpus))
	return true
}

// extractMatterByEmbed runs the embedding router on a filename and
// returns (label, margin, ok). ok=false if router unavailable, init
// failed, classify errored, or context expired.
func extractMatterByEmbed(filename string) (string, float64, bool) {
	matterRouterMu.RLock()
	r := matterRouter
	matterRouterMu.RUnlock()
	if r == nil {
		return "", 0, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	if !ensureMatterCorpus(ctx) {
		return "", 0, false
	}
	res, err := r.Classify(ctx, filename)
	if err != nil {
		return "", 0, false
	}
	return res.Label, res.Margin, true
}
