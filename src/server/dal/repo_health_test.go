package dal

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func Test_Repo_FeedHealth_MigrationAndDefaultValues(t *testing.T) {
	repo := newTestRepo(t)

	// Verify schema version is 9
	r := repo.(*Repo)
	var ver int
	row := r.db.QueryRow("SELECT val FROM sys_params WHERE name='schema_ver'")
	require.NoError(t, row.Scan(&ver))
	assert.Equal(t, 9, ver)

	// Add an account and check initial ErrorCount and LastError
	now := time.Now().UTC()
	acct := &Account{
		CreatedAt:       now,
		UserUrl:         "https://parrot.test/u/feed1.test",
		Handle:          "feed1.test",
		FeedName:        "Test Feed 1",
		SiteUrl:         "https://feed1.test",
		FeedUrl:         "https://feed1.test/rss",
		FeedLastUpdated: now,
		PubKey:          "pubkey1",
	}
	isNew, err := repo.AddAccountIfNotExist(acct, "privkey1")
	require.NoError(t, err)
	assert.True(t, isNew)

	loaded, err := repo.GetAccount("feed1.test")
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, 0, loaded.ErrorCount)
	assert.Equal(t, "", loaded.LastError)
}

func Test_Repo_RecordFeedCheckError_And_Reset(t *testing.T) {
	repo := newTestRepo(t)
	now := time.Now().UTC()

	acct := &Account{
		CreatedAt:       now,
		UserUrl:         "https://parrot.test/u/feed_err.test",
		Handle:          "feed_err.test",
		FeedName:        "Erroring Feed",
		SiteUrl:         "https://feed_err.test",
		FeedUrl:         "https://feed_err.test/rss",
		FeedLastUpdated: now,
		PubKey:          "pubkey_err",
	}
	_, err := repo.AddAccountIfNotExist(acct, "privkey")
	require.NoError(t, err)

	loaded, err := repo.GetAccount("feed_err.test")
	require.NoError(t, err)

	// 1. Record first error
	nextDue1 := now.Add(15 * time.Minute)
	err = repo.RecordFeedCheckError(loaded.Id, "connection refused", nextDue1)
	require.NoError(t, err)

	loaded, err = repo.GetAccount("feed_err.test")
	require.NoError(t, err)
	assert.Equal(t, 1, loaded.ErrorCount)
	assert.Equal(t, "connection refused", loaded.LastError)

	// 2. Record second error
	nextDue2 := now.Add(30 * time.Minute)
	err = repo.RecordFeedCheckError(loaded.Id, "HTTP 503 Service Unavailable", nextDue2)
	require.NoError(t, err)

	loaded, err = repo.GetAccount("feed_err.test")
	require.NoError(t, err)
	assert.Equal(t, 2, loaded.ErrorCount)
	assert.Equal(t, "HTTP 503 Service Unavailable", loaded.LastError)

	// 3. Successful feed update resets error_count and last_error
	newLastUpdated := now.Add(1 * time.Hour)
	nextDue3 := now.Add(2 * time.Hour)
	err = repo.UpdateAccountFeedTimes(loaded.Id, newLastUpdated, nextDue3)
	require.NoError(t, err)

	loaded, err = repo.GetAccount("feed_err.test")
	require.NoError(t, err)
	assert.Equal(t, 0, loaded.ErrorCount)
	assert.Equal(t, "", loaded.LastError)
	assert.Equal(t, newLastUpdated.Unix(), loaded.FeedLastUpdated.Unix())
}

func Test_Repo_GetFeedHealthStatus(t *testing.T) {
	repo := newTestRepo(t)
	now := time.Now().UTC()

	// 1. Healthy feed: updated 1 day ago, 0 errors
	healthyAcct := &Account{
		CreatedAt:       now.Add(-10 * 24 * time.Hour),
		UserUrl:         "https://parrot.test/u/healthy.test",
		Handle:          "healthy.test",
		FeedName:        "Healthy Feed",
		SiteUrl:         "https://healthy.test",
		FeedUrl:         "https://healthy.test/rss",
		FeedLastUpdated: now.Add(-24 * time.Hour),
		PubKey:          "pk_healthy",
	}
	_, err := repo.AddAccountIfNotExist(healthyAcct, "priv")
	require.NoError(t, err)
	hLoaded, _ := repo.GetAccount("healthy.test")
	err = repo.UpdateAccountFeedTimes(hLoaded.Id, now.Add(-24*time.Hour), now.Add(time.Hour))
	require.NoError(t, err)

	// 2. Stale feed: updated 10 days ago (> 7 days), 0 errors
	staleAcct := &Account{
		CreatedAt:       now.Add(-20 * 24 * time.Hour),
		UserUrl:         "https://parrot.test/u/stale.test",
		Handle:          "stale.test",
		FeedName:        "Stale Feed",
		SiteUrl:         "https://stale.test",
		FeedUrl:         "https://stale.test/rss",
		FeedLastUpdated: now.Add(-10 * 24 * time.Hour),
		PubKey:          "pk_stale",
	}
	_, err = repo.AddAccountIfNotExist(staleAcct, "priv")
	require.NoError(t, err)
	sLoaded, _ := repo.GetAccount("stale.test")
	err = repo.UpdateAccountFeedTimes(sLoaded.Id, now.Add(-10*24*time.Hour), now.Add(time.Hour))
	require.NoError(t, err)

	// 3. Erroring feed: has error_count > 0
	errorAcct := &Account{
		CreatedAt:       now.Add(-5 * 24 * time.Hour),
		UserUrl:         "https://parrot.test/u/erroring.test",
		Handle:          "erroring.test",
		FeedName:        "Erroring Feed",
		SiteUrl:         "https://erroring.test",
		FeedUrl:         "https://erroring.test/rss",
		FeedLastUpdated: now.Add(-2 * 24 * time.Hour),
		PubKey:          "pk_err",
	}
	_, err = repo.AddAccountIfNotExist(errorAcct, "priv")
	require.NoError(t, err)
	eLoaded, _ := repo.GetAccount("erroring.test")
	err = repo.RecordFeedCheckError(eLoaded.Id, "404 Not Found", now.Add(time.Hour))
	require.NoError(t, err)

	// Add followers to healthyAcct: 1 approved, 1 unapproved
	err = repo.AddFollower("healthy.test", &FollowerInfo{
		RequestId:     "req1",
		ApproveStatus: 1,
		UserUrl:       "https://mastodon.social/users/fan1",
		Handle:        "fan1",
		Host:          "mastodon.social",
		UserInbox:     "https://mastodon.social/users/fan1/inbox",
	})
	require.NoError(t, err)
	err = repo.AddFollower("healthy.test", &FollowerInfo{
		RequestId:     "req2",
		ApproveStatus: 0,
		UserUrl:       "https://mastodon.social/users/fan2",
		Handle:        "fan2",
		Host:          "mastodon.social",
		UserInbox:     "https://mastodon.social/users/fan2/inbox",
	})
	require.NoError(t, err)

	// Get health status (birb handle is "birb")
	items, err := repo.GetFeedHealthStatus("birb")
	require.NoError(t, err)

	// Should have 3 feeds (birb excluded)
	require.Len(t, items, 3)

	// Erroring feed should be first due to ORDER BY error_count DESC
	assert.Equal(t, "erroring.test", items[0].Handle)
	assert.Equal(t, 1, items[0].ErrorCount)
	assert.Equal(t, "404 Not Found", items[0].LastError)

	// Stale feed should be second (oldest feed_last_updated among error_count=0)
	assert.Equal(t, "stale.test", items[1].Handle)
	assert.True(t, items[1].IsStale)
	assert.Equal(t, 0, items[1].ErrorCount)

	// Healthy feed should be third
	assert.Equal(t, "healthy.test", items[2].Handle)
	assert.False(t, items[2].IsStale)
	assert.Equal(t, 0, items[2].ErrorCount)
	assert.Equal(t, uint(1), items[2].FollowerCount) // only approved follower counted
	assert.Equal(t, hLoaded.Id, items[2].Id)
}
