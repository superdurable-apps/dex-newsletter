package render

import (
	"strings"
	"testing"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

const sampleFooter = "You subscribed to Dex updates.\n\n[Unsubscribe](https://dex.example.com/unsubscribe)"

func TestRenderNewsletterSubject(t *testing.T) {
	tests := []struct {
		name      string
		subject   string
		want      string
		wantError string
	}{
		{name: "plain", subject: "Weekly update", want: "Weekly update"},
		{name: "trimmed", subject: "  Weekly update \t", want: "Weekly update"},
		{name: "CRLF header injection flattened", subject: "Hello\r\nBcc: victim@example.com", want: "Hello Bcc: victim@example.com"},
		{name: "bare CR and LF", subject: "a\rb\nc", want: "a b c"},
		{name: "unicode line separators", subject: "a\u2028b\u2029c\u0085d", want: "a b c d"},
		{name: "control characters removed", subject: "a\x00b\x07c\x1b[31md", want: "abc[31md"},
		{name: "bidi override replaced", subject: "invoice\u202egpj.exe", want: "invoice gpj.exe"},
		{name: "limit is allowed", subject: strings.Repeat("s", maxSubjectCharacters), want: strings.Repeat("s", maxSubjectCharacters)},
		{name: "empty", subject: "", wantError: "subject is required"},
		{name: "only line breaks", subject: "\r\n\r\n", wantError: "subject is required"},
		{name: "too long", subject: strings.Repeat("s", maxSubjectCharacters+1), wantError: "151 characters"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			draft := sampleNewsletterDraft()
			draft.Subject = test.subject
			rendered, err := RenderNewsletter(draft, sampleBlogPost(), samplePresentation(), sampleFooter)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error = %v, want one mentioning %q", err, test.wantError)
				}
				if rendered != (model.RenderedNewsletter{}) {
					t.Fatalf("failed render returned content")
				}
				return
			}
			if err != nil {
				t.Fatalf("RenderNewsletter: %v", err)
			}
			if rendered.Subject != test.want {
				t.Fatalf("subject = %q, want %q", rendered.Subject, test.want)
			}
			if strings.ContainsAny(rendered.Subject, "\r\n") {
				t.Fatalf("subject contains a line break")
			}
			for _, character := range rendered.Subject {
				if character < ' ' || character == 0x7f {
					t.Fatalf("subject contains control character %q", character)
				}
			}
		})
	}
}

func TestRenderNewsletterValidatesDraft(t *testing.T) {
	manyHighlights := make([]model.NewsletterHighlight, maxHighlightCount+1)
	for index := range manyHighlights {
		manyHighlights[index] = model.NewsletterHighlight{Title: "t", Text: "x"}
	}
	tests := []struct {
		name      string
		mutate    func(draft *model.NewsletterDraft, post *model.BlogPost, footer *string)
		wantError string
	}{
		{name: "missing intro", mutate: func(draft *model.NewsletterDraft, _ *model.BlogPost, _ *string) { draft.Intro = " \n\n " }, wantError: "intro is required"},
		{name: "intro too long", mutate: func(draft *model.NewsletterDraft, _ *model.BlogPost, _ *string) {
			draft.Intro = strings.Repeat("i", maxIntroCharacters+1)
		}, wantError: "intro"},
		{name: "preheader too long", mutate: func(draft *model.NewsletterDraft, _ *model.BlogPost, _ *string) {
			draft.Preheader = strings.Repeat("p", maxPreheaderCharacters+1)
		}, wantError: "preheader"},
		{name: "too many highlights", mutate: func(draft *model.NewsletterDraft, _ *model.BlogPost, _ *string) { draft.Highlights = manyHighlights }, wantError: "13 highlights"},
		{name: "highlight text too long", mutate: func(draft *model.NewsletterDraft, _ *model.BlogPost, _ *string) {
			draft.Highlights = []model.NewsletterHighlight{{Text: strings.Repeat("h", maxHighlightTextCharacters+1)}}
		}, wantError: "highlight 1 text"},
		{name: "closing too long", mutate: func(draft *model.NewsletterDraft, _ *model.BlogPost, _ *string) {
			draft.Closing = strings.Repeat("c", maxClosingCharacters+1)
		}, wantError: "closing"},
		{name: "footer too long", mutate: func(_ *model.NewsletterDraft, _ *model.BlogPost, footer *string) {
			*footer = strings.Repeat("f", maxFooterCharacters+1)
		}, wantError: "footer"},
		{name: "invalid post", mutate: func(_ *model.NewsletterDraft, post *model.BlogPost, _ *string) { post.Title = "" }, wantError: "title is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			draft := sampleNewsletterDraft()
			post := sampleBlogPost()
			footer := sampleFooter
			test.mutate(&draft, &post, &footer)
			_, err := RenderNewsletter(draft, post, samplePresentation(), footer)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want one mentioning %q", err, test.wantError)
			}
		})
	}
}

func TestRenderNewsletterHTMLIsEmailSafe(t *testing.T) {
	rendered, err := RenderNewsletter(sampleNewsletterDraft(), sampleBlogPost(), samplePresentation(), sampleFooter)
	if err != nil {
		t.Fatalf("RenderNewsletter: %v", err)
	}
	body := rendered.HTMLBody
	lowered := strings.ToLower(body)
	for _, forbidden := range []string{"<style", "<script", "<link", "src=", "class=", "url(", "@import", "<img", "<div"} {
		if strings.Contains(lowered, forbidden) {
			t.Errorf("email HTML contains %q", forbidden)
		}
	}
	for _, required := range []string{
		"<!doctype html>",
		`<meta charset="utf-8">`,
		"<title>Connector retries moved into the SDK</title>",
		`<table role="presentation" width="600" cellpadding="0" cellspacing="0" border="0" style="width:100%;max-width:600px;`,
		`<span style="display:none;`,
		">One retry policy for every connector.</span>",
		string(outlookContainerStart),
		string(outlookContainerEnd),
	} {
		if !strings.Contains(body, required) {
			t.Errorf("email HTML is missing %q", required)
		}
	}
	assertSafeMarkup(t, body, newsletterAllowedTags)
	for _, match := range markupTagPattern.FindAllStringSubmatch(body, -1) {
		if match[1] == "/" {
			continue
		}
		switch strings.ToLower(match[2]) {
		case "html", "head", "meta", "title", "tr", "br":
			continue
		}
		if !strings.Contains(match[3], ` style="`) {
			t.Errorf("element without an inline style: %q", match[0])
		}
	}
}

func TestRenderNewsletterIncludesFullPost(t *testing.T) {
	rendered, err := RenderNewsletter(sampleNewsletterDraft(), sampleBlogPost(), samplePresentation(), sampleFooter)
	if err != nil {
		t.Fatalf("RenderNewsletter: %v", err)
	}
	htmlFragments := []string{
		">Dex Engineering Blog</td>",
		">This week the connector SDK took over retries.</p>",
		">Here is what that means for you.</p>",
		">Highlights</h2>",
		`<strong style="color:#111827;">Shared policy</strong>: Every connector now uses <code style=`,
		`Attempts show up in <a href="https://dex.example.com/flows" rel="noopener" style=`,
		">Connector retries moved into the SDK</h1>",
		">What changed in the connector SDK over the past two weeks</p>",
		">By The Dex team</p>",
		">connectors · sdk</p>",
		">Why the policy moved</h2>",
		">How it works</h2>",
		`see <a href="https://github.com/superdurable/dex-connectors-library/pull/42" rel="noopener" style=`,
		">One policy for every connector</li>",
		">policy := sdkgo.RetryPolicy{\n\tMaxAttempts: 5,\n}</code></pre>",
		">Retries belong to the call, not the connector.</p>",
		">Existing connectors adopt the policy after a rebuild.</p>",
		">References</h2>",
		">PR 42: move retries into sdkgo</a></li>",
		">Thanks for reading.</p>",
		">You subscribed to Dex updates.</p>",
		`<a href="https://dex.example.com/unsubscribe" rel="noopener" style="color:#1d5fd1;text-decoration:underline;">Unsubscribe</a>`,
	}
	for _, fragment := range htmlFragments {
		if !strings.Contains(rendered.HTMLBody, fragment) {
			t.Errorf("email HTML is missing %q", fragment)
		}
	}
}

func TestRenderNewsletterWebLink(t *testing.T) {
	tests := []struct {
		name      string
		baseURL   string
		wantURL   string
		wantLink  bool
		wantInTxt string
	}{
		{name: "with base url", baseURL: "https://blog.example.com/posts/", wantURL: "https://blog.example.com/posts/connector-retries-sdk.html", wantLink: true, wantInTxt: "Read on the web: https://blog.example.com/posts/connector-retries-sdk.html"},
		{name: "without base url", baseURL: "", wantLink: false},
		{name: "unsafe base url", baseURL: "javascript:alert(1)", wantLink: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			presentation := samplePresentation()
			presentation.PublicBaseURL = test.baseURL
			rendered, err := RenderNewsletter(sampleNewsletterDraft(), sampleBlogPost(), presentation, sampleFooter)
			if err != nil {
				t.Fatalf("RenderNewsletter: %v", err)
			}
			hasButton := strings.Contains(rendered.HTMLBody, "Read on the web</a>")
			hasText := strings.Contains(rendered.TextBody, "Read on the web")
			if hasButton != test.wantLink || hasText != test.wantLink {
				t.Fatalf("web link in HTML = %v, in text = %v, want %v", hasButton, hasText, test.wantLink)
			}
			if test.wantLink {
				if !strings.Contains(rendered.HTMLBody, `<a href="`+test.wantURL+`" rel="noopener"`) {
					t.Errorf("HTML button does not link to %q", test.wantURL)
				}
				if !strings.Contains(rendered.TextBody, test.wantInTxt) {
					t.Errorf("text body does not contain %q", test.wantInTxt)
				}
			}
			if strings.Contains(strings.ToLower(rendered.HTMLBody), "javascript:alert") && test.baseURL == "javascript:alert(1)" {
				t.Errorf("unsafe base URL leaked into the email")
			}
		})
	}
}

func TestRenderNewsletterTextBody(t *testing.T) {
	rendered, err := RenderNewsletter(sampleNewsletterDraft(), sampleBlogPost(), samplePresentation(), sampleFooter)
	if err != nil {
		t.Fatalf("RenderNewsletter: %v", err)
	}
	want := "Dex Engineering Blog\n" +
		"\n" +
		"This week the connector SDK took over retries.\n" +
		"\n" +
		"Here is what that means for you.\n" +
		"\n" +
		"Highlights\n" +
		"----------\n" +
		"- Shared policy: Every connector now uses `sdkgo.RetryPolicy`.\n" +
		"- Attempts show up in Dex Web (https://dex.example.com/flows).\n" +
		"\n" +
		"Read on the web: https://blog.example.com/posts/connector-retries-sdk.html\n" +
		"\n" +
		strings.Repeat("=", plainTextRuleWidth) + "\n" +
		"\n" +
		"Connector retries moved into the SDK\n" +
		"====================================\n" +
		"\n" +
		"What changed in the connector SDK over the past two weeks\n" +
		"\n" +
		"By The Dex team\n" +
		"\n" +
		"Tags: connectors, sdk\n" +
		"\n" +
		"Why the policy moved\n" +
		"--------------------\n" +
		"\n" +
		"Each connector used to retry on its own. Now `sdkgo` owns the policy; see PR 42 (https://github.com/superdurable/dex-connectors-library/pull/42).\n" +
		"\n" +
		"- One policy for every connector\n" +
		"- Attempts are recorded as `ConnectorAttempt` events\n" +
		"\n" +
		"How it works\n" +
		"------------\n" +
		"\n" +
		"    policy := sdkgo.RetryPolicy{\n" +
		"    \tMaxAttempts: 5,\n" +
		"    }\n" +
		"\n" +
		"> Retries belong to the call, not the connector.\n" +
		"\n" +
		"Note: Existing connectors adopt the policy after a rebuild.\n" +
		"\n" +
		"References\n" +
		"----------\n" +
		"1. PR 42: move retries into sdkgo (https://github.com/superdurable/dex-connectors-library/pull/42)\n" +
		"\n" +
		strings.Repeat("=", plainTextRuleWidth) + "\n" +
		"\n" +
		"Thanks for reading.\n" +
		"\n" +
		"-- \n" +
		"You subscribed to Dex updates.\n" +
		"\n" +
		"Unsubscribe (https://dex.example.com/unsubscribe)\n" +
		"\n" +
		"Unsubscribe: " + UnsubscribeURLPlaceholder + "\n"
	if rendered.TextBody != want {
		t.Fatalf("text body mismatch\n got:\n%s\nwant:\n%s", rendered.TextBody, want)
	}
	if strings.Contains(rendered.TextBody, "\r") {
		t.Fatalf("text body contains CR")
	}
}

func TestRenderNewsletterTextBodyBlocks(t *testing.T) {
	post := model.BlogPost{Title: "T", Sections: []model.BlogSection{{Blocks: []model.BlogBlock{
		{Type: model.BlogBlockQuote, Text: "line one\nline two\n\nsecond paragraph"},
		{Type: model.BlogBlockCallout, Text: "first\n\nsecond"},
		{Type: model.BlogBlockCode, Text: "Lead-in:", Code: "a\n\n\tb"},
		{Type: model.BlogBlockBullets, Text: "Items:", Items: []string{"[x](javascript:alert(1))", "[ok](https://ok.example)"}},
	}}}}
	rendered, err := RenderNewsletter(model.NewsletterDraft{Subject: "S", Intro: "I"}, post, BlogPresentation{}, "")
	if err != nil {
		t.Fatalf("RenderNewsletter: %v", err)
	}
	for _, want := range []string{
		"> line one\n> line two\n\n> second paragraph\n",
		"Note: first\n\nsecond\n",
		"Lead-in:\n\n    a\n\n    \tb\n",
		"Items:\n\n- [x](javascript:alert(1))\n- ok (https://ok.example)\n",
	} {
		if !strings.Contains(rendered.TextBody, want) {
			t.Errorf("text body does not contain %q:\n%s", want, rendered.TextBody)
		}
	}
	// With no footer configured, the signature holds only the unsubscribe link.
	if !strings.HasSuffix(rendered.TextBody, "-- \nUnsubscribe: "+UnsubscribeURLPlaceholder+"\n") {
		t.Errorf("text body does not end with the unsubscribe link:\n%s", rendered.TextBody)
	}
	for _, absent := range []string{"Read on the web", "Highlights", "References", "By "} {
		if strings.Contains(rendered.TextBody, absent) {
			t.Errorf("text body contains %q", absent)
		}
	}
	if strings.Contains(rendered.HTMLBody, `<span style="display:none;`) {
		t.Errorf("preheader span rendered without a preheader or summary")
	}
}

func TestRenderNewsletterPreheader(t *testing.T) {
	tests := []struct {
		name      string
		preheader string
		summary   string
		want      string
	}{
		{name: "draft preheader", preheader: "From the draft", summary: "Summary", want: ">From the draft</span>"},
		{name: "falls back to summary", preheader: "  ", summary: "Summary text", want: ">Summary text</span>"},
		{name: "preheader flattened", preheader: "a\r\nb", summary: "", want: ">a b</span>"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			draft := sampleNewsletterDraft()
			draft.Preheader = test.preheader
			post := sampleBlogPost()
			post.Summary = test.summary
			rendered, err := RenderNewsletter(draft, post, samplePresentation(), "")
			if err != nil {
				t.Fatalf("RenderNewsletter: %v", err)
			}
			if !strings.Contains(rendered.HTMLBody, test.want) {
				t.Fatalf("email HTML does not contain %q", test.want)
			}
		})
	}
}

func TestRenderNewsletterEscapesInjection(t *testing.T) {
	payloads := []string{
		"<script>alert(1)</script>",
		"\"><img src=x onerror=alert(1)>",
		"\" onmouseover=\"alert(1)",
		"</title><script>alert(1)</script>",
		"[x](javascript:alert(1))",
		"[x](data:text/html,<script>alert(1)</script>)",
		"[x](//evil.example)",
		"`</code><script>`",
		"[`</a><script>`](https://ok.example)",
	}
	fields := []struct {
		name   string
		mutate func(draft *model.NewsletterDraft, post *model.BlogPost, presentation *BlogPresentation, footer *string, payload string)
	}{
		{name: "subject", mutate: func(draft *model.NewsletterDraft, _ *model.BlogPost, _ *BlogPresentation, _ *string, payload string) {
			draft.Subject = payload
		}},
		{name: "preheader", mutate: func(draft *model.NewsletterDraft, _ *model.BlogPost, _ *BlogPresentation, _ *string, payload string) {
			draft.Preheader = payload
		}},
		{name: "intro", mutate: func(draft *model.NewsletterDraft, _ *model.BlogPost, _ *BlogPresentation, _ *string, payload string) {
			draft.Intro = payload
		}},
		{name: "highlight title", mutate: func(draft *model.NewsletterDraft, _ *model.BlogPost, _ *BlogPresentation, _ *string, payload string) {
			draft.Highlights = []model.NewsletterHighlight{{Title: payload, Text: "t"}}
		}},
		{name: "highlight text", mutate: func(draft *model.NewsletterDraft, _ *model.BlogPost, _ *BlogPresentation, _ *string, payload string) {
			draft.Highlights = []model.NewsletterHighlight{{Title: "t", Text: payload}}
		}},
		{name: "closing", mutate: func(draft *model.NewsletterDraft, _ *model.BlogPost, _ *BlogPresentation, _ *string, payload string) {
			draft.Closing = payload
		}},
		{name: "footer", mutate: func(_ *model.NewsletterDraft, _ *model.BlogPost, _ *BlogPresentation, footer *string, payload string) {
			*footer = payload
		}},
		{name: "post title", mutate: func(_ *model.NewsletterDraft, post *model.BlogPost, _ *BlogPresentation, _ *string, payload string) {
			post.Title = payload
		}},
		{name: "post paragraph", mutate: func(_ *model.NewsletterDraft, post *model.BlogPost, _ *BlogPresentation, _ *string, payload string) {
			post.Sections[0].Blocks[0].Text = payload
		}},
		{name: "post code", mutate: func(_ *model.NewsletterDraft, post *model.BlogPost, _ *BlogPresentation, _ *string, payload string) {
			post.Sections[1].Blocks[0].Code = payload
		}},
		{name: "post tag", mutate: func(_ *model.NewsletterDraft, post *model.BlogPost, _ *BlogPresentation, _ *string, payload string) {
			post.Tags = []string{payload}
		}},
		{name: "post subtitle", mutate: func(_ *model.NewsletterDraft, post *model.BlogPost, _ *BlogPresentation, _ *string, payload string) {
			post.Subtitle = payload
		}},
		{name: "post summary as preheader fallback", mutate: func(draft *model.NewsletterDraft, post *model.BlogPost, _ *BlogPresentation, _ *string, payload string) {
			draft.Preheader = ""
			post.Summary = payload
		}},
		{name: "post slug", mutate: func(_ *model.NewsletterDraft, post *model.BlogPost, _ *BlogPresentation, _ *string, payload string) {
			post.Slug = payload
		}},
		{name: "section heading", mutate: func(_ *model.NewsletterDraft, post *model.BlogPost, _ *BlogPresentation, _ *string, payload string) {
			post.Sections[0].Heading = payload
		}},
		{name: "bullet item", mutate: func(_ *model.NewsletterDraft, post *model.BlogPost, _ *BlogPresentation, _ *string, payload string) {
			post.Sections[0].Blocks[1].Items = []string{payload, "second " + payload}
		}},
		{name: "bullet lead-in", mutate: func(_ *model.NewsletterDraft, post *model.BlogPost, _ *BlogPresentation, _ *string, payload string) {
			post.Sections[0].Blocks[1].Text = payload
		}},
		{name: "code language", mutate: func(_ *model.NewsletterDraft, post *model.BlogPost, _ *BlogPresentation, _ *string, payload string) {
			post.Sections[1].Blocks[0].Language = payload
		}},
		{name: "code lead-in", mutate: func(_ *model.NewsletterDraft, post *model.BlogPost, _ *BlogPresentation, _ *string, payload string) {
			post.Sections[1].Blocks[0].Text = payload
		}},
		{name: "block type", mutate: func(_ *model.NewsletterDraft, post *model.BlogPost, _ *BlogPresentation, _ *string, payload string) {
			post.Sections[1].Blocks[0].Type = model.BlogBlockType(payload)
			post.Sections[1].Blocks[0].Text = payload
		}},
		{name: "quote", mutate: func(_ *model.NewsletterDraft, post *model.BlogPost, _ *BlogPresentation, _ *string, payload string) {
			post.Sections[1].Blocks[1].Text = payload
		}},
		{name: "callout", mutate: func(_ *model.NewsletterDraft, post *model.BlogPost, _ *BlogPresentation, _ *string, payload string) {
			post.Sections[1].Blocks[2].Text = payload
		}},
		{name: "reference label", mutate: func(_ *model.NewsletterDraft, post *model.BlogPost, _ *BlogPresentation, _ *string, payload string) {
			post.References = []model.SourceReference{{Label: payload, URL: "https://ok.example/pr"}}
		}},
		{name: "reference url", mutate: func(_ *model.NewsletterDraft, post *model.BlogPost, _ *BlogPresentation, _ *string, payload string) {
			post.References = []model.SourceReference{{Label: "r", URL: payload}}
		}},
		{name: "site name", mutate: func(_ *model.NewsletterDraft, _ *model.BlogPost, presentation *BlogPresentation, _ *string, payload string) {
			presentation.SiteName = payload
		}},
		{name: "author", mutate: func(_ *model.NewsletterDraft, _ *model.BlogPost, presentation *BlogPresentation, _ *string, payload string) {
			presentation.Author = payload
		}},
		{name: "public base url", mutate: func(_ *model.NewsletterDraft, _ *model.BlogPost, presentation *BlogPresentation, _ *string, payload string) {
			presentation.PublicBaseURL = payload
		}},
	}
	for _, field := range fields {
		for _, payload := range payloads {
			t.Run(field.name+"/"+payload, func(t *testing.T) {
				draft := sampleNewsletterDraft()
				post := sampleBlogPost()
				presentation := samplePresentation()
				footer := sampleFooter
				field.mutate(&draft, &post, &presentation, &footer, payload)
				rendered, err := RenderNewsletter(draft, post, presentation, footer)
				if err != nil {
					t.Fatalf("RenderNewsletter: %v", err)
				}
				assertSafeMarkup(t, rendered.HTMLBody, newsletterAllowedTags)
				if count := strings.Count(rendered.HTMLBody, "</title>"); count != 1 {
					t.Errorf("email HTML has %d title end tags", count)
				}
				if count := strings.Count(rendered.HTMLBody, "<!--"); count != 2 {
					t.Errorf("email HTML has %d comment openers, want only the two Outlook conditionals", count)
				}
				if strings.ContainsAny(rendered.Subject, "\r\n") {
					t.Errorf("subject contains a line break")
				}
			})
		}
	}
}

func TestRenderNewsletterOutlookContainer(t *testing.T) {
	const cardTable = `<table role="presentation" width="600" cellpadding="0" cellspacing="0" border="0" style="width:100%;max-width:600px;background-color:#ffffff;`
	const footerTable = `<table role="presentation" width="600" cellpadding="0" cellspacing="0" border="0" style="width:100%;max-width:600px;">`
	tests := []struct {
		name       string
		footer     string
		wantFooter bool
	}{
		{name: "with footer", footer: sampleFooter, wantFooter: true},
		// The footer table always holds the unsubscribe link.
		{name: "without footer", footer: "", wantFooter: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rendered, err := RenderNewsletter(sampleNewsletterDraft(), sampleBlogPost(), samplePresentation(), test.footer)
			if err != nil {
				t.Fatalf("RenderNewsletter: %v", err)
			}
			body := rendered.HTMLBody
			if count := strings.Count(body, string(outlookContainerStart)); count != 1 {
				t.Fatalf("email HTML has %d Outlook container starts, want 1", count)
			}
			if count := strings.Count(body, string(outlookContainerEnd)); count != 1 {
				t.Fatalf("email HTML has %d Outlook container ends, want 1", count)
			}
			start := strings.Index(body, string(outlookContainerStart))
			end := strings.Index(body, string(outlookContainerEnd))
			card := strings.Index(body, cardTable)
			if card < 0 {
				t.Fatalf("email HTML has no 600 pixel card table")
			}
			if !(start < card && card < end) {
				t.Errorf("Outlook container (%d..%d) does not wrap the card table at %d", start, end, card)
			}
			footer := strings.Index(body, footerTable)
			if (footer >= 0) != test.wantFooter {
				t.Fatalf("footer table present = %v, want %v", footer >= 0, test.wantFooter)
			}
			if test.wantFooter && !(start < footer && footer < end) {
				t.Errorf("Outlook container (%d..%d) does not wrap the footer table at %d", start, end, footer)
			}
			if !strings.HasPrefix(body[start:], "<!--[if mso]><table role=\"presentation\" width=\"600\" align=\"center\"") {
				t.Errorf("Outlook container does not open a fixed 600 pixel table")
			}
			for _, container := range []string{string(outlookContainerStart), string(outlookContainerEnd)} {
				if !strings.HasPrefix(container, "<!--[if mso]>") || !strings.HasSuffix(container, "<![endif]-->") || strings.Count(container, "-->") != 1 {
					t.Errorf("Outlook container %q is not one complete conditional comment", container)
				}
			}
			assertSafeMarkup(t, body, newsletterAllowedTags)
		})
	}
}

func TestRenderNewsletterBoundsRenderedSize(t *testing.T) {
	codeDensePost := model.BlogPost{Title: "Code dense"}
	for index := 0; index < maxSectionCount; index++ {
		codeDensePost.Sections = append(codeDensePost.Sections, paragraphSection("", strings.Repeat("`a` ", 1200)))
	}
	if _, err := NormalizeBlogPost(codeDensePost); err != nil {
		t.Fatalf("the code-dense post must be within the text bounds: %v", err)
	}
	rendered, err := RenderNewsletter(sampleNewsletterDraft(), codeDensePost, samplePresentation(), sampleFooter)
	if err == nil || !strings.Contains(err.Error(), "newsletter HTML body renders to") || !strings.Contains(err.Error(), "the limit is 2097152") {
		t.Fatalf("error = %v, want the newsletter HTML size limit", err)
	}
	if rendered != (model.RenderedNewsletter{}) {
		t.Fatalf("an oversized render returned content")
	}

	realistic := realisticLongBlogPost()
	normalized, err := NormalizeBlogPost(realistic)
	if err != nil {
		t.Fatalf("NormalizeBlogPost: %v", err)
	}
	if total := blogPostTextCharacters(normalized); total < maxTotalTextCharacters*3/4 {
		t.Fatalf("realistic post has only %d characters; it should be near the text limit", total)
	}
	rendered, err = RenderNewsletter(sampleNewsletterDraft(), realistic, samplePresentation(), sampleFooter)
	if err != nil {
		t.Fatalf("a realistic post near the text limit must render: %v", err)
	}
	if len(rendered.HTMLBody) > maxNewsletterHTMLBytes/2 || len(rendered.TextBody) > maxNewsletterTextBytes/2 {
		t.Errorf("realistic render uses %d HTML and %d text bytes; the bounds leave too little headroom", len(rendered.HTMLBody), len(rendered.TextBody))
	}
}

// realisticLongBlogPost returns a post near the total text limit whose
// markup density (a code span and a link every sentence, a code listing
// every fourth block) is at the heavy end of real technical writing.
func realisticLongBlogPost() model.BlogPost {
	sentence := "The `dex.Context` value now carries [the retry policy](https://github.com/superdurable/dex/pull/1234) so each Step sees it. "
	listing := strings.Repeat("if err := step.Execute(ctx); err != nil {\n\treturn err\n}\n", 5)
	post := model.BlogPost{Title: "A long week of changes", Summary: "Everything that changed."}
	for sectionIndex := 0; sectionIndex < maxSectionCount; sectionIndex++ {
		section := model.BlogSection{Heading: "What changed in `sdkgo`"}
		for blockIndex := 0; blockIndex < 8; blockIndex++ {
			if blockIndex%4 == 3 {
				section.Blocks = append(section.Blocks, model.BlogBlock{Type: model.BlogBlockCode, Language: "go", Code: listing})
				continue
			}
			section.Blocks = append(section.Blocks, model.BlogBlock{Type: model.BlogBlockParagraph, Text: strings.Repeat(sentence, 5)})
		}
		post.Sections = append(post.Sections, section)
	}
	return post
}

func TestCheckByteLimit(t *testing.T) {
	tests := []struct {
		name      string
		rendered  string
		limit     int
		wantError bool
	}{
		{name: "empty", rendered: "", limit: 0},
		{name: "under", rendered: "abc", limit: 4},
		{name: "at limit", rendered: "abcd", limit: 4},
		{name: "over", rendered: "abcde", limit: 4, wantError: true},
		{name: "counts bytes not characters", rendered: "éé", limit: 3, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := checkByteLimit("document", test.rendered, test.limit)
			if (err != nil) != test.wantError {
				t.Fatalf("checkByteLimit error = %v, want error %v", err, test.wantError)
			}
			if err != nil && !strings.Contains(err.Error(), "document renders to") {
				t.Fatalf("error %q does not name the document", err)
			}
		})
	}
}

func TestRenderNewsletterIsDeterministic(t *testing.T) {
	first, err := RenderNewsletter(sampleNewsletterDraft(), sampleBlogPost(), samplePresentation(), sampleFooter)
	if err != nil {
		t.Fatalf("RenderNewsletter: %v", err)
	}
	for attempt := 0; attempt < 5; attempt++ {
		again, err := RenderNewsletter(sampleNewsletterDraft(), sampleBlogPost(), samplePresentation(), sampleFooter)
		if err != nil {
			t.Fatalf("RenderNewsletter: %v", err)
		}
		if again != first {
			t.Fatalf("render %d differs from the first render", attempt+1)
		}
	}
}

func TestMarkupProblemsDetectsUnsafeMarkup(t *testing.T) {
	tests := []struct {
		name     string
		document string
	}{
		{name: "script element", document: "<p>ok</p><script>alert(1)</script>"},
		{name: "image element", document: `<img src="x">`},
		{name: "event handler attribute", document: `<p onclick="x">t</p>`},
		{name: "unquoted attribute", document: `<a href=https://a.example>t</a>`},
		{name: "javascript href", document: `<a href="javascript:alert(1)">t</a>`},
		{name: "relative href", document: `<a href="/x">t</a>`},
		{name: "unknown element", document: "<marquee>t</marquee>"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if problems := markupProblems(test.document, blogAllowedTags); len(problems) == 0 {
				t.Fatalf("markupProblems found nothing in %q", test.document)
			}
		})
	}
	if problems := markupProblems(`<p class="x">a &lt;script&gt; <a href="https://a.example" rel="noopener">b</a></p>`, blogAllowedTags); len(problems) != 0 {
		t.Fatalf("markupProblems flagged safe markup: %v", problems)
	}
}

func TestRenderNewsletterAlwaysLinksToTheUnsubscribePlaceholder(t *testing.T) {
	for name, footer := range map[string]string{"with footer": sampleFooter, "without footer": ""} {
		t.Run(name, func(t *testing.T) {
			rendered, err := RenderNewsletter(sampleNewsletterDraft(), sampleBlogPost(), samplePresentation(), footer)
			if err != nil {
				t.Fatalf("RenderNewsletter: %v", err)
			}
			if count := strings.Count(rendered.HTMLBody, `href="`+UnsubscribeURLPlaceholder+`"`); count != 1 {
				t.Errorf("HTML body links to the placeholder %d times, want 1", count)
			}
			if count := strings.Count(rendered.TextBody, "Unsubscribe: "+UnsubscribeURLPlaceholder); count != 1 {
				t.Errorf("text body names the placeholder %d times, want 1", count)
			}
		})
	}
}

func TestPersonalizeNewsletterReplacesThePlaceholderForOneRecipient(t *testing.T) {
	rendered, err := RenderNewsletter(sampleNewsletterDraft(), sampleBlogPost(), samplePresentation(), sampleFooter)
	if err != nil {
		t.Fatalf("RenderNewsletter: %v", err)
	}
	link := "https://news.example.com/?ref=mail&unsubscribe=Ab0-_Ab0-_Ab0-_Ab0-_Ab"
	personalized, err := PersonalizeNewsletter(rendered, link)
	if err != nil {
		t.Fatalf("PersonalizeNewsletter: %v", err)
	}
	if strings.Contains(personalized.HTMLBody, UnsubscribeURLPlaceholder) || strings.Contains(personalized.TextBody, UnsubscribeURLPlaceholder) {
		t.Fatal("the placeholder survived personalization")
	}
	if !strings.Contains(personalized.HTMLBody, `href="https://news.example.com/?ref=mail&amp;unsubscribe=Ab0-_Ab0-_Ab0-_Ab0-_Ab"`) {
		t.Error("the HTML body does not carry the HTML-escaped link")
	}
	if !strings.Contains(personalized.TextBody, "Unsubscribe: "+link+"\n") {
		t.Error("the text body does not carry the link")
	}
	if personalized.Subject != rendered.Subject || strings.Contains(rendered.HTMLBody, link) {
		t.Error("personalization changed the subject or its input")
	}
	for _, invalid := range []string{"", "javascript:alert(1)", "/relative", `https://example.com/"><script>`} {
		if _, err := PersonalizeNewsletter(rendered, invalid); err == nil && !strings.Contains(invalid, "example.com") {
			t.Errorf("PersonalizeNewsletter accepted %q", invalid)
		}
	}
	quoted, err := PersonalizeNewsletter(rendered, `https://example.com/"><script>`)
	if err == nil && strings.Contains(quoted.HTMLBody, `"><script>`) {
		t.Error("a quote in the link broke out of the href attribute")
	}
}
