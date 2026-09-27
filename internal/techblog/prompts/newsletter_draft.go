package prompts

import (
	"fmt"
	"strings"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/config"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

// Newsletter copy bounds.
const (
	maximumNewsletterSubjectRunes        = 90
	maximumNewsletterPreheaderRunes      = 140
	maximumNewsletterIntroRunes          = 1000
	minimumNewsletterHighlights          = 3
	maximumNewsletterHighlights          = 5
	maximumNewsletterHighlightTitleRunes = 120
	maximumNewsletterHighlightTextRunes  = 400
	maximumNewsletterClosingRunes        = 500
)

// BuildNewsletterDraftRequest builds the draft-newsletter generation request
// for the email copy that frames the blog post. The full post is appended
// below the copy by the renderer, so the model is told to frame rather than
// duplicate it.
func BuildNewsletterDraftRequest(configuration config.ProcessConfiguration, interpretation model.RequestInterpretation, post model.BlogPost) model.GenerationRequest {
	publication := publicationDocument{SiteName: configuration.Blog.SiteName, Author: configuration.Blog.Author}
	focus := researchFocusDocument{
		Topic:        interpretation.Topic,
		Instructions: interpretation.Instructions,
		Audience:     interpretation.Audience,
	}

	var prompt strings.Builder
	writeParagraph(&prompt, "Task: write the newsletter email copy that introduces the blog post in the blog-post section to subscribers "+
		"of the publication in the publication section.")
	writeParagraph(&prompt, "The full blog post is included automatically below this copy in the same email, so frame the post instead "+
		"of duplicating it: tell readers why this issue is worth their time and what they will learn, without restating it section by section.")
	writeBulletList(&prompt, "Answer fields:", []string{
		fmt.Sprintf("subject: at most %d characters; specific and factual; no clickbait, no all caps, no exclamation marks, no emoji.",
			maximumNewsletterSubjectRunes),
		fmt.Sprintf("preheader: at most %d characters; the inbox preview line that complements the subject without repeating it.",
			maximumNewsletterPreheaderRunes),
		"intro: two or three sentences.",
		fmt.Sprintf("highlights: %d to %d items, each a short title and one sentence of text about a key change covered in the post.",
			minimumNewsletterHighlights, maximumNewsletterHighlights),
		"closing: one short sentence, such as an invitation to read the full post below or to reply with feedback.",
		"Plain text only: no Markdown, HTML, links, URLs, or web addresses such as www.example.com or example.com/path. Links are " +
			"reduced to their label and URLs and web addresses are removed, inside `code` spans too; the post below the copy carries the sources.",
		"Use only facts from the blog post. The instructions and audience fields of the research-request section are the requester's " +
			"preferences for tone and readers; honor them unless they conflict with these rules.",
	})
	writeDelimitedSection(&prompt, sectionPublication, publication)
	writeDelimitedSection(&prompt, sectionResearchRequest, focus)
	writeDelimitedSection(&prompt, sectionBlogPost, post)

	systemInstruction := composeSystemInstruction(
		"You write concise, trustworthy engineering newsletter emails for experienced engineers.",
		"Never invent facts, numbers, or claims that the blog post does not support.",
	)
	return newStageGenerationRequest(configuration, PurposeDraftNewsletter, configuration.LanguageModel.DraftNewsletter,
		systemInstruction, finishPrompt(&prompt), newsletterDraftSchema())
}

// newsletterDraftSchema describes model.NewsletterDraft.
func newsletterDraftSchema() map[string]any {
	highlight := objectSchema("One key change covered in the post.", map[string]any{
		"title": requiredStringSchema("Short highlight title.", maximumNewsletterHighlightTitleRunes),
		"text":  requiredStringSchema("One sentence about the change.", maximumNewsletterHighlightTextRunes),
	}, "title", "text")
	return objectSchema("Newsletter email copy that frames the blog post.", map[string]any{
		"subject":    requiredStringSchema("Specific, factual subject line without clickbait.", maximumNewsletterSubjectRunes),
		"preheader":  stringSchema("Inbox preview line that complements the subject.", maximumNewsletterPreheaderRunes),
		"intro":      requiredStringSchema("Two or three sentences introducing the post.", maximumNewsletterIntroRunes),
		"highlights": arraySchema("Key changes covered in the post.", highlight, minimumNewsletterHighlights, maximumNewsletterHighlights),
		"closing":    stringSchema("One short closing sentence.", maximumNewsletterClosingRunes),
	}, "subject", "preheader", "intro", "highlights", "closing")
}

// ParseNewsletterDraft strictly decodes the draft-newsletter answer. Every
// field is collapsed to a single line (so no line break can reach an email
// header) and bounded: subject 90 characters, preheader 140, intro 1000,
// highlight title 120 and text 400, closing 500. The copy is plain text with
// no URLs, as the prompt requires, so every [label](url) link is reduced to
// its label and every bare web address (an http(s) URL, a www. host, or a
// host with a path such as example.com/login) is removed, inside code spans
// too; the post below the copy carries the citations. Subject and intro must
// be non-empty; highlights missing a title or text are dropped, at most five
// are kept, and at least one must remain.
func ParseNewsletterDraft(result model.GenerationResult) (model.NewsletterDraft, error) {
	var output model.NewsletterDraft
	if err := decodeGenerationResult(result, PurposeDraftNewsletter, &output); err != nil {
		return model.NewsletterDraft{}, err
	}
	plainCopyRules := strictInlineMarkupRules(newCitableSourceCatalog())
	draft := model.NewsletterDraft{
		Subject:    sanitizeInlineMarkupLine(output.Subject, plainCopyRules, maximumNewsletterSubjectRunes),
		Preheader:  sanitizeInlineMarkupLine(output.Preheader, plainCopyRules, maximumNewsletterPreheaderRunes),
		Intro:      sanitizeInlineMarkupLine(output.Intro, plainCopyRules, maximumNewsletterIntroRunes),
		Highlights: []model.NewsletterHighlight{},
		Closing:    sanitizeInlineMarkupLine(output.Closing, plainCopyRules, maximumNewsletterClosingRunes),
	}
	if draft.Subject == "" {
		return model.NewsletterDraft{}, invalidModelOutput(PurposeDraftNewsletter, "subject is empty")
	}
	if draft.Intro == "" {
		return model.NewsletterDraft{}, invalidModelOutput(PurposeDraftNewsletter, "intro is empty")
	}
	for _, highlight := range output.Highlights {
		if len(draft.Highlights) >= maximumNewsletterHighlights {
			break
		}
		title := sanitizeInlineMarkupLine(highlight.Title, plainCopyRules, maximumNewsletterHighlightTitleRunes)
		text := sanitizeInlineMarkupLine(highlight.Text, plainCopyRules, maximumNewsletterHighlightTextRunes)
		if title == "" || text == "" {
			continue
		}
		draft.Highlights = append(draft.Highlights, model.NewsletterHighlight{Title: title, Text: text})
	}
	if len(draft.Highlights) == 0 {
		return model.NewsletterDraft{}, invalidModelOutput(PurposeDraftNewsletter, "no complete highlight")
	}
	return draft, nil
}
