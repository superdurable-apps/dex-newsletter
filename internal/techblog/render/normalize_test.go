package render

import (
	"reflect"
	"strings"
	"testing"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

// paragraphSection returns a section holding one paragraph block.
func paragraphSection(heading string, text string) model.BlogSection {
	return model.BlogSection{Heading: heading, Blocks: []model.BlogBlock{{Type: model.BlogBlockParagraph, Text: text}}}
}

func TestNormalizeBlogPostRejectsInvalidPosts(t *testing.T) {
	manySections := make([]model.BlogSection, maxSectionCount+1)
	for index := range manySections {
		manySections[index] = paragraphSection("", "text")
	}
	manyBlocks := make([]model.BlogBlock, maxBlocksPerSection+1)
	for index := range manyBlocks {
		manyBlocks[index] = model.BlogBlock{Type: model.BlogBlockParagraph, Text: "text"}
	}
	manyTags := make([]string, maxTagCount+1)
	for index := range manyTags {
		manyTags[index] = "tag" + strings.Repeat("x", index)
	}
	manyItems := make([]string, maxItemsPerBulletsBlock+1)
	for index := range manyItems {
		manyItems[index] = "item"
	}
	manyReferences := make([]model.SourceReference, maxReferenceCount+1)
	for index := range manyReferences {
		manyReferences[index] = model.SourceReference{Label: "r", URL: "https://a.example/" + strings.Repeat("x", index)}
	}
	hugeSections := make([]model.BlogSection, 5)
	for index := range hugeSections {
		hugeSections[index] = paragraphSection("", strings.Repeat("word ", 9000))
	}

	tests := []struct {
		name      string
		mutate    func(post *model.BlogPost)
		wantError string
	}{
		{name: "blank title", mutate: func(post *model.BlogPost) { post.Title = " \n\t " }, wantError: "title is required"},
		{name: "control-only title", mutate: func(post *model.BlogPost) { post.Title = "\x00\x01\x7f" }, wantError: "title is required"},
		{name: "title too long", mutate: func(post *model.BlogPost) { post.Title = strings.Repeat("é", maxTitleCharacters+1) }, wantError: "title has 201 characters"},
		{name: "subtitle too long", mutate: func(post *model.BlogPost) { post.Subtitle = strings.Repeat("s", maxSubtitleCharacters+1) }, wantError: "subtitle"},
		{name: "summary too long", mutate: func(post *model.BlogPost) { post.Summary = strings.Repeat("s", maxSummaryCharacters+1) }, wantError: "summary"},
		{name: "no sections", mutate: func(post *model.BlogPost) { post.Sections = nil }, wantError: "at least one section"},
		{name: "only empty sections", mutate: func(post *model.BlogPost) {
			post.Sections = []model.BlogSection{
				{Heading: "Heading only"},
				{Heading: "Blank blocks", Blocks: []model.BlogBlock{{Type: model.BlogBlockParagraph, Text: "  "}, {Type: model.BlogBlockBullets, Items: []string{" ", ""}}, {Type: model.BlogBlockCode, Code: "\n\n"}, {Type: "mystery"}}},
			}
		}, wantError: "at least one section"},
		{name: "too many sections", mutate: func(post *model.BlogPost) { post.Sections = manySections }, wantError: "41 sections"},
		{name: "too many blocks", mutate: func(post *model.BlogPost) { post.Sections = []model.BlogSection{{Blocks: manyBlocks}} }, wantError: "61 blocks"},
		{name: "too many items", mutate: func(post *model.BlogPost) {
			post.Sections = []model.BlogSection{{Blocks: []model.BlogBlock{{Type: model.BlogBlockBullets, Items: manyItems}}}}
		}, wantError: "101 items"},
		{name: "too many tags", mutate: func(post *model.BlogPost) { post.Tags = manyTags }, wantError: "21 tags"},
		{name: "tag too long", mutate: func(post *model.BlogPost) { post.Tags = []string{strings.Repeat("t", maxTagCharacters+1)} }, wantError: "tag"},
		{name: "heading too long", mutate: func(post *model.BlogPost) {
			post.Sections = []model.BlogSection{paragraphSection(strings.Repeat("h", maxHeadingCharacters+1), "text")}
		}, wantError: "section 1 heading"},
		{name: "too many references", mutate: func(post *model.BlogPost) { post.References = manyReferences }, wantError: "101 references"},
		{name: "reference label too long", mutate: func(post *model.BlogPost) {
			post.References = []model.SourceReference{{Label: strings.Repeat("l", maxReferenceLabelCharacters+1), URL: "https://a.example"}}
		}, wantError: "label"},
		{name: "total text too long", mutate: func(post *model.BlogPost) { post.Sections = hugeSections }, wantError: "characters of text"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			post := sampleBlogPost()
			test.mutate(&post)
			normalized, err := NormalizeBlogPost(post)
			if err == nil {
				t.Fatalf("NormalizeBlogPost succeeded with %#v", normalized)
			}
			if !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error %q does not mention %q", err, test.wantError)
			}
			if !reflect.DeepEqual(normalized, model.BlogPost{}) {
				t.Fatalf("failed normalization returned a non-zero post: %#v", normalized)
			}
		})
	}
}

func TestNormalizeBlogPostCanonicalizesBlocks(t *testing.T) {
	tests := []struct {
		name  string
		input model.BlogBlock
		want  []model.BlogBlock
	}{
		{
			name:  "paragraph trimmed and irrelevant fields cleared",
			input: model.BlogBlock{Type: model.BlogBlockParagraph, Text: "  Hello \r\n world  ", Items: []string{"x"}, Language: "go", Code: "x"},
			want:  []model.BlogBlock{{Type: model.BlogBlockParagraph, Text: "Hello\nworld", Items: []string{}}},
		},
		{
			name:  "paragraph blank lines collapsed",
			input: model.BlogBlock{Type: model.BlogBlockParagraph, Text: "\n\nOne\n\n\n\n  Two  \n\n"},
			want:  []model.BlogBlock{{Type: model.BlogBlockParagraph, Text: "One\n\nTwo", Items: []string{}}},
		},
		{
			name:  "type matched case-insensitively",
			input: model.BlogBlock{Type: "  Callout ", Text: "Heads up"},
			want:  []model.BlogBlock{{Type: model.BlogBlockCallout, Text: "Heads up", Items: []string{}}},
		},
		{
			name:  "quote kept",
			input: model.BlogBlock{Type: model.BlogBlockQuote, Text: "Said"},
			want:  []model.BlogBlock{{Type: model.BlogBlockQuote, Text: "Said", Items: []string{}}},
		},
		{
			name:  "unknown type with text becomes paragraph",
			input: model.BlogBlock{Type: "<script>", Text: "Kept text"},
			want:  []model.BlogBlock{{Type: model.BlogBlockParagraph, Text: "Kept text", Items: []string{}}},
		},
		{
			name:  "unknown type without text dropped",
			input: model.BlogBlock{Type: "table", Items: []string{"a", "b"}},
			want:  nil,
		},
		{
			name:  "empty paragraph dropped",
			input: model.BlogBlock{Type: model.BlogBlockParagraph, Text: " \t\n "},
			want:  nil,
		},
		{
			name:  "bullets drop empty items and keep lead-in",
			input: model.BlogBlock{Type: model.BlogBlockBullets, Text: " Lead ", Items: []string{" one ", "", "  ", "two\nlines"}},
			want:  []model.BlogBlock{{Type: model.BlogBlockBullets, Text: "Lead", Items: []string{"one", "two lines"}}},
		},
		{
			name:  "bullets without items but with text become paragraph",
			input: model.BlogBlock{Type: model.BlogBlockBullets, Text: "Only text", Items: []string{" "}},
			want:  []model.BlogBlock{{Type: model.BlogBlockParagraph, Text: "Only text", Items: []string{}}},
		},
		{
			name:  "bullets without items or text dropped",
			input: model.BlogBlock{Type: model.BlogBlockBullets},
			want:  nil,
		},
		{
			name:  "code keeps indentation and trims blank edges",
			input: model.BlogBlock{Type: model.BlogBlockCode, Language: " Go ", Code: "\n\n\tif x {  \r\n\t\treturn\n\t}\n\n  "},
			want:  []model.BlogBlock{{Type: model.BlogBlockCode, Language: "go", Code: "\tif x {\n\t\treturn\n\t}", Items: []string{}}},
		},
		{
			name:  "code language sanitized",
			input: model.BlogBlock{Type: model.BlogBlockCode, Language: "go\" onload=\"alert(1)", Code: "x"},
			want:  []model.BlogBlock{{Type: model.BlogBlockCode, Language: "goonloadalert1", Code: "x", Items: []string{}}},
		},
		{
			name:  "code without code but with text becomes paragraph",
			input: model.BlogBlock{Type: model.BlogBlockCode, Text: "Explained", Language: "go", Code: "  \n "},
			want:  []model.BlogBlock{{Type: model.BlogBlockParagraph, Text: "Explained", Items: []string{}}},
		},
		{
			name:  "code without code or text dropped",
			input: model.BlogBlock{Type: model.BlogBlockCode, Language: "go"},
			want:  nil,
		},
		{
			name:  "control and bidi characters removed",
			input: model.BlogBlock{Type: model.BlogBlockParagraph, Text: "a\x00b\x1bc\u202ed\u2066e\ufefff\u0085g"},
			want:  []model.BlogBlock{{Type: model.BlogBlockParagraph, Text: "abcdefg", Items: []string{}}},
		},
		{
			name:  "invalid utf-8 replaced",
			input: model.BlogBlock{Type: model.BlogBlockParagraph, Text: "a\xffb"},
			want:  []model.BlogBlock{{Type: model.BlogBlockParagraph, Text: "a\ufffdb", Items: []string{}}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			post := model.BlogPost{Title: "T", Sections: []model.BlogSection{
				paragraphSection("Anchor", "anchor"),
				{Heading: "Under test", Blocks: []model.BlogBlock{test.input}},
			}}
			normalized, err := NormalizeBlogPost(post)
			if err != nil {
				t.Fatalf("NormalizeBlogPost: %v", err)
			}
			var got []model.BlogBlock
			if len(normalized.Sections) == 2 {
				got = normalized.Sections[1].Blocks
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("blocks\n got %#v\nwant %#v", got, test.want)
			}
		})
	}
}

func TestNormalizeBlogPostCleansPostFields(t *testing.T) {
	post := model.BlogPost{
		Title:    "  Connectors\n\tweekly  ",
		Subtitle: " A\r\nsubtitle ",
		Slug:     "",
		Summary:  "  Summary\n\nparagraphs  ",
		Tags:     []string{" #Connectors ", "connectors", "", "  ", "SDK", "#", "# #", "# #sdk", "# #Durable execution"},
		Sections: []model.BlogSection{
			{Heading: "  Empty  "},
			{Heading: "  Kept\nheading ", Blocks: []model.BlogBlock{{Type: model.BlogBlockParagraph, Text: "Body"}}},
		},
		References: []model.SourceReference{
			{Label: " PR 1 ", URL: " https://github.com/o/r/pull/1 "},
			{Label: "duplicate", URL: "https://github.com/o/r/pull/1"},
			{Label: "", URL: "http://example.com/x"},
			{Label: "js", URL: "javascript:alert(1)"},
			{Label: "relative", URL: "/pull/2"},
			{Label: "empty", URL: ""},
		},
	}
	want := model.BlogPost{
		Title:    "Connectors weekly",
		Subtitle: "A subtitle",
		Slug:     "connectors-weekly",
		Summary:  "Summary paragraphs",
		Tags:     []string{"Connectors", "SDK", "Durable execution"},
		Sections: []model.BlogSection{
			{Heading: "Kept heading", Blocks: []model.BlogBlock{{Type: model.BlogBlockParagraph, Text: "Body", Items: []string{}}}},
		},
		References: []model.SourceReference{
			{Label: "PR 1", URL: "https://github.com/o/r/pull/1"},
			{Label: "http://example.com/x", URL: "http://example.com/x"},
		},
	}
	got, err := NormalizeBlogPost(post)
	if err != nil {
		t.Fatalf("NormalizeBlogPost: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeBlogPost\n got %#v\nwant %#v", got, want)
	}
}

func TestNormalizeBlogPostTags(t *testing.T) {
	tests := []struct {
		name string
		tags []string
		want []string
	}{
		{name: "leading mark removed", tags: []string{"#go"}, want: []string{"go"}},
		{name: "repeated marks removed", tags: []string{"###go"}, want: []string{"go"}},
		{name: "mark space mark", tags: []string{"# #a"}, want: []string{"a"}},
		{name: "mark tab mark", tags: []string{"#\t#x"}, want: []string{"x"}},
		{name: "spaces before and between marks", tags: []string{"  # # #  y "}, want: []string{"y"}},
		{name: "marks and spaces only dropped", tags: []string{"# #", "#", " # \t # "}, want: []string{}},
		{name: "interior and trailing marks kept", tags: []string{"c#", "a#b"}, want: []string{"c#", "a#b"}},
		{name: "duplicate after stripping", tags: []string{"# #go", "go", "#GO"}, want: []string{"go"}},
		{name: "reviewer case", tags: []string{"# #a", "a", "# #"}, want: []string{"a"}},
		{name: "interior whitespace collapsed", tags: []string{"# durable \n execution"}, want: []string{"durable execution"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			post := model.BlogPost{Title: "T", Tags: test.tags, Sections: []model.BlogSection{paragraphSection("", "body")}}
			once, err := NormalizeBlogPost(post)
			if err != nil {
				t.Fatalf("NormalizeBlogPost: %v", err)
			}
			if !reflect.DeepEqual(once.Tags, test.want) {
				t.Fatalf("tags = %q, want %q", once.Tags, test.want)
			}
			twice, err := NormalizeBlogPost(once)
			if err != nil {
				t.Fatalf("second NormalizeBlogPost: %v", err)
			}
			if !reflect.DeepEqual(twice.Tags, once.Tags) {
				t.Fatalf("tag normalization is not idempotent: once %q, twice %q", once.Tags, twice.Tags)
			}
		})
	}
}

func TestNormalizeBlogPostDropsBeforeCheckingLimits(t *testing.T) {
	longHeading := strings.Repeat("h", maxHeadingCharacters+100)
	longURL := "https://github.com/o/r/pull/1?" + strings.Repeat("a", 600)
	longestURL := "https://example.com/" + strings.Repeat("b", maxWebURLBytes-len("https://example.com/"))
	tests := []struct {
		name   string
		mutate func(post *model.BlogPost)
		check  func(t *testing.T, post model.BlogPost)
	}{
		{
			name: "section without blocks and with an over-long heading is dropped",
			mutate: func(post *model.BlogPost) {
				post.Sections = append(post.Sections, model.BlogSection{Heading: longHeading})
			},
			check: func(t *testing.T, post model.BlogPost) {
				if len(post.Sections) != 1 || post.Sections[0].Heading != "Kept" {
					t.Fatalf("sections = %#v, want only the kept section", post.Sections)
				}
			},
		},
		{
			name: "section with only blank blocks and an over-long heading is dropped",
			mutate: func(post *model.BlogPost) {
				post.Sections = append([]model.BlogSection{{Heading: longHeading, Blocks: []model.BlogBlock{
					{Type: model.BlogBlockParagraph, Text: " \n "},
					{Type: model.BlogBlockBullets, Items: []string{"", " "}},
					{Type: model.BlogBlockCode, Code: "\n"},
				}}}, post.Sections...)
			},
			check: func(t *testing.T, post model.BlogPost) {
				if len(post.Sections) != 1 || post.Sections[0].Heading != "Kept" {
					t.Fatalf("sections = %#v, want only the kept section", post.Sections)
				}
			},
		},
		{
			name: "unlabeled reference with a URL longer than the label limit is kept",
			mutate: func(post *model.BlogPost) {
				post.References = []model.SourceReference{{Label: "  ", URL: longURL}}
			},
			check: func(t *testing.T, post model.BlogPost) {
				want := []model.SourceReference{{Label: longURL, URL: longURL}}
				if !reflect.DeepEqual(post.References, want) {
					t.Fatalf("references = %#v, want the URL as its own label", post.References)
				}
			},
		},
		{
			name: "reference labeled with its own longest allowed URL is kept",
			mutate: func(post *model.BlogPost) {
				post.References = []model.SourceReference{{Label: longestURL, URL: " " + longestURL + " "}}
			},
			check: func(t *testing.T, post model.BlogPost) {
				want := []model.SourceReference{{Label: longestURL, URL: longestURL}}
				if !reflect.DeepEqual(post.References, want) {
					t.Fatalf("references = %#v, want the URL as its own label", post.References)
				}
			},
		},
		{
			name: "dropped reference with an over-long label is not checked",
			mutate: func(post *model.BlogPost) {
				post.References = []model.SourceReference{{Label: strings.Repeat("l", maxReferenceLabelCharacters+1), URL: "javascript:alert(1)"}}
			},
			check: func(t *testing.T, post model.BlogPost) {
				if len(post.References) != 0 {
					t.Fatalf("references = %#v, want none", post.References)
				}
			},
		},
		{
			name: "duplicate tag and mark-only tag are not checked",
			mutate: func(post *model.BlogPost) {
				post.Tags = []string{"go", "# #", "#GO"}
			},
			check: func(t *testing.T, post model.BlogPost) {
				if !reflect.DeepEqual(post.Tags, []string{"go"}) {
					t.Fatalf("tags = %q, want [go]", post.Tags)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			post := model.BlogPost{Title: "T", Sections: []model.BlogSection{paragraphSection("Kept", "body")}}
			test.mutate(&post)
			once, err := NormalizeBlogPost(post)
			if err != nil {
				t.Fatalf("NormalizeBlogPost: %v", err)
			}
			test.check(t, once)
			twice, err := NormalizeBlogPost(once)
			if err != nil {
				t.Fatalf("second NormalizeBlogPost: %v", err)
			}
			if !reflect.DeepEqual(once, twice) {
				t.Fatalf("normalization is not idempotent\n once %#v\ntwice %#v", once, twice)
			}
		})
	}
}

func TestNormalizeBlogPostSlug(t *testing.T) {
	longTitle := strings.Repeat("durable ", 20)
	tests := []struct {
		name  string
		slug  string
		title string
		want  string
	}{
		{name: "valid slug kept", slug: "connectors-weekly", title: "Other", want: "connectors-weekly"},
		{name: "slug lowercased and dashed", slug: "  Connectors__Weekly!! 2026 ", title: "Other", want: "connectors-weekly-2026"},
		{name: "dashes collapsed and trimmed", slug: "--a---b--", title: "Other", want: "a-b"},
		{name: "path traversal neutralized", slug: "../../etc/passwd", title: "Other", want: "etc-passwd"},
		{name: "html in slug neutralized", slug: "<script>alert(1)</script>", title: "Other", want: "script-alert-1-script"},
		{name: "apostrophes removed", slug: "", title: "Dex's new connectors", want: "dexs-new-connectors"},
		{name: "blank slug derived from title", slug: "  ", title: "Hello, World!", want: "hello-world"},
		{name: "punctuation-only slug derived from title", slug: "!!!---", title: "Retries in the SDK", want: "retries-in-the-sdk"},
		{name: "non-ascii slug derived from title", slug: "连接器", title: "Connector news", want: "connector-news"},
		{name: "fallback when title has no ascii", slug: "", title: "连接器更新", want: fallbackBlogSlug},
		{name: "long title cut at word boundary", slug: "", title: longTitle, want: strings.TrimSuffix(strings.Repeat("durable-", 10), "-")},
		{name: "long word cut at limit", slug: strings.Repeat("a", 100), title: "Other", want: strings.Repeat("a", maxSlugBytes)},
		{name: "cut never leaves a trailing dash", slug: strings.Repeat("a", 79) + "-bcd", title: "Other", want: strings.Repeat("a", 79)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			post := model.BlogPost{Title: test.title, Slug: test.slug, Sections: []model.BlogSection{paragraphSection("", "body")}}
			normalized, err := NormalizeBlogPost(post)
			if err != nil {
				t.Fatalf("NormalizeBlogPost: %v", err)
			}
			if normalized.Slug != test.want {
				t.Fatalf("slug = %q, want %q", normalized.Slug, test.want)
			}
			if len(normalized.Slug) > maxSlugBytes || strings.Trim(normalized.Slug, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
				t.Fatalf("slug %q is not lowercase [a-z0-9-] within %d bytes", normalized.Slug, maxSlugBytes)
			}
			if name := BlogArtifactFileName(post); name != test.want+".html" {
				t.Fatalf("BlogArtifactFileName = %q, want %q", name, test.want+".html")
			}
		})
	}
}

func TestSanitizeCodeLanguage(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "Go", want: "go"},
		{input: "C++", want: "c++"},
		{input: "C#", want: "c#"},
		{input: "objective-c", want: "objective-c"},
		{input: " shell script ", want: "shellscript"},
		{input: "-ts-", want: "ts"},
		{input: "\"><script>", want: "script"},
		{input: strings.Repeat("x", 50), want: strings.Repeat("x", maxCodeLanguageBytes)},
		{input: "日本語", want: ""},
	}
	for _, test := range tests {
		if got := sanitizeCodeLanguage(test.input); got != test.want {
			t.Errorf("sanitizeCodeLanguage(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestIsAllowedWebURL(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{input: "https://github.com/o/r/pull/1", want: true},
		{input: "http://example.com", want: true},
		{input: "HTTPS://EXAMPLE.COM/A", want: true},
		{input: "https://example.com:8443/a?b=c#d", want: true},
		{input: "https://[::1]/x", want: true},
		{input: "", want: false},
		{input: "javascript:alert(1)", want: false},
		{input: "JAVASCRIPT:alert(1)", want: false},
		{input: "data:text/html,<script>", want: false},
		{input: "vbscript:msgbox", want: false},
		{input: "ftp://example.com", want: false},
		{input: "mailto:a@example.com", want: false},
		{input: "/relative/path", want: false},
		{input: "//example.com/x", want: false},
		{input: "https:example.com", want: false},
		{input: "https:///path", want: false},
		{input: "https://user:pass@example.com", want: false},
		{input: "https://example.com/a b", want: false},
		{input: "https://example.com/\"x", want: false},
		{input: "https://example.com/<x>", want: false},
		{input: "https://example.com\\@evil.com", want: false},
		{input: "https://example.com/\nx", want: false},
		{input: "https://例え.jp", want: false},
		{input: "https://example.com/" + strings.Repeat("a", maxWebURLBytes), want: false},
	}
	for _, test := range tests {
		if got := isAllowedWebURL(test.input); got != test.want {
			t.Errorf("isAllowedWebURL(%q) = %v, want %v", test.input, got, test.want)
		}
	}
}

func TestNormalizeBlogPostIsIdempotent(t *testing.T) {
	messy := model.BlogPost{
		Title:    "  Dex's \u202eweekly\u202c update: `retries`  ",
		Subtitle: "sub\r\ntitle",
		Slug:     strings.Repeat("Word ", 30),
		Summary:  " s ",
		Tags:     []string{"#a", "A", "b", "# #c", "#\t#x", "# #", "# #b"},
		Sections: []model.BlogSection{
			{Heading: "h", Blocks: []model.BlogBlock{
				{Type: "PARAGRAPH", Text: "p\n\n\n q"},
				{Type: model.BlogBlockBullets, Items: []string{" x "}},
				{Type: model.BlogBlockCode, Code: "\n  code  \n"},
				{Type: "unknown", Text: "u"},
			}},
			{Heading: strings.Repeat("h", maxHeadingCharacters+1)},
		},
		References: []model.SourceReference{
			{URL: "https://a.example"},
			{Label: " ", URL: "https://a.example/long?" + strings.Repeat("q", maxReferenceLabelCharacters)},
		},
	}
	for name, post := range map[string]model.BlogPost{"sample": sampleBlogPost(), "messy": messy} {
		t.Run(name, func(t *testing.T) {
			once, err := NormalizeBlogPost(post)
			if err != nil {
				t.Fatalf("first NormalizeBlogPost: %v", err)
			}
			twice, err := NormalizeBlogPost(once)
			if err != nil {
				t.Fatalf("second NormalizeBlogPost: %v", err)
			}
			if !reflect.DeepEqual(once, twice) {
				t.Fatalf("normalization is not idempotent\n once %#v\ntwice %#v", once, twice)
			}
		})
	}
	normalizedSample, err := NormalizeBlogPost(sampleBlogPost())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(normalizedSample, sampleBlogPost()) {
		t.Fatalf("the already-normalized sample changed\n got %#v\nwant %#v", normalizedSample, sampleBlogPost())
	}
}

func TestNormalizeBlogPostDoesNotModifyInput(t *testing.T) {
	post := model.BlogPost{
		Title: " Title ",
		Tags:  []string{" a ", ""},
		Sections: []model.BlogSection{{Heading: " h ", Blocks: []model.BlogBlock{
			{Type: model.BlogBlockBullets, Items: []string{" x ", ""}},
			{Type: "odd", Text: " t "},
		}}},
		References: []model.SourceReference{{Label: " l ", URL: " https://a.example "}},
	}
	before := model.BlogPost{
		Title: " Title ",
		Tags:  []string{" a ", ""},
		Sections: []model.BlogSection{{Heading: " h ", Blocks: []model.BlogBlock{
			{Type: model.BlogBlockBullets, Items: []string{" x ", ""}},
			{Type: "odd", Text: " t "},
		}}},
		References: []model.SourceReference{{Label: " l ", URL: " https://a.example "}},
	}
	if _, err := NormalizeBlogPost(post); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(post, before) {
		t.Fatalf("input modified\n got %#v\nwant %#v", post, before)
	}
}

func TestCleanTextAndProseText(t *testing.T) {
	tests := []struct {
		name     string
		function func(string) string
		input    string
		want     string
	}{
		{name: "clean CRLF", function: cleanText, input: "a\r\nb\rc\n", want: "a\nb\nc\n"},
		{name: "clean CR CR LF", function: cleanText, input: "a\r\r\nb", want: "a\n\nb"},
		{name: "clean separators", function: cleanText, input: "a\u2028b\u2029c", want: "a\nb\nc"},
		{name: "clean keeps tab", function: cleanText, input: "a\tb", want: "a\tb"},
		{name: "single line collapses", function: singleLineText, input: " a \n\t b\u00a0 c ", want: "a b c"},
		{name: "prose trims lines", function: proseText, input: "  a  \n  b  ", want: "a\nb"},
		{name: "prose collapses blank lines", function: proseText, input: "a\n \n\t\n\nb", want: "a\n\nb"},
		{name: "code trims trailing space only", function: codeText, input: "  a  \n\n    b\t\n", want: "  a\n\n    b"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.function(test.input); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}
