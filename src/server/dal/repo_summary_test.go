package dal

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"rss_parrot/shared"
	"testing"
	"time"
)

// nullLogger keeps InitUpdateDb's progress chatter out
// of the test output.
type nullLogger struct {
	shared.ILogger
}

func (l *nullLogger) Printf(format string, v ...interface{}) {}
func (l *nullLogger) Errorf(format string, v ...interface{}) {}

// newTestRepo brings up a real SQLite file and runs
// every migration script on it, so the toot_summaries
// schema is exercised exactly as it will be on rpi.
func newTestRepo(t *testing.T) IRepo {
	cfg := &shared.Config{
		Host:   "parrot.test",
		DbFile: filepath.Join(t.TempDir(), "test.db"),
		// InitUpdateDb seeds the birb account, so the
		// config needs one even in a schema-only test.
		Birb: &shared.UserInfo{
			User:      "birb",
			Published: time.Now().UTC(),
		},
	}
	repo := NewRepo(cfg, &nullLogger{})
	repo.InitUpdateDb()
	return repo
}

func addTestToot(t *testing.T, repo IRepo, statusId, content string) {
	err := repo.AddToot(1, &Toot{
		PostGuidHash: 123,
		TootedAt:     time.Now().UTC(),
		StatusId:     statusId,
		Content:      content,
	})
	require.NoError(t, err)
}

func Test_Repo_PendingSummaryRoundTrip(t *testing.T) {

	repo := newTestRepo(t)
	now := time.Now().UTC().Truncate(time.Second)
	statusId := "https://parrot.test/u/x.test/status/1"

	err := repo.AddPendingSummaryIfNew(&PendingSummary{
		AccountId:    1,
		StatusId:     statusId,
		ArticleText:  "Body.",
		Attempts:     0,
		NextRetryDue: now.Add(-time.Minute),
		State:        PsPending,
	})
	require.NoError(t, err)

	ps, err := repo.GetPendingSummaryToRetry(now)
	require.NoError(t, err)
	require.NotNil(t, ps)
	assert.Equal(t, statusId, ps.StatusId)
	assert.Equal(t, "Body.", ps.ArticleText)
	assert.Equal(t, 0, ps.Attempts)
	assert.Equal(t, PsPending, ps.State)
}

func Test_Repo_PendingSummaryIgnoresDuplicateStatus(t *testing.T) {

	repo := newTestRepo(t)
	now := time.Now().UTC()
	statusId := "https://parrot.test/u/x.test/status/2"

	first := &PendingSummary{
		AccountId: 1, StatusId: statusId, ArticleText: "First.",
		Attempts: 2, NextRetryDue: now.Add(-time.Minute), State: PsPending,
	}
	require.NoError(t, repo.AddPendingSummaryIfNew(first))

	// A re-queue must not reset the spent retry count.
	second := *first
	second.ArticleText = "Second."
	second.Attempts = 0
	require.NoError(t, repo.AddPendingSummaryIfNew(&second))

	ps, err := repo.GetPendingSummaryToRetry(now)
	require.NoError(t, err)
	require.NotNil(t, ps)
	assert.Equal(t, "First.", ps.ArticleText)
	assert.Equal(t, 2, ps.Attempts)
}

func Test_Repo_PendingSummaryNotDueYet(t *testing.T) {

	repo := newTestRepo(t)
	now := time.Now().UTC()

	require.NoError(t, repo.AddPendingSummaryIfNew(&PendingSummary{
		AccountId: 1, StatusId: "https://parrot.test/u/x.test/status/3",
		ArticleText: "Body.", NextRetryDue: now.Add(time.Hour),
		State: PsPending,
	}))

	ps, err := repo.GetPendingSummaryToRetry(now)
	require.NoError(t, err)
	assert.Nil(t, ps)
}

func Test_Repo_RescheduleAndFinishPendingSummary(t *testing.T) {

	repo := newTestRepo(t)
	now := time.Now().UTC().Truncate(time.Second)
	statusId := "https://parrot.test/u/x.test/status/4"

	require.NoError(t, repo.AddPendingSummaryIfNew(&PendingSummary{
		AccountId: 1, StatusId: statusId, ArticleText: "Body.",
		NextRetryDue: now.Add(-time.Minute), State: PsPending,
	}))

	later := now.Add(time.Hour)
	require.NoError(t, repo.ReschedulePendingSummary(statusId, 1, later))

	ps, err := repo.GetPendingSummaryToRetry(now)
	require.NoError(t, err)
	assert.Nil(t, ps, "rescheduled row must not be due yet")

	ps, err = repo.GetPendingSummaryToRetry(later)
	require.NoError(t, err)
	require.NotNil(t, ps)
	assert.Equal(t, 1, ps.Attempts)

	// A finished row drops out of the queue for good.
	require.NoError(t, repo.FinishPendingSummary(statusId, PsDone))
	ps, err = repo.GetPendingSummaryToRetry(later.Add(time.Hour))
	require.NoError(t, err)
	assert.Nil(t, ps)
}

func Test_Repo_UpdateTootContent(t *testing.T) {

	repo := newTestRepo(t)
	statusId := "https://parrot.test/u/x.test/status/5"
	addTestToot(t, repo, statusId, "<p>Before.</p>")

	require.NoError(t, repo.UpdateTootContent(statusId, "<p>After.</p>"))

	toot, err := repo.GetToot(statusId)
	require.NoError(t, err)
	require.NotNil(t, toot)
	assert.Equal(t, "<p>After.</p>", toot.Content)
}

func Test_Repo_PurgeAlsoDropsPendingSummaries(t *testing.T) {

	repo := newTestRepo(t)
	now := time.Now().UTC()
	statusId := "https://parrot.test/u/x.test/status/6"
	addTestToot(t, repo, statusId, "<p>Old.</p>")
	require.NoError(t, repo.AddPendingSummaryIfNew(&PendingSummary{
		AccountId: 1, StatusId: statusId, ArticleText: "Body.",
		NextRetryDue: now.Add(-time.Minute), State: PsPending,
	}))

	require.NoError(t, repo.PurgePostsAndToots(1, now.Add(time.Hour)))

	ps, err := repo.GetPendingSummaryToRetry(now)
	require.NoError(t, err)
	assert.Nil(t, ps, "purged toot must not leave an orphan retry row")
}
