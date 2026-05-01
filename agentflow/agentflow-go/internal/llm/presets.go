package llm

// Preset configuration for different task types, optimized for speed/quality tradeoffs

// FastConfig returns a generation config optimized for speed (lower quality)
func FastConfig() GenerationConfig {
	return GenerationConfig{
		MaxTokens: 128,   // Short responses
		Temp:      0.0,   // Deterministic, faster sampling
	}
}

// BalancedConfig returns a generation config for balanced speed/quality
func BalancedConfig() GenerationConfig {
	return GenerationConfig{
		MaxTokens: 512,
		Temp:      0.1,
	}
}

// QualityConfig returns a generation config for highest quality (slower)
func QualityConfig() GenerationConfig {
	return GenerationConfig{
		MaxTokens: 2048,
		Temp:      0.2,
	}
}

// Task-specific presets

// IntakeConfig for fast case intake/classification
func IntakeConfig() GenerationConfig {
	return GenerationConfig{
		MaxTokens: 256,
		Temp:      0.0, // Deterministic for consistent classification
	}
}

// SummaryConfig for document summarization
func SummaryConfig() GenerationConfig {
	return GenerationConfig{
		MaxTokens: 512,
		Temp:      0.1,
	}
}

// DraftConfig for document drafting
func DraftConfig() GenerationConfig {
	return GenerationConfig{
		MaxTokens: 2048,
		Temp:      0.2, // Slightly higher for creativity
	}
}

// ChatConfig for simple Q&A
func ChatConfig() GenerationConfig {
	return GenerationConfig{
		MaxTokens: 256,
		Temp:      0.1,
	}
}
