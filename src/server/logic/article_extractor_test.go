package logic

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"rss_parrot/shared"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

// silentLogger embeds the interface so only the one
// method the extractor uses needs a body; anything else
// panics loudly rather than passing silently.
type silentLogger struct {
	shared.ILogger
}

func (l *silentLogger) Infof(format string, args ...interface{}) {}

// articleBody is long enough for readability to accept
// the page as an article rather than a stub.
const articleBody = "The harbour master kept a ledger of every " +
	"ship that came in on the morning tide, and the ledger " +
	"outlived him by ninety years. It is the only reason we " +
	"know what the port traded in, and with whom, across four " +
	"decades of a century that otherwise left no accounts at " +
	"all. Historians have read it as a customs record, as a " +
	"weather diary, and lately as something closer to a "

func articlePage(body string) string {
	return `<html><head><title>Harbour Ledger</title></head>` +
		`<body><nav><a href="/">Home</a></nav>` +
		`<article><h1>Harbour Ledger</h1><p>` + body +
		`</p><p>` + body + `</p></article>` +
		`<footer>Copyright</footer></body></html>`
}

// countingArticleServer serves one article page and
// counts how many times it was asked for.
func countingArticleServer(
	t *testing.T,
	handler http.HandlerFunc,
) (*httptest.Server, *atomic.Int32) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			handler(w, r)
		}))
	t.Cleanup(ts.Close)
	return ts, &hits
}

func okArticleHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, articlePage(articleBody))
}

// newTestExtractor builds an extractor whose HTTP
// client may talk to the test server on loopback, which
// the production client refuses by design.
func newTestExtractor(enabled bool) *articleExtractor {
	return newArticleExtractor(
		&shared.Config{ExtractFullText: enabled},
		&silentLogger{},
		&dummyUserAgent{},
		&http.Client{Timeout: 5 * time.Second},
		time.Hour, 8, 0)
}

func Test_Extractor_PullsArticleBodyOffThePage(t *testing.T) {

	ts, hits := countingArticleServer(t, okArticleHandler)
	ae := newTestExtractor(true)

	text := ae.Extract(ts.URL + "/article")

	assert.Contains(t, text, "harbour master kept a ledger")
	// Readability strips the page furniture around it.
	assert.NotContains(t, text, "Copyright")
	assert.Equal(t, int32(1), hits.Load())
}

func Test_Extractor_Disabled_MakesNoRequest(t *testing.T) {

	ts, hits := countingArticleServer(t, okArticleHandler)
	ae := newTestExtractor(false)

	assert.Equal(t, "", ae.Extract(ts.URL+"/article"))
	assert.Equal(t, int32(0), hits.Load())
}

func Test_Extractor_CachesResultAcrossFeeds(t *testing.T) {

	ts, hits := countingArticleServer(t, okArticleHandler)
	ae := newTestExtractor(true)

	first := ae.Extract(ts.URL + "/article")
	second := ae.Extract(ts.URL + "/article")

	assert.Equal(t, first, second)
	assert.NotEqual(t, "", first)
	// The same article in a second feed costs no
	// second download.
	assert.Equal(t, int32(1), hits.Load())
}

func Test_Extractor_CachesFailureToo(t *testing.T) {

	ts, hits := countingArticleServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		})
	ae := newTestExtractor(true)

	assert.Equal(t, "", ae.Extract(ts.URL+"/article"))
	assert.Equal(t, "", ae.Extract(ts.URL+"/article"))
	// A site that walls us off is not asked twice.
	assert.Equal(t, int32(1), hits.Load())
}

func Test_Extractor_RefusesNonHtml(t *testing.T) {

	ts, _ := countingArticleServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/pdf")
			fmt.Fprint(w, strings.Repeat("x", 1000))
		})
	ae := newTestExtractor(true)

	assert.Equal(t, "", ae.Extract(ts.URL+"/paper.pdf"))
}

func Test_Extractor_IgnoresPageWithTooLittleText(t *testing.T) {

	ts, _ := countingArticleServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w,
				`<html><body><article><p>Too short.</p>`+
					`</article></body></html>`)
		})
	ae := newTestExtractor(true)

	assert.Equal(t, "", ae.Extract(ts.URL+"/stub"))
}

func Test_Extractor_RejectsUnusableLinks(t *testing.T) {

	ae := newTestExtractor(true)

	assert.Equal(t, "", ae.Extract(""))
	assert.Equal(t, "", ae.Extract("ftp://x.test/article"))
	assert.Equal(t, "", ae.Extract("mailto:someone@x.test"))
	assert.Equal(t, "", ae.Extract("/relative/path"))
}

// A feed item is untrusted input, so a link pointing at
// something on the parrot's own network must not be
// fetched — its response would end up summarized into a
// public toot.
func Test_Extractor_WillNotReachInternalAddresses(t *testing.T) {

	ts, hits := countingArticleServer(t, okArticleHandler)
	ae := newArticleExtractor(
		&shared.Config{ExtractFullText: true},
		&silentLogger{},
		&dummyUserAgent{},
		newPublicWebClient(5*time.Second),
		time.Hour, 8, 0)

	assert.Equal(t, "", ae.Extract(ts.URL+"/article"))
	assert.Equal(t, int32(0), hits.Load())
}

func Test_RefuseNonPublicAddress(t *testing.T) {

	blocked := []string{
		"127.0.0.1:80",       // loopback
		"[::1]:80",           // loopback, v6
		"10.0.0.5:80",        // RFC 1918
		"192.168.1.20:80",    // RFC 1918
		"169.254.169.254:80", // link-local metadata
		"100.100.0.1:80",     // RFC 6598 shared space
		"0.0.0.0:80",         // unspecified
	}
	for _, addr := range blocked {
		assert.Error(t,
			refuseNonPublicAddress("tcp", addr, nil), addr)
	}

	allowed := []string{"93.184.216.34:80", "[2606:2800::1]:443"}
	for _, addr := range allowed {
		assert.NoError(t,
			refuseNonPublicAddress("tcp", addr, nil), addr)
	}
}

func Test_Extractor_CutsVeryLongArticle(t *testing.T) {

	// A long body, in a multi-byte script, so a naive
	// byte cut would leave a broken rune behind.
	long := strings.Repeat("ăâîșț ", 20000)
	ts, _ := countingArticleServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, articlePage(articleBody+long))
		})
	ae := newTestExtractor(true)

	text := ae.Extract(ts.URL + "/long")

	assert.LessOrEqual(t, len(text), maxExtractedLen)
	// ...and the cut really happened, at the very end
	// of the budget.
	assert.Greater(t, len(text), maxExtractedLen-8)
	assert.True(t, utf8.ValidString(text))
}

func Test_Extractor_DetectsHtmlLang(t *testing.T) {
	page := `<html lang="es"><head><title>Título</title></head>` +
		`<body><article><h1>Título</h1><p>` + articleBody +
		`</p><p>` + articleBody + `</p></article></body></html>`

	ts, _ := countingArticleServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, page)
		})
	ae := newTestExtractor(true)

	art := ae.ExtractArticle(ts.URL + "/spanish")
	assert.NotEmpty(t, art.Text)
	assert.Equal(t, "Spanish", art.Language)
}

func Test_Extractor_DetectsHttpContentLanguage(t *testing.T) {
	ts, _ := countingArticleServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Content-Language", "ro-RO")
			fmt.Fprint(w, articlePage(articleBody))
		})
	ae := newTestExtractor(true)

	art := ae.ExtractArticle(ts.URL + "/romanian")
	assert.NotEmpty(t, art.Text)
	assert.Equal(t, "Romanian", art.Language)
}

func Test_Extractor_HtmlLangBeatsHttpHeader(t *testing.T) {
	page := `<html lang="es"><head><title>Título</title></head>` +
		`<body><article><h1>Título</h1><p>` + articleBody +
		`</p><p>` + articleBody + `</p></article></body></html>`

	ts, _ := countingArticleServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Content-Language", "fr")
			fmt.Fprint(w, page)
		})
	ae := newTestExtractor(true)

	art := ae.ExtractArticle(ts.URL + "/spanish-beats-french")
	assert.Equal(t, "Spanish", art.Language)
}

func Test_Extractor_DetectsContentLanguageFallback(t *testing.T) {
	germanBody := "Dies ist ein ausführlicher deutscher Artikel über " +
		"Softwareentwicklung, Cloud-Systeme und verteilte Datenbanken in Europa. " +
		"Der Text enthält genügend Wörter und Struktur für eine verlässliche " +
		"automatische Erkennung der natürlichen Sprache durch den Algorithmus."

	ts, _ := countingArticleServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, articlePage(germanBody))
		})
	ae := newTestExtractor(true)

	art := ae.ExtractArticle(ts.URL + "/german")
	assert.NotEmpty(t, art.Text)
	assert.Equal(t, "German", art.Language)
}

func Test_Extractor_CachesLanguageWithText(t *testing.T) {
	page := `<html lang="fr"><head><title>Titre</title></head>` +
		`<body><article><h1>Titre</h1><p>` + articleBody +
		`</p><p>` + articleBody + `</p></article></body></html>`

	ts, hits := countingArticleServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, page)
		})
	ae := newTestExtractor(true)

	art1 := ae.ExtractArticle(ts.URL + "/french")
	assert.Equal(t, "French", art1.Language)
	assert.Equal(t, int32(1), hits.Load())

	// Second fetch must hit cache and preserve language
	art2 := ae.ExtractArticle(ts.URL + "/french")
	assert.Equal(t, art1.Text, art2.Text)
	assert.Equal(t, "French", art2.Language)
	assert.Equal(t, int32(1), hits.Load())
}
