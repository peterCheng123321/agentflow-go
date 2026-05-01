package llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// GenerateStream generates text with token-level streaming. For every incremental
// chunk of assistant text the backend emits, onDelta is called with the new text
// only (not cumulative). The disk response cache is intentionally bypassed on this
// path — streaming is for interactive UX, not repeat-request deduplication.
func (p *Provider) GenerateStream(prompt, context string, config GenerationConfig, onDelta func(string) error) error {
	switch p.backend {
	case BackendOpenAICompat:
		return p.streamOpenAICompat(prompt, context, config, onDelta)
	default:
		if !p.isReady {
			if err := p.checkReady(); err != nil {
				return fmt.Errorf("model server unavailable: %w", err)
			}
			p.isReady = true
		}
		return p.streamOllama(prompt, context, config, onDelta)
	}
}

func (p *Provider) streamOllama(prompt, context string, config GenerationConfig, onDelta func(string) error) error {
	modelToUse := p.modelName
	if config.Model != "" {
		modelToUse = config.Model
	}
	payload := map[string]interface{}{
		"model":    modelToUse,
		"messages": []map[string]string{{"role": "user", "content": buildOllamaUserContentForKey(prompt, context)}},
		"stream":   true,
		"options": map[string]interface{}{
			"num_predict":  config.MaxTokens,
			"temperature":  config.Temp,
			"mirostat":     2,
			"mirostat_tau": 5.0,
			"mirostat_eta": 0.1,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", p.baseURL+"/api/chat", bytes.NewBuffer(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama stream %d: %s", resp.StatusCode, string(body))
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var chunk struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Done bool `json:"done"`
		}
		if err := json.Unmarshal(line, &chunk); err != nil {
			continue
		}
		if chunk.Message.Content != "" {
			if err := onDelta(chunk.Message.Content); err != nil {
				return err
			}
		}
		if chunk.Done {
			return nil
		}
	}
	return scanner.Err()
}

func (p *Provider) streamOpenAICompat(prompt, context string, config GenerationConfig, onDelta func(string) error) error {
	maxTok := config.MaxTokens
	if maxTok <= 0 {
		maxTok = 2048
	}
	temp := config.Temp
	if temp <= 0 {
		temp = 0.1
	}
	modelToUse := p.modelName
	if config.Model != "" {
		modelToUse = config.Model
	}
	payload := map[string]interface{}{
		"model": modelToUse,
		"messages": []map[string]interface{}{
			{"role": "system", "content": openAISystemMessage},
			{"role": "user", "content": buildOpenAICompatUserContentForKey(prompt, context)},
		},
		"max_tokens":  maxTok,
		"temperature": temp,
		"stream":      true,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", p.baseURL+"/chat/completions", bytes.NewBuffer(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("dashscope stream %d: %s", resp.StatusCode, string(body))
	}
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			if payload == "[DONE]" {
				return nil
			}
			continue
		}
		var evt struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &evt); err != nil {
			continue
		}
		if len(evt.Choices) == 0 {
			continue
		}
		if c := evt.Choices[0].Delta.Content; c != "" {
			if err := onDelta(c); err != nil {
				return err
			}
		}
	}
}
