package llm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strconv"
)

const maxCloudChars = 200000

// System prompt sent to OpenAI-compatible APIs; must stay in sync with generateOpenAICompatChat.
const openAISystemMessage = "You are a careful legal-domain assistant. Follow the user's task exactly; output only what they asked for when they specify a format."

func buildOpenAICompatUserContentForKey(prompt, context string) string {
	raw := fmt.Sprintf("--- Reference material ---\n%s\n--- End reference ---\n\nTask:\n%s", context, prompt)
	if len(raw) > maxCloudChars {
		log.Printf("[LLM] DashScope prompt truncated from %d to %d chars", len(raw), maxCloudChars)
		return raw[:maxCloudChars] + "\n\n[... truncated ...]\n"
	}
	return raw
}

func buildOllamaUserContentForKey(prompt, context string) string {
	return fmt.Sprintf("You are a careful legal-domain assistant.\n\n--- Reference material ---\n%s\n--- End reference ---\n\nTask:\n%s", context, prompt)
}

func (p *Provider) cacheKeyHex(prompt, context string, config GenerationConfig) string {
	model := p.modelName
	if config.Model != "" {
		model = config.Model
	}
	var payload string
	switch p.backend {
	case BackendOpenAICompat:
		maxTok := config.MaxTokens
		if maxTok <= 0 {
			maxTok = 2048
		}
		temp := config.Temp
		if temp <= 0 {
			temp = 0.1
		}
		uc := buildOpenAICompatUserContentForKey(prompt, context)
		payload = fmt.Sprintf("v3|openai_compat|%s|%s|%d|%s|%s|%s",
			p.baseURL, model, maxTok, strconv.FormatFloat(temp, 'f', 4, 64), openAISystemMessage, uc)
	default:
		uc := buildOllamaUserContentForKey(prompt, context)
		temp := config.Temp
		payload = fmt.Sprintf("v3|ollama|%s|%s|%d|%s|%s",
			p.baseURL, model, config.MaxTokens, strconv.FormatFloat(temp, 'f', 4, 64), uc)
	}
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}
