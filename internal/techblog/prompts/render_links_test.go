package prompts_test

import (
	"encoding/json"
	"html"
	"math/rand/v2"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/prompts"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/render"
)

// This test runs the parsers' output through the real renderer. It proves the
// end-to-end property the parsers exist for, and it fails if the renderer's
// inline link grammar drifts away from the one the parsers mirror.

const (
	renderedPullRequestURL = "https://github.com/superdurable/dex-connectors-library/pull/101"
	renderedCommitURL      = "https://github.com/superdurable/dex-connectors-library/commit/abcdef1234567890"
)

var hrefPattern = regexp.MustCompile(`href="([^"]*)"`)

// adversarialInlineTexts each try to smuggle a link to attacker.example past
// the parsers and into rendered output.
func adversarialInlineTexts() []string {
	nested := "x"
	for level := 0; level < 20; level++ {
		nested = "[" + nested + "](" + renderedPullRequestURL + ")"
	}
	return []string{
		"See [design doc](https://attacker.example/phish).",
		"Bare https://attacker.example/bare link.",
		"Shouted HTTPS://ATTACKER.EXAMPLE/SHOUT link.",
		"[[x](https://attacker.example/nested)](" + renderedPullRequestURL + ")",
		"[[x](https://attacker.example/nested2)](https://unlisted.example/)",
		"`\n\n[y](https://attacker.example/split)`",
		"`\n \t \n[y](https://attacker.example/blank-line)`",
		"[x `]` ](https://attacker.example/code-bracket)",
		"[a]" + string(rune(0x200B)) + "(https://attacker.example/zero-width)",
		"[a]" + string(rune(0x202E)) + "(https://attacker.example/bidi)",
		"[a]\x01(https://attacker.example/control)",
		"[a]" + string(rune(0xFEFF)) + "(https://attacker.example/bom)",
		"[[a](https://attacker.example/long)](https://x.example/" + strings.Repeat("a", 2100) + ")",
		"[https://attacker.example/label](" + renderedPullRequestURL + ")",
		"[p]https://attacker.example(https://attacker.example/rejoin)",
		"[a\n](https://attacker.example/split-label)\nb",
		"[a]\r\n(https://attacker.example/crlf)",
		nested,
		// Scheme-less web addresses, which email clients link too.
		"Log in at www.attacker.example/login today.",
		"Shouted WWW.ATTACKER.EXAMPLE link.",
		"Reset at attacker.example/reset today.",
		"Port attacker.example:8443/port today.",
		"Scheme ftp://attacker.example/ftp today.",
		"[www.attacker.example](" + renderedPullRequestURL + ")",
		"[see attacker.example/label](" + renderedPullRequestURL + ")",
		"[docs](www.attacker.example/destination)",
		"[attacker](https://x.example/q).example/joined",
	}
}

func TestParsedOutputRendersOnlyCitableLinks(t *testing.T) {
	brief := model.ResearchBrief{
		HasNotableChanges: true, Headline: "H", Overview: "O",
		Highlights: []model.ChangeHighlight{{
			Title: "Retries", WhatChanged: "W", HowItWorks: "H", WhyItMatters: "Y",
			References: []model.SourceReference{{Label: "PR 101", URL: renderedPullRequestURL}, {Label: "Commit", URL: renderedCommitURL}},
		}},
	}
	texts := adversarialInlineTexts()
	// A bullet item is one line, so the two texts whose code span is split
	// by a blank line become real code spans there, and published blog code
	// spans are kept verbatim by design. Everywhere else they are attacks.
	var items []string
	for _, text := range texts {
		if !strings.Contains(text, "`\n") {
			items = append(items, text)
		}
	}
	blocks := []model.BlogBlock{{Type: model.BlogBlockBullets, Text: "Items:", Items: items}}
	for _, text := range texts {
		blocks = append(blocks, model.BlogBlock{Type: model.BlogBlockParagraph, Text: "Before. " + text + " After."})
	}
	answer := model.BlogPost{
		Title: "Sturdier connectors [t](https://attacker.example/title)", Subtitle: "https://attacker.example/subtitle",
		Slug: "sturdier-connectors", Summary: "Summary https://attacker.example/summary", Tags: []string{"connectors"},
		Sections: []model.BlogSection{{Heading: "Heading [h](https://attacker.example/heading)", Blocks: blocks}},
		References: []model.SourceReference{
			{Label: "PR 101 https://attacker.example/reference-label", URL: renderedPullRequestURL},
			{Label: "Invented", URL: "https://attacker.example/reference"},
		},
	}
	post, err := prompts.ParseBlogPost(succeeded(t, prompts.PurposeDraftBlogPost, answer), brief)
	if err != nil {
		t.Fatalf("ParseBlogPost: %v", err)
	}
	normalized, err := render.NormalizeBlogPost(post)
	if err != nil {
		t.Fatalf("NormalizeBlogPost: %v", err)
	}
	blogHTML, err := render.RenderBlogHTML(normalized, render.BlogPresentation{SiteName: "Blog"}, time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("RenderBlogHTML: %v", err)
	}
	allowed := []string{renderedPullRequestURL, renderedCommitURL}
	assertOnlyAllowedLinks(t, "blog HTML", blogHTML, allowed)
	if !strings.Contains(blogHTML, `href="`+renderedPullRequestURL+`"`) {
		t.Errorf("blog HTML lost the citable link")
	}

	// Control: without the parser the same answer does render attacker links.
	rawNormalized, err := render.NormalizeBlogPost(answer)
	if err != nil {
		t.Fatalf("NormalizeBlogPost(raw): %v", err)
	}
	rawHTML, err := render.RenderBlogHTML(rawNormalized, render.BlogPresentation{SiteName: "Blog"}, time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("RenderBlogHTML(raw): %v", err)
	}
	if !strings.Contains(rawHTML, `href="https://attacker.example/`) {
		t.Fatalf("control failed: the unsanitized answer renders no attacker link, so this test proves nothing")
	}

	newsletterAnswer := map[string]any{
		"subject": "Connectors https://attacker.example/subject", "preheader": "[p](https://attacker.example/preheader)",
		"intro": strings.Join(texts, " "), "closing": "Bye " + texts[0],
		"highlights": []any{
			map[string]any{"title": "One https://attacker.example/t1", "text": texts[3]},
			map[string]any{"title": "Two", "text": texts[5] + " " + texts[14]},
			map[string]any{"title": "Three", "text": "[PR 101](" + renderedPullRequestURL + ")"},
		},
	}
	draft, err := prompts.ParseNewsletterDraft(succeeded(t, prompts.PurposeDraftNewsletter, newsletterAnswer))
	if err != nil {
		t.Fatalf("ParseNewsletterDraft: %v", err)
	}
	rendered, err := render.RenderNewsletter(draft, post, render.BlogPresentation{SiteName: "Blog"}, "Footer.")
	if err != nil {
		t.Fatalf("RenderNewsletter: %v", err)
	}
	assertOnlyAllowedLinks(t, "newsletter HTML", rendered.HTMLBody, allowed)
	if draft.Intro == "" || len(draft.Highlights) != 3 {
		t.Errorf("newsletter copy lost content: intro %q, %d highlights", draft.Intro, len(draft.Highlights))
	}
	for name, text := range map[string]string{"subject": rendered.Subject, "text body": rendered.TextBody} {
		if strings.Contains(strings.ToLower(text), "attacker.example") {
			t.Errorf("newsletter %s mentions the attacker host", name)
		}
	}
}

// codeElementPattern matches rendered code spans and code blocks, whose text
// the parsers keep verbatim in published blog text by design.
var codeElementPattern = regexp.MustCompile(`(?s)<code[^>]*>.*?</code>`)

// TestRandomMarkupRendersOnlyCitableLinks cross-checks the parsers' mirror of
// the renderer's inline grammar on many deterministic pseudo-random texts
// built from markup delimiters, whitespace, invisible characters, and URLs.
func TestRandomMarkupRendersOnlyCitableLinks(t *testing.T) {
	pieces := []string{
		"[", "]", "(", ")", "`", "``", " ", "\n", "\n\n", "\t", "a", "h", "://", ".", string(rune(0x200B)), string(rune(0x202E)),
		"https://attacker.example/x", "HTTPS://ATTACKER.EXAMPLE", "http://attacker.example", renderedPullRequestURL,
		strings.ToUpper(renderedPullRequestURL) + "/", "[l](", "](", "](https://attacker.example/y)", "](" + renderedPullRequestURL + ")",
		"www.", "attacker.example/z", "www.attacker.example", strings.TrimPrefix(renderedPullRequestURL, "https://"),
	}
	generator := rand.New(rand.NewPCG(1, 2))
	randomPieces := func(maximum int) string {
		var text strings.Builder
		for count := 1 + generator.IntN(maximum); count > 0; count-- {
			text.WriteString(pieces[generator.IntN(len(pieces))])
		}
		return text.String()
	}
	randomText := func() string { return randomPieces(24) }
	brief := model.ResearchBrief{
		HasNotableChanges: true,
		Highlights:        []model.ChangeHighlight{{Title: "T", References: []model.SourceReference{{Label: "PR 101", URL: renderedPullRequestURL}}}},
	}
	allowed := []string{renderedPullRequestURL}
	presentation := render.BlogPresentation{SiteName: "Blog"}
	renderedRounds, rawAttackRounds := 0, 0
	defer func() {
		if renderedRounds < 100 || rawAttackRounds < 50 {
			t.Errorf("only %d rounds rendered and %d unsanitized answers rendered attacker links; the test lost its power",
				renderedRounds, rawAttackRounds)
		}
	}()
	for round := 0; round < 150; round++ {
		blocks := []model.BlogBlock{{Type: model.BlogBlockParagraph, Text: "Anchor."}}
		for count := 0; count < 12; count++ {
			blocks = append(blocks,
				model.BlogBlock{Type: model.BlogBlockParagraph, Text: randomText()},
				model.BlogBlock{Type: model.BlogBlockBullets, Items: []string{randomText(), randomText()}})
		}
		answer := model.BlogPost{Title: "Title " + randomPieces(4), Summary: randomText(), References: []model.SourceReference{},
			Sections: []model.BlogSection{{Heading: "Heading " + randomPieces(4), Blocks: blocks}}}
		post, err := prompts.ParseBlogPost(succeeded(t, prompts.PurposeDraftBlogPost, answer), brief)
		if err != nil {
			continue // a title reduced to nothing is a legitimate rejection
		}
		normalized, err := render.NormalizeBlogPost(post)
		if err != nil {
			t.Fatalf("round %d: NormalizeBlogPost: %v", round, err)
		}
		blogHTML, err := render.RenderBlogHTML(normalized, presentation, time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatalf("round %d: RenderBlogHTML: %v", round, err)
		}
		assertOnlyAllowedLinks(t, "blog HTML", codeElementPattern.ReplaceAllString(blogHTML, ""), allowed)
		if t.Failed() {
			encoded, _ := json.Marshal(answer)
			t.Fatalf("round %d answer: %s", round, encoded)
		}
		renderedRounds++
		if rawPost, err := render.NormalizeBlogPost(answer); err == nil {
			if rawHTML, err := render.RenderBlogHTML(rawPost, presentation, time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)); err == nil &&
				strings.Contains(strings.ToLower(rawHTML), `href="https://attacker.example`) {
				rawAttackRounds++
			}
		}

		newsletterAnswer := map[string]any{"subject": "S " + randomText(), "preheader": randomText(), "intro": "I " + randomText(),
			"closing": randomText(), "highlights": []any{map[string]any{"title": "T " + randomText(), "text": "X " + randomText()}}}
		draft, err := prompts.ParseNewsletterDraft(succeeded(t, prompts.PurposeDraftNewsletter, newsletterAnswer))
		if err != nil {
			continue
		}
		for name, field := range map[string]string{"subject": draft.Subject, "preheader": draft.Preheader, "intro": draft.Intro,
			"closing": draft.Closing, "highlight title": draft.Highlights[0].Title, "highlight text": draft.Highlights[0].Text} {
			if strings.Contains(strings.ToLower(field), "attacker.example") || strings.Contains(field, "://"+"github.com") {
				t.Fatalf("round %d: newsletter %s %q keeps a URL", round, name, field)
			}
		}
		rendered, err := render.RenderNewsletter(draft, post, presentation, "Footer.")
		if err != nil {
			t.Fatalf("round %d: RenderNewsletter: %v", round, err)
		}
		assertOnlyAllowedLinks(t, "newsletter HTML", codeElementPattern.ReplaceAllString(rendered.HTMLBody, ""), allowed)
		if t.Failed() {
			t.Fatalf("round %d newsletter: %+v", round, draft)
		}
	}
}

func assertOnlyAllowedLinks(t *testing.T, name string, document string, allowed []string) {
	t.Helper()
	for _, match := range hrefPattern.FindAllStringSubmatch(document, -1) {
		if url := html.UnescapeString(match[1]); !slices.Contains(allowed, url) {
			t.Errorf("%s links to %q", name, url)
		}
	}
	if strings.Contains(strings.ToLower(document), "attacker.example") {
		t.Errorf("%s mentions the attacker host", name)
	}
}

func succeeded(t *testing.T, purpose string, answer any) model.GenerationResult {
	t.Helper()
	text, err := json.Marshal(answer)
	if err != nil {
		t.Fatalf("encode answer: %v", err)
	}
	return model.GenerationResult{Purpose: purpose, Status: model.GenerationSucceeded, Text: string(text)}
}
