package logic

import (
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

type dummyUserAgent struct{}

func (d *dummyUserAgent) AddUserAgent(req *http.Request) {}

func Test_FeedFollower_DiscoverSiteIcon(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/apple", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><link rel="apple-touch-icon" href="/apple-icon.png"></head></html>`)
	})
	mux.HandleFunc("/icon", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><link rel="icon" href="/favicon.png"></head></html>`)
	})
	mux.HandleFunc("/none", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head></head></html>`)
	})
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" {
			w.WriteHeader(http.StatusOK)
		}
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	ff := &feedFollower{
		userAgent: &dummyUserAgent{},
	}

	t.Run("apple-touch-icon", func(t *testing.T) {
		u, _ := url.Parse(ts.URL + "/apple")
		res, _ := http.Get(ts.URL + "/apple")
		defer res.Body.Close()
		doc, _ := goquery.NewDocumentFromReader(res.Body)
		icon := ff.discoverSiteIcon(u, doc)
		assert.Equal(t, ts.URL+"/apple-icon.png", icon)
	})

	t.Run("icon", func(t *testing.T) {
		u, _ := url.Parse(ts.URL + "/icon")
		res, _ := http.Get(ts.URL + "/icon")
		defer res.Body.Close()
		doc, _ := goquery.NewDocumentFromReader(res.Body)
		icon := ff.discoverSiteIcon(u, doc)
		assert.Equal(t, ts.URL+"/favicon.png", icon)
	})

	t.Run("favicon fallback", func(t *testing.T) {
		u, _ := url.Parse(ts.URL + "/none")
		res, _ := http.Get(ts.URL + "/none")
		defer res.Body.Close()
		doc, _ := goquery.NewDocumentFromReader(res.Body)
		icon := ff.discoverSiteIcon(u, doc)
		assert.Equal(t, ts.URL+"/favicon.ico", icon)
	})

	t.Run("no doc, favicon exists", func(t *testing.T) {
		u, _ := url.Parse(ts.URL + "/whatever")
		icon := ff.discoverSiteIcon(u, nil)
		assert.Equal(t, ts.URL+"/favicon.ico", icon)
	})
}
