package logic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"rss_parrot/shared"
	"time"
)

//go:generate mockgen --build_flags=--mod=mod -destination ../test/mocks/mock_summarizer.go -package mocks rss_parrot/logic ISummarizer

// ISummarizer generates short summaries of article text
// using a local LLM via the Ollama API.
type ISummarizer interface {
	// Summarize returns a 1-2 sentence summary of the
	// given text. Returns empty string if summarization
	// is disabled or fails.
	Summarize(text string) string
}

type summarizer struct {
	cfg    *shared.Config
	logger shared.ILogger
}

func NewSummarizer(
	cfg *shared.Config,
	logger shared.ILogger,
) ISummarizer {
	return &summarizer{cfg: cfg, logger: logger}
}

type ollamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type ollamaResponse struct {
	Response string `json:"response"`
}

const (
	// Max input text length to send to the LLM.
	maxInputLen = 2000
	// Timeout for Ollama API calls. TinyLlama on RPi 4
	// takes ~30s for short summaries.
	ollamaTimeout = 60 * time.Second
	// Prompt template for summarization.
	summaryPrompt = "Summarize the following article " +
		"in 1-2 sentences. Be concise and " +
		"informative. Only output the summary, " +
		"nothing else.\n\n%s"
)

func (s *summarizer) Summarize(text string) string {
	if s.cfg.OllamaUrl == "" || s.cfg.OllamaModel == "" {
		return ""
	}

	if len(text) > maxInputLen {
		text = text[:maxInputLen]
	}

	prompt := fmt.Sprintf(summaryPrompt, text)
	reqBody := ollamaRequest{
		Model:  s.cfg.OllamaModel,
		Prompt: prompt,
		Stream: false,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		s.logger.Warnf("Summarizer: marshal error: %v",
			err)
		return ""
	}

	url := s.cfg.OllamaUrl + "/api/generate"
	client := http.Client{Timeout: ollamaTimeout}
	resp, err := client.Post(
		url, "application/json",
		bytes.NewReader(bodyBytes))
	if err != nil {
		s.logger.Warnf(
			"Summarizer: Ollama request failed: %v",
			err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		s.logger.Warnf(
			"Summarizer: Ollama returned %d",
			resp.StatusCode)
		return ""
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		s.logger.Warnf(
			"Summarizer: read response error: %v",
			err)
		return ""
	}

	var ollamaResp ollamaResponse
	if err := json.Unmarshal(respBytes, &ollamaResp); err != nil {
		s.logger.Warnf(
			"Summarizer: unmarshal error: %v", err)
		return ""
	}

	return ollamaResp.Response
}
