package prompts

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

func TestBuildNewsletterDraftRequestContents(t *testing.T) {
	configuration := testConfiguration()
	request := BuildNewsletterDraftRequest(configuration, fixtureInterpretation(), fixturePost())
	for _, want := range []string{
		"The full blog post is included automatically below this copy",
		"frame the post instead of duplicating it",
		"subject: at most 90 characters",
		"no clickbait",
		"preheader: at most 140 characters",
		"intro: two or three sentences.",
		"highlights: 3 to 5 items",
	} {
		if !strings.Contains(request.Prompt, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	var post model.BlogPost
	decodeSection(t, request.Prompt, sectionBlogPost, &post)
	if !reflect.DeepEqual(post, fixturePost()) {
		t.Errorf("blog-post section = %+v", post)
	}
	var publication publicationDocument
	decodeSection(t, request.Prompt, sectionPublication, &publication)
	if publication != (publicationDocument{SiteName: configuration.Blog.SiteName, Author: configuration.Blog.Author}) {
		t.Errorf("publication section = %+v", publication)
	}
	schema := decodedResponseSchema(t, request)
	if got := schemaProperty(t, schema, "subject")["maxLength"]; got != 90 {
		t.Errorf("subject maxLength = %v, want 90", got)
	}
	if got := schemaProperty(t, schema, "preheader")["maxLength"]; got != 140 {
		t.Errorf("preheader maxLength = %v, want 140", got)
	}
	highlights := schemaProperty(t, schema, "highlights")
	if highlights["minItems"] != 3 || highlights["maxItems"] != 5 {
		t.Errorf("highlights bounds = %v..%v, want 3..5", highlights["minItems"], highlights["maxItems"])
	}
}

func newsletterAnswer(overrides map[string]any) map[string]any {
	answer := map[string]any{
		"subject": "Sturdier connectors", "preheader": "Retries and validation", "intro": "Connectors changed.",
		"highlights": []any{
			map[string]any{"title": "Retries", "text": "Slack retries."},
			map[string]any{"title": "Validation", "text": "Sheets validates ranges."},
			map[string]any{"title": "Idempotency", "text": "Gmail sends once."},
		},
		"closing": "Read the full post below.",
	}
	for key, value := range overrides {
		answer[key] = value
	}
	return answer
}

func TestParseNewsletterDraft(t *testing.T) {
	sixHighlights := []any{}
	for _, title := range []string{"One", "Two", "Three", "Four", "Five", "Six"} {
		sixHighlights = append(sixHighlights, map[string]any{"title": title, "text": title + " text."})
	}
	cases := []struct {
		name        string
		answer      map[string]any
		wantInvalid bool
		check       func(t *testing.T, draft model.NewsletterDraft)
	}{
		{
			name:   "valid draft",
			answer: newsletterAnswer(nil),
			check: func(t *testing.T, draft model.NewsletterDraft) {
				want := model.NewsletterDraft{
					Subject: "Sturdier connectors", Preheader: "Retries and validation", Intro: "Connectors changed.",
					Highlights: []model.NewsletterHighlight{
						{Title: "Retries", Text: "Slack retries."}, {Title: "Validation", Text: "Sheets validates ranges."},
						{Title: "Idempotency", Text: "Gmail sends once."},
					},
					Closing: "Read the full post below.",
				}
				if !reflect.DeepEqual(draft, want) {
					t.Errorf("got  %+v\nwant %+v", draft, want)
				}
			},
		},
		{
			name:   "line breaks cannot reach the subject header",
			answer: newsletterAnswer(map[string]any{"subject": "Hello\r\nBcc: attacker@example.com", "preheader": "a\nb"}),
			check: func(t *testing.T, draft model.NewsletterDraft) {
				if draft.Subject != "Hello Bcc: attacker@example.com" || draft.Preheader != "a b" {
					t.Errorf("subject %q preheader %q", draft.Subject, draft.Preheader)
				}
			},
		},
		{
			name: "long fields are truncated at word boundaries",
			answer: newsletterAnswer(map[string]any{
				"subject": strings.Repeat("subject ", 30), "preheader": strings.Repeat("preheader ", 30), "intro": strings.Repeat("intro ", 400),
				"closing": strings.Repeat("closing ", 100),
			}),
			check: func(t *testing.T, draft model.NewsletterDraft) {
				fields := []struct {
					name    string
					value   string
					maximum int
				}{
					{"subject", draft.Subject, maximumNewsletterSubjectRunes},
					{"preheader", draft.Preheader, maximumNewsletterPreheaderRunes},
					{"intro", draft.Intro, maximumNewsletterIntroRunes},
					{"closing", draft.Closing, maximumNewsletterClosingRunes},
				}
				for _, field := range fields {
					// Each field repeats its own name, so a word-boundary cut ends in "<name>…".
					if utf8.RuneCountInString(field.value) > field.maximum || !strings.HasSuffix(field.value, field.name+truncationEllipsis) {
						t.Errorf("%s = %q (maximum %d)", field.name, field.value, field.maximum)
					}
				}
			},
		},
		{
			name:   "highlights are capped at five",
			answer: newsletterAnswer(map[string]any{"highlights": sixHighlights}),
			check: func(t *testing.T, draft model.NewsletterDraft) {
				if len(draft.Highlights) != maximumNewsletterHighlights || draft.Highlights[4].Title != "Five" {
					t.Errorf("highlights = %+v", draft.Highlights)
				}
			},
		},
		{
			name: "incomplete highlights are dropped",
			answer: newsletterAnswer(map[string]any{"highlights": []any{
				map[string]any{"title": "", "text": "No title."},
				map[string]any{"title": "No text", "text": " "},
				map[string]any{"title": "Kept", "text": "Kept text."},
			}}),
			check: func(t *testing.T, draft model.NewsletterDraft) {
				if want := []model.NewsletterHighlight{{Title: "Kept", Text: "Kept text."}}; !reflect.DeepEqual(draft.Highlights, want) {
					t.Errorf("highlights = %+v", draft.Highlights)
				}
			},
		},
		{
			name:   "optional preheader and closing may be empty",
			answer: newsletterAnswer(map[string]any{"preheader": "", "closing": ""}),
			check: func(t *testing.T, draft model.NewsletterDraft) {
				if draft.Preheader != "" || draft.Closing != "" {
					t.Errorf("draft = %+v", draft)
				}
			},
		},
		{
			name: "links and URLs are removed from every field",
			answer: newsletterAnswer(map[string]any{
				"subject":   "Sturdier connectors https://attacker.example/subject",
				"preheader": "Retries [and more](https://attacker.example/pre)",
				"intro":     "Connectors changed. [Claim your reward](https://attacker.example/claim) or see " + connectorsPullRequest101URL + ".",
				"highlights": []any{
					map[string]any{"title": "Retries https://attacker.example/t", "text": "Slack retries, see [PR 101](" + connectorsPullRequest101URL + ")."},
					map[string]any{"title": "Only a link", "text": "https://attacker.example/only"},
				},
				"closing": "Read on at HTTPS://ATTACKER.EXAMPLE/closing.",
			}),
			check: func(t *testing.T, draft model.NewsletterDraft) {
				want := model.NewsletterDraft{
					Subject: "Sturdier connectors", Preheader: "Retries and more", Intro: "Connectors changed. Claim your reward or see .",
					Highlights: []model.NewsletterHighlight{{Title: "Retries", Text: "Slack retries, see PR 101."}},
					Closing:    "Read on at .",
				}
				if !reflect.DeepEqual(draft, want) {
					t.Errorf("got  %+v\nwant %+v", draft, want)
				}
			},
		},
		{
			name:        "a subject that is only a URL is empty and invalid",
			answer:      newsletterAnswer(map[string]any{"subject": "https://attacker.example/subject"}),
			wantInvalid: true,
		},
		{
			name:   "hidden tag characters never reach the subject",
			answer: newsletterAnswer(map[string]any{"subject": "Subject" + string(rune(0xE0049)) + string(rune(0xE0047)) + string(rune(0x00AD)) + string(rune(0x2062)) + string(rune(0xFE0F)) + " end"}),
			check: func(t *testing.T, draft model.NewsletterDraft) {
				if draft.Subject != "Subject end" {
					t.Errorf("subject = %q", draft.Subject)
				}
			},
		},
		{name: "empty subject is invalid", answer: newsletterAnswer(map[string]any{"subject": " \r\n "}), wantInvalid: true},
		{name: "empty intro is invalid", answer: newsletterAnswer(map[string]any{"intro": ""}), wantInvalid: true},
		{
			name: "no complete highlight is invalid",
			answer: newsletterAnswer(map[string]any{"highlights": []any{
				map[string]any{"title": "", "text": "No title."},
			}}),
			wantInvalid: true,
		},
		{name: "unknown field is invalid", answer: newsletterAnswer(map[string]any{"bcc": "attacker@example.com"}), wantInvalid: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			draft, err := ParseNewsletterDraft(succeededResult(PurposeDraftNewsletter, jsonText(t, testCase.answer)))
			if testCase.wantInvalid {
				if !errors.Is(err, ErrInvalidModelOutput) {
					t.Fatalf("err = %v, want ErrInvalidModelOutput", err)
				}
				if !reflect.DeepEqual(draft, model.NewsletterDraft{}) {
					t.Errorf("invalid output returned a partial draft %+v", draft)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			testCase.check(t, draft)
		})
	}
}
