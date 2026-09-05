package logic

import (
	"rss_parrot/dal"
	"rss_parrot/shared"
	"strings"
	"time"
)

//go:generate mockgen --build_flags=--mod=mod -destination ../test/mocks/mock_summary_retrier.go -package mocks rss_parrot/logic ISummaryRetrier

const (
	// How long the retry loop sleeps when nothing is
	// due.
	summaryRetryIdleWakeSec = 60
	// Background retries allowed per article, on top
	// of the inline attempt made when the toot was
	// first created. Bounded so a permanently down
	// Ollama cannot spin the loop forever.
	maxSummaryRetries = 3
)

// summaryRetryBackoff is the wait before the Nth
// background retry, indexed by attempts already made.
// Its length must match maxSummaryRetries.
var summaryRetryBackoff = [maxSummaryRetries]time.Duration{
	15 * time.Minute,
	60 * time.Minute,
	240 * time.Minute,
}

// ISummaryRetrier fills in AI summaries that were
// missing when an article's toot was first posted,
// because Ollama was down or timed out.
//
// The late summary is written back into the stored
// toot content only; it is never federated as an edit.
// This server sends no ActivityPub Update activity and
// dto.Note carries no "updated" field, which is what
// Mastodon and GoToSocial key an edit off, so a fresh
// fetch of the status shows the summary but copies
// already delivered to timelines do not change.
type ISummaryRetrier interface {
	// Start launches the background retry loop. Called
	// once at startup.
	Start()

	// QueueForRetry records an article whose toot was
	// posted without a summary, so the loop can try
	// again later. A no-op when summarization is
	// disabled, since a missing summary is then
	// expected rather than a failure.
	QueueForRetry(accountId int, statusId, articleText string, now time.Time)

	// RetryNextDue makes at most one retry attempt: it
	// takes the longest-due pending article, re-runs
	// the summarizer, and either rewrites the toot,
	// reschedules, or abandons the article.
	//
	// Returns true only when the article's row actually
	// moved on. A false return means the caller should
	// wait before asking again, because retrying at
	// once would just hit the same row.
	RetryNextDue(now time.Time) bool
}

type summaryRetrier struct {
	logger     shared.ILogger
	repo       dal.IRepo
	summarizer ISummarizer
}

func NewSummaryRetrier(
	logger shared.ILogger,
	repo dal.IRepo,
	summarizer ISummarizer,
) ISummaryRetrier {
	return &summaryRetrier{
		logger:     logger,
		repo:       repo,
		summarizer: summarizer,
	}
}

func (sr *summaryRetrier) Start() {
	go sr.retryLoop()
}

// retryLoop drains due articles one at a time, backing
// off to a sleep whenever the queue runs dry. It never
// exits: a panic in one attempt must not take the
// summary queue down for the life of the process.
func (sr *summaryRetrier) retryLoop() {
	for {
		if !sr.retryOnceSafely() {
			time.Sleep(summaryRetryIdleWakeSec * time.Second)
		}
	}
}

func (sr *summaryRetrier) retryOnceSafely() (didWork bool) {
	defer func() {
		if r := recover(); r != nil {
			sr.logger.Errorf("Summary retry panicked: %v", r)
			didWork = false
		}
	}()
	return sr.RetryNextDue(time.Now())
}

func (sr *summaryRetrier) QueueForRetry(
	accountId int,
	statusId, articleText string,
	now time.Time,
) {
	// With no summarizer configured a missing summary
	// is the expected outcome, so there is nothing to
	// retry. Same for an article with no text: no
	// number of retries will summarize nothing.
	if !sr.summarizer.IsEnabled() {
		return
	}
	if strings.TrimSpace(articleText) == "" {
		return
	}
	ps := dal.PendingSummary{
		AccountId:    accountId,
		StatusId:     statusId,
		ArticleText:  sr.summarizer.TrimForSummary(articleText),
		Attempts:     0,
		NextRetryDue: now.Add(summaryRetryBackoff[0]),
		State:        dal.PsPending,
	}
	if err := sr.repo.AddPendingSummaryIfNew(&ps); err != nil {
		sr.logger.Errorf(
			"Failed to queue summary retry for %s: %v",
			statusId, err)
		return
	}
	sr.logger.Infof("Queued summary retry for %s", statusId)
}

func (sr *summaryRetrier) RetryNextDue(now time.Time) bool {

	ps, err := sr.repo.GetPendingSummaryToRetry(now)
	if err != nil {
		sr.logger.Errorf(
			"Failed to read pending summaries: %v", err)
		return false
	}
	if ps == nil {
		return false
	}

	summary := strings.TrimSpace(sr.summarizer.Summarize(ps.ArticleText))
	if summary == "" {
		return sr.rescheduleOrAbandon(ps, now)
	}

	// The toot can be gone by now: accounts with no
	// followers get purged, and so do old posts. There
	// is then nothing left to fill in.
	toot, err := sr.repo.GetToot(ps.StatusId)
	if err != nil {
		sr.logger.Errorf("Failed to read toot %s: %v",
			ps.StatusId, err)
		return sr.rescheduleOrAbandon(ps, now)
	}
	if toot == nil {
		sr.logger.Infof(
			"Toot %s is gone; abandoning its summary",
			ps.StatusId)
		return sr.finish(ps.StatusId, dal.PsAbandoned)
	}

	content := shared.InsertSummaryHtml(toot.Content, summary)
	if err = sr.repo.UpdateTootContent(ps.StatusId, content); err != nil {
		sr.logger.Errorf(
			"Failed to store late summary for %s: %v",
			ps.StatusId, err)
		return sr.rescheduleOrAbandon(ps, now)
	}
	sr.logger.Infof("Stored late summary for %s", ps.StatusId)
	return sr.finish(ps.StatusId, dal.PsDone)
}

// rescheduleOrAbandon books a failed attempt: either a
// later slot with a longer backoff, or the end of the
// line once the retry cap is spent. Reports whether the
// row was actually moved on.
func (sr *summaryRetrier) rescheduleOrAbandon(
	ps *dal.PendingSummary,
	now time.Time,
) bool {
	attempts := ps.Attempts + 1
	if attempts >= maxSummaryRetries {
		sr.logger.Infof(
			"Giving up on summary for %s after %d retries",
			ps.StatusId, attempts)
		return sr.finish(ps.StatusId, dal.PsAbandoned)
	}
	due := now.Add(summaryRetryBackoff[attempts])
	if err := sr.repo.ReschedulePendingSummary(
		ps.StatusId, attempts, due,
	); err != nil {
		// The row is still due, so coming straight back
		// would spin on it. Report no progress and let
		// the loop sleep first.
		sr.logger.Errorf(
			"Failed to reschedule summary for %s: %v",
			ps.StatusId, err)
		return false
	}
	return true
}

// finish closes a row out in a terminal state, and
// reports whether that write landed.
func (sr *summaryRetrier) finish(
	statusId string,
	state dal.PendingSummaryState,
) bool {
	if err := sr.repo.FinishPendingSummary(statusId, state); err != nil {
		sr.logger.Errorf(
			"Failed to close out summary for %s: %v",
			statusId, err)
		return false
	}
	return true
}
