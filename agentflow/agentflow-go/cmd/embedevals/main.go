// embedevals runs the embedding-based router against the same labeled
// eval set as cmd/routereval, so the two architectures can be compared
// apples-to-apples. Reports accuracy, per-class P/R, latency, and the
// confidence-margin distribution split by correct vs incorrect.
//
// Usage:
//   go run ./cmd/embedevals                                  # bge-m3 via local Ollama
//   EMBED_MODEL=bge-m3 EMBED_BASE_URL=... go run ./cmd/embedevals
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"agentflow-go/internal/embedrouter"
)

type sample struct {
	Prompt string `json:"prompt"`
	Label  string `json:"label"`
}

func main() {
	log.SetFlags(log.LstdFlags)

	var (
		dataset  = flag.String("dataset", "testdata/router_eval.json", "labeled eval set (JSON)")
		backend  = flag.String("backend", envOr("EMBED_BACKEND", "ollama"), "embedding backend: ollama | dashscope")
		model    = flag.String("model", envOr("EMBED_MODEL", ""), "embedding model id (defaults: ollama=bge-m3, dashscope=text-embedding-v3)")
		baseURL  = flag.String("base-url", envOr("EMBED_BASE_URL", ""), "embedding base URL (defaults: ollama=localhost:11434, dashscope=compatible-mode endpoint)")
		warm     = flag.Bool("warm", true, "send a warmup query before timing")
	)
	flag.Parse()

	samples, err := loadDataset(*dataset)
	if err != nil {
		log.Fatalf("load dataset: %v", err)
	}
	var emb embedrouter.Embedder
	switch *backend {
	case "ollama":
		emb = embedrouter.NewOllamaEmbedder(*baseURL, *model)
	case "dashscope":
		key := strings.TrimSpace(os.Getenv("AGENTFLOW_DASHSCOPE_API_KEY"))
		if key == "" {
			key = strings.TrimSpace(os.Getenv("DASHSCOPE_API_KEY"))
		}
		if key == "" {
			// Fall back to the on-disk key the prod server uses.
			home, _ := os.UserHomeDir()
			b, err := os.ReadFile(home + "/Library/Application Support/AgentFlow/secrets/dashscope_api_key.txt")
			if err == nil {
				key = strings.TrimSpace(string(b))
			}
		}
		if key == "" {
			log.Fatalf("dashscope backend requires DASHSCOPE_API_KEY (or AGENTFLOW_DASHSCOPE_API_KEY, or the prod secrets file)")
		}
		emb = embedrouter.NewDashScopeEmbedder(*baseURL, key, *model)
	default:
		log.Fatalf("unknown backend %q (use: ollama | dashscope)", *backend)
	}
	log.Printf("loaded %d samples from %s", len(samples), *dataset)
	log.Printf("backend=%s model=%q baseURL=%q", *backend, *model, *baseURL)
	r := embedrouter.New(emb)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	t0 := time.Now()
	if err := r.Init(ctx, embedrouter.DefaultCorpus); err != nil {
		log.Fatalf("init: %v", err)
	}
	log.Printf("corpus embedded in %dms (%d utterances)", time.Since(t0).Milliseconds(), len(embedrouter.DefaultCorpus))

	if *warm {
		_, _ = r.Classify(ctx, "warmup ping")
	}

	type result struct {
		Prompt    string
		Gold      string
		Pred      string
		Score     float64
		Margin    float64
		Best      string
		LatencyMS int64
	}
	results := make([]result, 0, len(samples))
	for i, s := range samples {
		ctx2, c2 := context.WithTimeout(context.Background(), 5*time.Second)
		t1 := time.Now()
		res, err := r.Classify(ctx2, s.Prompt)
		c2()
		lat := time.Since(t1).Milliseconds()
		if err != nil {
			log.Fatalf("classify %d: %v", i, err)
		}
		ok := "✓"
		if res.Label != s.Label {
			ok = "✗"
		}
		fmt.Printf("[%2d/%2d] %s gold=%-14s pred=%-14s score=%.3f margin=%.3f %dms  %q\n",
			i+1, len(samples), ok, s.Label, res.Label, res.Score, res.Margin, lat, truncate(s.Prompt, 50))
		results = append(results, result{
			Prompt: s.Prompt, Gold: s.Label, Pred: res.Label,
			Score: res.Score, Margin: res.Margin,
			Best: res.Best.Utterance, LatencyMS: lat,
		})
	}

	// Report.
	fmt.Println("\n========== EMBED ROUTER QUALITY REPORT ==========")
	correct, totalLat := 0, int64(0)
	conf := map[string]map[string]int{}
	labels := []string{"NEEDS_TOOLS", "NEEDS_RAG", "CONVERSATIONAL"}
	for _, l := range labels {
		conf[l] = map[string]int{}
	}
	for _, r := range results {
		if r.Pred == r.Gold {
			correct++
		}
		totalLat += r.LatencyMS
		conf[r.Gold][r.Pred]++
	}
	n := len(results)
	fmt.Printf("Overall accuracy: %d/%d = %.1f%%\n", correct, n, 100*float64(correct)/float64(n))
	fmt.Printf("Avg latency:      %d ms\n", totalLat/int64(n))

	fmt.Println("\nPer-class precision/recall:")
	for _, gold := range labels {
		tp := conf[gold][gold]
		fn := 0
		for _, p := range labels {
			if p != gold {
				fn += conf[gold][p]
			}
		}
		fp := 0
		for _, og := range labels {
			if og != gold {
				fp += conf[og][gold]
			}
		}
		var p, r float64
		if tp+fp > 0 {
			p = float64(tp) / float64(tp+fp)
		}
		if tp+fn > 0 {
			r = float64(tp) / float64(tp+fn)
		}
		fmt.Printf("  %-15s P=%.2f R=%.2f  (TP=%d FP=%d FN=%d)\n", gold, p, r, tp, fp, fn)
	}

	fmt.Println("\nConfusion matrix (rows=gold, cols=pred):")
	header := "  gold \\ pred  "
	for _, c := range labels {
		header += fmt.Sprintf(" %-14s", c)
	}
	fmt.Println(header)
	for _, gold := range labels {
		row := fmt.Sprintf("  %-13s ", gold)
		for _, p := range labels {
			row += fmt.Sprintf(" %-14d", conf[gold][p])
		}
		fmt.Println(row)
	}

	// Margin distribution — useful for picking a confidence threshold.
	correctMargins := []float64{}
	wrongMargins := []float64{}
	miss := []result{}
	for _, r := range results {
		if r.Pred == r.Gold {
			correctMargins = append(correctMargins, r.Margin)
		} else {
			wrongMargins = append(wrongMargins, r.Margin)
			miss = append(miss, r)
		}
	}
	sort.Float64s(correctMargins)
	sort.Float64s(wrongMargins)
	fmt.Println("\nMargin distribution (top1 − top2):")
	if len(correctMargins) > 0 {
		fmt.Printf("  correct: n=%d  min=%.3f  p50=%.3f  p95=%.3f  max=%.3f\n",
			len(correctMargins), correctMargins[0],
			correctMargins[len(correctMargins)/2],
			correctMargins[min(len(correctMargins)-1, 95*len(correctMargins)/100)],
			correctMargins[len(correctMargins)-1])
	}
	if len(wrongMargins) > 0 {
		fmt.Printf("  wrong:   n=%d  min=%.3f  p50=%.3f  p95=%.3f  max=%.3f\n",
			len(wrongMargins), wrongMargins[0],
			wrongMargins[len(wrongMargins)/2],
			wrongMargins[min(len(wrongMargins)-1, 95*len(wrongMargins)/100)],
			wrongMargins[len(wrongMargins)-1])
	}

	if len(miss) > 0 {
		fmt.Println("\nMispredictions:")
		sort.Slice(miss, func(i, j int) bool { return miss[i].Gold < miss[j].Gold })
		for _, r := range miss {
			fmt.Printf("  gold=%-14s pred=%-14s score=%.3f margin=%.3f  %q\n    matched: %q\n",
				r.Gold, r.Pred, r.Score, r.Margin, truncate(r.Prompt, 60), truncate(r.Best, 60))
		}
	}
	fmt.Println("==================================================")
}

func loadDataset(path string) ([]sample, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s []sample
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return s, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
