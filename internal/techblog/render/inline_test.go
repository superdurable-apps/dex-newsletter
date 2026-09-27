package render

import (
	"reflect"
	"strings"
	"testing"
)

func textSegment(text string) inlineSegment { return inlineSegment{Kind: inlineText, Text: text} }

func codeSegment(text string) inlineSegment { return inlineSegment{Kind: inlineCode, Text: text} }

func linkSegment(url string, label ...inlineSegment) inlineSegment {
	return inlineSegment{Kind: inlineLink, URL: url, Label: label}
}

func TestParseInline(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		allowLinks bool
		want       []inlineSegment
	}{
		{name: "empty", input: "", allowLinks: true, want: nil},
		{name: "plain text", input: "plain <b>text</b> & more", allowLinks: true, want: []inlineSegment{textSegment("plain <b>text</b> & more")}},
		{name: "code span", input: "run `go test` now", allowLinks: true, want: []inlineSegment{textSegment("run "), codeSegment("go test"), textSegment(" now")}},
		{name: "double backtick span holds a backtick", input: "``a ` b``", allowLinks: true, want: []inlineSegment{codeSegment("a ` b")}},
		{name: "one space stripped from both ends", input: "`` `x` ``", allowLinks: true, want: []inlineSegment{codeSegment("`x`")}},
		{name: "all-space content kept", input: "` `", allowLinks: true, want: []inlineSegment{codeSegment(" ")}},
		{name: "line feed in code becomes space", input: "`a\nb`", allowLinks: true, want: []inlineSegment{codeSegment("a b")}},
		{name: "unbalanced backtick is literal", input: "a `b c", allowLinks: true, want: []inlineSegment{textSegment("a `b c")}},
		{name: "mismatched run lengths are literal", input: "``a`", allowLinks: true, want: []inlineSegment{textSegment("``a`")}},
		{name: "unmatched long run then span", input: "``` `x`", allowLinks: true, want: []inlineSegment{textSegment("``` "), codeSegment("x")}},
		{name: "three backticks alone", input: "```", allowLinks: true, want: []inlineSegment{textSegment("```")}},
		{name: "code span content is not parsed as a link", input: "`[x](https://a.example)`", allowLinks: true, want: []inlineSegment{codeSegment("[x](https://a.example)")}},
		{name: "html inside code stays text", input: "`</code><script>`", allowLinks: true, want: []inlineSegment{codeSegment("</code><script>")}},
		{name: "https link", input: "see [the PR](https://github.com/o/r/pull/1).", allowLinks: true, want: []inlineSegment{textSegment("see "), linkSegment("https://github.com/o/r/pull/1", textSegment("the PR")), textSegment(".")}},
		{name: "http link", input: "[docs](http://docs.example/x)", allowLinks: true, want: []inlineSegment{linkSegment("http://docs.example/x", textSegment("docs"))}},
		{name: "uppercase scheme link", input: "[docs](HTTPS://docs.example)", allowLinks: true, want: []inlineSegment{linkSegment("HTTPS://docs.example", textSegment("docs"))}},
		{name: "links disabled", input: "[docs](https://docs.example)", allowLinks: false, want: []inlineSegment{textSegment("[docs](https://docs.example)")}},
		{name: "label with code", input: "[`Run`](https://a.example)", allowLinks: true, want: []inlineSegment{linkSegment("https://a.example", codeSegment("Run"))}},
		{name: "label code span holding a bracket", input: "[call `a]b` now](https://a.example)", allowLinks: true, want: []inlineSegment{linkSegment("https://a.example", textSegment("call "), codeSegment("a]b"), textSegment(" now"))}},
		{name: "nested brackets in label", input: "[a [b] c](https://a.example)", allowLinks: true, want: []inlineSegment{linkSegment("https://a.example", textSegment("a [b] c"))}},
		{name: "link inside label stays literal", input: "[a [b](https://b.example) c](https://a.example)", allowLinks: true, want: []inlineSegment{linkSegment("https://a.example", textSegment("a [b](https://b.example) c"))}},
		{name: "blank label uses url", input: "[ ](https://a.example)", allowLinks: true, want: []inlineSegment{linkSegment("https://a.example", textSegment("https://a.example"))}},
		{name: "parentheses in url", input: "[Go](https://en.wikipedia.org/wiki/Go_(language))", allowLinks: true, want: []inlineSegment{linkSegment("https://en.wikipedia.org/wiki/Go_(language)", textSegment("Go"))}},
		{name: "two links", input: "[a](https://a.example) and [b](https://b.example)", allowLinks: true, want: []inlineSegment{linkSegment("https://a.example", textSegment("a")), textSegment(" and "), linkSegment("https://b.example", textSegment("b"))}},
		{name: "javascript link is literal", input: "[x](javascript:alert(1))", allowLinks: true, want: []inlineSegment{textSegment("[x](javascript:alert(1))")}},
		{name: "mixed case javascript link is literal", input: "[x](JaVaScRiPt:alert(1))", allowLinks: true, want: []inlineSegment{textSegment("[x](JaVaScRiPt:alert(1))")}},
		{name: "data link is literal", input: "[x](data:text/html;base64,PHNjcmlwdD4=)", allowLinks: true, want: []inlineSegment{textSegment("[x](data:text/html;base64,PHNjcmlwdD4=)")}},
		{name: "vbscript link is literal", input: "[x](vbscript:msgbox(1))", allowLinks: true, want: []inlineSegment{textSegment("[x](vbscript:msgbox(1))")}},
		{name: "relative link is literal", input: "[x](/admin)", allowLinks: true, want: []inlineSegment{textSegment("[x](/admin)")}},
		{name: "scheme relative link is literal", input: "[x](//evil.example/)", allowLinks: true, want: []inlineSegment{textSegment("[x](//evil.example/)")}},
		{name: "mailto link is literal", input: "[x](mailto:a@example.com)", allowLinks: true, want: []inlineSegment{textSegment("[x](mailto:a@example.com)")}},
		{name: "userinfo link is literal", input: "[x](https://good.example@evil.example/)", allowLinks: true, want: []inlineSegment{textSegment("[x](https://good.example@evil.example/)")}},
		{name: "quote in url is literal", input: "[x](https://a.example/\"onmouseover=\"alert(1))", allowLinks: true, want: []inlineSegment{textSegment("[x](https://a.example/\"onmouseover=\"alert(1))")}},
		{name: "empty destination is literal", input: "[x]()", allowLinks: true, want: []inlineSegment{textSegment("[x]()")}},
		{name: "disallowed link keeps label code literal", input: "[`x`](javascript:y) `z`", allowLinks: true, want: []inlineSegment{textSegment("[`x`](javascript:y) "), codeSegment("z")}},
		{name: "link inside disallowed destination stays literal", input: "[a](javascript:[b](https://b.example))", allowLinks: true, want: []inlineSegment{textSegment("[a](javascript:[b](https://b.example))")}},
		{name: "space in destination is not a link", input: "[x](https://a.example/ b)", allowLinks: true, want: []inlineSegment{textSegment("[x](https://a.example/ b)")}},
		{name: "space before destination is not a link", input: "[x] (https://a.example)", allowLinks: true, want: []inlineSegment{textSegment("[x] (https://a.example)")}},
		{name: "unclosed destination is literal", input: "[x](https://a.example", allowLinks: true, want: []inlineSegment{textSegment("[x](https://a.example")}},
		{name: "unclosed label is literal", input: "[x(https://a.example)", allowLinks: true, want: []inlineSegment{textSegment("[x(https://a.example)")}},
		{name: "stray closing bracket", input: "a] [b](https://b.example)", allowLinks: true, want: []inlineSegment{textSegment("a] "), linkSegment("https://b.example", textSegment("b"))}},
		{name: "unbalanced open bracket before link", input: "[[b](https://b.example)", allowLinks: true, want: []inlineSegment{textSegment("["), linkSegment("https://b.example", textSegment("b"))}},
		{name: "code span swallows a link", input: "[a `b](https://x.example) c` d", allowLinks: true, want: []inlineSegment{textSegment("[a "), codeSegment("b](https://x.example) c"), textSegment(" d")}},
		{name: "backtick in destination is not a link", input: "[a](https://x.example/`y)`", allowLinks: true, want: []inlineSegment{textSegment("[a](https://x.example/"), codeSegment("y)")}},
		{name: "non-ascii destination is not a link", input: "[a](https://例え.example)", allowLinks: true, want: []inlineSegment{textSegment("[a](https://例え.example)")}},
		{name: "bare url is not a link", input: "see https://a.example now", allowLinks: true, want: []inlineSegment{textSegment("see https://a.example now")}},
		{name: "multibyte text around markup", input: "日本 `コード` [リンク](https://a.example)", allowLinks: true, want: []inlineSegment{textSegment("日本 "), codeSegment("コード"), textSegment(" "), linkSegment("https://a.example", textSegment("リンク"))}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := parseInline(test.input, test.allowLinks)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parseInline(%q)\n got %#v\nwant %#v", test.input, got, test.want)
			}
		})
	}
}

func TestParseInlineAdversarialInputIsLinear(t *testing.T) {
	const size = 200000
	inputs := map[string]string{
		"open brackets":           strings.Repeat("[", size),
		"bracket paren pairs":     strings.Repeat("[](", size/3),
		"nested labels":           strings.Repeat("[a](", size/4) + strings.Repeat(")", size/4),
		"growing backtick runs":   growingBacktickRuns(size),
		"alternating code starts": strings.Repeat("`[", size/2),
		"long destination":        "[x](https://a.example/" + strings.Repeat("(", size) + ")",
	}
	for name, input := range inputs {
		t.Run(name, func(t *testing.T) {
			segments := parseInline(input, true)
			if len(segments) == 0 {
				t.Fatal("no segments")
			}
		})
	}
}

// growingBacktickRuns returns runs of 1, 2, 3, ... backticks separated by
// spaces, none of which closes, up to about size bytes.
func growingBacktickRuns(size int) string {
	var builder strings.Builder
	for length := 1; builder.Len() < size; length++ {
		builder.WriteString(strings.Repeat("`", length))
		builder.WriteByte(' ')
	}
	return builder.String()
}

func TestPlainInlineText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "text", input: "plain", want: "plain"},
		{name: "code keeps backticks", input: "use `x`", want: "use `x`"},
		{name: "link label and url", input: "[PR 1](https://a.example/1)", want: "PR 1 (https://a.example/1)"},
		{name: "link labeled with its url", input: "[https://a.example](https://a.example)", want: "https://a.example"},
		{name: "blank label", input: "[](https://a.example)", want: "https://a.example"},
		{name: "disallowed link literal", input: "[x](javascript:alert(1))", want: "[x](javascript:alert(1))"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := plainInlineText(parseInline(test.input, true)); got != test.want {
				t.Fatalf("plainInlineText(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}
