package content

import (
	"errors"
	"fmt"
	"strings"
)

// Editor limits match the model-draft bounds, so an edited draft renders like a generated one.
// The blog post is the only document: the email is the same post in email formatting.

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
			problems = append(problems, fmt.Errorf("%s links to a page the draft did not cite; keep its original link", name))
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

func runeLimit(name, text string, limit int) error {
	count := len([]rune(text))
	// Drafts written before truncation counted its ellipsis may hold limit+1 runes ending in one.
	if count == limit+1 && strings.HasSuffix(text, "…") {
		return nil
	}
	if count > limit {
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

// SlackReviewMessage is the review post: the draft as plain text, then how to respond. The email
// carries the same content, so the draft appears once.
// heading is the already-escaped first line, such as "*Draft 2 ready for review*: <title>".
// Model text is escaped, so research text can never mention the channel or disguise a link.
func SlackReviewMessage(heading string, blog BlogDraft, summary, editorURL, dexWebURL string) string {
	var message strings.Builder
	fmt.Fprintf(&message, "%s\nBased on %s. The email sends this same post, with the subject line \"%s\".\n\n",
		heading, SlackText(summary), SlackText(EmailSubject(blog)))
	blogText := slackBlogText(blog)
	if runes := []rune(blogText); len(runes) > maxSlackDraftRunes {
		blogText = string(runes[:maxSlackDraftRunes]) + "\n… (cut to fit Slack; the editor has the full post)"
	}
	message.WriteString(blogText)
	fmt.Fprintf(&message, "\n\n*Reply in this thread* with `approve` to send it, `reject` to stop, or any feedback to get a revised draft.\n"+
		"Edit the text directly and approve in the editor: %s\nDex Web: %s", editorURL, dexWebURL)
	return message.String()
}

func slackBlogText(draft BlogDraft) string {
	var text strings.Builder
	fmt.Fprintf(&text, "*%s*\n", SlackText(draft.Title))
	if draft.Subtitle != "" {
		fmt.Fprintf(&text, "_%s_\n", SlackText(draft.Subtitle))
	}
	if draft.Summary != "" {
		fmt.Fprintf(&text, "\n%s\n", SlackText(draft.Summary))
	}
	for _, section := range draft.Sections {
		fmt.Fprintf(&text, "\n*%s*\n", SlackText(section.Heading))
		for _, paragraph := range section.Paragraphs {
			fmt.Fprintf(&text, "%s\n", SlackText(paragraph))
		}
		for _, bullet := range section.Bullets {
			fmt.Fprintf(&text, "• %s\n", SlackText(bullet))
		}
	}
	if len(draft.Highlights) > 0 {
		text.WriteString("\n*Highlights*\n")
		for _, highlight := range draft.Highlights {
			if highlight.URL != "" {
				fmt.Fprintf(&text, "• <%s|%s>: %s\n", highlight.URL, slackEscape(highlight.Title), SlackText(highlight.Description))
			} else {
				fmt.Fprintf(&text, "• %s: %s\n", SlackText(highlight.Title), SlackText(highlight.Description))
			}
		}
	}
	if draft.Closing != "" {
		fmt.Fprintf(&text, "\n%s\n", SlackText(draft.Closing))
	}
	return strings.TrimRight(text.String(), "\n")
}

// SlackText escapes the three characters Slack's mrkdwn gives meaning to, so text cannot
// become a mention, a channel broadcast, or a link.
func SlackText(text string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(text)
}

// slackEscape also keeps link labels from closing Slack's <url|label> syntax.
func slackEscape(text string) string {
	return strings.ReplaceAll(SlackText(text), "|", "¦")
}
