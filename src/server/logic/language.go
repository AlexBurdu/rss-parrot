package logic

import (
	"strings"

	"github.com/abadojack/whatlanggo"
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

// DefaultLanguage is the fallback language for summaries when detection fails.
const DefaultLanguage = "English"

// NormalizeLanguageTag normalizes a BCP 47 language tag, ISO 639 code, or
// language name into a standardized English language name (e.g. "Spanish",
// "French", "Romanian", "German"). Returns an empty string if the tag is
// empty, unrecognized, or invalid.
func NormalizeLanguageTag(tagStr string) string {
	tagStr = strings.TrimSpace(tagStr)
	if tagStr == "" {
		return ""
	}

	// Clean up multi-value headers (e.g. "de-DE;q=0.9, en-US;q=0.8")
	// by taking the primary language before any comma or semicolon.
	if commaIdx := strings.Index(tagStr, ","); commaIdx != -1 {
		tagStr = strings.TrimSpace(tagStr[:commaIdx])
	}
	if semiIdx := strings.Index(tagStr, ";"); semiIdx != -1 {
		tagStr = strings.TrimSpace(tagStr[:semiIdx])
	}

	// Normalize underscores (e.g. "en_US" -> "en-US")
	tagStr = strings.ReplaceAll(tagStr, "_", "-")
	lowerTag := strings.ToLower(tagStr)

	// Explicit check for undefined tag.
	if lowerTag == "und" {
		return ""
	}

	// Attempt BCP 47 parsing via golang.org/x/text/language.
	tag, err := language.Parse(tagStr)
	if err == nil && tag != language.Und && !tag.IsRoot() {
		base, conf := tag.Base()
		if conf > language.Low && base.String() != "und" {
			name := display.English.Languages().Name(base)
			if name != "" && name != "Unknown language" {
				return name
			}
		}
	}

	// Fall back to ISO 639-3 or language name lookup via whatlanggo.
	// This covers 3-letter codes (e.g. "spa", "ron") and full names.
	for lang, name := range whatlanggo.Langs {
		if strings.ToLower(name) == lowerTag {
			return name
		}
		if lang.Iso6393() == lowerTag || lang.Iso6391() == lowerTag {
			return name
		}
	}

	return ""
}

// DetectContentLanguage uses natural language detection on the text body.
// Returns the language name if confident, or an empty string if detection
// fails or the text is too short.
func DetectContentLanguage(text string) string {
	text = strings.TrimSpace(text)
	// Short text samples cannot reliably be distinguished from noise.
	if len(text) < 20 {
		return ""
	}

	info := whatlanggo.Detect(text)
	// Require reliable detection or high confidence before accepting.
	if info.IsReliable() || info.Confidence >= 0.7 {
		langName := info.Lang.String()
		if langName != "" && langName != "Unknown" {
			return langName
		}
	}
	return ""
}

// DetectArticleLanguage resolves the target language for an article across
// multiple signals in priority order:
// 1. HTML lang attribute from the article webpage
// 2. HTTP Content-Language response header
// 3. Feed-level language declaration
// 4. Content-based natural language detection
// 5. Fall back to DefaultLanguage ("English")
func DetectArticleLanguage(
	httpLang string,
	htmlLang string,
	feedLang string,
	text string,
) string {
	if html := NormalizeLanguageTag(htmlLang); html != "" {
		return html
	}
	if http := NormalizeLanguageTag(httpLang); http != "" {
		return http
	}
	if feed := NormalizeLanguageTag(feedLang); feed != "" {
		return feed
	}
	if content := DetectContentLanguage(text); content != "" {
		return content
	}
	return DefaultLanguage
}
