package render

import (
	"strings"
	"testing"
	"time"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

func TestRenderBlogHTMLDocumentStructure(t *testing.T) {
	document, err := RenderBlogHTML(sampleBlogPost(), samplePresentation(), sampleGeneratedAt)
	if err != nil {
		t.Fatalf("RenderBlogHTML: %v", err)
	}
	if !strings.HasPrefix(document, "<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n") {
		t.Fatalf("document does not start with doctype, html, head, and charset:\n%.200s", document)
	}
	required := []string{
		`<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:">`,
		`<meta name="viewport" content="width=device-width, initial-scale=1">`,
		`<title>Connector retries moved into the SDK · Dex Engineering Blog</title>`,
		`<meta name="description" content="Connector Steps now share one retry policy.">`,
		"<style>\n",
		"prefers-color-scheme:dark",
		"max-width:68ch",
		`<p class="site-name">Dex Engineering Blog</p>`,
		`<h1>Connector retries moved into the SDK</h1>`,
		`<p class="subtitle">What changed in the connector SDK over the past two weeks</p>`,
		// 23:30 PDT on September 26 is September 27 in UTC.
		`<p class="byline">By The Dex team · <time datetime="2026-09-27">September 27, 2026</time></p>`,
		"<ul class=\"tags\">\n<li>connectors</li>\n<li>sdk</li>\n</ul>",
		`<h2>Why the policy moved</h2>`,
		`<h2>How it works</h2>`,
		`Now <code>sdkgo</code> owns the policy; see <a href="https://github.com/superdurable/dex-connectors-library/pull/42" rel="noopener">PR 42</a>.`,
		"<ul>\n<li>One policy for every connector</li>\n<li>Attempts are recorded as <code>ConnectorAttempt</code> events</li>\n</ul>",
		"<pre><code class=\"language-go\">policy := sdkgo.RetryPolicy{\n\tMaxAttempts: 5,\n}</code></pre>",
		"<blockquote>\n<p>Retries belong to the call, not the connector.</p>\n</blockquote>",
		"<aside class=\"callout\">\n<p>Existing connectors adopt the policy after a rebuild.</p>\n</aside>",
		`<section class="references">`,
		`<li><a href="https://github.com/superdurable/dex-connectors-library/pull/42" rel="noopener">PR 42: move retries into sdkgo</a></li>`,
		`<footer class="site-footer">`,
		`<a href="https://blog.example.com/posts/connector-retries-sdk.html" rel="noopener">Permanent link to this post</a>`,
	}
	for _, fragment := range required {
		if !strings.Contains(document, fragment) {
			t.Errorf("document is missing %q", fragment)
		}
	}
	if !strings.HasSuffix(document, "</body>\n</html>\n") {
		t.Errorf("document does not end with </body></html>")
	}
	if strings.Index(document, "Content-Security-Policy") > strings.Index(document, "<style>") {
		t.Errorf("the Content-Security-Policy must precede the style element")
	}
	if count := strings.Count(document, "<h2>"); count != 3 {
		t.Errorf("document has %d h2 headings, want 2 sections plus references", count)
	}
	assertSafeMarkup(t, document, blogAllowedTags)
}

func TestRenderBlogHTMLIsSelfContained(t *testing.T) {
	document, err := RenderBlogHTML(sampleBlogPost(), samplePresentation(), sampleGeneratedAt)
	if err != nil {
		t.Fatalf("RenderBlogHTML: %v", err)
	}
	lowered := strings.ToLower(document)
	for _, forbidden := range []string{"src=", "<link", "<script", "<img", "<iframe", "<object", "<embed", "<video", "<audio", "<source", "@import", "url(", "@font-face", "srcset"} {
		if strings.Contains(lowered, forbidden) {
			t.Errorf("document references an external resource: contains %q", forbidden)
		}
	}
	styles := styleElementContents(document)
	if len(styles) != 1 {
		t.Fatalf("document has %d style elements, want 1", len(styles))
	}
	for _, forbidden := range []string{"http", "//", "url(", "@import", "expression("} {
		if strings.Contains(strings.ToLower(styles[0]), forbidden) {
			t.Errorf("style element contains %q", forbidden)
		}
	}
	if strings.Contains(blogStyleSheet, "</") {
		t.Errorf("style sheet must not contain an end-tag sequence")
	}
}

func TestRenderBlogHTMLEscapesInjection(t *testing.T) {
	payloads := []string{
		"<script>alert(1)</script>",
		"\"><img src=x onerror=alert(1)>",
		"' onmouseover='alert(1)",
		"\" onmouseover=\"alert(1)",
		"</style><script>alert(1)</script>",
		"</title><script>alert(1)</script>",
		"<!-- comment --><iframe src=//evil.example>",
		"[x](javascript:alert(1))",
		"[x](JaVaScRiPt:alert(1))",
		"[x](data:text/html;base64,PHNjcmlwdD4=)",
		"[x](vbscript:msgbox(1))",
		"[x](//evil.example/steal)",
		"[x](/relative)",
		"[x](https://ok.example/\"onmouseover=\"alert(1))",
		"`</code><script>alert(1)</script>`",
		"[`</a><script>`](https://ok.example)",
		"&lt;script&gt;alert(1)&lt;/script&gt;",
	}
	fields := []struct {
		name   string
		mutate func(post *model.BlogPost, presentation *BlogPresentation, payload string)
	}{
		{name: "title", mutate: func(post *model.BlogPost, _ *BlogPresentation, payload string) { post.Title = payload }},
		{name: "subtitle", mutate: func(post *model.BlogPost, _ *BlogPresentation, payload string) { post.Subtitle = payload }},
		{name: "summary", mutate: func(post *model.BlogPost, _ *BlogPresentation, payload string) { post.Summary = payload }},
		{name: "slug", mutate: func(post *model.BlogPost, _ *BlogPresentation, payload string) { post.Slug = payload }},
		{name: "tag", mutate: func(post *model.BlogPost, _ *BlogPresentation, payload string) { post.Tags = []string{payload} }},
		{name: "heading", mutate: func(post *model.BlogPost, _ *BlogPresentation, payload string) { post.Sections[0].Heading = payload }},
		{name: "paragraph", mutate: func(post *model.BlogPost, _ *BlogPresentation, payload string) {
			post.Sections[0].Blocks[0].Text = payload
		}},
		{name: "bullet item", mutate: func(post *model.BlogPost, _ *BlogPresentation, payload string) {
			post.Sections[0].Blocks[1].Items = []string{payload}
		}},
		{name: "bullet lead-in", mutate: func(post *model.BlogPost, _ *BlogPresentation, payload string) {
			post.Sections[0].Blocks[1].Text = payload
		}},
		{name: "code", mutate: func(post *model.BlogPost, _ *BlogPresentation, payload string) {
			post.Sections[1].Blocks[0].Code = payload
		}},
		{name: "code language", mutate: func(post *model.BlogPost, _ *BlogPresentation, payload string) {
			post.Sections[1].Blocks[0].Language = payload
		}},
		{name: "block type", mutate: func(post *model.BlogPost, _ *BlogPresentation, payload string) {
			post.Sections[1].Blocks[0].Type = model.BlogBlockType(payload)
			post.Sections[1].Blocks[0].Text = payload
		}},
		{name: "quote", mutate: func(post *model.BlogPost, _ *BlogPresentation, payload string) {
			post.Sections[1].Blocks[1].Text = payload
		}},
		{name: "callout", mutate: func(post *model.BlogPost, _ *BlogPresentation, payload string) {
			post.Sections[1].Blocks[2].Text = payload
		}},
		{name: "reference label", mutate: func(post *model.BlogPost, _ *BlogPresentation, payload string) {
			post.References = []model.SourceReference{{Label: payload, URL: "https://ok.example/pr"}}
		}},
		{name: "reference url", mutate: func(post *model.BlogPost, _ *BlogPresentation, payload string) {
			post.References = []model.SourceReference{{Label: "ref", URL: payload}}
		}},
		{name: "site name", mutate: func(_ *model.BlogPost, presentation *BlogPresentation, payload string) {
			presentation.SiteName = payload
		}},
		{name: "author", mutate: func(_ *model.BlogPost, presentation *BlogPresentation, payload string) { presentation.Author = payload }},
		{name: "public base url", mutate: func(_ *model.BlogPost, presentation *BlogPresentation, payload string) {
			presentation.PublicBaseURL = payload
		}},
	}
	for _, field := range fields {
		for _, payload := range payloads {
			t.Run(field.name+"/"+payload, func(t *testing.T) {
				post := sampleBlogPost()
				presentation := samplePresentation()
				field.mutate(&post, &presentation, payload)
				document, err := RenderBlogHTML(post, presentation, sampleGeneratedAt)
				if err != nil {
					t.Fatalf("RenderBlogHTML: %v", err)
				}
				assertSafeMarkup(t, document, blogAllowedTags)
				if count := strings.Count(document, "<style>"); count != 1 {
					t.Errorf("document has %d style elements", count)
				}
				if count := strings.Count(document, "</title>"); count != 1 {
					t.Errorf("document has %d title end tags", count)
				}
			})
		}
	}
}

func TestRenderBlogHTMLInlineMarkup(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "code span", text: "Run `go test ./...` first.", want: "<p>Run <code>go test ./...</code> first.</p>"},
		{name: "code span escapes html", text: "`<b>&</b>`", want: "<p><code>&lt;b&gt;&amp;&lt;/b&gt;</code></p>"},
		{name: "https link", text: "[PR](https://github.com/o/r/pull/1)", want: `<p><a href="https://github.com/o/r/pull/1" rel="noopener">PR</a></p>`},
		{name: "http link", text: "[site](http://example.com/a?b=1&c=2)", want: `<p><a href="http://example.com/a?b=1&amp;c=2" rel="noopener">site</a></p>`},
		{name: "link label with code", text: "[`dexcli`](https://a.example)", want: `<p><a href="https://a.example" rel="noopener"><code>dexcli</code></a></p>`},
		{name: "javascript link is escaped text", text: "[x](javascript:alert(1))", want: "<p>[x](javascript:alert(1))</p>"},
		{name: "data link is escaped text", text: "[x](data:text/html,<script>)", want: "<p>[x](data:text/html,&lt;script&gt;)</p>"},
		{name: "relative link is escaped text", text: "[x](../admin)", want: "<p>[x](../admin)</p>"},
		{name: "unbalanced backtick literal", text: "a `b", want: "<p>a `b</p>"},
		{name: "unbalanced bracket literal", text: "[a](https://a.example", want: "<p>[a](https://a.example</p>"},
		{name: "raw html escaped", text: "<em>hi</em> & \"q\" 'a'", want: "<p>&lt;em&gt;hi&lt;/em&gt; &amp; &#34;q&#34; &#39;a&#39;</p>"},
		{name: "paragraphs split on blank lines", text: "One\n\nTwo", want: "<p>One</p>\n<p>Two</p>"},
		{name: "parentheses in url normalized", text: "[Go](https://en.wikipedia.org/wiki/Go_(language))", want: `<a href="https://en.wikipedia.org/wiki/Go_%28language%29" rel="noopener">Go</a>`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			post := model.BlogPost{Title: "T", Sections: []model.BlogSection{paragraphSection("", test.text)}}
			document, err := RenderBlogHTML(post, BlogPresentation{}, time.Time{})
			if err != nil {
				t.Fatalf("RenderBlogHTML: %v", err)
			}
			if !strings.Contains(document, test.want) {
				t.Fatalf("document does not contain %q:\n%s", test.want, articleOf(document))
			}
		})
	}
}

// articleOf returns the article element of a blog document for failure
// messages.
func articleOf(document string) string {
	start := strings.Index(document, "<article>")
	end := strings.Index(document, "</article>")
	if start < 0 || end < start {
		return document
	}
	return document[start : end+len("</article>")]
}

func TestRenderBlogHTMLHeadingSupportsCodeSpansOnly(t *testing.T) {
	post := model.BlogPost{Title: "T", Sections: []model.BlogSection{paragraphSection("Meet `dexcli` and [docs](https://a.example)", "body")}}
	document, err := RenderBlogHTML(post, BlogPresentation{}, time.Time{})
	if err != nil {
		t.Fatalf("RenderBlogHTML: %v", err)
	}
	want := "<h2>Meet <code>dexcli</code> and [docs](https://a.example)</h2>"
	if !strings.Contains(document, want) {
		t.Fatalf("document does not contain %q:\n%s", want, articleOf(document))
	}
}

func TestRenderBlogHTMLByline(t *testing.T) {
	tests := []struct {
		name        string
		author      string
		generatedAt time.Time
		want        string
		absent      string
	}{
		{name: "author and date", author: "Ada", generatedAt: time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC), want: `<p class="byline">By Ada · <time datetime="2026-03-05">March 5, 2026</time></p>`},
		{name: "date converted to utc", author: "Ada", generatedAt: time.Date(2026, time.January, 1, 1, 0, 0, 0, time.FixedZone("CET", 3600)), want: `<time datetime="2026-01-01">January 1, 2026</time>`},
		{name: "date without author", author: "  ", generatedAt: time.Date(2026, time.March, 5, 0, 0, 0, 0, time.UTC), want: `<p class="byline"><time datetime="2026-03-05">March 5, 2026</time></p>`, absent: "By "},
		{name: "author without date", author: "Ada", generatedAt: time.Time{}, want: `<p class="byline">By Ada</p>`, absent: "<time"},
		{name: "neither", author: "", generatedAt: time.Time{}, want: "<h1>T</h1>", absent: `class="byline"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			post := model.BlogPost{Title: "T", Sections: []model.BlogSection{paragraphSection("", "body")}}
			document, err := RenderBlogHTML(post, BlogPresentation{Author: test.author}, test.generatedAt)
			if err != nil {
				t.Fatalf("RenderBlogHTML: %v", err)
			}
			if !strings.Contains(document, test.want) {
				t.Errorf("document does not contain %q", test.want)
			}
			if test.absent != "" && strings.Contains(document, test.absent) {
				t.Errorf("document contains %q", test.absent)
			}
		})
	}
}

func TestRenderBlogHTMLOptionalParts(t *testing.T) {
	post := model.BlogPost{Title: "Only title", Sections: []model.BlogSection{paragraphSection("", "body")}}
	document, err := RenderBlogHTML(post, BlogPresentation{}, time.Time{})
	if err != nil {
		t.Fatalf("RenderBlogHTML: %v", err)
	}
	for _, absent := range []string{`name="description"`, `class="site-name"`, `class="subtitle"`, `class="tags"`, `class="references"`, "<h2>", "Permanent link", "<a "} {
		if strings.Contains(document, absent) {
			t.Errorf("minimal document contains %q", absent)
		}
	}
	if !strings.Contains(document, "<title>Only title</title>") {
		t.Errorf("document title without site name is wrong")
	}
	assertSafeMarkup(t, document, blogAllowedTags)
}

func TestRenderBlogHTMLCodeBlocks(t *testing.T) {
	tests := []struct {
		name     string
		block    model.BlogBlock
		want     string
		mustSkip string
	}{
		{name: "language class", block: model.BlogBlock{Type: model.BlogBlockCode, Language: "go", Code: "x := 1"}, want: `<pre><code class="language-go">x := 1</code></pre>`},
		{name: "plus signs escaped in class", block: model.BlogBlock{Type: model.BlogBlockCode, Language: "C++", Code: "int x;"}, want: `<code class="language-c&#43;&#43;">`},
		{name: "no language no class", block: model.BlogBlock{Type: model.BlogBlockCode, Code: "plain"}, want: "<pre><code>plain</code></pre>"},
		{name: "hostile language sanitized", block: model.BlogBlock{Type: model.BlogBlockCode, Language: "\" onload=\"x", Code: "y"}, want: `<code class="language-onloadx">`},
		{name: "code html escaped and whitespace kept", block: model.BlogBlock{Type: model.BlogBlockCode, Code: "if a < b && c > d {\n    `tick`\n}"}, want: "<pre><code>if a &lt; b &amp;&amp; c &gt; d {\n    `tick`\n}</code></pre>"},
		{name: "code is not inline-parsed", block: model.BlogBlock{Type: model.BlogBlockCode, Code: "[x](https://a.example)"}, want: "<pre><code>[x](https://a.example)</code></pre>", mustSkip: "<a "},
		{name: "lead-in rendered before listing", block: model.BlogBlock{Type: model.BlogBlockCode, Text: "Try `this`:", Code: "run"}, want: "<p>Try <code>this</code>:</p>\n<pre><code>run</code></pre>"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			post := model.BlogPost{Title: "T", Sections: []model.BlogSection{{Blocks: []model.BlogBlock{test.block}}}}
			document, err := RenderBlogHTML(post, BlogPresentation{}, time.Time{})
			if err != nil {
				t.Fatalf("RenderBlogHTML: %v", err)
			}
			if !strings.Contains(document, test.want) {
				t.Fatalf("document does not contain %q:\n%s", test.want, articleOf(document))
			}
			if test.mustSkip != "" && strings.Contains(document, test.mustSkip) {
				t.Fatalf("document contains %q", test.mustSkip)
			}
		})
	}
}

func TestRenderBlogHTMLUnknownBlockTypes(t *testing.T) {
	post := model.BlogPost{Title: "T", Sections: []model.BlogSection{{Blocks: []model.BlogBlock{
		{Type: "table", Text: "Rendered as a paragraph"},
		{Type: "image", Code: "https://evil.example/x.png"},
		{Type: "", Text: "Typeless text"},
	}}}}
	document, err := RenderBlogHTML(post, BlogPresentation{}, time.Time{})
	if err != nil {
		t.Fatalf("RenderBlogHTML: %v", err)
	}
	for _, want := range []string{"<p>Rendered as a paragraph</p>", "<p>Typeless text</p>"} {
		if !strings.Contains(document, want) {
			t.Errorf("document does not contain %q", want)
		}
	}
	if strings.Contains(document, "evil.example") {
		t.Errorf("dropped block content leaked into the document")
	}
}

func TestRenderBlogHTMLIsDeterministic(t *testing.T) {
	first, err := RenderBlogHTML(sampleBlogPost(), samplePresentation(), sampleGeneratedAt)
	if err != nil {
		t.Fatalf("RenderBlogHTML: %v", err)
	}
	for attempt := 0; attempt < 5; attempt++ {
		again, err := RenderBlogHTML(sampleBlogPost(), samplePresentation(), sampleGeneratedAt)
		if err != nil {
			t.Fatalf("RenderBlogHTML: %v", err)
		}
		if again != first {
			t.Fatalf("render %d differs from the first render", attempt+1)
		}
	}
	sameInstant, err := RenderBlogHTML(sampleBlogPost(), samplePresentation(), sampleGeneratedAt.UTC())
	if err != nil {
		t.Fatalf("RenderBlogHTML: %v", err)
	}
	if sameInstant != first {
		t.Fatalf("the same instant in another location renders differently")
	}
}

func TestRenderBlogHTMLBoundsDocumentSize(t *testing.T) {
	// One-character quote items: every item costs 16 bytes of markup and
	// escaping for one character of text.
	items := make([]string, maxItemsPerBulletsBlock)
	for index := range items {
		items[index] = `"`
	}
	blocks := make([]model.BlogBlock, 49)
	for index := range blocks {
		blocks[index] = model.BlogBlock{Type: model.BlogBlockBullets, Items: items}
	}
	markupDense := model.BlogPost{Title: "T"}
	for index := 0; index < maxSectionCount; index++ {
		markupDense.Sections = append(markupDense.Sections, model.BlogSection{Blocks: blocks})
	}
	if _, err := NormalizeBlogPost(markupDense); err != nil {
		t.Fatalf("the markup-dense post must be within the text bounds: %v", err)
	}
	document, err := RenderBlogHTML(markupDense, samplePresentation(), sampleGeneratedAt)
	if err == nil || !strings.Contains(err.Error(), "blog HTML document renders to") || !strings.Contains(err.Error(), "the limit is 2097152") {
		t.Fatalf("error = %v, want the blog document size limit", err)
	}
	if document != "" {
		t.Fatalf("an oversized render returned a document")
	}

	tests := []struct {
		name string
		post model.BlogPost
	}{
		{name: "realistic long post", post: realisticLongBlogPost()},
		{name: "every character escaped", post: model.BlogPost{Title: "T", Sections: func() []model.BlogSection {
			var sections []model.BlogSection
			for index := 0; index < maxSectionCount; index++ {
				sections = append(sections, paragraphSection("", strings.Repeat(`"`, 4900)))
			}
			return sections
		}()}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document, err := RenderBlogHTML(test.post, samplePresentation(), sampleGeneratedAt)
			if err != nil {
				t.Fatalf("a post within the text bounds must render: %v", err)
			}
			if len(document) > maxBlogDocumentBytes {
				t.Fatalf("document has %d bytes", len(document))
			}
		})
	}
}

func TestRenderBlogHTMLRejectsInvalidPost(t *testing.T) {
	tests := []struct {
		name string
		post model.BlogPost
	}{
		{name: "empty post", post: model.BlogPost{}},
		{name: "no content", post: model.BlogPost{Title: "T", Sections: []model.BlogSection{{Heading: "H"}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document, err := RenderBlogHTML(test.post, samplePresentation(), sampleGeneratedAt)
			if err == nil {
				t.Fatalf("RenderBlogHTML succeeded")
			}
			if document != "" {
				t.Fatalf("RenderBlogHTML returned a document with an error")
			}
		})
	}
}
