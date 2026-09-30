package content

import (
	"reflect"
	"regexp"
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

const (
	testPostURL        = "https://example.com/blog/connectors-ship"
	testUnsubscribeURL = "https://example.com/unsubscribe?email=reader%40example.com&token=0f0f"
)

// emailPost is a complete post: every field the blog renders, with inline code in each rich field.
func emailPost() BlogDraft {
	return BlogDraft{
		Title: "Connectors ship", Subtitle: "Retries, docs, and a faster build", Slug: "connectors-ship",
		Summary: "Three changes landed; `dexcli dev` got faster.",
		Sections: []BlogSection{
			{Heading: "What changed", Paragraphs: []string{"Retries now back off.", "Run `make check` before you push."}, Bullets: []string{"Retry on 429", "Honor `Retry-After`"}},
			{Heading: "What is next", Paragraphs: []string{"More connectors."}},
		},
		Highlights: []BlogHighlight{
			{Title: "Retry with backoff", Description: "Fewer failed `sync` runs.", URL: "https://example.com/acme/connectors/pull/1"},
			{Title: "Docs refresh", Description: "A new guide."},
		},
		Closing: "Thanks for reading.",
	}
}

func emailWindow() Window {
	return NewWindow(time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 28, 15, 0, 0, 0, time.UTC))
}

var hrefValue = regexp.MustCompile(`href="([^"]*)"`)

func hrefs(html string) []string {
	var links []string
	for _, match := range hrefValue.FindAllStringSubmatch(html, -1) {
		links = append(links, match[1])
	}
	return links
}

// indexInOrder reports the first piece of html missing from, or out of, the given order.
func indexInOrder(html string, pieces []string) (string, bool) {
	from := 0
	for _, piece := range pieces {
		index := strings.Index(html[from:], piece)
		if index < 0 {
			return piece, false
		}
		from += index + len(piece)
	}
	return "", true
}

func TestEmailIsTheSamePostAsTheBlog(t *testing.T) {
	draft := emailPost()
	email := Email{Draft: draft, PublicationName: "Acme Notes", Window: emailWindow(), PostURL: testPostURL, UnsubscribeURL: testUnsubscribeURL}
	html, err := RenderEmailHTML(email)
	if err != nil {
		t.Fatal(err)
	}
	blog, err := RenderBlogHTML(BlogPage{Draft: draft, PublicationName: "Acme Notes", Window: emailWindow()})
	if err != nil {
		t.Fatal(err)
	}
	// Every piece of the post, in the post's order; only the markup around each piece differs.
	post := []string{
		">Acme Notes</div>",
		">Connectors ship</h1>",
		">Retries, docs, and a faster build</p>",
		"Changes from Sep 14 – Sep 28, 2026",
		">Three changes landed; <code>dexcli dev</code> got faster.</p>",
		">What changed</h2>",
		">Retries now back off.</p>",
		">Run <code>make check</code> before you push.</p>",
		"<li>Retry on 429</li>",
		"<li>Honor <code>Retry-After</code></li>",
		">What is next</h2>",
		">More connectors.</p>",
		">Highlights</h2>",
		`href="https://example.com/acme/connectors/pull/1"`,
		">Retry with backoff</a>",
		">Fewer failed <code>sync</code> runs.</",
		">Docs refresh</",
		">A new guide.</",
		">Thanks for reading.</p>",
	}
	if missing, ok := indexInOrder(blog, post); !ok {
		t.Fatalf("the blog lacks %q in order:\n%s", missing, blog)
	}
	if missing, ok := indexInOrder(html, post); !ok {
		t.Fatalf("the email lacks %q in order:\n%s", missing, html)
	}
	if strings.Contains(html, ">Docs refresh</a>") {
		t.Fatal("the email linked a highlight that has no link")
	}

	if subject := EmailSubject(draft); subject != "Connectors ship" {
		t.Fatalf("subject = %q, want the post title", subject)
	}
	if !strings.Contains(html, "<title>Connectors ship</title>") {
		t.Fatal("the email document is not titled with the post title")
	}
	// The subtitle is the preheader: hidden text ahead of everything the reader sees.
	preheader := regexp.MustCompile(`<span style="display:none[^"]*">Retries, docs, and a faster build</span>`).FindStringIndex(html)
	if preheader == nil || preheader[0] > strings.Index(html, ">Acme Notes</div>") {
		t.Fatalf("the subtitle is not the preheader:\n%s", html)
	}

	// The email links only to the post's own highlight, the published post, and unsubscribing.
	wantLinks := []string{"https://example.com/acme/connectors/pull/1", testPostURL, "https://example.com/unsubscribe?email=reader%40example.com&amp;token=0f0f"}
	if links := hrefs(html); !reflect.DeepEqual(links, wantLinks) {
		t.Fatalf("links = %q, want %q", links, wantLinks)
	}
	if !strings.Contains(html, `href="`+testPostURL+`"`) || !strings.Contains(html, ">Read it on the blog</a>") {
		t.Fatal("the email does not link to the published post")
	}
	if !strings.Contains(html, "subscribed to Acme Notes.") || !strings.Contains(html, ">Unsubscribe</a>") {
		t.Fatal("the email lacks its unsubscribe footer")
	}
}

func TestEmailOmitsWhatThePostLacks(t *testing.T) {
	draft := BlogDraft{Title: "Short post", Slug: "short-post", Sections: []BlogSection{{Heading: "Only", Bullets: []string{"One change"}}}}
	email := Email{Draft: draft, PublicationName: "Acme Notes", Window: emailWindow(), UnsubscribeURL: testUnsubscribeURL}
	html, err := RenderEmailHTML(email)
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"display:none", "Read it on the blog", ">Highlights</h2>", "<code>"} {
		if strings.Contains(html, unwanted) {
			t.Errorf("email contains %q:\n%s", unwanted, html)
		}
	}
	if links := hrefs(html); len(links) != 1 || !strings.HasPrefix(links[0], "https://example.com/unsubscribe?") {
		t.Fatalf("links = %q, want only the unsubscribe link", links)
	}
	if missing, ok := indexInOrder(html, []string{">Short post</h1>", ">Only</h2>", "<li>One change</li>"}); !ok {
		t.Fatalf("email lacks %q", missing)
	}
	if text := RenderEmailText(email); strings.Contains(text, "Read it on the blog") {
		t.Fatalf("text links to an unpublished post:\n%s", text)
	}
}

func TestEmailTextIsThePostText(t *testing.T) {
	draft := emailPost()
	text := RenderEmailText(Email{Draft: draft, PublicationName: "Acme Notes", Window: emailWindow(), PostURL: testPostURL, UnsubscribeURL: testUnsubscribeURL})
	want := RenderBlogText(draft) +
		"\nRead it on the blog: " + testPostURL + "\n" +
		"\n--\nYou receive this because you subscribed to Acme Notes.\nUnsubscribe: " + testUnsubscribeURL + "\n"
	if text != want {
		t.Fatalf("text = %q\nwant  %q", text, want)
	}
	for _, piece := range []string{
		"Connectors ship\nRetries, docs, and a faster build\n", "Three changes landed; `dexcli dev` got faster.",
		"## What changed", "Run `make check` before you push.", "- Honor `Retry-After`", "## What is next", "More connectors.",
		"- Retry with backoff: Fewer failed `sync` runs. (https://example.com/acme/connectors/pull/1)", "- Docs refresh: A new guide.\n",
		"Thanks for reading.",
	} {
		if !strings.Contains(text, piece) {
			t.Errorf("text lacks %q", piece)
		}
	}
	withoutPost := RenderEmailText(Email{Draft: draft, PublicationName: "Acme Notes", UnsubscribeURL: testUnsubscribeURL})
	if withoutPost != RenderBlogText(draft)+"\n--\nYou receive this because you subscribed to Acme Notes.\nUnsubscribe: "+testUnsubscribeURL+"\n" {
		t.Fatalf("text without a post URL = %q", withoutPost)
	}
}

func TestEmailEscapesModelText(t *testing.T) {
	email := Email{PublicationName: "Acme <Notes>", Window: emailWindow(), UnsubscribeURL: testUnsubscribeURL, Draft: BlogDraft{
		Title: `<img src=x onerror=alert(1)>`, Subtitle: "<script>alert(1)</script>", Summary: "Tom & Jerry <i>",
		Sections:   []BlogSection{{Heading: "<em>Heading</em>", Paragraphs: []string{"Use `<b>code</b>` here"}, Bullets: []string{"<!-- hidden -->"}}},
		Highlights: []BlogHighlight{{Title: "x", Description: "<a href=https://evil.example>click</a>", URL: "javascript:alert(1)"}},
		Closing:    "<style>body{display:none}</style>",
	}}
	html, err := RenderEmailHTML(email)
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"<img", "<script", "<i>", "<em>", "<b>", "<!--", "javascript:", "<a href=https://evil.example", "<style", "<Notes>"} {
		if strings.Contains(html, unwanted) {
			t.Errorf("email contains %q", unwanted)
		}
	}
	// Model text never becomes a link: the unsafe highlight URL is neutralized and the description stays text.
	for _, link := range hrefs(html) {
		if link != "#ZgotmplZ" && !strings.HasPrefix(link, "https://example.com/unsubscribe?") {
			t.Errorf("email links to %q", link)
		}
	}
	for _, escaped := range []string{
		"<title>&lt;img src=x onerror=alert(1)&gt;</title>", ">&lt;img src=x onerror=alert(1)&gt;</h1>",
		">&lt;script&gt;alert(1)&lt;/script&gt;</span>", "Tom &amp; Jerry &lt;i&gt;", "&lt;em&gt;Heading&lt;/em&gt;",
		"<code>&lt;b&gt;code&lt;/b&gt;</code>", "&lt;!-- hidden --&gt;", "&lt;a href=https://evil.example&gt;click&lt;/a&gt;", "Acme &lt;Notes&gt;",
	} {
		if !strings.Contains(html, escaped) {
			t.Errorf("email lacks %q:\n%s", escaped, html)
		}
	}
	if subject := EmailSubject(BlogDraft{Title: "Tom & Jerry <i>"}); subject != "Tom & Jerry <i>" {
		t.Fatalf("subject = %q; a subject line is plain text", subject)
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
	if got := len([]rune(pullRequests[0].Body)); got > maxPullRequestBodyRunes {
		t.Fatalf("body has %d runes", got)
	}
	commits := CommitsFromPage(github.CommitPage{Commits: []github.CommitSummary{
		{SHA: "a", Message: "Merge pull request #7 from x/y"}, {SHA: "b", Message: "Squashed change (#7)"}, {SHA: "c", Message: "Direct fix (#8)"},
	}}, pullRequests, 10)
	if len(commits) != 1 || commits[0].SHA != "c" {
		t.Fatalf("commits = %+v", commits)
	}
}
