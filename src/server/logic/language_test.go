package logic

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_NormalizeLanguageTag_StandardTags(t *testing.T) {
	cases := map[string]string{
		"en":                         "English",
		"en-US":                      "English",
		"en_US":                      "English",
		"es":                         "Spanish",
		"es-ES":                      "Spanish",
		"es_ES":                      "Spanish",
		"spa":                        "Spanish",
		"ro":                         "Romanian",
		"ro-RO":                      "Romanian",
		"ro_RO":                      "Romanian",
		"ron":                        "Romanian",
		"fr":                         "French",
		"fr-FR":                      "French",
		"fra":                        "French",
		"de":                         "German",
		"de-DE":                      "German",
		"deu":                        "German",
		"it":                         "Italian",
		"pt-BR":                      "Portuguese",
		"ja":                         "Japanese",
		"zh-CN":                      "Chinese",
		"de-DE;q=0.9, en-US;q=0.8":   "German",
		"es, en;q=0.5":               "Spanish",
		"Spanish":                    "Spanish",
		"romanian":                   "Romanian",
	}

	// Verify each language tag or header resolves to the
	// expected standardized English language name.
	for tag, expected := range cases {
		t.Run(tag, func(t *testing.T) {
			assert.Equal(t, expected, NormalizeLanguageTag(tag))
		})
	}
}

func Test_NormalizeLanguageTag_InvalidAndEmpty(t *testing.T) {
	invalidTags := []string{
		"",
		"   ",
		"und",
		"invalid",
		"xyz999",
	}

	for _, tag := range invalidTags {
		t.Run("invalid_"+tag, func(t *testing.T) {
			assert.Equal(t, "", NormalizeLanguageTag(tag))
		})
	}
}

func Test_DetectContentLanguage_Languages(t *testing.T) {
	cases := map[string]string{
		"es": "Este es un artículo completo sobre el desarrollo de software " +
			"y sistemas distribuidos en España y América Latina.",
		"fr": "Ceci est un article complet sur les nouvelles technologies " +
			"et la recherche scientifique en France.",
		"ro": "Acesta este un articol detaliat despre inteligența artificială " +
			"și programare publicat în limba română.",
		"de": "Dies ist ein ausführlicher Artikel über moderne Webentwicklung " +
			"und Cloud-Infrastruktur in Deutschland.",
		"en": "This is a detailed article discussing distributed systems " +
			"and software architecture in production.",
	}

	for expectedCode, text := range cases {
		expectedLang := map[string]string{
			"es": "Spanish",
			"fr": "French",
			"ro": "Romanian",
			"de": "German",
			"en": "English",
		}[expectedCode]

		t.Run(expectedLang, func(t *testing.T) {
			assert.Equal(t, expectedLang, DetectContentLanguage(text))
		})
	}
}

func Test_DetectContentLanguage_ShortOrEmpty(t *testing.T) {
	assert.Equal(t, "", DetectContentLanguage(""))
	assert.Equal(t, "", DetectContentLanguage("   "))
	assert.Equal(t, "", DetectContentLanguage("short text"))
}

func Test_DetectArticleLanguage_Priority(t *testing.T) {
	spanishText := "Este es un artículo completo sobre el desarrollo de software."

	// 1. HTML lang takes highest precedence over HTTP header, feed, and content
	lang := DetectArticleLanguage("fr", "es", "ro", "This is English text.")
	assert.Equal(t, "Spanish", lang)

	// 2. HTTP header takes precedence when HTML lang is missing
	lang = DetectArticleLanguage("fr", "", "ro", "This is English text.")
	assert.Equal(t, "French", lang)

	// 3. Feed lang takes precedence when HTML and HTTP are missing
	lang = DetectArticleLanguage("", "", "ro", "This is English text.")
	assert.Equal(t, "Romanian", lang)

	// 4. Content detection is used when HTML, HTTP, and Feed lang are missing
	lang = DetectArticleLanguage("", "", "", spanishText)
	assert.Equal(t, "Spanish", lang)

	// 5. English fallback when all signals are empty or unrecognized
	lang = DetectArticleLanguage("", "", "", "not enough text")
	assert.Equal(t, DefaultLanguage, lang)
	assert.Equal(t, "English", lang)
}
