package shared

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestEllipticalTruncate(t *testing.T) {
	assert.Equal(t, "…", TruncateWithEllipsis("1 2 3", 0))
	assert.Equal(t, "1…", TruncateWithEllipsis("1 2 3", 1))
	assert.Equal(t, "1…", TruncateWithEllipsis("1 2 3", 2))
	assert.Equal(t, "1 2…", TruncateWithEllipsis("1 2 3", 3))
	assert.Equal(t, "1 2 3", TruncateWithEllipsis("1 2 3", 5))
}

const tootWithoutSummary = `<p><strong>Title</strong></p>` +
	`<p><a href="https://x.test/a">x.test/a</a></p>` +
	`<p>The description.</p>`

func Test_InsertSummaryHtml_GoesBetweenLinkAndDescription(t *testing.T) {
	res := InsertSummaryHtml(tootWithoutSummary, "A summary.")
	expected := `<p><strong>Title</strong></p>` +
		`<p><a href="https://x.test/a">x.test/a</a></p>` +
		`<p><em>A summary.</em></p>` +
		`<p>The description.</p>`
	assert.Equal(t, expected, res)
}

func Test_InsertSummaryHtml_EscapesSummary(t *testing.T) {
	res := InsertSummaryHtml(tootWithoutSummary, `<b>x</b> & "y"`)
	assert.Contains(t, res, `<p><em>&lt;b&gt;x&lt;/b&gt; &amp; &#34;y&#34;</em></p>`)
	assert.NotContains(t, res, "<b>x</b>")
}

func Test_InsertSummaryHtml_EmptySummaryLeavesContentAlone(t *testing.T) {
	assert.Equal(t, tootWithoutSummary,
		InsertSummaryHtml(tootWithoutSummary, ""))
}

func Test_InsertSummaryHtml_MissingAnchorLeavesContentAlone(t *testing.T) {
	noAnchor := "<p>Just a paragraph.</p>"
	assert.Equal(t, noAnchor, InsertSummaryHtml(noAnchor, "A summary."))
}
