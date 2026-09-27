package prompts

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

func TestSanitizeSingleLine(t *testing.T) {
	zeroWidthJoiner := string(rune(0x200D))
	cases := []struct {
		name    string
		text    string
		maximum int
		want    string
	}{
		{name: "trims and collapses whitespace", text: "  a \r\n\t b  ", want: "a b"},
		{name: "collapses unicode spaces", text: "a" + string(rune(0x2028)) + string(rune(0x00A0)) + "b", want: "a b"},
		{name: "drops control characters", text: "a" + string(rune(0)) + string(rune(0x7F)) + string(rune(0x1B)) + "[31mb", want: "a[31mb"},
		{name: "drops bidi overrides and zero-width spaces", text: "a" + string(rune(0x202E)) + string(rune(0x2066)) + string(rune(0x200B)) + string(rune(0xFEFF)) + "b", want: "ab"},
		{name: "keeps the zero-width joiner", text: "a" + zeroWidthJoiner + "b", want: "a" + zeroWidthJoiner + "b"},
		{name: "keeps the zero-width non-joiner", text: "a" + string(rune(0x200C)) + "b", want: "a" + string(rune(0x200C)) + "b"},
		{
			name: "drops tag characters used for ASCII smuggling",
			text: "Subject" + string(rune(0xE0049)) + string(rune(0xE0047)) + string(rune(0xE004E)) + string(rune(0xE0001)) + string(rune(0xE007F)) + " end",
			want: "Subject end",
		},
		{
			name: "drops the soft hyphen, invisible operators, and other format characters",
			text: "a" + string(rune(0x00AD)) + "b" + string(rune(0x2061)) + string(rune(0x2062)) + string(rune(0x2063)) + string(rune(0x2064)) +
				"c" + string(rune(0x180E)) + "d" + string(rune(0x2060)) + string(rune(0xFFF9)) + "e" + string(rune(0x061C)) + string(rune(0x200E)) + "f",
			want: "abcdef",
		},
		{name: "keeps an emoji presentation selector", text: "I " + string(rune(0x2764)) + string(rune(0xFE0F)) + " Go", want: "I " + string(rune(0x2764)) + string(rune(0xFE0F)) + " Go"},
		{name: "keeps a keycap sequence", text: "1" + string(rune(0xFE0F)) + string(rune(0x20E3)), want: "1" + string(rune(0xFE0F)) + string(rune(0x20E3))},
		{name: "keeps a math symbol variant", text: string(rune(0x2229)) + string(rune(0xFE00)), want: string(rune(0x2229)) + string(rune(0xFE00))},
		{name: "drops a variation selector after a letter", text: "a" + string(rune(0xFE0F)) + "b", want: "ab"},
		{name: "drops a second variation selector", text: string(rune(0x2764)) + string(rune(0xFE0F)) + string(rune(0xFE0E)) + string(rune(0xFE01)), want: string(rune(0x2764)) + string(rune(0xFE0F))},
		{name: "drops a variation selector after a space", text: string(rune(0x2764)) + " " + string(rune(0xFE0F)) + "x", want: string(rune(0x2764)) + " x"},
		{name: "drops a leading variation selector", text: string(rune(0xFE0F)) + "x", want: "x"},
		{name: "drops supplementary variation selectors", text: "葛" + string(rune(0xE0100)) + string(rune(0xE01EF)) + "x", want: "葛x"},
		{name: "replaces invalid UTF-8", text: "a" + string([]byte{0xff}) + "b", want: "a" + string(utf8.RuneError) + "b"},
		{name: "unbounded when maximum is zero", text: strings.Repeat("x", 500), want: strings.Repeat("x", 500)},
		{name: "exact length is kept", text: "abcde", maximum: 5, want: "abcde"},
		{name: "cuts at a word boundary", text: "alpha beta gamma delta", maximum: 15, want: "alpha beta…"},
		{name: "cuts mid-word without a nearby boundary", text: "abcdefghijklmnop", maximum: 6, want: "abcde…"},
		{name: "counts runes, not bytes", text: "ééééééé", maximum: 4, want: "ééé…"},
		{name: "maximum of one", text: "abc", maximum: 1, want: "…"},
		{name: "empty stays empty", text: " \n ", maximum: 10, want: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := sanitizeSingleLine(testCase.text, testCase.maximum)
			if got != testCase.want {
				t.Errorf("sanitizeSingleLine(%q, %d) = %q, want %q", testCase.text, testCase.maximum, got, testCase.want)
			}
			if testCase.maximum > 0 && utf8.RuneCountInString(got) > testCase.maximum {
				t.Errorf("result has %d characters, maximum %d", utf8.RuneCountInString(got), testCase.maximum)
			}
		})
	}
}

func TestRemoveHiddenCharacters(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{name: "keeps layout and ordinary text", text: "line one\r\n\tline two\n", want: "line one\r\n\tline two\n"},
		{name: "drops tag characters", text: "body" + string(rune(0xE0041)) + string(rune(0xE0042)) + "\nnext", want: "body\nnext"},
		{name: "drops format characters", text: "a" + string(rune(0x00AD)) + string(rune(0x200B)) + string(rune(0x202E)) + string(rune(0xFEFF)) + "b", want: "ab"},
		{name: "keeps joiners", text: "a" + string(rune(0x200C)) + string(rune(0x200D)) + "b", want: "a" + string(rune(0x200C)) + string(rune(0x200D)) + "b"},
		{name: "keeps an emoji selector and drops a stray one", text: string(rune(0x2764)) + string(rune(0xFE0F)) + "\n" + string(rune(0xFE0F)) + "x", want: string(rune(0x2764)) + string(rune(0xFE0F)) + "\nx"},
		{name: "replaces invalid UTF-8", text: "a" + string([]byte{0xff}) + "b", want: "a" + string(utf8.RuneError) + "b"},
	}
	for _, testCase := range cases {
		if got := removeHiddenCharacters(testCase.text); got != testCase.want {
			t.Errorf("%s: removeHiddenCharacters(%q) = %q, want %q", testCase.name, testCase.text, got, testCase.want)
		}
	}
}

func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		text    string
		maximum int
		want    string
	}{
		{text: "line one\nline two", maximum: 0, want: "line one\nline two"},
		{text: "line one\nline two", maximum: 17, want: "line one\nline two"},
		{text: "line one\nline two", maximum: 10, want: "line one\n…"},
		{text: "ééé", maximum: 2, want: "é…"},
		{text: "abc", maximum: 1, want: "…"},
	}
	for _, testCase := range cases {
		if got := truncateRunes(testCase.text, testCase.maximum); got != testCase.want {
			t.Errorf("truncateRunes(%q, %d) = %q, want %q", testCase.text, testCase.maximum, got, testCase.want)
		}
	}
}

func TestIsAbsoluteHTTPURL(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"https://github.com/superdurable/dex/pull/1", true},
		{"http://example.com", true},
		{"HTTPS://GITHUB.COM/x", true},
		{"", false},
		{"github.com/superdurable/dex", false},
		{"/relative/path", false},
		{"javascript:alert(1)", false},
		{"data:text/html,hi", false},
		{"mailto:someone@example.com", false},
		{"ftp://example.com/file", false},
		{"https://", false},
		{"https://exa mple.com", false},
		{"https://example.com/\nSet-Cookie: x", false},
		{"https://example.com/" + string(rune(0x202E)), false},
		{"https://example.com/" + string(rune(0xE0041)), false},
		{"https://example.com/" + string(rune(0x200D)), false},
		{"https://example.com/" + string(rune(0xFE0F)), false},
		{"https://example.com/" + string(rune(0x00AD)), false},
		{"https://example.com/%zz", false},
	}
	for _, testCase := range cases {
		if got := isAbsoluteHTTPURL(testCase.value); got != testCase.want {
			t.Errorf("isAbsoluteHTTPURL(%q) = %v, want %v", testCase.value, got, testCase.want)
		}
	}
}

func TestCitableSourceCatalog(t *testing.T) {
	catalog := newCitableSourceCatalog()
	catalog.addSource(" https://github.com/Owner/Repo/pull/1 ", "Owner/Repo#1")
	catalog.addSource("https://github.com/owner/repo/pull/1/", "second spelling loses")
	catalog.addSource("javascript:alert(1)", "never citable")
	catalog.addSource("", "never citable")

	cases := []struct {
		cited     string
		wantURL   string
		wantLabel string
		wantFound bool
	}{
		{cited: "https://github.com/Owner/Repo/pull/1", wantURL: "https://github.com/Owner/Repo/pull/1", wantLabel: "Owner/Repo#1", wantFound: true},
		{cited: "  HTTPS://GITHUB.COM/OWNER/REPO/PULL/1///  ", wantURL: "https://github.com/Owner/Repo/pull/1", wantLabel: "Owner/Repo#1", wantFound: true},
		{cited: "https://github.com/Owner/Repo/pull/12"},
		{cited: "https://github.com/Owner/Repo/pull/1?utm=x"},
		{cited: "javascript:alert(1)"},
		{cited: ""},
	}
	for _, testCase := range cases {
		gotURL, gotLabel, gotFound := catalog.resolveSource(testCase.cited)
		if gotURL != testCase.wantURL || gotLabel != testCase.wantLabel || gotFound != testCase.wantFound {
			t.Errorf("resolveSource(%q) = (%q, %q, %v), want (%q, %q, %v)", testCase.cited, gotURL, gotLabel, gotFound,
				testCase.wantURL, testCase.wantLabel, testCase.wantFound)
		}
	}
}

func TestSanitizeSourceReferencesLabels(t *testing.T) {
	catalog := newCitableSourceCatalog()
	catalog.addSource("https://example.com/a", "")
	catalog.addSource("https://example.com/b", "Default B")
	references := sanitizeSourceReferences([]model.SourceReference{
		{Label: "", URL: "https://example.com/a"},
		{Label: "\n", URL: "https://example.com/b"},
		{Label: strings.Repeat("l", 500), URL: "https://example.com/b"},
	}, catalog, 0)
	want := []model.SourceReference{{Label: "https://example.com/a", URL: "https://example.com/a"}, {Label: "Default B", URL: "https://example.com/b"}}
	if len(references) != len(want) || references[0] != want[0] || references[1] != want[1] {
		t.Errorf("references = %+v, want %+v", references, want)
	}
	long := sanitizeSourceReferences([]model.SourceReference{{Label: strings.Repeat("l", 500), URL: "https://example.com/a"}}, catalog, 0)
	if got := utf8.RuneCountInString(long[0].Label); got != maximumReferenceLabelRunes {
		t.Errorf("label has %d characters, want %d", got, maximumReferenceLabelRunes)
	}
}
