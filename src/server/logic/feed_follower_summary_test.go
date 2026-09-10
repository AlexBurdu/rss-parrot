package logic

import (
	"github.com/mmcdole/gofeed"
	"github.com/stretchr/testify/assert"
	"html"
	"rss_parrot/dal"
	"rss_parrot/shared"
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
	result   string
	disabled bool
	// lastText is what createToot decided to summarize,
	// which is how the article-text tests below see
	// which source won.
	lastText string
}

func (s *fakeSummarizer) Summarize(title, text string) string {
	s.lastText = text
	return s.result
}

func (s *fakeSummarizer) IsEnabled() bool                   { return !s.disabled }
func (s *fakeSummarizer) TrimForSummary(text string) string { return text }

type fakeExtractor struct {
	result    string
	askedFor  string
	callCount int
}

func (e *fakeExtractor) IsEnabled() bool { return true }

func (e *fakeExtractor) Extract(articleUrl string) string {
	e.callCount++
	e.askedFor = articleUrl
	return e.result
}

type fakeRetrier struct {
	ISummaryRetrier
	queuedStatusId    string
	queuedTitle       string
	queuedArticleText string
	queueCount        int
}

func (r *fakeRetrier) QueueForRetry(
	accountId int, statusId, title, articleText string, now time.Time,
) {
	r.queueCount++
	r.queuedStatusId = statusId
	r.queuedTitle = title
	r.queuedArticleText = articleText
}

type createTootFakes struct {
	repo       *fakeTootRepo
	retrier    *fakeRetrier
	messenger  *fakeTootMessenger
	summarizer *fakeSummarizer
	extractor  *fakeExtractor
}

func setupCreateTootTest(summary string) (
	*feedFollower, *createTootFakes,
) {
	f := &createTootFakes{
		repo:       &fakeTootRepo{},
		retrier:    &fakeRetrier{},
		messenger:  &fakeTootMessenger{},
		summarizer: &fakeSummarizer{result: summary},
		extractor:  &fakeExtractor{},
	}
	ff := &feedFollower{
		cfg:            &shared.Config{Host: "parrot.test"},
		repo:           f.repo,
		messenger:      f.messenger,
		txt:            &fakeTootTexts{},
		extractor:      f.extractor,
		summarizer:     f.summarizer,
		summaryRetrier: f.retrier,
	}
	return ff, f
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

	ff, f := setupCreateTootTest("A summary.")

	err := ff.createToot(7, "x.test", tootTestItem(), true)

	assert.NoError(t, err)
	assert.NotNil(t, f.repo.added)
	assert.Contains(t, f.repo.added.Content, "<p><em>A summary.</em></p>")
	assert.Equal(t, 0, f.retrier.queueCount)
	assert.Equal(t, 1, f.messenger.broadcasts)
}

func Test_CreateToot_SummaryMissing_QueuesRetryAndStillPosts(t *testing.T) {

	ff, f := setupCreateTootTest("")

	err := ff.createToot(7, "x.test", tootTestItem(), true)

	assert.NoError(t, err)
	// The toot goes out right away, without a summary.
	assert.NotNil(t, f.repo.added)
	assert.NotContains(t, f.repo.added.Content, "<em>")
	assert.Equal(t, 1, f.messenger.broadcasts)
	// ...and the article is queued for a later retry.
	assert.Equal(t, 1, f.retrier.queueCount)
	assert.Equal(t, f.repo.added.StatusId, f.retrier.queuedStatusId)
	assert.Equal(t, "The Title", f.retrier.queuedTitle)
	assert.Equal(t, "The full article body.", f.retrier.queuedArticleText)
}

func Test_CreateToot_FeedWithContent_IsNotDownloaded(t *testing.T) {

	ff, f := setupCreateTootTest("A summary.")
	f.extractor.result = "Extracted body text."

	err := ff.createToot(7, "x.test", tootTestItem(), true)

	assert.NoError(t, err)
	// The feed already carries the whole article, so
	// there is nothing to go and fetch.
	assert.Equal(t, 0, f.extractor.callCount)
	assert.Equal(t, "The full article body.", f.summarizer.lastText)
}

func Test_CreateToot_ExcerptOnlyFeed_SummarizesExtractedText(t *testing.T) {

	ff, f := setupCreateTootTest("A summary.")
	f.extractor.result = "Extracted body text."
	itm := tootTestItem()
	itm.Content = ""

	err := ff.createToot(7, "x.test", itm, true)

	assert.NoError(t, err)
	assert.Equal(t, 1, f.extractor.callCount)
	assert.Equal(t, itm.Link, f.extractor.askedFor)
	assert.Equal(t,
		"Extracted body text.", f.summarizer.lastText)
}

func Test_CreateToot_ExtractionFails_FallsBackToDescription(t *testing.T) {

	ff, f := setupCreateTootTest("A summary.")
	f.extractor.result = ""
	itm := tootTestItem()
	itm.Content = ""

	err := ff.createToot(7, "x.test", itm, true)

	assert.NoError(t, err)
	assert.Equal(t, 1, f.extractor.callCount)
	// The teaser is still better than nothing, and the
	// toot goes out either way.
	assert.Equal(t, "The description.", f.summarizer.lastText)
	assert.NotNil(t, f.repo.added)
}

func Test_CreateToot_SummariesOff_NothingIsDownloaded(t *testing.T) {

	ff, f := setupCreateTootTest("")
	f.summarizer.disabled = true
	f.extractor.result = "Extracted body text."
	itm := tootTestItem()
	itm.Content = ""

	err := ff.createToot(7, "x.test", itm, true)

	assert.NoError(t, err)
	// Nobody would read the extracted text, so the
	// article page is left alone.
	assert.Equal(t, 0, f.extractor.callCount)
	assert.NotNil(t, f.repo.added)
}
