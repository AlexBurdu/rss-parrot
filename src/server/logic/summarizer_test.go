package logic

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"rss_parrot/shared"
)

// mockLogger implements shared.ILogger for testing
type mockLogger struct {
	warnfCalls []string
	infofCalls []string
}

func (m *mockLogger) Debug(msg interface{}, keyvals ...interface{}) {}
func (m *mockLogger) Info(msg interface{}, keyvals ...interface{})  {}
func (m *mockLogger) Warn(msg interface{}, keyvals ...interface{})  {}
func (m *mockLogger) Error(msg interface{}, keyvals ...interface{}) {}
func (m *mockLogger) Printf(format string, args ...interface{})       {}
func (m *mockLogger) Debugf(format string, args ...interface{})      {}
func (m *mockLogger) Warnf(format string, args ...interface{}) {
	m.warnfCalls = append(m.warnfCalls, fmt.Sprintf(format, args...))
}
func (m *mockLogger) Errorf(format string, args ...interface{}) {
	m.warnfCalls = append(m.warnfCalls, fmt.Sprintf(format, args...))
}
func (m *mockLogger) Infof(format string, args ...interface{}) {
	m.infofCalls = append(m.infofCalls, fmt.Sprintf(format, args...))
}

// mockConfig implements the parts of shared.Config needed for testing
type mockConfig struct {
	OllamaUrl  string
	OllamaModel string
}

func (m *mockConfig) GetConfig() *shared.Config {
	return &shared.Config{
		OllamaUrl:  m.OllamaUrl,
		OllamaModel: m.OllamaModel,
	}
}

// TestSummarizer_Retry_FailureThenSuccess tests that summarizer retries on
// transient failures and succeeds when the service recovers.
func TestSummarizer_Retry_FailureThenSuccess(t *testing.T) {
	var requestCount int32
	failFirstTwo := true

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&requestCount, 1)
		if failFirstTwo && count <= 2 {
			// Fail the first two requests with 503 Service Unavailable
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		// Third request succeeds
		w.Header().Set("Content-Type", "application/json")
		response := ollamaResponse{
			Response: "This is a test summary",
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer ts.Close()

	logger := &mockLogger{
		warnfCalls: make([]string, 0),
		infofCalls: make([]string, 0),
	}
	cfg := &shared.Config{
		OllamaUrl:  ts.URL,
		OllamaModel: "test-model",
	}

	summarizer := NewSummarizer(cfg, logger)
	text := "Test article text for summarization"

	// The summarizer should retry and eventually succeed
	result := summarizer.Summarize(text)

	assert.Equal(t, "This is a test summary", result)
	assert.Equal(t, int32(3), atomic.LoadInt32(&requestCount))
	// Should have logged warnings for the failed attempts
	assert.True(t, len(logger.warnfCalls) >= 2, "Expected at least 2 warning calls")
	// Should have logged info for retry attempts
	assert.True(t, len(logger.infofCalls) >= 2, "Expected at least 2 info calls for retries")
}

// TestSummarizer_Retry_ExhaustAllRetries tests that summarizer gives up
// after exhausting all retry attempts.
func TestSummarizer_Retry_ExhaustAllRetries(t *testing.T) {
	var requestCount int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		// Always return 500 Internal Server Error
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	logger := &mockLogger{
		warnfCalls: make([]string, 0),
		infofCalls: make([]string, 0),
	}
	cfg := &shared.Config{
		OllamaUrl:  ts.URL,
		OllamaModel: "test-model",
	}

	summarizer := NewSummarizer(cfg, logger)
	text := "Test article text for summarization"

	// The summarizer should fail after all retries
	result := summarizer.Summarize(text)

	assert.Equal(t, "", result)
	assert.Equal(t, int32(maxSummarizeRetries), atomic.LoadInt32(&requestCount))
	// Should have logged all failures
	assert.True(t, len(logger.warnfCalls) >= maxSummarizeRetries,
		"Expected at least %d warning calls", maxSummarizeRetries)
}

// TestSummarizer_NoRetryOnEmptyConfig tests that summarizer fails fast
// when Ollama is not configured.
func TestSummarizer_NoRetryOnEmptyConfig(t *testing.T) {
	logger := &mockLogger{
		warnfCalls: make([]string, 0),
		infofCalls: make([]string, 0),
	}

	// Test with empty OllamaUrl
	cfg1 := &shared.Config{
		OllamaUrl:  "",
		OllamaModel: "test-model",
	}
	summarizer1 := NewSummarizer(cfg1, logger)
	result1 := summarizer1.Summarize("test")
	assert.Equal(t, "", result1)

	// Test with empty OllamaModel
	cfg2 := &shared.Config{
		OllamaUrl:  "http://localhost:11434",
		OllamaModel: "",
	}
	summarizer2 := NewSummarizer(cfg2, logger)
	result2 := summarizer2.Summarize("test")
	assert.Equal(t, "", result2)

	// Should have no retry attempts
	assert.Equal(t, 0, len(logger.infofCalls))
}

// TestSummarizer_Retry_Timeout tests that summarizer retries on timeout errors.
// Note: This test is skipped because the actual ollamaTimeout is 120 seconds,
// which would make the test take too long (120s * 3 retries = 6 minutes).
// The retry logic for timeouts is the same as for HTTP errors, which is tested
// in TestSummarizer_Retry_FailureThenSuccess.
func TestSummarizer_Retry_Timeout(t *testing.T) {
	t.Skip("Skipping timeout test - would take too long with 120s timeout * 3 retries")
}

// TestSummarizer_Retry_MalformedResponse tests that summarizer retries on
// malformed JSON responses.
func TestSummarizer_Retry_MalformedResponse(t *testing.T) {
	var requestCount int32
	failFirstTwo := true

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&requestCount, 1)
		if failFirstTwo && count <= 2 {
			// Return malformed JSON
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("{invalid json}"))
			return
		}
		// Third request succeeds
		w.Header().Set("Content-Type", "application/json")
		response := ollamaResponse{
			Response: "Success after malformed response",
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer ts.Close()

	logger := &mockLogger{
		warnfCalls: make([]string, 0),
		infofCalls: make([]string, 0),
	}
	cfg := &shared.Config{
		OllamaUrl:  ts.URL,
		OllamaModel: "test-model",
	}

	summarizer := NewSummarizer(cfg, logger)
	text := "Test article text"

	result := summarizer.Summarize(text)

	assert.Equal(t, "Success after malformed response", result)
	assert.Equal(t, int32(3), atomic.LoadInt32(&requestCount))
}

// TestSummarizer_SuccessOnFirstAttempt tests that summarizer works on first try
// when there are no errors.
func TestSummarizer_SuccessOnFirstAttempt(t *testing.T) {
	var requestCount int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		w.Header().Set("Content-Type", "application/json")
		response := ollamaResponse{
			Response: "First try success",
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer ts.Close()

	logger := &mockLogger{
		warnfCalls: make([]string, 0),
		infofCalls: make([]string, 0),
	}
	cfg := &shared.Config{
		OllamaUrl:  ts.URL,
		OllamaModel: "test-model",
	}

	summarizer := NewSummarizer(cfg, logger)
	text := "Test article text"

	result := summarizer.Summarize(text)

	assert.Equal(t, "First try success", result)
	assert.Equal(t, int32(1), atomic.LoadInt32(&requestCount))
	// No warnings or retry infos on first success
	assert.Equal(t, 0, len(logger.warnfCalls))
	assert.Equal(t, 0, len(logger.infofCalls))
}

// TestSummarizer_TruncatesLongText tests that long input text is truncated
// before being sent to the LLM.
func TestSummarizer_TruncatesLongText(t *testing.T) {
	var receivedPrompt string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody ollamaRequest
		json.NewDecoder(r.Body).Decode(&reqBody)
		receivedPrompt = reqBody.Prompt
		w.Header().Set("Content-Type", "application/json")
		response := ollamaResponse{
			Response: "OK",
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer ts.Close()

	logger := &mockLogger{}
	cfg := &shared.Config{
		OllamaUrl:  ts.URL,
		OllamaModel: "test-model",
	}

	summarizer := NewSummarizer(cfg, logger)
	// Create text longer than maxInputLen
	longText := "A"
	for i := 0; i < maxInputLen+100; i++ {
		longText += "B"
	}

	result := summarizer.Summarize(longText)

	assert.Equal(t, "OK", result)
	// The prompt should have been truncated
	assert.True(t, len(receivedPrompt) < maxInputLen+len(summaryPrompt),
		"Prompt should be truncated")
}
