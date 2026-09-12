package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"rss_parrot/dal"
	"rss_parrot/shared"
	"rss_parrot/test/mocks"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

type dummyObs struct{}

func (o *dummyObs) Finish() {}

func Test_WebHandlers_GetStatus(t *testing.T) {
	if _, err := os.Stat("www"); os.IsNotExist(err) {
		if _, err := os.Stat("../www"); err == nil {
			_ = os.Chdir("..")
		}
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockRepo := mocks.NewMockIRepo(ctrl)
	mockLogger := mocks.NewMockILogger(ctrl)
	mockMetrics := mocks.NewMockIMetrics(ctrl)

	mockLogger.EXPECT().Printf(gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Infof(gomock.Any(), gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
	mockMetrics.EXPECT().StartWebRequestIn(gomock.Any()).Return(&dummyObs{}).AnyTimes()

	cfg := &shared.Config{
		Host: "parrot.test",
		Birb: &shared.UserInfo{User: "birb"},
	}

	now := time.Now().UTC()
	items := []*dal.FeedHealthItem{
		{
			Id:              1,
			CreatedAt:       now.Add(-10 * 24 * time.Hour),
			Handle:          "erroring.test",
			FeedName:        "Erroring Feed",
			FeedUrl:         "https://erroring.test/feed.xml",
			SiteUrl:         "https://erroring.test",
			FeedLastUpdated: now.Add(-2 * 24 * time.Hour),
			NextCheckDue:    now.Add(time.Hour),
			ErrorCount:      3,
			LastError:       "HTTP 500 Internal Server Error",
			FollowerCount:   2,
			IsStale:         false,
		},
		{
			Id:              2,
			CreatedAt:       now.Add(-20 * 24 * time.Hour),
			Handle:          "stale.test",
			FeedName:        "Stale Feed",
			FeedUrl:         "https://stale.test/feed.xml",
			SiteUrl:         "https://stale.test",
			FeedLastUpdated: now.Add(-14 * 24 * time.Hour),
			NextCheckDue:    now.Add(time.Hour),
			ErrorCount:      0,
			LastError:       "",
			FollowerCount:   1,
			IsStale:         true,
		},
		{
			Id:              3,
			CreatedAt:       now.Add(-5 * 24 * time.Hour),
			Handle:          "healthy.test",
			FeedName:        "Healthy Feed",
			FeedUrl:         "https://healthy.test/feed.xml",
			SiteUrl:         "https://healthy.test",
			FeedLastUpdated: now.Add(-2 * time.Hour),
			NextCheckDue:    now.Add(30 * time.Minute),
			ErrorCount:      0,
			LastError:       "",
			FollowerCount:   5,
			IsStale:         false,
		},
	}

	mockRepo.EXPECT().GetFeedHealthStatus("birb").Return(items, nil).AnyTimes()

	hg := NewWebHandlerGroup(cfg, mockLogger, mockRepo, nil, mockMetrics)

	// 1. Test GET /web/status (all feeds)
	{
		req := httptest.NewRequest("GET", "/web/status", nil)
		rec := httptest.NewRecorder()

		router := NewMux([]IHandlerGroup{hg}, mockLogger)
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		body := rec.Body.String()
		assert.Contains(t, body, "Feed Health Status")
		assert.Contains(t, body, "Total Feeds")
		assert.Contains(t, body, "erroring.test")
		assert.Contains(t, body, "stale.test")
		assert.Contains(t, body, "healthy.test")
		assert.Contains(t, body, "HTTP 500 Internal Server Error")
	}

	// 2. Test GET /web/status?filter=errors
	{
		req := httptest.NewRequest("GET", "/web/status?filter=errors", nil)
		rec := httptest.NewRecorder()

		router := NewMux([]IHandlerGroup{hg}, mockLogger)
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		body := rec.Body.String()
		assert.Contains(t, body, "erroring.test")
		assert.NotContains(t, body, "healthy.test")
	}

	// 3. Test GET /web/status?filter=stale
	{
		req := httptest.NewRequest("GET", "/web/status?filter=stale", nil)
		rec := httptest.NewRecorder()

		router := NewMux([]IHandlerGroup{hg}, mockLogger)
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		body := rec.Body.String()
		assert.Contains(t, body, "stale.test")
		assert.NotContains(t, body, "healthy.test")
	}

	// 4. Test GET /web/status?filter=healthy
	{
		req := httptest.NewRequest("GET", "/web/status?filter=healthy", nil)
		rec := httptest.NewRecorder()

		router := NewMux([]IHandlerGroup{hg}, mockLogger)
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		body := rec.Body.String()
		assert.Contains(t, body, "healthy.test")
		assert.NotContains(t, body, "erroring.test")
	}

	// 5. Test GET /status redirect to /web/status
	{
		req := httptest.NewRequest("GET", "/status", nil)
		rec := httptest.NewRecorder()

		router := NewMux([]IHandlerGroup{hg}, mockLogger)
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusFound, rec.Code)
		assert.Equal(t, "/web/status", rec.Header().Get("Location"))
	}
}
