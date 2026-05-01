// routereval measures classification quality of the local MLX router on a
// fixed labeled set under a fixed seed. Boots its own supervisor on a
// dedicated port so it doesn't collide with a running agentflow server.
//
// Usage:
//   go run ./cmd/routereval                                 # default eval set + default prompt
//   ROUTER_PROMPT_VARIANT=fewshot go run ./cmd/routereval   # try the few-shot variant
//   ROUTER_MODEL=... ROUTER_PORT=... go run ./cmd/routereval
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"agentflow-go/internal/mlxserver"
	"agentflow-go/internal/server"
)

type sample struct {
	Prompt string `json:"prompt"`
	Label  string `json:"label"`
}

type result struct {
	Prompt    string
	Gold      string
	Predicted string
	Raw       string
	LatencyMS int64
}

var labels = []string{"NEEDS_TOOLS", "NEEDS_RAG", "CONVERSATIONAL"}

// promptBasic is the definitions-only prompt used as a regression baseline.
// promptFewshot is the production prompt — sourced from internal/server so
// the eval cannot drift from what the server actually sends.
const promptBasic = `You are a router. Reply with exactly one of: NEEDS_TOOLS, NEEDS_RAG, CONVERSATIONAL. No prose, no other text.

Definitions:
- NEEDS_TOOLS: structured action or lookup (case status, deadlines, scheduling, sending email, marking records).
- NEEDS_RAG: synthesize an answer from the user's documents (summaries, clause lookup, comparisons, evidence).
- CONVERSATIONAL: greetings, thanks, capability questions, smalltalk — no data needed.`

var promptFewshot = server.RouterSystemPrompt

// promptTight: same User/Assistant format as prod but with 4 examples
// instead of 9. Roughly half the prompt tokens.
const promptTight = `You are a router. Reply with exactly one of: NEEDS_TOOLS, NEEDS_RAG, CONVERSATIONAL. No prose.
- NEEDS_TOOLS: action/lookup (case status, scheduling, sending email).
- NEEDS_RAG: synthesize from user's documents.
- CONVERSATIONAL: smalltalk, greetings, capability questions.

User: What's the deadline for case 8421?
Assistant: NEEDS_TOOLS
User: Summarize the indemnification clauses.
Assistant: NEEDS_RAG
User: What can you help me with?
Assistant: CONVERSATIONAL
User: 总结合同保密条款
Assistant: NEEDS_RAG`

func main() {
	log.SetFlags(log.LstdFlags)

	var (
		dataset    = flag.String("dataset", "testdata/router_eval.json", "labeled eval set (JSON)")
		variant    = flag.String("variant", envOr("ROUTER_PROMPT_VARIANT", "basic"), "prompt variant: basic | fewshot | tight")
		seed       = flag.Int("seed", 42, "sampling seed")
		port       = flag.Int("port", envInt("ROUTER_PORT", 8092), "port for the eval router (separate from prod 8090)")
		model      = flag.String("model", envOr("ROUTER_MODEL", "mlx-community/Qwen3-1.7B-4bit"), "router model id")
		draftModel = flag.String("draft-model", envOr("ROUTER_DRAFT_MODEL", ""), "speculative-decoding draft model (e.g. mlx-community/Qwen3-0.6B-4bit)")
		numDraft   = flag.Int("num-draft", envInt("ROUTER_NUM_DRAFT", 4), "speculative tokens per step")
		maxTokens  = flag.Int("max-tokens", envInt("ROUTER_MAX_TOKENS", server.RouterMaxTokens), "max output tokens per request")
		warm       = flag.Bool("warm", true, "send a warm-up request before timing the eval")
	)
	flag.Parse()

	var systemPrompt string
	switch *variant {
	case "fewshot":
		systemPrompt = promptFewshot
	case "tight":
		systemPrompt = promptTight
	default:
		systemPrompt = promptBasic
	}

	samples, err := loadDataset(*dataset)
	if err != nil {
		log.Fatalf("load dataset: %v", err)
	}
	log.Printf("loaded %d samples from %s", len(samples), *dataset)
	log.Printf("variant=%s seed=%d model=%s draft=%s max_tokens=%d", *variant, *seed, *model, *draftModel, *maxTokens)

	extra := []string{}
	if *draftModel != "" {
		extra = append(extra, "--draft-model", *draftModel, "--num-draft-tokens", fmt.Sprintf("%d", *numDraft))
	}
	mgr := mlxserver.New(mlxserver.Config{
		Cmd: "mlx_lm.server", Model: *model, Port: *port, LogPrefix: "[eval-router]",
		ExtraArgs: extra,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := mgr.Start(ctx); err != nil {
		log.Fatalf("start router: %v", err)
	}
	defer mgr.Stop()

	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, os.Interrupt, syscall.SIGTERM)
	go func() { <-sigC; mgr.Stop(); os.Exit(130) }()

	log.Printf("waiting for router ready (cached weights warm in ~10s, cold ~2-3min)...")
	for !mgr.Ready() {
		time.Sleep(time.Second)
	}
	log.Printf("router ready at %s", mgr.BaseURL())

	client := &http.Client{Timeout: 60 * time.Second}
	if *warm {
		// Warmup: send the system prompt once so KV-cache / first-batch
		// overhead doesn't pollute the measured latency.
		log.Printf("warming up...")
		_ = classify(client, mgr.BaseURL(), *model, systemPrompt, "ping", *seed, *maxTokens)
	}

	results := make([]result, 0, len(samples))
	for i, s := range samples {
		r := classify(client, mgr.BaseURL(), *model, systemPrompt, s.Prompt, *seed, *maxTokens)
		r.Gold = s.Label
		results = append(results, r)
		ok := "✓"
		if r.Predicted != r.Gold {
			ok = "✗"
		}
		fmt.Printf("[%2d/%2d] %s  gold=%-14s pred=%-14s  %dms  %q\n",
			i+1, len(samples), ok, r.Gold, r.Predicted, r.LatencyMS, truncate(r.Prompt, 50))
	}

	report(results)
}

func classify(client *http.Client, baseURL, model, systemPrompt, userPrompt string, seed, maxTokens int) result {
	body := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"max_tokens":  maxTokens,
		"temperature": 0,
		"seed":        seed,
		"chat_template_kwargs": map[string]any{
			// Qwen3 thinking models honor this to skip <think> blocks; older
			// models (Qwen3.5-OptiQ etc.) just ignore it. Cheap insurance.
			"enable_thinking": false,
		},
	}
	jb, _ := json.Marshal(body)

	t0 := time.Now()
	req, _ := http.NewRequest("POST", baseURL+"/chat/completions", bytes.NewReader(jb))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer router-local")
	resp, err := client.Do(req)
	latencyMS := time.Since(t0).Milliseconds()
	if err != nil {
		return result{Prompt: userPrompt, Predicted: "ERROR", Raw: err.Error(), LatencyMS: latencyMS}
	}
	defer resp.Body.Close()
	bodyB, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return result{Prompt: userPrompt, Predicted: "ERROR", Raw: string(bodyB), LatencyMS: latencyMS}
	}
	var parsed struct {
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
	}
	_ = json.Unmarshal(bodyB, &parsed)
	raw := ""
	if len(parsed.Choices) > 0 {
		raw = parsed.Choices[0].Message.Content
	}
	return result{Prompt: userPrompt, Predicted: normalize(raw), Raw: raw, LatencyMS: latencyMS}
}

// normalize maps the model output to one of our label tokens, or "OTHER".
// Accepts partial labels too (e.g. "NEEDS_T"/"NEEDS_R"/"CONV") so we can
// run with max_tokens=3 — the first 3 tokens fully disambiguate which
// label the model intended, no need to spend gen budget finishing it.
func normalize(raw string) string {
	s := strings.ToUpper(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, "<|IM_END|>", "")
	s = strings.ReplaceAll(s, "<|ENDOFTEXT|>", "")
	s = strings.TrimSpace(s)
	switch {
	case strings.Contains(s, "NEEDS_T") || strings.Contains(s, "NEEDS_TOOLS"):
		return "NEEDS_TOOLS"
	case strings.Contains(s, "NEEDS_R") || strings.Contains(s, "NEEDS_RAG"):
		return "NEEDS_RAG"
	case strings.Contains(s, "CONV"):
		return "CONVERSATIONAL"
	}
	return "OTHER"
}

func report(results []result) {
	fmt.Println("\n========== ROUTER QUALITY REPORT ==========")
	correct := 0
	var totalLat int64
	conf := map[string]map[string]int{} // gold -> pred -> count
	for _, l := range labels {
		conf[l] = map[string]int{}
	}
	conf["OTHER"] = map[string]int{}
	for _, r := range results {
		if r.Predicted == r.Gold {
			correct++
		}
		totalLat += r.LatencyMS
		conf[r.Gold][r.Predicted]++
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
		fn += conf[gold]["OTHER"]
		fp := 0
		for _, otherGold := range labels {
			if otherGold != gold {
				fp += conf[otherGold][gold]
			}
		}
		var precision, recall float64
		if tp+fp > 0 {
			precision = float64(tp) / float64(tp+fp)
		}
		if tp+fn > 0 {
			recall = float64(tp) / float64(tp+fn)
		}
		fmt.Printf("  %-15s P=%.2f R=%.2f  (TP=%d FP=%d FN=%d)\n", gold, precision, recall, tp, fp, fn)
	}

	fmt.Println("\nConfusion matrix (rows=gold, cols=pred):")
	header := "  gold \\ pred  "
	cols := append([]string{}, labels...)
	cols = append(cols, "OTHER")
	for _, c := range cols {
		header += fmt.Sprintf(" %-14s", c)
	}
	fmt.Println(header)
	for _, gold := range labels {
		row := fmt.Sprintf("  %-13s ", gold)
		for _, pred := range cols {
			row += fmt.Sprintf(" %-14d", conf[gold][pred])
		}
		fmt.Println(row)
	}

	fmt.Println("\nMispredictions:")
	miss := []result{}
	for _, r := range results {
		if r.Predicted != r.Gold {
			miss = append(miss, r)
		}
	}
	sort.Slice(miss, func(i, j int) bool { return miss[i].Gold < miss[j].Gold })
	if len(miss) == 0 {
		fmt.Println("  (none)")
	}
	for _, r := range miss {
		fmt.Printf("  gold=%-14s pred=%-14s raw=%q  prompt=%q\n",
			r.Gold, r.Predicted, strings.TrimSpace(r.Raw), truncate(r.Prompt, 60))
	}
	fmt.Println("===========================================")
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

func envInt(k string, def int) int {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	var n int
	_, err := fmt.Sscanf(v, "%d", &n)
	if err != nil {
		return def
	}
	return n
}
