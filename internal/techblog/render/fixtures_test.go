package render

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

// sampleGeneratedAt is the fixed render time used by the tests.
var sampleGeneratedAt = time.Date(2026, time.September, 26, 23, 30, 0, 0, time.FixedZone("PDT", -7*60*60))

// sampleBlogPost returns a small, valid, already-normalized blog post that
// exercises every block type.
func sampleBlogPost() model.BlogPost {
	return model.BlogPost{
		Title:    "Connector retries moved into the SDK",
		Subtitle: "What changed in the connector SDK over the past two weeks",
		Slug:     "connector-retries-sdk",
		Summary:  "Connector Steps now share one retry policy.",
		Tags:     []string{"connectors", "sdk"},
		Sections: []model.BlogSection{
			{
				Heading: "Why the policy moved",
				Blocks: []model.BlogBlock{
					{Type: model.BlogBlockParagraph, Text: "Each connector used to retry on its own. Now `sdkgo` owns the policy; see [PR 42](https://github.com/superdurable/dex-connectors-library/pull/42).", Items: []string{}},
					{Type: model.BlogBlockBullets, Items: []string{"One policy for every connector", "Attempts are recorded as `ConnectorAttempt` events"}},
				},
			},
			{
				Heading: "How it works",
				Blocks: []model.BlogBlock{
					{Type: model.BlogBlockCode, Language: "go", Code: "policy := sdkgo.RetryPolicy{\n\tMaxAttempts: 5,\n}", Items: []string{}},
					{Type: model.BlogBlockQuote, Text: "Retries belong to the call, not the connector.", Items: []string{}},
					{Type: model.BlogBlockCallout, Text: "Existing connectors adopt the policy after a rebuild.", Items: []string{}},
				},
			},
		},
		References: []model.SourceReference{
			{Label: "PR 42: move retries into sdkgo", URL: "https://github.com/superdurable/dex-connectors-library/pull/42"},
		},
	}
}

// samplePresentation returns a presentation with a public base URL.
func samplePresentation() BlogPresentation {
	return BlogPresentation{SiteName: "Dex Engineering Blog", Author: "The Dex team", PublicBaseURL: "https://blog.example.com/posts/"}
}

// sampleNewsletterDraft returns valid newsletter copy.
func sampleNewsletterDraft() model.NewsletterDraft {
	return model.NewsletterDraft{
		Subject:   "Connector retries moved into the SDK",
		Preheader: "One retry policy for every connector.",
		Intro:     "This week the connector SDK took over retries.\n\nHere is what that means for you.",
		Highlights: []model.NewsletterHighlight{
			{Title: "Shared policy", Text: "Every connector now uses `sdkgo.RetryPolicy`."},
			{Title: "", Text: "Attempts show up in [Dex Web](https://dex.example.com/flows)."},
		},
		Closing: "Thanks for reading.",
	}
}

var (
	markupTagPattern       = regexp.MustCompile(`<(/?)([a-zA-Z][a-zA-Z0-9]*)([^>]*)>`)
	markupAttributePattern = regexp.MustCompile(`^\s+([a-zA-Z][a-zA-Z-]*)(="([^"]*)")?`)
	styleElementPattern    = regexp.MustCompile(`(?s)<style>(.*?)</style>`)
)

// blogAllowedTags are the only elements the blog artifact may contain.
var blogAllowedTags = map[string]bool{
	"html": true, "head": true, "meta": true, "title": true, "style": true, "body": true,
	"div": true, "header": true, "main": true, "article": true, "section": true, "footer": true,
	"h1": true, "h2": true, "p": true, "ul": true, "ol": true, "li": true, "pre": true,
	"code": true, "blockquote": true, "aside": true, "a": true, "time": true,
}

// newsletterAllowedTags are the only elements the newsletter email may
// contain.
var newsletterAllowedTags = map[string]bool{
	"html": true, "head": true, "meta": true, "title": true, "body": true, "span": true,
	"table": true, "tr": true, "td": true, "h1": true, "h2": true, "p": true, "ul": true,
	"ol": true, "li": true, "pre": true, "code": true, "blockquote": true, "a": true,
	"strong": true,
}

// allowedAttributes are the only attribute names either document may use.
var allowedAttributes = map[string]bool{
	"lang": true, "charset": true, "http-equiv": true, "content": true, "name": true,
	"class": true, "href": true, "rel": true, "datetime": true, "style": true, "role": true,
	"width": true, "cellpadding": true, "cellspacing": true, "border": true, "align": true,
	"bgcolor": true,
}

// assertSafeMarkup fails the test when markupProblems finds any problem.
func assertSafeMarkup(t *testing.T, document string, allowedTags map[string]bool) {
	t.Helper()
	for _, problem := range markupProblems(document, allowedTags) {
		t.Error(problem)
	}
}

// markupProblems checks that every tag in document is an allowed element with
// only allowed, double-quoted attributes, that every href is an http or https
// URL, and that no script, image, frame, or similar element exists.
func markupProblems(document string, allowedTags map[string]bool) []string {
	var problems []string
	for _, match := range markupTagPattern.FindAllStringSubmatch(document, -1) {
		tagName := strings.ToLower(match[2])
		if !allowedTags[tagName] {
			problems = append(problems, "unexpected element <"+tagName+"> in "+match[0])
			continue
		}
		if match[1] == "/" {
			if strings.TrimSpace(match[3]) != "" {
				problems = append(problems, "closing tag with attributes: "+match[0])
			}
			continue
		}
		remaining := match[3]
		for remaining != "" {
			attribute := markupAttributePattern.FindStringSubmatch(remaining)
			if attribute == nil {
				problems = append(problems, "unparseable attribute text "+remaining+" in tag "+match[0])
				break
			}
			attributeName := strings.ToLower(attribute[1])
			if !allowedAttributes[attributeName] {
				problems = append(problems, "unexpected attribute "+attributeName+" in tag "+match[0])
			}
			if attributeName == "href" {
				value := attribute[3]
				if !strings.HasPrefix(value, "https://") && !strings.HasPrefix(value, "http://") {
					problems = append(problems, "href "+value+" is not an http(s) URL")
				}
			}
			remaining = remaining[len(attribute[0]):]
		}
	}
	lowered := strings.ToLower(document)
	for _, forbidden := range []string{"<script", "<img", "<iframe", "<link", "<object", "<embed", "<svg", "<form", "<base", "href=\"javascript", "href=\"data", "href=\"vbscript"} {
		if strings.Contains(lowered, forbidden) {
			problems = append(problems, "document contains forbidden "+forbidden)
		}
	}
	return problems
}

// styleElementContents returns the contents of every style element.
func styleElementContents(document string) []string {
	var contents []string
	for _, match := range styleElementPattern.FindAllStringSubmatch(document, -1) {
		contents = append(contents, match[1])
	}
	return contents
}
