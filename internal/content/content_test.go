package content

import (
	"strings"
	"testing"
	"time"

	github "github.com/superdurable/dex-connectors-library/connectors/github"
)

func TestResolveWindow(t *testing.T) {
	received := time.Date(2026, 9, 28, 15, 0, 0, 0, time.UTC)
	cases := []struct {
		name           string
		interpretation RequestInterpretation
		since, until   time.Time
	}{
		{"default", RequestInterpretation{}, received.AddDate(0, 0, -7), received},
		{"lookback", RequestInterpretation{LookbackDays: 14}, received.AddDate(0, 0, -14), received},
		{"capped", RequestInterpretation{LookbackDays: 400}, received.AddDate(0, 0, -90), received},
		{"explicit", RequestInterpretation{Since: "2026-09-01", Until: "2026-09-10"},
			time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 10, 23, 59, 59, 0, time.UTC)},
		{"future until clamps to now", RequestInterpretation{Until: "2027-01-01"}, received.AddDate(0, 0, -7), received},
		{"garbage dates fall back", RequestInterpretation{Since: "last week", Until: "soon"}, received.AddDate(0, 0, -7), received},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			window := ResolveWindow(testCase.interpretation, received, 7, 90)
			if !window.Since.Equal(testCase.since) || !window.Until.Equal(testCase.until) {
				t.Fatalf("window = %s..%s, want %s..%s", window.Since, window.Until, testCase.since, testCase.until)
			}
		})
	}
	if label := NewWindow(time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC), received).Label; label != "Sep 14 – Sep 28, 2026" {
		t.Fatalf("label = %q", label)
	}
}

func TestParseInterpretation(t *testing.T) {
	interpretation, err := ParseInterpretation("```json\n{\"isBlogRequest\":true,\"topic\":\"  connectors \",\"lookbackDays\":14,\"since\":\"\",\"until\":\"\",\"explanation\":\"x\"}\n```")
	if err != nil || interpretation.Topic != "connectors" || interpretation.LookbackDays != 14 {
		t.Fatalf("interpretation = %+v, %v", interpretation, err)
	}
	if _, err := ParseInterpretation(`{"isBlogRequest":true,"topic":"","lookbackDays":0,"since":"","until":"","explanation":""}`); err == nil {
		t.Fatal("an accepted request without a topic parsed")
	}
	if _, err := ParseInterpretation("not json"); err == nil {
		t.Fatal("prose parsed")
	}
}

func TestParseRepositoryChoiceKeepsOnlyCandidates(t *testing.T) {
	candidates := []RepositoryCandidate{{Owner: "acme", Name: "connectors", URL: "https://github.com/acme/connectors"}, {Owner: "acme", Name: "web"}}
	selected, err := ParseRepositoryChoice(`{"repositories":[
		{"fullName":"ACME/connectors","reason":"r","focusAreas":["a","b","c","d","e","f"]},
		{"fullName":"acme/connectors","reason":"dup","focusAreas":[]},
		{"fullName":"evil/repo","reason":"r","focusAreas":[]},
		{"fullName":"acme/web","reason":"r","focusAreas":[]}]}`, candidates, 1)
	if err != nil || len(selected) != 1 || selected[0].FullName() != "acme/connectors" || len(selected[0].FocusAreas) != 5 || selected[0].URL == "" {
		t.Fatalf("selected = %+v, %v", selected, err)
	}
}

func TestParseBlogDraftGroundsLinksAndSlug(t *testing.T) {
	draft, err := ParseBlogDraft(`{"title":"Hello","subtitle":"","slug":"Hello, World!!","summary":"s","closing":"",
		"sections":[{"heading":"A","paragraphs":["p"],"bullets":[]},{"heading":"","paragraphs":["orphan"],"bullets":[]},{"heading":"Empty","paragraphs":[],"bullets":[]}],
		"highlights":[{"title":"Real","description":"d","url":"https://github.com/acme/connectors/pull/1"},{"title":"Fake","description":"d","url":"https://evil.example"}]}`,
		map[string]bool{"https://github.com/acme/connectors/pull/1": true})
	if err != nil {
		t.Fatal(err)
	}
	if draft.Slug != "hello-world" || len(draft.Sections) != 1 || draft.Highlights[0].URL == "" || draft.Highlights[1].URL != "" {
		t.Fatalf("draft = %+v", draft)
	}
	if _, err := ParseBlogDraft(`{"title":"T","subtitle":"","slug":"","summary":"","closing":"","sections":[],"highlights":[]}`, nil); err == nil {
		t.Fatal("a draft without sections parsed")
	}
}

func TestRenderedHTMLEscapesModelText(t *testing.T) {
	html, err := RenderBlogHTML(BlogPage{PublicationName: "Notes", Window: NewWindow(time.Now().AddDate(0, 0, -7), time.Now()), Draft: BlogDraft{
		Title: `<img src=x onerror=alert(1)>`, Sections: []BlogSection{{Heading: "H", Paragraphs: []string{"Use `<b>code</b>` here"}}},
		Highlights: []BlogHighlight{{Title: "x", URL: "javascript:alert(1)"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"<img", "<b>", "javascript:alert"} {
		if strings.Contains(html, unwanted) {
			t.Fatalf("HTML contains %q", unwanted)
		}
	}
	if !strings.Contains(html, "<code>&lt;b&gt;code&lt;/b&gt;</code>") || strings.Contains(html, "<script") {
		t.Fatal("inline code was not escaped into <code>")
	}
}

func TestNewsletterRendersUnsubscribeLink(t *testing.T) {
	email := NewsletterEmail{Draft: NewsletterDraft{Subject: "S", Intro: "I", Items: []NewsletterItem{{Title: "T", Summary: "U"}}},
		PublicationName: "Notes", PostURL: "https://blog.example/post", UnsubscribeURL: "https://news.example/unsubscribe?email=a%40b.c&token=t"}
	html, err := RenderNewsletterHTML(email)
	if err != nil || !strings.Contains(html, `href="https://news.example/unsubscribe?email=a%40b.c&amp;token=t"`) || !strings.Contains(html, "Read the full post") {
		t.Fatalf("html = %s, %v", html, err)
	}
	if text := RenderNewsletterText(email); !strings.Contains(text, "Unsubscribe: https://news.example/unsubscribe") {
		t.Fatalf("text = %s", text)
	}
}

func TestEvidenceBounds(t *testing.T) {
	window := NewWindow(time.Now().AddDate(0, 0, -7), time.Now())
	old := time.Now().AddDate(-1, 0, 0)
	candidates := CandidatesFromRepositories("acme", []github.Repository{
		{Name: "live"}, {Name: "fork", Fork: true}, {Name: "archived", Archived: true}, {Name: "old", PushedAt: &old},
	}, window)
	if len(candidates) != 1 || candidates[0].Name != "live" {
		t.Fatalf("candidates = %+v", candidates)
	}
	pullRequests := PullRequestsFromPage(github.MergedPullRequestPage{PullRequests: []github.MergedPullRequest{{Number: 7, Body: strings.Repeat("x", 5000)}}}, 10)
	if got := len([]rune(pullRequests[0].Body)); got > maxPullRequestBodyRunes+1 {
		t.Fatalf("body has %d runes", got)
	}
	commits := CommitsFromPage(github.CommitPage{Commits: []github.CommitSummary{
		{SHA: "a", Message: "Merge pull request #7 from x/y"}, {SHA: "b", Message: "Squashed change (#7)"}, {SHA: "c", Message: "Direct fix (#8)"},
	}}, pullRequests, 10)
	if len(commits) != 1 || commits[0].SHA != "c" {
		t.Fatalf("commits = %+v", commits)
	}
}
