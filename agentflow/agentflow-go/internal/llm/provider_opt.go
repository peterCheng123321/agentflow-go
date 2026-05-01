package llm

import (
	"net/http"
	"time"
)

// OptimizedConfig returns provider options optimized for speed with Ollama
func OptimizedForOllama() Option {
	return func(p *Provider) {
		// Aggressive connection pooling for local Ollama
		p.client = &http.Client{
			Timeout: 60 * time.Second, // Much lower than default 300s
			Transport: &http.Transport{
				MaxIdleConns:          100,              // Up from 10
				MaxIdleConnsPerHost:   50,               // Up from 5
				MaxConnsPerHost:       0,                // Unlimited
				IdleConnTimeout:       30 * time.Second, // Down from 90s
				DisableKeepAlives:     false,
				ForceAttemptHTTP2:     true,
				// Disable compression for local - faster CPU
				DisableCompression:    true,
			},
		}
		p.maxRetries = 2 // Down from 3 - fail faster
		p.ttl = 5 * time.Minute // Down from 30min - keep model warm
	}
}

// OptimizedForCloud returns provider options optimized for cloud APIs (DashScope)
func OptimizedForCloud() Option {
	return func(p *Provider) {
		p.client = &http.Client{
			Timeout: 120 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:          50,
				MaxIdleConnsPerHost:   25,
				MaxConnsPerHost:       0,
				IdleConnTimeout:       90 * time.Second,
				DisableKeepAlives:     false,
				ForceAttemptHTTP2:     true,
				// Enable compression for cloud
				DisableCompression:    false,
			},
		}
		p.maxRetries = 3
		p.ttl = 30 * time.Minute
	}
}

// LowLatencyConfig returns options for lowest latency (trading some quality)
func LowLatencyConfig() Option {
	return func(p *Provider) {
		OptimizedForOllama()(p)
		p.client.Timeout = 30 * time.Second
		p.maxRetries = 1 // Fail immediately on error
	}
}
