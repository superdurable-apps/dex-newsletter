package notices

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestEscapeBounded(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		limit     int
		mode      textMode
		truncated bool
		want      string
	}{
		{name: "escapes Slack control characters", text: "a & b <c> @d", limit: 100, mode: plainTextMode, want: "a &amp; b &lt;c&gt; @" + testZeroWidthSpace + "d"},
		{name: "keeps pre-escaped entities literal", text: "&lt;!channel&gt;", limit: 100, mode: plainTextMode, want: "&amp;lt;!channel&amp;gt;"},
		{name: "plain mode keeps backticks and pipes", text: "`x|y`", limit: 100, mode: plainTextMode, want: "`x|y`"},
		{name: "code span mode replaces backticks", text: "`x|y`", limit: 100, mode: codeSpanMode, want: "'x|y'"},
		{name: "link label mode replaces pipes", text: "`x|y`", limit: 100, mode: linkLabelMode, want: "`x¦y`"},
		{name: "escaped text exactly at limit", text: "&&&", limit: 15, mode: plainTextMode, want: "&amp;&amp;&amp;"},
		{name: "never splits an entity", text: "&&&", limit: 14, mode: plainTextMode, want: "&amp;&amp;…"},
		{name: "never splits a mention breaker", text: "@@", limit: 3, mode: plainTextMode, want: "@" + testZeroWidthSpace + "…"},
		{name: "truncates with ellipsis", text: "abcdef", limit: 4, mode: plainTextMode, want: "abc…"},
		{name: "text exactly at limit", text: "abc", limit: 3, mode: plainTextMode, want: "abc"},
		{name: "trims a trailing space before the ellipsis", text: "ab cd", limit: 4, mode: plainTextMode, want: "ab…"},
		{name: "trims a trailing line break before the ellipsis", text: "ab\ncd", limit: 4, mode: plainTextMode, want: "ab…"},
		{name: "limit of one leaves only the ellipsis", text: "abc", limit: 1, mode: plainTextMode, want: "…"},
		{name: "zero limit", text: "abc", limit: 0, mode: plainTextMode, want: ""},
		{name: "negative limit", text: "abc", limit: -1, mode: plainTextMode, want: ""},
		{name: "empty text already truncated", text: "", limit: 10, mode: plainTextMode, truncated: true, want: "…"},
		{name: "short text already truncated", text: "abc", limit: 10, mode: plainTextMode, truncated: true, want: "abc…"},
		{name: "counts runes, not bytes", text: "日本語テキスト", limit: 4, mode: plainTextMode, want: "日本語…"},
		{name: "keeps line breaks", text: "a\nb", limit: 10, mode: plainTextMode, want: "a\nb"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := escapeBounded(test.text, test.limit, test.mode, test.truncated)
			if got != test.want {
				t.Errorf("escapeBounded(%q, %d) = %q, want %q", test.text, test.limit, got, test.want)
			}
			if test.limit > 0 && utf8.RuneCountInString(got) > test.limit {
				t.Errorf("escapeBounded(%q, %d) has %d runes", test.text, test.limit, utf8.RuneCountInString(got))
			}
		})
	}
}

func TestSanitizedText(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "every line break becomes LF", value: "a\r\nb\rc" + testLineSeparator + "d" + testNextLine + "e\vf\fg\xe2\x80\xa9h", want: "a\nb\nc\nd\ne\nf\ng\nh"},
		{name: "other whitespace becomes a space", value: "a\tb\xc2\xa0c\xe3\x80\x80d", want: "a b c d"},
		{name: "C0 controls dropped", value: "a\x00\x1b[31mb\x7f", want: "a[31mb"},
		{name: "C1 controls dropped", value: "a\xc2\x9fb", want: "ab"},
		{name: "format characters dropped", value: "a" + testZeroWidthSpace + "b" + testRightToLeftOverride + "c" + testByteOrderMark + "d\xc2\xade\xe2\x81\xa6f", want: "abcdef"},
		{name: "invalid UTF-8 replaced", value: "a\xffb", want: "a" + string(utf8.RuneError) + "b"},
		{name: "visible text unchanged", value: "Émoji :tada: 日本", want: "Émoji :tada: 日本"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sanitizedText(test.value); got != test.want {
				t.Errorf("sanitizedText(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

func TestCappedInput(t *testing.T) {
	tests := []struct {
		name          string
		value         string
		capBytes      int
		want          string
		wantTruncated bool
	}{
		{name: "within cap", value: "alpha beta", capBytes: 10, want: "alpha beta"},
		{name: "cut back to last space so no partial email survives", value: "alpha beta victim@example.com", capBytes: 20, want: "alpha beta", wantTruncated: true},
		{name: "cut back to last line break", value: "alpha\nbeta gamma", capBytes: 8, want: "alpha", wantTruncated: true},
		{name: "cut back to last tab", value: "alpha\tbetagamma", capBytes: 10, want: "alpha", wantTruncated: true},
		{name: "one oversized token is dropped", value: strings.Repeat("x", 30), capBytes: 20, want: "", wantTruncated: true},
		{name: "multi-byte text stays valid", value: "日本 語テキスト", capBytes: 9, want: "日本", wantTruncated: true},
		{name: "leading whitespace does not use up the cap", value: strings.Repeat(" \n\t", 20) + "alpha", capBytes: 8, want: "alpha"},
		{name: "leading invisible characters do not use up the cap", value: strings.Repeat(testZeroWidthSpace+"\x00", 10) + "alpha", capBytes: 8, want: "alpha"},
		{name: "leading padding then a long value", value: strings.Repeat(" ", 30) + "alpha beta gamma", capBytes: 12, want: "alpha beta", wantTruncated: true},
		{name: "CJK prose cut at punctuation", value: "中文，中文。中文", capBytes: 19, want: "中文，中文", wantTruncated: true},
		{name: "punctuation after an early space wins", value: "a 中文中文，中文中文", capBytes: 22, want: "a 中文中文", wantTruncated: true},
		{name: "CJK letters without a boundary are one token", value: strings.Repeat("中", 10), capBytes: 10, want: "", wantTruncated: true},
		{name: "invalid UTF-8 is a boundary", value: "ab\xffcdefgh", capBytes: 6, want: "ab", wantTruncated: true},
		{name: "zero-width characters are not boundaries", value: "ab" + testZeroWidthSpace + "cdefgh", capBytes: 8, want: "", wantTruncated: true},
		{name: "never ends inside an open URL authority", value: "see，https://user:pa，ss@example.com/x", capBytes: 30, want: "see，https", wantTruncated: true},
		{name: "keeps a URL whose authority has ended", value: "https://example.com/a，b", capBytes: 24, want: "https://example.com/a", wantTruncated: true},
		{name: "zero cap", value: "alpha", capBytes: 0, want: "", wantTruncated: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, truncated := cappedInput(test.value, test.capBytes)
			if got != test.want || truncated != test.wantTruncated {
				t.Errorf("cappedInput(%q, %d) = (%q, %t), want (%q, %t)", test.value, test.capBytes, got, truncated, test.want, test.wantTruncated)
			}
		})
	}
}

func TestSingleLineText(t *testing.T) {
	tests := []struct {
		name  string
		value string
		limit int
		want  string
	}{
		{name: "collapses whitespace and line breaks", value: "  a \t\n\r b  \xc2\xa0c" + testLineSeparator + "d ", limit: 50, want: "a b c d"},
		{name: "drops invisible characters", value: "a" + testZeroWidthSpace + "b" + testRightToLeftOverride + "c\x00d", limit: 50, want: "abcd"},
		{name: "blank value", value: " \n\t" + testZeroWidthSpace, limit: 50, want: ""},
		{name: "escapes and neutralizes mentions", value: "<!channel> <@U123> @here", limit: 100, want: "&lt;!channel&gt; &lt;@" + testZeroWidthSpace + "U123&gt; @" + testZeroWidthSpace + "here"},
		{name: "redacts email addresses", value: "mail bob@example.com now", limit: 50, want: "mail [redacted email] now"},
		{name: "redacts an email split by a zero-width space", value: "bob" + testZeroWidthSpace + "@example.com", limit: 50, want: "[redacted email]"},
		{name: "truncates long text", value: strings.Repeat("word ", 100), limit: 12, want: "word word w…"},
		{name: "oversized single token", value: strings.Repeat("x", 5000), limit: 200, want: "…"},
		{name: "padding beyond the input cap", value: strings.Repeat(" ", 20000) + "Which repo?", limit: 200, want: "Which repo?"},
		{name: "long CJK prose is truncated, not dropped", value: strings.Repeat("中文内容很长，", 250), limit: 12, want: "中文内容很长，中文内容…"},
		{name: "redacts a prefixed credential", value: "config: DB_PASSWORD=hunter2 invalid", limit: 100, want: "config: DB_PASSWORD=[redacted] invalid"},
		{name: "redacts a dotless address", value: "invalid subscriber jane.doe@gmail", limit: 100, want: "invalid subscriber [redacted email]"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := singleLineText(test.value, test.limit); got != test.want {
				t.Errorf("singleLineText(%q, %d) = %q, want %q", test.value, test.limit, got, test.want)
			}
		})
	}
}

func TestQuotedText(t *testing.T) {
	var twentyLines []string
	for index := 1; index <= 20; index++ {
		twentyLines = append(twentyLines, "l"+strings.Repeat("x", index))
	}
	var twelveQuotedLines []string
	for index := 1; index <= 12; index++ {
		twelveQuotedLines = append(twelveQuotedLines, "> l"+strings.Repeat("x", index))
	}
	tests := []struct {
		name  string
		value string
		limit int
		want  string
	}{
		{name: "CRLF lines", value: "one\r\ntwo", limit: 100, want: "> one\n> two"},
		{name: "blank lines collapsed and trimmed", value: "\n\n one  \n\n\n  two \n\n", limit: 100, want: "> one\n>\n> two"},
		{name: "blank value", value: " \n \r\n\t", limit: 100, want: ""},
		{name: "line count capped with ellipsis", value: strings.Join(twentyLines, "\n"), limit: 1000, want: strings.Join(twelveQuotedLines, "\n") + "…"},
		{name: "blank line at the line cap is dropped", value: strings.Repeat("a\n", 11) + "\n" + "b", limit: 1000, want: strings.TrimSuffix(strings.Repeat("> a\n", 11), "\n") + "…"},
		{name: "escapes every line", value: "<!channel>\n@here", limit: 100, want: "> &lt;!channel&gt;\n> @" + testZeroWidthSpace + "here"},
		{name: "fake quote markers stay inside the quote", value: "> fake\n>>> fake", limit: 100, want: "> &gt; fake\n> &gt;&gt;&gt; fake"},
		{name: "rune limit spans lines", value: strings.Repeat("abcd\n", 10), limit: 12, want: "> abcd\n> abcd\n> a…"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := quotedText(test.value, test.limit); got != test.want {
				t.Errorf("quotedText(%q, %d) =\n%q\nwant\n%q", test.value, test.limit, got, test.want)
			}
		})
	}
}

func TestCodeSpan(t *testing.T) {
	tests := []struct {
		name  string
		value string
		limit int
		want  string
	}{
		{name: "plain", value: "artifacts/post.html", limit: 100, want: "`artifacts/post.html`"},
		{name: "backtick cannot close the span", value: "flow`id", limit: 100, want: "`flow'id`"},
		{name: "escapes and neutralizes mentions", value: "a <b> & @c", limit: 100, want: "`a &lt;b&gt; &amp; @" + testZeroWidthSpace + "c`"},
		{name: "blank", value: "  ", limit: 100, want: ""},
		{name: "truncated inside the backticks", value: strings.Repeat("x", 50), limit: 10, want: "`xxxxxxx…`"},
		{name: "limit too small for any content", value: "x", limit: 2, want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := codeSpan(test.value, test.limit); got != test.want {
				t.Errorf("codeSpan(%q, %d) = %q, want %q", test.value, test.limit, got, test.want)
			}
		})
	}
}

func TestSlackLinkTarget(t *testing.T) {
	tests := []struct {
		name         string
		rawURL       string
		want         string
		wantLinkable bool
	}{
		{name: "https", rawURL: "https://example.com", want: "https://example.com", wantLinkable: true},
		{name: "http with port and path", rawURL: "http://127.0.0.1:8802/v2/runs/x", want: "http://127.0.0.1:8802/v2/runs/x", wantLinkable: true},
		{name: "uppercase scheme", rawURL: "HTTPS://Example.com/a", want: "HTTPS://Example.com/a", wantLinkable: true},
		{name: "ampersand escaped", rawURL: "https://example.com/a?b=1&c=2", want: "https://example.com/a?b=1&amp;c=2", wantLinkable: true},
		{name: "percent-encoded characters", rawURL: "https://example.com/%3C%21channel%3E", want: "https://example.com/%3C%21channel%3E", wantLinkable: true},
		{name: "IPv6 host", rawURL: "https://[::1]:8080/x", want: "https://[::1]:8080/x", wantLinkable: true},
		{name: "empty", rawURL: ""},
		{name: "no scheme", rawURL: "example.com/a"},
		{name: "relative", rawURL: "/v2/runs/x"},
		{name: "ftp", rawURL: "ftp://example.com"},
		{name: "javascript", rawURL: "javascript:alert(1)"},
		{name: "mailto", rawURL: "mailto:bob@example.com"},
		{name: "opaque http", rawURL: "https:example.com"},
		{name: "no host", rawURL: "https://"},
		{name: "port without host", rawURL: "https://:80/x"},
		{name: "user name", rawURL: "https://user@example.com"},
		{name: "empty user info", rawURL: "https://@example.com"},
		{name: "user and password", rawURL: "https://user:pass@example.com"},
		{name: "space", rawURL: "https://example.com/a b"},
		{name: "pipe", rawURL: "https://example.com/a|b"},
		{name: "angle brackets", rawURL: "https://example.com/<!channel>"},
		{name: "closing angle bracket", rawURL: "https://example.com/a>b"},
		{name: "quote", rawURL: "https://example.com/a\"b"},
		{name: "backslash", rawURL: "https://example.com/a\\b"},
		{name: "line break", rawURL: "https://example.com/\n"},
		{name: "non-ASCII host", rawURL: "https://ex\xc3\xa4mple.com"},
		{name: "API key in query", rawURL: "https://example.com/?key=AIzaSyA1234567890123456789012345678901"},
		{name: "email in query", rawURL: "https://example.com/?email=bob@example.com"},
		{name: "too long", rawURL: "https://example.com/" + strings.Repeat("a", maximumLinkTargetRunes)},
		{name: "too long once escaped", rawURL: "https://example.com/?" + strings.Repeat("&", 150)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, linkable := slackLinkTarget(test.rawURL)
			if got != test.want || linkable != test.wantLinkable {
				t.Errorf("slackLinkTarget(%q) = (%q, %t), want (%q, %t)", test.rawURL, got, linkable, test.want, test.wantLinkable)
			}
		})
	}
}

func TestSlackLink(t *testing.T) {
	tests := []struct {
		name   string
		rawURL string
		label  string
		want   string
	}{
		{name: "link", rawURL: " https://example.com/p ", label: "Read", want: "<https://example.com/p|Read>"},
		{name: "empty label", rawURL: "https://example.com/p", label: "", want: ""},
		{name: "invalid URL", rawURL: "https://example.com/<p>", label: "Read", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := slackLink(test.rawURL, test.label); got != test.want {
				t.Errorf("slackLink(%q, %q) = %q, want %q", test.rawURL, test.label, got, test.want)
			}
		})
	}
}

func TestBoundMessage(t *testing.T) {
	line := strings.Repeat("x", 49)
	manyLines := strings.TrimSuffix(strings.Repeat(line+"\n", 100), "\n")
	wideLines := strings.TrimSuffix(strings.Repeat(strings.Repeat("日", 49)+"\n", 100), "\n")
	tests := []struct {
		name    string
		message string
		want    string
	}{
		{name: "short message unchanged", message: "hello\nworld", want: "hello\nworld"},
		{name: "exactly at the limit unchanged", message: strings.Repeat("x", maximumMessageRunes), want: strings.Repeat("x", maximumMessageRunes)},
		{name: "cut at a line boundary", message: manyLines, want: strings.TrimSuffix(strings.Repeat(line+"\n", 69), "\n") + "\n" + messageTruncationMarker},
		{name: "counts runes, not bytes", message: wideLines, want: strings.TrimSuffix(strings.Repeat(strings.Repeat("日", 49)+"\n", 69), "\n") + "\n" + messageTruncationMarker},
		{name: "one oversized line", message: strings.Repeat("x", maximumMessageRunes+1), want: messageTruncationMarker},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := boundMessage(test.message)
			if got != test.want {
				t.Errorf("boundMessage() = %d runes ending %q, want %d runes ending %q", utf8.RuneCountInString(got), tail(got), utf8.RuneCountInString(test.want), tail(test.want))
			}
			if utf8.RuneCountInString(got) > maximumMessageRunes {
				t.Errorf("boundMessage() returned %d runes", utf8.RuneCountInString(got))
			}
		})
	}
}

// tail returns the last few runes of text for failure messages.
func tail(text string) string {
	runes := []rune(text)
	if len(runes) <= 40 {
		return text
	}
	return string(runes[len(runes)-40:])
}
