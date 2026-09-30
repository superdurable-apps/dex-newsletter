package content

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

const researchPullURL = "https://example.com/acme/connectors/pull/1"

func originalBlogDraft() BlogDraft {
	return BlogDraft{
		Title: "Connectors ship", Slug: "connectors-ship", Summary: "s",
		Sections:   []BlogSection{{Heading: "What changed", Paragraphs: []string{"p"}}},
		Highlights: []BlogHighlight{{Title: "Retry", Description: "d", URL: researchPullURL}, {Title: "Unlinked", Description: "d"}},
	}
}

func TestValidateEditedBlogTrimsAndKeepsSlug(t *testing.T) {
	edited := BlogDraft{
		Title: "  Faster \n builds  ", Subtitle: " A\tsubtitle ", Slug: "renamed-by-editor",
		Summary: "  First line.\n\nSecond line.  ", Closing: "\n Thanks. \n",
		Sections: []BlogSection{
			{Heading: "  ", Paragraphs: []string{" ", ""}, Bullets: []string{"\n"}},
			{Heading: " What \n changed ", Paragraphs: []string{"  one  ", "", " two "}, Bullets: []string{" ", " b1 "}},
		},
		Highlights: []BlogHighlight{
			{Title: "  ", Description: " "},
			{Title: " Retry \n logic ", Description: " Fewer \t failures ", URL: "  " + researchPullURL + " "},
			{Title: "No link", Description: "d"},
		},
	}
	draft, err := ValidateEditedBlog(edited, originalBlogDraft())
	if err != nil {
		t.Fatal(err)
	}
	want := BlogDraft{
		Title: "Faster builds", Subtitle: "A subtitle", Slug: "connectors-ship",
		Summary: "First line.\n\nSecond line.", Closing: "Thanks.",
		Sections: []BlogSection{{Heading: "What changed", Paragraphs: []string{"one", "two"}, Bullets: []string{"b1"}}},
		Highlights: []BlogHighlight{
			{Title: "Retry logic", Description: "Fewer failures", URL: researchPullURL},
			{Title: "No link", Description: "d"},
		},
	}
	if !reflect.DeepEqual(draft, want) {
		t.Fatalf("draft = %+v\nwant  %+v", draft, want)
	}
}

func TestValidateEditedBlogRequiresTitleAndSections(t *testing.T) {
	cases := []struct {
		name     string
		sections []BlogSection
		want     []string
	}{
		{"no sections", nil, []string{"Title is required", "keep at least one section"}},
		{"only blank sections", []BlogSection{{Heading: " ", Paragraphs: []string{" "}}, {}}, []string{"Title is required", "keep at least one section"}},
		{"heading without body", []BlogSection{{Heading: "H", Paragraphs: []string{"  "}}}, []string{"Section 1 needs a paragraph or a bullet"}},
		{"body without heading", []BlogSection{{}, {Heading: " ", Bullets: []string{"b"}}}, []string{"Section 2 needs a heading"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			draft, err := ValidateEditedBlog(BlogDraft{Title: "  ", Sections: testCase.sections}, originalBlogDraft())
			if err == nil {
				t.Fatalf("accepted %+v", draft)
			}
			if !reflect.DeepEqual(draft, BlogDraft{}) {
				t.Fatalf("a rejected draft came back as %+v", draft)
			}
			for _, message := range testCase.want {
				if !strings.Contains(err.Error(), message) {
					t.Errorf("error %q lacks %q", err, message)
				}
			}
		})
	}
	// A heading plus bullets alone is a complete section.
	if _, err := ValidateEditedBlog(BlogDraft{Title: "T", Sections: []BlogSection{{Heading: "H", Bullets: []string{"b"}}}}, originalBlogDraft()); err != nil {
		t.Fatal(err)
	}
}

func TestValidateEditedBlogLimits(t *testing.T) {
	valid := func() BlogDraft {
		return BlogDraft{Title: "T", Sections: []BlogSection{{Heading: "H", Paragraphs: []string{"p"}}}}
	}
	repeat := func(count int) []string {
		texts := make([]string, count)
		for index := range texts {
			texts[index] = "x"
		}
		return texts
	}
	cases := []struct {
		name   string
		mutate func(*BlogDraft)
		want   string
	}{
		{"title", func(draft *BlogDraft) { draft.Title = strings.Repeat("é", 161) }, "Title has 161 characters; keep it to 160"},
		{"subtitle", func(draft *BlogDraft) { draft.Subtitle = strings.Repeat("s", 241) }, "Subtitle has 241 characters; keep it to 240"},
		{"summary", func(draft *BlogDraft) { draft.Summary = strings.Repeat("s", 1201) }, "Summary has 1201 characters; keep it to 1200"},
		{"closing", func(draft *BlogDraft) { draft.Closing = strings.Repeat("c", 1201) }, "Closing has 1201 characters; keep it to 1200"},
		{"heading", func(draft *BlogDraft) { draft.Sections[0].Heading = strings.Repeat("h", 161) }, "Section 1 heading has 161 characters; keep it to 160"},
		{"paragraph", func(draft *BlogDraft) { draft.Sections[0].Paragraphs = []string{strings.Repeat("p", 2001)} }, "Section 1 paragraph has 2001 characters; keep it to 2000"},
		{"paragraph count", func(draft *BlogDraft) { draft.Sections[0].Paragraphs = repeat(9) }, "Section 1 paragraphs has 9 entries; keep it to 8"},
		{"bullet", func(draft *BlogDraft) { draft.Sections[0].Bullets = []string{strings.Repeat("b", 401)} }, "Section 1 bullet has 401 characters; keep it to 400"},
		{"bullet count", func(draft *BlogDraft) { draft.Sections[0].Bullets = repeat(13) }, "Section 1 bullets has 13 entries; keep it to 12"},
		{"section count", func(draft *BlogDraft) {
			for len(draft.Sections) < 9 {
				draft.Sections = append(draft.Sections, BlogSection{Heading: "H", Paragraphs: []string{"p"}})
			}
		}, "keep at most 8 sections"},
		{"highlight title", func(draft *BlogDraft) { draft.Highlights = []BlogHighlight{{Title: strings.Repeat("t", 161)}} }, "Highlight 1 title has 161 characters; keep it to 160"},
		{"highlight description", func(draft *BlogDraft) {
			draft.Highlights = []BlogHighlight{{Title: "t", Description: strings.Repeat("d", 401)}}
		}, "Highlight 1 description has 401 characters; keep it to 400"},
		{"highlight count", func(draft *BlogDraft) {
			for len(draft.Highlights) < 11 {
				draft.Highlights = append(draft.Highlights, BlogHighlight{Title: "t"})
			}
		}, "Highlights has 11 entries; keep it to 10"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			draft := valid()
			testCase.mutate(&draft)
			_, err := ValidateEditedBlog(draft, originalBlogDraft())
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}
	// Limits count characters, not bytes, and each limit is inclusive.
	atLimit := valid()
	atLimit.Title = strings.Repeat("é", 160)
	atLimit.Sections[0].Paragraphs = repeat(8)
	atLimit.Sections[0].Bullets = []string{strings.Repeat("ü", 400)}
	for len(atLimit.Highlights) < 10 {
		atLimit.Highlights = append(atLimit.Highlights, BlogHighlight{Title: "t"})
	}
	if _, err := ValidateEditedBlog(atLimit, originalBlogDraft()); err != nil {
		t.Fatal(err)
	}
}

func TestValidateEditedBlogKeepsResearchLinks(t *testing.T) {
	cases := []struct {
		name      string
		highlight BlogHighlight
		want      string
	}{
		{"new link", BlogHighlight{Title: "T", URL: "https://example.com/elsewhere"}, "Highlight 1 links to a page the draft did not cite; keep its original link"},
		{"link without title", BlogHighlight{URL: "https://example.com/elsewhere"}, "Highlight 1 links to a page the draft did not cite; keep its original link"},
		{"description without title", BlogHighlight{Title: "  ", Description: "d"}, "Highlight 1 needs a title"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			draft := BlogDraft{Title: "T", Sections: []BlogSection{{Heading: "H", Paragraphs: []string{"p"}}}, Highlights: []BlogHighlight{testCase.highlight}}
			if _, err := ValidateEditedBlog(draft, originalBlogDraft()); err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}
	// Allowed links come only from the original highlights, not from the edited ones.
	original := BlogDraft{Slug: "s", Highlights: []BlogHighlight{{Title: "a"}}}
	draft := BlogDraft{Title: "T", Sections: []BlogSection{{Heading: "H", Paragraphs: []string{"p"}}}, Highlights: []BlogHighlight{{Title: "a", URL: researchPullURL}}}
	if _, err := ValidateEditedBlog(draft, original); err == nil {
		t.Fatal("a link absent from the original highlights was accepted")
	}
}

func TestValidateEditedBlogReportsEveryProblem(t *testing.T) {
	_, err := ValidateEditedBlog(BlogDraft{
		Title: strings.Repeat("t", 161), Sections: []BlogSection{{Heading: "H"}},
		Highlights: []BlogHighlight{{Title: "x", URL: "https://example.com/elsewhere"}},
	}, originalBlogDraft())
	if err == nil {
		t.Fatal("an invalid draft was accepted")
	}
	for _, message := range []string{"Title has 161 characters", "Section 1 needs a paragraph or a bullet", "Highlight 1 links to a page"} {
		if !strings.Contains(err.Error(), message) {
			t.Errorf("error %q lacks %q", err, message)
		}
	}
}

func TestValidateEditedNewsletter(t *testing.T) {
	draft, err := ValidateEditedNewsletter(NewsletterDraft{
		Subject: "  This \n week ", Preheader: " In\tshort ", Intro: "\n Hello.\n\nMore. ", Closing: " Bye. ",
		Items: []NewsletterItem{{Title: " ", Summary: "\n"}, {Title: " Faster \n builds ", Summary: " Less \t waiting. "}, {Title: "Title only"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := NewsletterDraft{
		Subject: "This week", Preheader: "In short", Intro: "Hello.\n\nMore.", Closing: "Bye.",
		Items: []NewsletterItem{{Title: "Faster builds", Summary: "Less waiting."}, {Title: "Title only"}},
	}
	if !reflect.DeepEqual(draft, want) {
		t.Fatalf("draft = %+v\nwant  %+v", draft, want)
	}

	items := func(count int) []NewsletterItem {
		kept := make([]NewsletterItem, count)
		for index := range kept {
			kept[index] = NewsletterItem{Title: "t"}
		}
		return kept
	}
	cases := []struct {
		name  string
		draft NewsletterDraft
		want  string
	}{
		{"subject required", NewsletterDraft{Subject: " \n "}, "Subject is required"},
		{"subject", NewsletterDraft{Subject: strings.Repeat("é", 121)}, "Subject has 121 characters; keep it to 120"},
		{"preheader", NewsletterDraft{Subject: "S", Preheader: strings.Repeat("p", 201)}, "Preheader has 201 characters; keep it to 200"},
		{"intro", NewsletterDraft{Subject: "S", Intro: strings.Repeat("i", 1201)}, "Intro has 1201 characters; keep it to 1200"},
		{"closing", NewsletterDraft{Subject: "S", Closing: strings.Repeat("c", 601)}, "Closing has 601 characters; keep it to 600"},
		{"item title required", NewsletterDraft{Subject: "S", Items: []NewsletterItem{{}, {Summary: "s"}}}, "Item 2 needs a title"},
		{"item title", NewsletterDraft{Subject: "S", Items: []NewsletterItem{{Title: strings.Repeat("t", 161)}}}, "Item 1 title has 161 characters; keep it to 160"},
		{"item summary", NewsletterDraft{Subject: "S", Items: []NewsletterItem{{Title: "t", Summary: strings.Repeat("s", 601)}}}, "Item 1 summary has 601 characters; keep it to 600"},
		{"item count", NewsletterDraft{Subject: "S", Items: items(9)}, "Items has 9 entries; keep it to 8"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			draft, err := ValidateEditedNewsletter(testCase.draft)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
			if !reflect.DeepEqual(draft, NewsletterDraft{}) {
				t.Fatalf("a rejected draft came back as %+v", draft)
			}
		})
	}
	if _, err := ValidateEditedNewsletter(NewsletterDraft{Subject: strings.Repeat("é", 120), Items: append(items(8), NewsletterItem{})}); err != nil {
		t.Fatalf("a draft at every limit, plus a blank item, was rejected: %v", err)
	}
}

func TestSlackReviewMessage(t *testing.T) {
	blog := BlogDraft{
		Title: "Connectors ship", Subtitle: "Retries and more", Summary: "The short version.", Closing: "See you next week.",
		Sections: []BlogSection{{Heading: "What changed", Paragraphs: []string{"First paragraph."}, Bullets: []string{"Retry on 429"}}},
		Highlights: []BlogHighlight{
			{Title: "Faster | safer <builds> & more", Description: "Cuts build time.", URL: researchPullURL},
			{Title: "Unlinked", Description: "No page."},
		},
	}
	editorURL := "https://news.example.com/edit/blog-post-T-C1-1.0?token=0f0f"
	dexWebURL := "https://dex.example.com/v2/runs/blog-post-T-C1-1.0"
	message := SlackReviewMessage(blog, "Subject: This week at Acme\n\nHello readers.", "12 merged pull requests in acme/connectors", editorURL, dexWebURL, 3)
	for _, want := range []string{
		"*Draft 3 ready for review*: Connectors ship\nBased on 12 merged pull requests in acme/connectors.\n",
		"*Connectors ship*\n_Retries and more_\n\nThe short version.\n",
		"\n*What changed*\nFirst paragraph.\n• Retry on 429\n",
		"• <" + researchPullURL + "|Faster ¦ safer &lt;builds&gt; &amp; more>: Cuts build time.\n",
		"• Unlinked: No page.\n",
		"See you next week.\n\n*Newsletter email*\nSubject: This week at Acme\n\nHello readers.",
		"*Reply in this thread* with `approve` to send it, `reject` to stop, or any feedback to get a revised draft.",
		"approve in the editor: " + editorURL,
		"Dex Web: " + dexWebURL,
	} {
		if !strings.Contains(message, want) {
			t.Errorf("message lacks %q:\n%s", want, message)
		}
	}
	if strings.Contains(message, "cut to fit Slack") {
		t.Fatal("a short draft was cut")
	}
}

func TestSlackReviewMessageCutsLongDrafts(t *testing.T) {
	blog := BlogDraft{Title: "Long", Sections: []BlogSection{{Heading: "H", Paragraphs: []string{strings.Repeat("é", 2000)}}}}
	for len(blog.Sections) < 8 {
		blog.Sections = append(blog.Sections, blog.Sections[0])
	}
	newsletterText := strings.Repeat("ü", 40000)
	editorURL := "https://news.example.com/edit/run?token=ab"
	message := SlackReviewMessage(blog, newsletterText, "summary", editorURL, "https://dex.example.com/v2/runs/run", 1)
	if !strings.Contains(message, "\n… (cut to fit Slack; the editor has the full draft)") {
		t.Fatal("a long draft was not marked as cut")
	}
	if !utf8.ValidString(message) {
		t.Fatal("the cut split a character")
	}
	if !strings.Contains(message, "`approve`") || !strings.Contains(message, editorURL) {
		t.Fatal("the cut dropped the reply instructions or the editor link")
	}
	// The body is capped at maxSlackDraftRunes; the header and instructions add well under 1,000 more.
	if count := utf8.RuneCountInString(message); count < maxSlackDraftRunes || count > maxSlackDraftRunes+1000 || count >= 40000 {
		t.Fatalf("message has %d characters", count)
	}
	if strings.Count(message, "ü") >= 40000 {
		t.Fatal("the whole newsletter was posted")
	}
}
