package content

import (
	"errors"
	"fmt"
	"strings"
)

// Editor limits match the model-draft bounds, so an edited draft renders like a generated one.

// ValidateEditedBlog bounds an editor's blog draft. The slug and every link stay as
// they were in original: links must come from the research, and the slug names the post.
func ValidateEditedBlog(edited, original BlogDraft) (BlogDraft, error) {
	var problems []error
	draft := BlogDraft{
		Title: collapseSpace(edited.Title), Subtitle: collapseSpace(edited.Subtitle), Slug: original.Slug,
		Summary: strings.TrimSpace(edited.Summary), Closing: strings.TrimSpace(edited.Closing),
	}
	problems = append(problems, runeLimit("Title", draft.Title, 160), runeLimit("Subtitle", draft.Subtitle, 240),
		runeLimit("Summary", draft.Summary, 1200), runeLimit("Closing", draft.Closing, 1200))
	if draft.Title == "" {
		problems = append(problems, errors.New("Title is required"))
	}
	if len(edited.Sections) > 8 {
		problems = append(problems, errors.New("keep at most 8 sections"))
	}
	for index, section := range edited.Sections {
		name := fmt.Sprintf("Section %d", index+1)
		section.Heading = collapseSpace(section.Heading)
		section.Paragraphs = nonEmpty(section.Paragraphs)
		section.Bullets = nonEmpty(section.Bullets)
		if section.Heading == "" && len(section.Paragraphs) == 0 && len(section.Bullets) == 0 {
			continue
		}
		if section.Heading == "" {
			problems = append(problems, fmt.Errorf("%s needs a heading", name))
		}
		if len(section.Paragraphs) == 0 && len(section.Bullets) == 0 {
			problems = append(problems, fmt.Errorf("%s needs a paragraph or a bullet", name))
		}
		problems = append(problems, runeLimit(name+" heading", section.Heading, 160),
			countLimit(name+" paragraphs", len(section.Paragraphs), 8), countLimit(name+" bullets", len(section.Bullets), 12))
		for _, paragraph := range section.Paragraphs {
			problems = append(problems, runeLimit(name+" paragraph", paragraph, 2000))
		}
		for _, bullet := range section.Bullets {
			problems = append(problems, runeLimit(name+" bullet", bullet, 400))
		}
		draft.Sections = append(draft.Sections, section)
	}
	if len(draft.Sections) == 0 {
		problems = append(problems, errors.New("keep at least one section"))
	}
	links := map[string]bool{}
	for _, highlight := range original.Highlights {
		if highlight.URL != "" {
			links[highlight.URL] = true
		}
	}
	for index, highlight := range edited.Highlights {
		name := fmt.Sprintf("Highlight %d", index+1)
		highlight.Title = collapseSpace(highlight.Title)
		highlight.Description = collapseSpace(highlight.Description)
		highlight.URL = strings.TrimSpace(highlight.URL)
		if highlight.URL != "" && !links[highlight.URL] {
			problems = append(problems, fmt.Errorf("%s links to a page that is not in the research", name))
		}
		if highlight.Title == "" {
			if highlight.Description != "" {
				problems = append(problems, fmt.Errorf("%s needs a title", name))
			}
			continue
		}
		problems = append(problems, runeLimit(name+" title", highlight.Title, 160), runeLimit(name+" description", highlight.Description, 400))
		draft.Highlights = append(draft.Highlights, highlight)
	}
	problems = append(problems, countLimit("Highlights", len(draft.Highlights), 10))
	if err := errors.Join(problems...); err != nil {
		return BlogDraft{}, err
	}
	return draft, nil
}

// ValidateEditedNewsletter bounds an editor's newsletter draft.
func ValidateEditedNewsletter(edited NewsletterDraft) (NewsletterDraft, error) {
	var problems []error
	draft := NewsletterDraft{
		Subject: collapseSpace(edited.Subject), Preheader: collapseSpace(edited.Preheader),
		Intro: strings.TrimSpace(edited.Intro), Closing: strings.TrimSpace(edited.Closing),
	}
	problems = append(problems, runeLimit("Subject", draft.Subject, 120), runeLimit("Preheader", draft.Preheader, 200),
		runeLimit("Intro", draft.Intro, 1200), runeLimit("Closing", draft.Closing, 600))
	if draft.Subject == "" {
		problems = append(problems, errors.New("Subject is required"))
	}
	for index, item := range edited.Items {
		name := fmt.Sprintf("Item %d", index+1)
		item.Title = collapseSpace(item.Title)
		item.Summary = collapseSpace(item.Summary)
		if item.Title == "" && item.Summary == "" {
			continue
		}
		if item.Title == "" {
			problems = append(problems, fmt.Errorf("%s needs a title", name))
		}
		problems = append(problems, runeLimit(name+" title", item.Title, 160), runeLimit(name+" summary", item.Summary, 600))
		draft.Items = append(draft.Items, item)
	}
	problems = append(problems, countLimit("Items", len(draft.Items), 8))
	if err := errors.Join(problems...); err != nil {
		return NewsletterDraft{}, err
	}
	return draft, nil
}

func runeLimit(name, text string, limit int) error {
	if count := len([]rune(text)); count > limit {
		return fmt.Errorf("%s has %d characters; keep it to %d", name, count, limit)
	}
	return nil
}

func countLimit(name string, count, limit int) error {
	if count > limit {
		return fmt.Errorf("%s has %d entries; keep it to %d", name, count, limit)
	}
	return nil
}

func nonEmpty(texts []string) []string {
	kept := []string{}
	for _, text := range texts {
		if text = strings.TrimSpace(text); text != "" {
			kept = append(kept, text)
		}
	}
	return kept
}

// maxSlackDraftRunes keeps the review post well inside Slack's 40,000-character message limit.
const maxSlackDraftRunes = 30000

// SlackReviewMessage is the review post: the blog and newsletter as plain text, then how to respond.
func SlackReviewMessage(blog BlogDraft, newsletterText, summary, editorURL, dexWebURL string, round int64) string {
	var message strings.Builder
	fmt.Fprintf(&message, "*Draft %d ready for review*: %s\nBased on %s.\n\n", round, blog.Title, summary)
	body := slackBlogText(blog) + "\n\n*Newsletter email*\n" + newsletterText
	if runes := []rune(body); len(runes) > maxSlackDraftRunes {
		body = string(runes[:maxSlackDraftRunes]) + "\n… (cut to fit Slack; the editor has the full draft)"
	}
	message.WriteString(body)
	fmt.Fprintf(&message, "\n\n*Reply in this thread* with `approve` to send it, `reject` to stop, or any feedback to get a revised draft.\n"+
		"Edit the text directly and approve in the editor: %s\nDex Web: %s", editorURL, dexWebURL)
	return message.String()
}

func slackBlogText(draft BlogDraft) string {
	var text strings.Builder
	fmt.Fprintf(&text, "*%s*\n", draft.Title)
	if draft.Subtitle != "" {
		fmt.Fprintf(&text, "_%s_\n", draft.Subtitle)
	}
	if draft.Summary != "" {
		fmt.Fprintf(&text, "\n%s\n", draft.Summary)
	}
	for _, section := range draft.Sections {
		fmt.Fprintf(&text, "\n*%s*\n", section.Heading)
		for _, paragraph := range section.Paragraphs {
			fmt.Fprintf(&text, "%s\n", paragraph)
		}
		for _, bullet := range section.Bullets {
			fmt.Fprintf(&text, "• %s\n", bullet)
		}
	}
	if len(draft.Highlights) > 0 {
		text.WriteString("\n*Highlights*\n")
		for _, highlight := range draft.Highlights {
			if highlight.URL != "" {
				fmt.Fprintf(&text, "• <%s|%s>: %s\n", highlight.URL, slackEscape(highlight.Title), highlight.Description)
			} else {
				fmt.Fprintf(&text, "• %s: %s\n", highlight.Title, highlight.Description)
			}
		}
	}
	if draft.Closing != "" {
		fmt.Fprintf(&text, "\n%s\n", draft.Closing)
	}
	return strings.TrimRight(text.String(), "\n")
}

// slackEscape keeps link labels from closing Slack's <url|label> syntax.
func slackEscape(text string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "|", "¦").Replace(text)
}
