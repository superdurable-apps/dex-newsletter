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
	message := SlackReviewMessage("*Draft 3 ready for review*: Connectors ship", blog, "12 merged pull requests in acme/connectors", editorURL, dexWebURL)
	for _, want := range []string{
		"*Draft 3 ready for review*: Connectors ship\nBased on 12 merged pull requests in acme/connectors. " +
			"The email sends this same post, with the subject line \"Connectors ship\".\n\n" +
			"*Connectors ship*\n_Retries and more_\n\nThe short version.\n",
		"\n*What changed*\nFirst paragraph.\n• Retry on 429\n",
		"• <" + researchPullURL + "|Faster ¦ safer &lt;builds&gt; &amp; more>: Cuts build time.\n",
		"• Unlinked: No page.\n",
		"See you next week.\n\n*Reply in this thread* with `approve` to send it, `reject` to stop, or any feedback to get a revised draft.\n",
		"approve in the editor: " + editorURL,
	} {
		if !strings.Contains(message, want) {
			t.Errorf("message lacks %q:\n%s", want, message)
		}
	}
	if !strings.HasSuffix(message, "\nDex Web: "+dexWebURL) {
		t.Errorf("message does not end with the Dex Web link:\n%s", message)
	}
	// The email is this same post, so the post appears once and there is no separate email to review.
	for _, piece := range []string{"*Connectors ship*", "_Retries and more_", "The short version.", "First paragraph.", "• Retry on 429", "Cuts build time.", "See you next week."} {
		if count := strings.Count(message, piece); count != 1 {
			t.Errorf("message has %q %d times:\n%s", piece, count, message)
		}
	}
	for _, stale := range []string{"ewsletter", "Subject:", "*Email*"} {
		if strings.Contains(message, stale) {
			t.Errorf("message keeps a separate email section (%q):\n%s", stale, message)
		}
	}
	if strings.Contains(message, "cut to fit Slack") {
		t.Fatal("a short draft was cut")
	}
}

func TestSlackReviewMessageEscapesModelText(t *testing.T) {
	blog := BlogDraft{
		Title: "Hi <!channel> & <https://phish.example|you>", Summary: "Read <https://phish.example|the notes> & more",
		Sections:   []BlogSection{{Heading: "<@U1>", Paragraphs: []string{"<!here> now"}, Bullets: []string{"a > b"}}},
		Highlights: []BlogHighlight{{Title: "Plain <b>", Description: "<!everyone>"}},
		Closing:    "Bye <#C1>",
	}
	message := SlackReviewMessage("*Draft 1 ready for review*: title", blog, "summary <x>", "https://news.example.com/e", "https://dex.example.com/r")
	for _, raw := range []string{"<!channel>", "<!here>", "<!everyone>", "<https://phish.example", "<@U1>", "<#C1>", "<b>", "<x>"} {
		if strings.Contains(message, raw) {
			t.Errorf("message keeps Slack control text %q:\n%s", raw, message)
		}
	}
	for _, escaped := range []string{
		"with the subject line \"Hi &lt;!channel&gt; &amp; &lt;https://phish.example|you&gt;\".",
		"*Hi &lt;!channel&gt; &amp; &lt;https://phish.example|you&gt;*", "&amp; more", "a &gt; b", "Based on summary &lt;x&gt;.",
	} {
		if !strings.Contains(message, escaped) {
			t.Errorf("message lacks %q:\n%s", escaped, message)
		}
	}
}

func TestSlackReviewMessageCutsLongDrafts(t *testing.T) {
	blog := BlogDraft{Title: "Long"}
	for len(blog.Sections) < 8 {
		blog.Sections = append(blog.Sections, BlogSection{Heading: "H", Paragraphs: []string{strings.Repeat("é", 2000), strings.Repeat("ü", 2000)}})
	}
	editorURL := "https://news.example.com/edit/run?token=ab"
	message := SlackReviewMessage("*Draft 1 ready for review*: Long", blog, "summary", editorURL, "https://dex.example.com/v2/runs/run")
	if !strings.Contains(message, "\n… (cut to fit Slack; the editor has the full post)") {
		t.Fatal("an oversized post was not marked as cut")
	}
	if !utf8.ValidString(message) {
		t.Fatal("the cut split a character")
	}
	if !strings.HasPrefix(message, "*Draft 1 ready for review*: Long\nBased on summary. The email sends this same post, with the subject line \"Long\".\n\n*Long*\n") {
		t.Fatalf("the cut dropped the heading or the subject line:\n%.300s", message)
	}
	if !strings.Contains(message, "`approve`") || !strings.Contains(message, editorURL) {
		t.Fatal("the cut dropped the reply instructions or the editor link")
	}
	// The post is capped at maxSlackDraftRunes; the header and instructions add well under 1,000 more.
	if count := utf8.RuneCountInString(message); count < maxSlackDraftRunes || count > maxSlackDraftRunes+1000 || count >= 40000 {
		t.Fatalf("message has %d characters", count)
	}
	if strings.Count(message, "é")+strings.Count(message, "ü") >= 8*4000 {
		t.Fatal("the whole post was posted")
	}
}

func TestValidateEditedDraftAcceptsLegacyEllipsisFields(t *testing.T) {
	legacy := strings.Repeat("t", 160) + "…"
	original := BlogDraft{Title: legacy, Slug: "s", Sections: []BlogSection{{Heading: "H", Paragraphs: []string{"P"}}}}
	if _, err := ValidateEditedBlog(original, original); err != nil {
		t.Fatalf("an unedited legacy title (limit+1 ending in …) was rejected: %v", err)
	}
	original.Title = strings.Repeat("t", 161) + "…"
	if _, err := ValidateEditedBlog(original, original); err == nil {
		t.Fatal("a title two characters over the limit was accepted")
	}
}
