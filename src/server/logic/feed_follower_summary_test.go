package logic

import (
	"github.com/mmcdole/gofeed"
	"github.com/stretchr/testify/assert"
	"html"
	"rss_parrot/dal"
	"rss_parrot/shared"
	"strings"
	"testing"
	"time"
)

// The fakes below embed the interface they stand for,
// so only the handful of methods createToot actually
// calls need a body; anything else would panic loudly
// rather than pass silently.

type fakeTootRepo struct {
	dal.IRepo
	added *dal.Toot
}

func (r *fakeTootRepo) GetNextId() uint64 { return 99 }

func (r *fakeTootRepo) AddToot(accountId int, toot *dal.Toot) error {
	r.added = toot
	return nil
}

type fakeTootTexts struct{}

// WithVals mirrors the real texts.WithVals for the
// toot template: placeholder values are HTML-escaped,
// so only the template's own markup is live.
func (t *fakeTootTexts) WithVals(id string, vals map[string]string) string {
	esc := func(k string) string { return html.EscapeString(vals[k]) }
	return "<p><strong>" + esc("title") + "</strong></p>" +
		`<p><a href="` + esc("url") + `">` + esc("prettyUrl") +
		"</a></p><p>" + esc("description") + "</p>"
}

func (t *fakeTootTexts) Get(id string) string { return id }

type fakeTootMessenger struct {
	IMessenger
	broadcasts int
}

func (m *fakeTootMessenger) EnqueueBroadcast(
	user, statusId string, tootedAt time.Time, msg string,
) error {
	m.broadcasts++
	return nil
}

type fakeSummarizer struct {
	result string
}

func (s *fakeSummarizer) Summarize(text string) string      { return s.result }
func (s *fakeSummarizer) IsEnabled() bool                   { return true }
func (s *fakeSummarizer) TrimForSummary(text string) string { return text }

type fakeRetrier struct {
	ISummaryRetrier
	queuedStatusId    string
	queuedArticleText string
	queueCount        int
}

func (r *fakeRetrier) QueueForRetry(
	accountId int, statusId, articleText string, now time.Time,
) {
	r.queueCount++
	r.queuedStatusId = statusId
	r.queuedArticleText = articleText
}

func setupCreateTootTest(summary string) (
	*feedFollower, *fakeTootRepo, *fakeRetrier, *fakeTootMessenger,
) {
	repo := &fakeTootRepo{}
	retrier := &fakeRetrier{}
	messenger := &fakeTootMessenger{}
	ff := &feedFollower{
		cfg:            &shared.Config{Host: "parrot.test"},
		repo:           repo,
		messenger:      messenger,
		txt:            &fakeTootTexts{},
		summarizer:     &fakeSummarizer{result: summary},
		summaryRetrier: retrier,
	}
	return ff, repo, retrier, messenger
}

func tootTestItem() *gofeed.Item {
	return &gofeed.Item{
		GUID:        "guid-1",
		Link:        "https://x.test/article",
		Title:       "The Title",
		Description: "The description.",
		Content:     "The full article body.",
	}
}

func Test_CreateToot_SummaryPresent_NothingQueued(t *testing.T) {

	ff, repo, retrier, messenger := setupCreateTootTest("A summary.")

	err := ff.createToot(7, "x.test", tootTestItem(), true)

	assert.NoError(t, err)
	assert.NotNil(t, repo.added)
	assert.Contains(t, repo.added.Content, "<p><em>A summary.</em></p>")
	assert.Equal(t, 0, retrier.queueCount)
	assert.Equal(t, 1, messenger.broadcasts)
}

func Test_CreateToot_SummaryMissing_QueuesRetryAndStillPosts(t *testing.T) {

	ff, repo, retrier, messenger := setupCreateTootTest("")

	err := ff.createToot(7, "x.test", tootTestItem(), true)

	assert.NoError(t, err)
	// The toot goes out right away, without a summary.
	assert.NotNil(t, repo.added)
	assert.NotContains(t, repo.added.Content, "<em>")
	assert.Equal(t, 1, messenger.broadcasts)
	// ...and the article is queued for a later retry.
	assert.Equal(t, 1, retrier.queueCount)
	assert.Equal(t, repo.added.StatusId, retrier.queuedStatusId)
	assert.True(t,
		strings.Contains(retrier.queuedArticleText, "full article body"))
}
