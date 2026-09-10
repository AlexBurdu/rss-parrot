package logic

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"rss_parrot/shared"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type nullSummarizerLogger struct {
	shared.ILogger
}

func (l *nullSummarizerLogger) Warnf(format string, v ...interface{}) {}

func Test_FormatSummaryPrompt_WithTitle(t *testing.T) {
	prompt := formatSummaryPrompt("Breaking News", "Something happened today.")

	assert.Contains(t, prompt, "Write a 1-2 sentence summary")
	assert.Contains(t, prompt, "Do not repeat the title")
	assert.Contains(t, prompt, "Title: Breaking News")
	assert.Contains(t, prompt, "Article:\nSomething happened today.")
}

func Test_FormatSummaryPrompt_WithoutTitle(t *testing.T) {
	prompt := formatSummaryPrompt("", "Something happened today.")

	assert.Contains(t, prompt, "Write a 1-2 sentence summary")
	assert.NotContains(t, prompt, "Title:")
	assert.Contains(t, prompt, "Article:\nSomething happened today.")
}

func Test_Summarize_DisabledWhenConfigEmpty(t *testing.T) {
	logger := &nullSummarizerLogger{}

	s1 := NewSummarizer(&shared.Config{OllamaUrl: "", OllamaModel: "m"}, logger)
	assert.False(t, s1.IsEnabled())
	assert.Equal(t, "", s1.Summarize("Title", "Body"))

	s2 := NewSummarizer(&shared.Config{OllamaUrl: "http://localhost", OllamaModel: ""}, logger)
	assert.False(t, s2.IsEnabled())
	assert.Equal(t, "", s2.Summarize("Title", "Body"))
}

func Test_Summarize_EmptyInput(t *testing.T) {
	logger := &nullSummarizerLogger{}
	s := NewSummarizer(&shared.Config{
		OllamaUrl:   "http://localhost:11434",
		OllamaModel: "gemma2:2b",
	}, logger)

	assert.Equal(t, "", s.Summarize("", ""))
	assert.Equal(t, "", s.Summarize("   ", "   "))
}

func Test_Summarize_OllamaSuccess(t *testing.T) {
	var receivedReq ollamaRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/generate", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		err := json.NewDecoder(r.Body).Decode(&receivedReq)
		require.NoError(t, err)

		resp := ollamaResponse{Response: "  The summary text.  "}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	logger := &nullSummarizerLogger{}
	s := NewSummarizer(&shared.Config{
		OllamaUrl:   server.URL,
		OllamaModel: "gemma2:2b",
	}, logger)

	result := s.Summarize("My Title", "My content body.")

	assert.Equal(t, "The summary text.", result)
	assert.Equal(t, "gemma2:2b", receivedReq.Model)
	assert.False(t, receivedReq.Stream)
	assert.Contains(t, receivedReq.Prompt, "Title: My Title")
	assert.Contains(t, receivedReq.Prompt, "Article:\nMy content body.")
}

func Test_Summarize_OllamaError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	logger := &nullSummarizerLogger{}
	s := NewSummarizer(&shared.Config{
		OllamaUrl:   server.URL,
		OllamaModel: "gemma2:2b",
	}, logger)

	result := s.Summarize("My Title", "My content body.")
	assert.Equal(t, "", result)
}

func Test_TrimForSummary(t *testing.T) {
	logger := &nullSummarizerLogger{}
	s := NewSummarizer(&shared.Config{}, logger)

	shortText := "Short text"
	assert.Equal(t, shortText, s.TrimForSummary(shortText))

	longText := strings.Repeat("a", 2500)
	trimmed := s.TrimForSummary(longText)
	assert.Equal(t, 2000, len(trimmed))
	assert.Equal(t, strings.Repeat("a", 2000), trimmed)
}
