package logic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"rss_parrot/shared"
	"strings"
	"time"
)

//go:generate mockgen --build_flags=--mod=mod -destination ../test/mocks/mock_summarizer.go -package mocks rss_parrot/logic ISummarizer

// ISummarizer generates short summaries of article text
// using a local LLM via the Ollama API.
type ISummarizer interface {
	// Summarize returns a 1-2 sentence summary of the
	// given article. If title is non-empty, it is included
	// as context. Returns empty string if summarization
	// is disabled or fails.
	Summarize(title, text string) string

	// IsEnabled reports whether summarization is
	// configured. Summarize returns an empty string
	// both when disabled and when Ollama fails, so
	// callers that need to tell the two apart — the
	// retry queue does — must ask here.
	IsEnabled() bool

	// TrimForSummary shortens text to the longest
	// input the summarizer would actually send, so a
	// caller storing it for a later retry does not
	// keep bytes that would be thrown away.
	TrimForSummary(text string) string
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
	// Timeout for Ollama API calls. Gemma 2B on RPi 4
	// takes ~30s model load + ~3min inference on a
	// 2000-char article.
	ollamaTimeout = 300 * time.Second
	// Base directive for summarization.
	summaryDirective = "Write a 1-2 sentence summary of this article " +
		"focusing on key facts. Be direct and concise. " +
		"Do not repeat the title, and avoid introductory filler " +
		"or conversational preambles (such as \"This article discusses\" " +
		"or \"In this episode\"). Only output the summary, nothing else."
)

func formatSummaryPrompt(title, text string) string {
	title = strings.TrimSpace(title)
	text = strings.TrimSpace(text)
	if title != "" {
		return fmt.Sprintf("%s\n\nTitle: %s\n\nArticle:\n%s",
			summaryDirective, title, text)
	}
	return fmt.Sprintf("%s\n\nArticle:\n%s", summaryDirective, text)
}

func (s *summarizer) IsEnabled() bool {
	return s.cfg.OllamaUrl != "" && s.cfg.OllamaModel != ""
}

func (s *summarizer) TrimForSummary(text string) string {
	if len(text) > maxInputLen {
		return text[:maxInputLen]
	}
	return text
}

func (s *summarizer) Summarize(title, text string) string {
	if !s.IsEnabled() {
		return ""
	}

	text = s.TrimForSummary(text)
	if strings.TrimSpace(title) == "" && strings.TrimSpace(text) == "" {
		return ""
	}

	prompt := formatSummaryPrompt(title, text)
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

	return strings.TrimSpace(ollamaResp.Response)
}
