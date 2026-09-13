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
	prompt := formatSummaryPrompt("Breaking News", "Something happened today.", "English")

	assert.Contains(t, prompt, "Write a 1-2 sentence summary of this article in English")
	assert.Contains(t, prompt, "Do not repeat the title")
	assert.Contains(t, prompt, "Title: Breaking News")
	assert.Contains(t, prompt, "Article:\nSomething happened today.")
}

func Test_FormatSummaryPrompt_WithoutTitle(t *testing.T) {
	prompt := formatSummaryPrompt("", "Something happened today.", "French")

	assert.Contains(t, prompt, "Write a 1-2 sentence summary of this article in French")
	assert.NotContains(t, prompt, "Title:")
	assert.Contains(t, prompt, "Article:\nSomething happened today.")
}

func Test_FormatSummaryPrompt_DefaultFallback(t *testing.T) {
	prompt := formatSummaryPrompt("Breaking News", "Something happened today.", "")

	assert.Contains(t, prompt, "Write a 1-2 sentence summary of this article in English")
	assert.Contains(t, prompt, "Title: Breaking News")
}

func Test_FormatSummaryPrompt_TargetLanguage(t *testing.T) {
	prompt := formatSummaryPrompt("Titlu", "Text în română.", "Romanian")

	assert.Contains(t, prompt, "Write a 1-2 sentence summary of this article in Romanian")
	assert.Contains(t, prompt, "Title: Titlu")
}

func Test_Summarize_DisabledWhenConfigEmpty(t *testing.T) {
	logger := &nullSummarizerLogger{}

	s1 := NewSummarizer(&shared.Config{OllamaUrl: "", OllamaModel: "m"}, logger)
	assert.False(t, s1.IsEnabled())
	assert.Equal(t, "", s1.Summarize("Title", "Body", "English"))

	s2 := NewSummarizer(&shared.Config{OllamaUrl: "http://localhost", OllamaModel: ""}, logger)
	assert.False(t, s2.IsEnabled())
	assert.Equal(t, "", s2.Summarize("Title", "Body", "English"))
}

func Test_Summarize_EmptyInput(t *testing.T) {
	logger := &nullSummarizerLogger{}
	s := NewSummarizer(&shared.Config{
		OllamaUrl:   "http://localhost:11434",
		OllamaModel: "gemma2:2b",
	}, logger)

	assert.Equal(t, "", s.Summarize("", "", "English"))
	assert.Equal(t, "", s.Summarize("   ", "   ", ""))
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

	result := s.Summarize("My Title", "My content body.", "Spanish")

	assert.Equal(t, "The summary text.", result)
	assert.Equal(t, "gemma2:2b", receivedReq.Model)
	assert.False(t, receivedReq.Stream)
	assert.Contains(t, receivedReq.Prompt, "in Spanish")
	assert.Contains(t, receivedReq.Prompt, "Title: My Title")
	assert.Contains(t, receivedReq.Prompt, "Article:\nMy content body.")
}

func Test_Summarize_ContentLanguageDetection(t *testing.T) {
	var receivedReq ollamaRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := json.NewDecoder(r.Body).Decode(&receivedReq)
		require.NoError(t, err)

		resp := ollamaResponse{Response: "Resumen del artículo."}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	logger := &nullSummarizerLogger{}
	s := NewSummarizer(&shared.Config{
		OllamaUrl:   server.URL,
		OllamaModel: "gemma2:2b",
	}, logger)

	spanishText := "Este es un artículo completo sobre el desarrollo de software en España y Latinoamérica."
	result := s.Summarize("Título", spanishText, "")

	assert.Equal(t, "Resumen del artículo.", result)
	assert.Contains(t, receivedReq.Prompt, "in Spanish")
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

	result := s.Summarize("My Title", "My content body.", "English")
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
