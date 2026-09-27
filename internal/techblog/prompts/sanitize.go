package prompts

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

// truncationEllipsis marks text shortened by this package.
const truncationEllipsis = "…"

// Bounds applied to language-model output. Response schemas advertise the
// same limits so a compliant model never hits them.
const (
	maximumHighlightTitleRunes    = 160
	maximumHighlightFieldRunes    = 2000
	maximumReferencesPerHighlight = 10
	maximumReferenceLabelRunes    = 200
)

// Characters with special handling in untrusted text.
const (
	zeroWidthNonJoiner rune = 0x200C
	zeroWidthJoiner    rune = 0x200D
)

// sanitizeSingleLine collapses every whitespace run (including line breaks)
// to one space, drops other control characters, invisible formatting
// characters, and stray variation selectors (see removeHiddenCharacters),
// replaces invalid UTF-8 with U+FFFD, trims, and truncates to maximumRunes
// (<= 0 means unbounded) at a word boundary when one is near.
func sanitizeSingleLine(text string, maximumRunes int) string {
	var sanitized strings.Builder
	pendingSpace := false
	previous := rune(-1)
	for _, character := range text {
		switch {
		case unicode.IsSpace(character):
			pendingSpace = sanitized.Len() > 0
		case unicode.IsControl(character), isInvisibleFormattingCharacter(character):
			continue
		case isVariationSelector(character) && (pendingSpace || !acceptsVariationSelector(previous)):
			continue
		default:
			if pendingSpace {
				sanitized.WriteByte(' ')
				pendingSpace = false
			}
			sanitized.WriteRune(character)
			previous = character
		}
	}
	return truncateAtWordBoundary(sanitized.String(), maximumRunes)
}

// removeHiddenCharacters drops invisible formatting characters and every
// variation selector that does not directly follow a character that accepts
// one, and replaces invalid UTF-8 with U+FFFD. Line breaks, other whitespace,
// and control characters are left alone, so multi-line text such as pull
// request descriptions and patches keeps its layout.
func removeHiddenCharacters(text string) string {
	var cleaned strings.Builder
	cleaned.Grow(len(text))
	previous := rune(-1)
	for _, character := range text {
		if isInvisibleFormattingCharacter(character) ||
			(isVariationSelector(character) && !acceptsVariationSelector(previous)) {
			continue
		}
		cleaned.WriteRune(character)
		previous = character
	}
	return cleaned.String()
}

// isInvisibleFormattingCharacter reports characters that render as nothing
// yet can hide, reorder, or smuggle text: every Unicode format character
// (category Cf) except the zero-width non-joiner and joiner, which emoji and
// several scripts need. That covers bidirectional controls, zero-width
// spaces, the soft hyphen, word joiner, invisible operators, the Mongolian
// vowel separator, and the byte order mark. The whole tag block
// U+E0000-U+E007F (used for "ASCII smuggling") and the supplementary
// variation selectors U+E0100-U+E01EF are included too.
func isInvisibleFormattingCharacter(character rune) bool {
	switch {
	case character == zeroWidthNonJoiner, character == zeroWidthJoiner:
		return false
	case character >= 0xE0000 && character <= 0xE007F:
		return true
	case character >= 0xE0100 && character <= 0xE01EF:
		return true
	default:
		return unicode.Is(unicode.Cf, character)
	}
}

// isVariationSelector reports the variation selectors U+FE00-U+FE0F, which
// choose emoji or text presentation and glyph variants of the character they
// follow.
func isVariationSelector(character rune) bool {
	return character >= 0xFE00 && character <= 0xFE0F
}

// acceptsVariationSelector reports whether a variation selector directly
// after base is a legitimate presentation choice: base is a symbol (emoji,
// dingbats, arrows, math symbols) or a keycap base. A selector after anything
// else, including another selector, only hides data.
func acceptsVariationSelector(base rune) bool {
	return unicode.In(base, unicode.So, unicode.Sm) || ('0' <= base && base <= '9') || base == '#' || base == '*'
}

// containsHiddenOrSpaceCharacter reports whether text contains whitespace, a
// control character, an invisible formatting character, a variation
// selector, or a zero-width (non-)joiner, none of which belong in a URL or a
// repository path.
func containsHiddenOrSpaceCharacter(text string) bool {
	return strings.ContainsFunc(text, func(character rune) bool {
		return unicode.IsSpace(character) || unicode.IsControl(character) || isInvisibleFormattingCharacter(character) ||
			isVariationSelector(character) || character == zeroWidthNonJoiner || character == zeroWidthJoiner
	})
}

// truncateAtWordBoundary shortens single-line text to at most maximumRunes
// runes including a trailing ellipsis, cutting at the last space in the
// second half of the kept text when there is one. maximumRunes <= 0 means
// unbounded.
func truncateAtWordBoundary(text string, maximumRunes int) string {
	if maximumRunes <= 0 || utf8.RuneCountInString(text) <= maximumRunes {
		return text
	}
	if maximumRunes == 1 {
		return truncationEllipsis
	}
	kept := []rune(text)[:maximumRunes-1]
	for index := len(kept) - 1; index >= len(kept)/2; index-- {
		if kept[index] == ' ' {
			kept = kept[:index]
			break
		}
	}
	return strings.TrimRight(string(kept), " ") + truncationEllipsis
}

// truncateRunes shortens text (which may span lines) to at most maximumRunes
// runes including a trailing ellipsis. maximumRunes <= 0 means unbounded.
func truncateRunes(text string, maximumRunes int) string {
	if maximumRunes <= 0 || utf8.RuneCountInString(text) <= maximumRunes {
		return text
	}
	if maximumRunes == 1 {
		return truncationEllipsis
	}
	return string([]rune(text)[:maximumRunes-1]) + truncationEllipsis
}

// isAbsoluteHTTPURL reports whether value is an absolute http(s) URL with a
// host and without whitespace, control, or hidden characters.
func isAbsoluteHTTPURL(value string) bool {
	if value == "" || containsHiddenOrSpaceCharacter(value) {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	return (scheme == "https" || scheme == "http") && parsed.Host != ""
}

// citableSourceCatalog is the closed set of URLs a model may cite at one
// stage, keyed case-insensitively and ignoring trailing slashes, with the
// canonical spelling and a fallback label for each. Resolved references
// always carry the canonical URL, so parsed output can never contain a URL
// that was not in the input.
type citableSourceCatalog struct {
	canonicalURLByKey map[string]string
	defaultLabelByKey map[string]string
	// canonicalURLByAddressKey resolves scheme-less web addresses such as
	// github.com/owner/repo/pull/1 by host and path (citableAddressKey).
	canonicalURLByAddressKey map[string]string
}

func newCitableSourceCatalog() citableSourceCatalog {
	return citableSourceCatalog{
		canonicalURLByKey:        map[string]string{},
		defaultLabelByKey:        map[string]string{},
		canonicalURLByAddressKey: map[string]string{},
	}
}

// citableSourceKey normalizes a URL for membership tests.
func citableSourceKey(rawURL string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(rawURL), "/"))
}

// citableAddressKey normalizes a scheme-less web address, or a URL with its
// http(s) scheme dropped, for membership tests: host, path, and any query or
// fragment, compared like citableSourceKey. A scheme-less address therefore
// matches a citable URL only when host and path (and query and fragment,
// when either has one) are the same.
func citableAddressKey(rawAddress string) string {
	key := citableSourceKey(rawAddress)
	for _, scheme := range []string{"https://", "http://"} {
		if strings.HasPrefix(key, scheme) {
			return strings.TrimRight(strings.TrimPrefix(key, scheme), "/")
		}
	}
	return key
}

// addSource registers one citable URL. Non-http(s) URLs are ignored, and the
// first spelling registered for a key wins.
func (catalog citableSourceCatalog) addSource(rawURL string, defaultLabel string) {
	canonicalURL := strings.TrimSpace(rawURL)
	if !isAbsoluteHTTPURL(canonicalURL) {
		return
	}
	key := citableSourceKey(canonicalURL)
	if _, registered := catalog.canonicalURLByKey[key]; registered {
		return
	}
	catalog.canonicalURLByKey[key] = canonicalURL
	catalog.defaultLabelByKey[key] = sanitizeSingleLine(defaultLabel, maximumReferenceLabelRunes)
	if addressKey := citableAddressKey(canonicalURL); catalog.canonicalURLByAddressKey[addressKey] == "" {
		catalog.canonicalURLByAddressKey[addressKey] = canonicalURL
	}
}

// resolveAddress returns the canonical URL of a scheme-less web address whose
// host and path match a citable URL.
func (catalog citableSourceCatalog) resolveAddress(address string) (canonicalURL string, found bool) {
	canonicalURL, found = catalog.canonicalURLByAddressKey[citableAddressKey(address)]
	return canonicalURL, found
}

// resolveSource returns the canonical URL and fallback label for a cited URL.
func (catalog citableSourceCatalog) resolveSource(rawURL string) (canonicalURL string, defaultLabel string, found bool) {
	key := citableSourceKey(rawURL)
	canonicalURL, found = catalog.canonicalURLByKey[key]
	if !found {
		return "", "", false
	}
	return canonicalURL, catalog.defaultLabelByKey[key], true
}

// sanitizeSourceReferences keeps references whose URL is in the catalog,
// replaces each URL with its canonical spelling, drops duplicates, bounds
// labels and removes every uncited link and URL from them (falling back to the
// catalog label, then to the URL), and caps the count. Labels are plain text
// wherever they are shown, so code spans get no exemption. The result is never
// nil.
func sanitizeSourceReferences(references []model.SourceReference, catalog citableSourceCatalog, maximumReferences int) []model.SourceReference {
	rules := strictInlineMarkupRules(catalog)
	sanitized := []model.SourceReference{}
	cited := map[string]bool{}
	for _, reference := range references {
		if maximumReferences > 0 && len(sanitized) >= maximumReferences {
			break
		}
		canonicalURL, defaultLabel, found := catalog.resolveSource(reference.URL)
		if !found || cited[citableSourceKey(canonicalURL)] {
			continue
		}
		cited[citableSourceKey(canonicalURL)] = true
		label := sanitizeInlineMarkupLine(reference.Label, rules, maximumReferenceLabelRunes)
		if label == "" {
			label = sanitizeInlineMarkupLine(defaultLabel, rules, maximumReferenceLabelRunes)
		}
		if label == "" {
			label = canonicalURL
		}
		sanitized = append(sanitized, model.SourceReference{Label: label, URL: canonicalURL})
	}
	return sanitized
}

// sanitizeChangeHighlights bounds every highlight field and removes every
// link and URL that is not in the catalog from it (code spans included, so
// research notes never carry an uncited URL), drops highlights without a
// title, filters references through the catalog, and caps the count. The
// result is never nil.
func sanitizeChangeHighlights(highlights []model.ChangeHighlight, catalog citableSourceCatalog, maximumHighlights int) []model.ChangeHighlight {
	rules := strictInlineMarkupRules(catalog)
	sanitized := []model.ChangeHighlight{}
	for _, highlight := range highlights {
		if len(sanitized) >= maximumHighlights {
			break
		}
		title := sanitizeInlineMarkupLine(highlight.Title, rules, maximumHighlightTitleRunes)
		if title == "" {
			continue
		}
		sanitized = append(sanitized, model.ChangeHighlight{
			Title:        title,
			WhatChanged:  sanitizeInlineMarkupLine(highlight.WhatChanged, rules, maximumHighlightFieldRunes),
			HowItWorks:   sanitizeInlineMarkupLine(highlight.HowItWorks, rules, maximumHighlightFieldRunes),
			WhyItMatters: sanitizeInlineMarkupLine(highlight.WhyItMatters, rules, maximumHighlightFieldRunes),
			References:   sanitizeSourceReferences(highlight.References, catalog, maximumReferencesPerHighlight),
		})
	}
	return sanitized
}

// sanitizeTextList bounds each entry and removes every link and URL that is
// not in the catalog from it, drops empty entries and exact duplicates, and
// caps the count. The result is never nil.
func sanitizeTextList(entries []string, catalog citableSourceCatalog, maximumEntryRunes int, maximumEntries int) []string {
	rules := strictInlineMarkupRules(catalog)
	sanitized := []string{}
	seen := map[string]bool{}
	for _, entry := range entries {
		if len(sanitized) >= maximumEntries {
			break
		}
		text := sanitizeInlineMarkupLine(entry, rules, maximumEntryRunes)
		if text == "" || seen[text] {
			continue
		}
		seen[text] = true
		sanitized = append(sanitized, text)
	}
	return sanitized
}
