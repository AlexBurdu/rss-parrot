package test

import (
	"errors"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"rss_parrot/dal"
	"rss_parrot/logic"
	"rss_parrot/test/mocks"
	"testing"
	"time"
)

const (
	retryStatusId    = "https://parrot.test/u/x.test/status/42"
	retryArticleText = "Article body to summarize."
	retryTootContent = `<p><strong>T</strong></p>` +
		`<p><a href="https://x.test/a">x.test/a</a></p>` +
		`<p>Desc.</p>`
)

var retryNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

type summaryRetrierHarness struct {
	mockLogger     *mocks.MockILogger
	mockRepo       *mocks.MockIRepo
	mockSummarizer *mocks.MockISummarizer
}

func setupSummaryRetrierTest(
	t *testing.T,
) (*gomock.Controller, *summaryRetrierHarness, logic.ISummaryRetrier) {

	ctrl := gomock.NewController(t)
	h := &summaryRetrierHarness{
		mockLogger:     mocks.NewMockILogger(ctrl),
		mockRepo:       mocks.NewMockIRepo(ctrl),
		mockSummarizer: mocks.NewMockISummarizer(ctrl),
	}
	setupDummyLogger(h.mockLogger)
	sr := logic.NewSummaryRetrier(h.mockLogger, h.mockRepo, h.mockSummarizer)
	return ctrl, h, sr
}

// pendingRow is a queued article that has already had
// the given number of background retries.
func pendingRow(attempts int) *dal.PendingSummary {
	return &dal.PendingSummary{
		AccountId:    7,
		StatusId:     retryStatusId,
		ArticleText:  retryArticleText,
		Attempts:     attempts,
		NextRetryDue: retryNow.Add(-time.Minute),
		State:        dal.PsPending,
	}
}

func Test_SummaryRetrier_QueuesArticleWhenSummarizerEnabled(t *testing.T) {

	ctrl, h, sr := setupSummaryRetrierTest(t)
	defer ctrl.Finish()

	h.mockSummarizer.EXPECT().IsEnabled().Return(true)
	h.mockSummarizer.EXPECT().TrimForSummary(retryArticleText).
		Return(retryArticleText)
	h.mockRepo.EXPECT().AddPendingSummaryIfNew(gomock.Any()).
		DoAndReturn(func(ps *dal.PendingSummary) error {
			assert.Equal(t, 7, ps.AccountId)
			assert.Equal(t, retryStatusId, ps.StatusId)
			assert.Equal(t, retryArticleText, ps.ArticleText)
			assert.Equal(t, 0, ps.Attempts)
			assert.Equal(t, dal.PsPending, ps.State)
			// First retry is due after the shortest backoff.
			assert.Equal(t, retryNow.Add(15*time.Minute), ps.NextRetryDue)
			return nil
		})

	sr.QueueForRetry(7, retryStatusId, retryArticleText, retryNow)
}

func Test_SummaryRetrier_DoesNotQueueWhenSummarizerDisabled(t *testing.T) {

	ctrl, h, sr := setupSummaryRetrierTest(t)
	defer ctrl.Finish()

	h.mockSummarizer.EXPECT().IsEnabled().Return(false)

	sr.QueueForRetry(7, retryStatusId, retryArticleText, retryNow)
}

func Test_SummaryRetrier_DoesNotQueueEmptyArticle(t *testing.T) {

	ctrl, h, sr := setupSummaryRetrierTest(t)
	defer ctrl.Finish()

	h.mockSummarizer.EXPECT().IsEnabled().Return(true).AnyTimes()

	sr.QueueForRetry(7, retryStatusId, "   ", retryNow)
}

func Test_SummaryRetrier_NoWorkWhenNothingDue(t *testing.T) {

	ctrl, h, sr := setupSummaryRetrierTest(t)
	defer ctrl.Finish()

	h.mockRepo.EXPECT().GetPendingSummaryToRetry(retryNow).
		Return(nil, nil)

	assert.False(t, sr.RetryNextDue(retryNow))
}

func Test_SummaryRetrier_SuccessRewritesTootAndMarksDone(t *testing.T) {

	ctrl, h, sr := setupSummaryRetrierTest(t)
	defer ctrl.Finish()

	h.mockRepo.EXPECT().GetPendingSummaryToRetry(retryNow).
		Return(pendingRow(0), nil)
	h.mockSummarizer.EXPECT().Summarize(retryArticleText).
		Return("  Late summary.  ")
	h.mockRepo.EXPECT().GetToot(retryStatusId).
		Return(&dal.Toot{StatusId: retryStatusId, Content: retryTootContent}, nil)
	h.mockRepo.EXPECT().UpdateTootContent(retryStatusId, gomock.Any()).
		DoAndReturn(func(statusId, content string) error {
			assert.Contains(t, content, "<p><em>Late summary.</em></p>")
			assert.Contains(t, content, "<p>Desc.</p>")
			return nil
		})
	h.mockRepo.EXPECT().FinishPendingSummary(retryStatusId, dal.PsDone).
		Return(nil)

	assert.True(t, sr.RetryNextDue(retryNow))
}

func Test_SummaryRetrier_FailureRescheduleWithLongerBackoff(t *testing.T) {

	ctrl, h, sr := setupSummaryRetrierTest(t)
	defer ctrl.Finish()

	h.mockRepo.EXPECT().GetPendingSummaryToRetry(retryNow).
		Return(pendingRow(0), nil)
	h.mockSummarizer.EXPECT().Summarize(retryArticleText).Return("")
	h.mockRepo.EXPECT().ReschedulePendingSummary(
		retryStatusId, 1, retryNow.Add(60*time.Minute)).Return(nil)

	assert.True(t, sr.RetryNextDue(retryNow))
}

func Test_SummaryRetrier_AbandonsAfterRetryCap(t *testing.T) {

	ctrl, h, sr := setupSummaryRetrierTest(t)
	defer ctrl.Finish()

	// Two retries already made; this third one fails,
	// which exhausts the cap.
	h.mockRepo.EXPECT().GetPendingSummaryToRetry(retryNow).
		Return(pendingRow(2), nil)
	h.mockSummarizer.EXPECT().Summarize(retryArticleText).Return("")
	h.mockRepo.EXPECT().FinishPendingSummary(
		retryStatusId, dal.PsAbandoned).Return(nil)

	assert.True(t, sr.RetryNextDue(retryNow))
}

func Test_SummaryRetrier_AbandonsWhenTootIsGone(t *testing.T) {

	ctrl, h, sr := setupSummaryRetrierTest(t)
	defer ctrl.Finish()

	h.mockRepo.EXPECT().GetPendingSummaryToRetry(retryNow).
		Return(pendingRow(0), nil)
	h.mockSummarizer.EXPECT().Summarize(retryArticleText).
		Return("Late summary.")
	h.mockRepo.EXPECT().GetToot(retryStatusId).Return(nil, nil)
	h.mockRepo.EXPECT().FinishPendingSummary(
		retryStatusId, dal.PsAbandoned).Return(nil)

	assert.True(t, sr.RetryNextDue(retryNow))
}

func Test_SummaryRetrier_ReadErrorDoesNotPanic(t *testing.T) {

	ctrl, h, sr := setupSummaryRetrierTest(t)
	defer ctrl.Finish()

	h.mockRepo.EXPECT().GetPendingSummaryToRetry(retryNow).
		Return(nil, errors.New("db is busy"))

	assert.False(t, sr.RetryNextDue(retryNow))
}

func Test_SummaryRetrier_ReportsNoProgressWhenRescheduleFails(t *testing.T) {

	ctrl, h, sr := setupSummaryRetrierTest(t)
	defer ctrl.Finish()

	h.mockRepo.EXPECT().GetPendingSummaryToRetry(retryNow).
		Return(pendingRow(0), nil)
	h.mockSummarizer.EXPECT().Summarize(retryArticleText).Return("")
	h.mockRepo.EXPECT().ReschedulePendingSummary(
		retryStatusId, 1, retryNow.Add(60*time.Minute)).
		Return(errors.New("db is busy"))

	// The row is still due, so the caller must wait
	// instead of spinning straight back onto it.
	assert.False(t, sr.RetryNextDue(retryNow))
}

func Test_SummaryRetrier_StoreFailureSchedulesAnotherAttempt(t *testing.T) {

	ctrl, h, sr := setupSummaryRetrierTest(t)
	defer ctrl.Finish()

	h.mockRepo.EXPECT().GetPendingSummaryToRetry(retryNow).
		Return(pendingRow(0), nil)
	h.mockSummarizer.EXPECT().Summarize(retryArticleText).
		Return("Late summary.")
	h.mockRepo.EXPECT().GetToot(retryStatusId).
		Return(&dal.Toot{StatusId: retryStatusId, Content: retryTootContent}, nil)
	h.mockRepo.EXPECT().UpdateTootContent(retryStatusId, gomock.Any()).
		Return(errors.New("db is busy"))
	h.mockRepo.EXPECT().ReschedulePendingSummary(
		retryStatusId, 1, retryNow.Add(60*time.Minute)).Return(nil)

	assert.True(t, sr.RetryNextDue(retryNow))
}
