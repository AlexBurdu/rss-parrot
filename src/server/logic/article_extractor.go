package logic

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"rss_parrot/shared"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	readability "codeberg.org/readeck/go-readability/v2"
)

//go:generate mockgen --build_flags=--mod=mod -destination ../test/mocks/mock_article_extractor.go -package mocks rss_parrot/logic IArticleExtractor

// IArticleExtractor pulls an article's own body text
// off the page it lives on.
//
// Many feeds carry only a headline and a one-paragraph
// teaser, which is far too little for the summarizer to
// work with. For those, the article page itself is the
// only place the text exists.
type IArticleExtractor interface {
	// Extract returns the plain-text body of the
	// article at articleUrl, or an empty string when
	// extraction is disabled, refused, or fails for
	// any reason. Callers fall back to whatever the
	// feed itself gave them.
	Extract(articleUrl string) string

	// IsEnabled reports whether full-text extraction is
	// configured at all.
	IsEnabled() bool
}

const (
	// Whole-request budget for one article download.
	// Generous enough for a slow blog, short enough
	// that a hanging site cannot stall the feed check
	// loop for long.
	articleFetchTimeout = 20 * time.Second
	// Hard cap on the bytes read from an article page.
	// Anything past this is dropped, and the truncated
	// HTML is still parsed: the readable body is at
	// the top of any sane page.
	maxArticleBytes = 4 << 20
	// How long an extraction result stays usable.
	articleCacheTtl = 6 * time.Hour
	// How many articles the cache holds.
	articleCacheSize = 512
	// Minimum spacing between two downloads from the
	// same host.
	articleHostGap = 2 * time.Second
	// How many hosts the throttle tracks.
	throttleMaxHosts = 1024
	// Extractions shorter than this are treated as a
	// failure. Readability returns a stub for pages
	// that are mostly navigation, and a stub is worse
	// input for the summarizer than the feed's own
	// teaser.
	minExtractedLen = 200
	// Extracted text is cut to this length before it
	// is cached or handed on. It sits well above what
	// the summarizer's own input window uses, and
	// keeps a full cache down to megabytes rather than
	// tens of them.
	maxExtractedLen = 16 * 1024
)

type articleExtractor struct {
	cfg       *shared.Config
	logger    shared.ILogger
	userAgent shared.IUserAgent
	client    *http.Client
	cache     *articleCache
	throttle  *hostThrottle
}

// NewArticleExtractor builds the extractor used in
// production. Tunables are constants here; the tests
// reach for newArticleExtractor to set their own.
func NewArticleExtractor(
	cfg *shared.Config,
	logger shared.ILogger,
	userAgent shared.IUserAgent,
) IArticleExtractor {
	return newArticleExtractor(
		cfg, logger, userAgent,
		newPublicWebClient(articleFetchTimeout),
		articleCacheTtl, articleCacheSize, articleHostGap)
}

// newArticleExtractor takes the pieces the production
// constructor fixes, so a test can supply a client that
// is allowed to reach its own local server and a cache
// and throttle it can drive.
func newArticleExtractor(
	cfg *shared.Config,
	logger shared.ILogger,
	userAgent shared.IUserAgent,
	client *http.Client,
	cacheTtl time.Duration,
	cacheSize int,
	hostGap time.Duration,
) *articleExtractor {
	return &articleExtractor{
		cfg:       cfg,
		logger:    logger,
		userAgent: userAgent,
		client:    client,
		cache:     newArticleCache(cacheTtl, cacheSize),
		throttle:  newHostThrottle(hostGap, throttleMaxHosts),
	}
}

func (ae *articleExtractor) IsEnabled() bool {
	return ae.cfg.ExtractFullText
}

func (ae *articleExtractor) Extract(articleUrl string) string {

	if !ae.IsEnabled() {
		return ""
	}
	parsed, err := parsePublicPageUrl(articleUrl)
	if err != nil {
		ae.logger.Infof(
			"Extractor: not fetching %s: %v",
			articleUrl, err)
		return ""
	}

	// A remembered failure counts as a hit: the point
	// of caching it is not to ask the site again.
	if text, ok := ae.cache.get(articleUrl, time.Now()); ok {
		return text
	}

	ae.waitForTurn(parsed.Host)
	text := ae.fetchAndExtract(articleUrl, parsed)
	ae.cache.put(articleUrl, text, time.Now())
	return text
}

// waitForTurn blocks until this host's next throttle
// slot comes up.
func (ae *articleExtractor) waitForTurn(host string) {
	at := ae.throttle.reserve(host, time.Now())
	if wait := time.Until(at); wait > 0 {
		time.Sleep(wait)
	}
}

func (ae *articleExtractor) fetchAndExtract(
	articleUrl string,
	parsed *url.URL,
) string {

	body, err := ae.fetchPage(articleUrl)
	if err != nil {
		ae.logger.Infof(
			"Extractor: %s not extracted: %v",
			articleUrl, err)
		return ""
	}
	defer body.Close()

	article, err := readability.FromReader(
		io.LimitReader(body, maxArticleBytes), parsed)
	if err != nil {
		ae.logger.Infof(
			"Extractor: %s not readable: %v",
			articleUrl, err)
		return ""
	}
	var sb strings.Builder
	if err = article.RenderText(&sb); err != nil {
		ae.logger.Infof(
			"Extractor: %s render failed: %v",
			articleUrl, err)
		return ""
	}

	text := strings.TrimSpace(sb.String())
	if len(text) < minExtractedLen {
		ae.logger.Infof(
			"Extractor: %s yielded only %d chars; ignoring",
			articleUrl, len(text))
		return ""
	}
	return truncateRunes(text, maxExtractedLen)
}

// truncateRunes cuts text to at most maxLen bytes
// without splitting the rune that straddles the cut.
func truncateRunes(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	cut := maxLen
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

// fetchPage GETs an article page and hands back its
// body for the caller to close. It refuses anything
// that is not HTML, so a feed linking to a PDF or a
// video does not get parsed as a document.
func (ae *articleExtractor) fetchPage(
	articleUrl string,
) (io.ReadCloser, error) {

	req, err := http.NewRequest("GET", articleUrl, nil)
	if err != nil {
		return nil, err
	}
	ae.userAgent.AddUserAgent(req)
	req.Header.Add("Accept", "text/html,application/xhtml+xml")

	resp, err := ae.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf(
			"got status %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !isHtmlContentType(ct) {
		resp.Body.Close()
		return nil, fmt.Errorf(
			"content type is %q, not HTML", ct)
	}
	return resp.Body, nil
}

func isHtmlContentType(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	return strings.HasPrefix(ct, "text/html") ||
		strings.HasPrefix(ct, "application/xhtml+xml")
}

// parsePublicPageUrl accepts only the kind of URL an
// article can plausibly live at. Item links come
// straight out of a feed that anyone on the fediverse
// can ask this server to follow, so they are untrusted
// input.
func parsePublicPageUrl(rawUrl string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawUrl))
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf(
			"scheme %q is not http(s)", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("no host in URL")
	}
	return parsed, nil
}

// newPublicWebClient builds an HTTP client that can
// only reach the public internet.
//
// The address check sits in the dialer rather than in a
// URL check, so it also covers redirects and hostnames
// that resolve to an internal address. Without it, a
// feed item could point at a service on the parrot's
// own network and have its response summarized into a
// public toot.
func newPublicWebClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
		Control:   refuseNonPublicAddress,
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:         dialer.DialContext,
			TLSHandshakeTimeout: timeout,
		},
	}
}

// cgNatRange is RFC 6598 shared address space, which
// net.IP has no predicate for. Tailscale and other
// overlay networks hand out addresses from it.
var cgNatRange = net.IPNet{
	IP:   net.IPv4(100, 64, 0, 0),
	Mask: net.CIDRMask(10, 32),
}

func refuseNonPublicAddress(
	network, address string,
	_ syscall.RawConn,
) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf(
			"cannot parse dial address %q", address)
	}
	if !ip.IsGlobalUnicast() ||
		ip.IsPrivate() ||
		cgNatRange.Contains(ip) {
		return fmt.Errorf(
			"refusing to connect to non-public address %s",
			ip)
	}
	return nil
}
