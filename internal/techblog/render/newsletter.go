package render

import (
	"fmt"
	"html/template"
	"strconv"
	"strings"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

// Size bounds for newsletter copy. Characters are Unicode code points.
const (
	maxSubjectCharacters            = 150
	maxPreheaderCharacters          = 300
	maxIntroCharacters              = 5000
	maxHighlightCount               = 12
	maxHighlightTitleCharacters     = 200
	maxHighlightTextCharacters      = 1000
	maxClosingCharacters            = 3000
	maxFooterCharacters             = 2000
	plainTextRuleWidth              = 60
	plainTextHeadingUnderlineMaxLen = 72
)

// Size bounds for the rendered newsletter, in bytes. Every inline element of
// the email carries its own style attribute, so markup-dense copy can render
// many times larger than its text even when the post is within its bounds.
// The bounds keep the stored email, the per-subscriber delivery input, and
// the MIME message (both bodies, transfer-encoded) far below the Gmail
// connector's default 10 MiB message limit, while leaving room for posts at
// the text limit with ordinary markup density.
const (
	maxNewsletterHTMLBytes = 2 << 20
	maxNewsletterTextBytes = 1 << 20
)

// Outlook-only conditional comments that wrap the email card and footer in a
// fixed 600 pixel table. Outlook for Windows renders with Word, which ignores
// max-width and would otherwise stretch the width:100% card across the whole
// reading pane; every other client ignores the comments. html/template drops
// comments written in template text, so they are passed in as trusted
// template.HTML constants. They never contain model text.
const (
	outlookContainerStart template.HTML = `<!--[if mso]><table role="presentation" width="600" align="center" cellpadding="0" cellspacing="0" border="0" style="width:600px;"><tr><td style="padding:0;"><![endif]-->`
	outlookContainerEnd   template.HTML = `<!--[if mso]></td></tr></table><![endif]-->`
)

// normalizedNewsletterDraft is validated newsletter copy.
type normalizedNewsletterDraft struct {
	Subject    string
	Preheader  string
	Intro      string
	Highlights []model.NewsletterHighlight
	Closing    string
}

// normalizeNewsletterDraft validates and cleans newsletter copy. The subject
// becomes one line: CR, LF, and the other line-break characters (VT, FF,
// NEL, U+2028, U+2029) and bidirectional controls become spaces, every other
// control character is removed, and whitespace is collapsed.
func normalizeNewsletterDraft(draft model.NewsletterDraft) (normalizedNewsletterDraft, error) {
	subject := singleLineText(strings.Map(func(character rune) rune {
		switch {
		case character == '\r', character == '\n', character == '\v', character == '\f', character == nextLine,
			character == lineSeparator, character == paragraphSeparator, isBidirectionalControl(character):
			return ' '
		default:
			return character
		}
	}, draft.Subject))
	if subject == "" {
		return normalizedNewsletterDraft{}, fmt.Errorf("newsletter subject is required")
	}
	if err := checkCharacterLimit("newsletter subject", subject, maxSubjectCharacters); err != nil {
		return normalizedNewsletterDraft{}, err
	}
	preheader := singleLineText(draft.Preheader)
	if err := checkCharacterLimit("newsletter preheader", preheader, maxPreheaderCharacters); err != nil {
		return normalizedNewsletterDraft{}, err
	}
	intro := proseText(draft.Intro)
	if intro == "" {
		return normalizedNewsletterDraft{}, fmt.Errorf("newsletter intro is required")
	}
	if err := checkCharacterLimit("newsletter intro", intro, maxIntroCharacters); err != nil {
		return normalizedNewsletterDraft{}, err
	}
	var highlights []model.NewsletterHighlight
	for index, rawHighlight := range draft.Highlights {
		highlight := model.NewsletterHighlight{Title: singleLineText(rawHighlight.Title), Text: singleLineText(rawHighlight.Text)}
		if highlight.Title == "" && highlight.Text == "" {
			continue
		}
		if err := checkCharacterLimit(fmt.Sprintf("newsletter highlight %d title", index+1), highlight.Title, maxHighlightTitleCharacters); err != nil {
			return normalizedNewsletterDraft{}, err
		}
		if err := checkCharacterLimit(fmt.Sprintf("newsletter highlight %d text", index+1), highlight.Text, maxHighlightTextCharacters); err != nil {
			return normalizedNewsletterDraft{}, err
		}
		highlights = append(highlights, highlight)
	}
	if len(highlights) > maxHighlightCount {
		return normalizedNewsletterDraft{}, fmt.Errorf("newsletter has %d highlights; the limit is %d", len(highlights), maxHighlightCount)
	}
	closing := proseText(draft.Closing)
	if err := checkCharacterLimit("newsletter closing", closing, maxClosingCharacters); err != nil {
		return normalizedNewsletterDraft{}, err
	}
	return normalizedNewsletterDraft{
		Subject:    subject,
		Preheader:  preheader,
		Intro:      intro,
		Highlights: highlights,
		Closing:    closing,
	}, nil
}

// newsletterHighlightView is one parsed newsletter highlight.
type newsletterHighlightView struct {
	// Title holds text and code segments.
	Title []inlineSegment
	// Text holds text, code, and link segments.
	Text []inlineSegment
}

// newsletterView is the data of the newsletter email template.
type newsletterView struct {
	Subject    string
	Preheader  string
	SiteName   string
	Author     string
	WebURL     string
	Intro      [][]inlineSegment
	Highlights []newsletterHighlightView
	Post       blogPostView
	Closing    [][]inlineSegment
	Footer     [][]inlineSegment
	// OutlookContainerStart and OutlookContainerEnd are always
	// outlookContainerStart and outlookContainerEnd.
	OutlookContainerStart template.HTML
	OutlookContainerEnd   template.HTML
}

// RenderNewsletter validates the newsletter draft, normalizes post, and
// renders the deliverable email.
//
// The subject is required, reduced to one line with CR, LF, and control
// characters replaced, and at most 150 characters; the intro is required.
// The preheader falls back to the post summary. HTMLBody is an email-client
// safe document: a table-based 600 pixel layout styled only with inline style
// attributes, held at 600 pixels in Outlook for Windows by an Outlook-only
// conditional table, with no style element, script, or external asset. It
// holds a hidden preheader, the intro, the highlights, a "Read on the web"
// button when PublishedBlogURL is non-empty, the full blog post, the closing,
// and footer. TextBody is the plain-text alternative with the same content,
// links written as "label (url)" and lines separated by LF. Intro, highlight
// text, closing, and footer support the same inline markup as blog text.
//
// It returns an error when the draft, post, or footer is invalid, or when
// HTMLBody would exceed 2 MiB or TextBody 1 MiB.
func RenderNewsletter(draft model.NewsletterDraft, post model.BlogPost, presentation BlogPresentation, footer string) (model.RenderedNewsletter, error) {
	normalizedDraft, err := normalizeNewsletterDraft(draft)
	if err != nil {
		return model.RenderedNewsletter{}, err
	}
	normalizedPost, err := NormalizeBlogPost(post)
	if err != nil {
		return model.RenderedNewsletter{}, err
	}
	footerText := proseText(footer)
	if err := checkCharacterLimit("newsletter footer", footerText, maxFooterCharacters); err != nil {
		return model.RenderedNewsletter{}, err
	}
	presentation = normalizedPresentation(presentation)
	view := newsletterView{
		Subject:   normalizedDraft.Subject,
		Preheader: normalizedDraft.Preheader,
		SiteName:  presentation.SiteName,
		Author:    presentation.Author,
		WebURL:    PublishedBlogURL(normalizedPost, presentation),
		Intro:     parseProseParagraphs(normalizedDraft.Intro),
		Post:      newBlogPostView(normalizedPost),
		Closing:   parseProseParagraphs(normalizedDraft.Closing),
		Footer:    parseProseParagraphs(footerText),

		OutlookContainerStart: outlookContainerStart,
		OutlookContainerEnd:   outlookContainerEnd,
	}
	if view.Preheader == "" {
		view.Preheader = normalizedPost.Summary
	}
	for _, highlight := range normalizedDraft.Highlights {
		view.Highlights = append(view.Highlights, newsletterHighlightView{
			Title: parseInline(highlight.Title, false),
			Text:  parseInline(highlight.Text, true),
		})
	}
	htmlBody, err := executeTemplate("newsletter", newsletterTemplateText, view)
	if err != nil {
		return model.RenderedNewsletter{}, err
	}
	if err := checkByteLimit("newsletter HTML body", htmlBody, maxNewsletterHTMLBytes); err != nil {
		return model.RenderedNewsletter{}, err
	}
	textBody := renderNewsletterText(view)
	if err := checkByteLimit("newsletter text body", textBody, maxNewsletterTextBytes); err != nil {
		return model.RenderedNewsletter{}, err
	}
	return model.RenderedNewsletter{
		Subject:  normalizedDraft.Subject,
		HTMLBody: htmlBody,
		TextBody: textBody,
	}, nil
}

// Email font stacks, substituted into the template source before parsing.
const (
	emailSansFontStack = "-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif"
	emailMonoFontStack = "ui-monospace,SFMono-Regular,Menlo,Consolas,'Liberation Mono','Courier New',monospace"
)

// newsletterTemplateText is the html/template source of the newsletter
// email. SANS_FONTS and MONO_FONTS are replaced by the font stacks.
var newsletterTemplateText = strings.NewReplacer(
	"SANS_FONTS", emailSansFontStack,
	"MONO_FONTS", emailMonoFontStack,
).Replace(`{{define "label"}}{{range .}}{{if .IsCode}}<code style="font-family:MONO_FONTS;font-size:0.9em;background-color:#f1f3f5;border-radius:4px;padding:1px 4px;color:#1f2937;">{{.Text}}</code>{{else}}{{.Text}}{{end}}{{end}}{{end}}
{{- define "inline"}}{{range .}}{{if .IsCode}}<code style="font-family:MONO_FONTS;font-size:0.9em;background-color:#f1f3f5;border-radius:4px;padding:1px 4px;color:#1f2937;">{{.Text}}</code>{{else if .IsLink}}<a href="{{.URL}}" rel="noopener" style="color:#1d5fd1;text-decoration:underline;">{{template "label" .Label}}</a>{{else}}{{.Text}}{{end}}{{end}}{{end}}
{{- define "paragraphs"}}{{range .}}<p style="margin:0 0 16px 0;">{{template "inline" .}}</p>
{{end}}{{end}}
{{- define "block"}}
{{- if eq .Kind "bullets"}}{{template "paragraphs" .Paragraphs}}<ul style="margin:0 0 16px 0;padding:0 0 0 24px;">
{{range .Items}}<li style="margin:0 0 6px 0;">{{template "inline" .}}</li>
{{end}}</ul>
{{else if eq .Kind "code"}}{{template "paragraphs" .Paragraphs}}<pre style="margin:0 0 16px 0;padding:12px 14px;background-color:#f6f8fa;border:1px solid #e3e7ec;border-radius:6px;font-family:MONO_FONTS;font-size:13px;line-height:20px;color:#1f2937;white-space:pre-wrap;word-wrap:break-word;overflow-wrap:anywhere;"><code style="font-family:MONO_FONTS;font-size:13px;">{{.Code}}</code></pre>
{{else if eq .Kind "quote"}}<blockquote style="margin:0 0 16px 0;padding:2px 0 2px 16px;border-left:3px solid #cfd6de;color:#4b5563;font-style:italic;">
{{template "paragraphs" .Paragraphs}}</blockquote>
{{else if eq .Kind "callout"}}<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="width:100%;margin:0 0 16px 0;border-collapse:collapse;">
<tr><td style="padding:14px 16px 0 16px;background-color:#eef4fd;border-left:4px solid #1d5fd1;font-family:SANS_FONTS;font-size:16px;line-height:26px;color:#1f2937;">
{{template "paragraphs" .Paragraphs}}</td></tr>
</table>
{{else}}{{template "paragraphs" .Paragraphs}}{{end}}
{{- end}}
{{- define "document"}}<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light">
<meta name="supported-color-schemes" content="light">
<title>{{.Subject}}</title>
</head>
<body style="margin:0;padding:0;background-color:#f3f4f6;">
{{if .Preheader}}<span style="display:none;visibility:hidden;mso-hide:all;font-size:1px;line-height:1px;color:#f3f4f6;max-height:0;max-width:0;opacity:0;overflow:hidden;">{{.Preheader}}</span>
{{end}}<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="width:100%;background-color:#f3f4f6;">
<tr>
<td align="center" style="padding:24px 12px;">
{{.OutlookContainerStart}}
<table role="presentation" width="600" cellpadding="0" cellspacing="0" border="0" style="width:100%;max-width:600px;background-color:#ffffff;border:1px solid #e5e7eb;border-radius:8px;">
{{if .SiteName}}<tr>
<td style="padding:28px 32px 0 32px;font-family:SANS_FONTS;font-size:12px;line-height:18px;font-weight:600;letter-spacing:1px;text-transform:uppercase;color:#6b7280;">{{.SiteName}}</td>
</tr>
{{end}}<tr>
<td style="padding:20px 32px 4px 32px;font-family:SANS_FONTS;font-size:16px;line-height:26px;color:#1f2937;">
{{template "paragraphs" .Intro}}</td>
</tr>
{{if .Highlights}}<tr>
<td style="padding:0 32px 4px 32px;font-family:SANS_FONTS;font-size:16px;line-height:26px;color:#1f2937;">
<h2 style="margin:0 0 10px 0;font-family:SANS_FONTS;font-size:18px;line-height:26px;font-weight:700;color:#111827;">Highlights</h2>
<ul style="margin:0 0 16px 0;padding:0 0 0 24px;">
{{range .Highlights}}<li style="margin:0 0 8px 0;">{{if .Title}}<strong style="color:#111827;">{{template "label" .Title}}</strong>{{if .Text}}: {{end}}{{end}}{{template "inline" .Text}}</li>
{{end}}</ul>
</td>
</tr>
{{end}}{{if .WebURL}}<tr>
<td style="padding:4px 32px 28px 32px;">
<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="border-collapse:separate;">
<tr>
<td align="center" bgcolor="#1d5fd1" style="border-radius:6px;background-color:#1d5fd1;">
<a href="{{.WebURL}}" rel="noopener" style="display:inline-block;padding:12px 22px;font-family:SANS_FONTS;font-size:15px;line-height:20px;font-weight:600;color:#ffffff;text-decoration:none;border-radius:6px;">Read on the web</a>
</td>
</tr>
</table>
</td>
</tr>
{{end}}<tr>
<td style="padding:0 32px;"><table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="width:100%;"><tr><td style="border-top:1px solid #e5e7eb;font-size:1px;line-height:1px;height:1px;">&nbsp;</td></tr></table></td>
</tr>
<tr>
<td style="padding:28px 32px 12px 32px;font-family:SANS_FONTS;font-size:16px;line-height:26px;color:#1f2937;">
<h1 style="margin:0 0 8px 0;font-family:SANS_FONTS;font-size:28px;line-height:34px;font-weight:700;color:#111827;">{{.Post.Title}}</h1>
{{if .Post.Subtitle}}<p style="margin:0 0 12px 0;font-size:18px;line-height:27px;color:#4b5563;">{{.Post.Subtitle}}</p>
{{end}}{{if .Author}}<p style="margin:0 0 12px 0;font-size:14px;line-height:21px;color:#6b7280;">By {{.Author}}</p>
{{end}}{{if .Post.Tags}}<p style="margin:0 0 20px 0;font-size:13px;line-height:20px;color:#6b7280;">{{range $index, $tag := .Post.Tags}}{{if $index}} · {{end}}{{$tag}}{{end}}</p>
{{end}}{{range .Post.Sections}}{{if .Heading}}<h2 style="margin:28px 0 12px 0;font-family:SANS_FONTS;font-size:21px;line-height:28px;font-weight:700;color:#111827;">{{template "label" .Heading}}</h2>
{{end}}{{range .Blocks}}{{template "block" .}}{{end}}{{end}}{{if .Post.References}}<h2 style="margin:28px 0 12px 0;font-family:SANS_FONTS;font-size:17px;line-height:24px;font-weight:700;color:#111827;">References</h2>
<ol style="margin:0 0 16px 0;padding:0 0 0 24px;font-size:14px;line-height:22px;">
{{range .Post.References}}<li style="margin:0 0 6px 0;"><a href="{{.URL}}" rel="noopener" style="color:#1d5fd1;text-decoration:underline;word-break:break-word;">{{.Label}}</a></li>
{{end}}</ol>
{{end}}</td>
</tr>
{{if .Closing}}<tr>
<td style="padding:0 32px 12px 32px;font-family:SANS_FONTS;font-size:16px;line-height:26px;color:#1f2937;">
{{template "paragraphs" .Closing}}</td>
</tr>
{{end}}</table>
{{if .Footer}}<table role="presentation" width="600" cellpadding="0" cellspacing="0" border="0" style="width:100%;max-width:600px;">
<tr>
<td style="padding:16px 32px 0 32px;font-family:SANS_FONTS;font-size:12px;line-height:18px;color:#6b7280;">
{{template "paragraphs" .Footer}}</td>
</tr>
</table>
{{end}}{{.OutlookContainerEnd}}
</td>
</tr>
</table>
</body>
</html>
{{end}}`)

// renderNewsletterText renders the plain-text body of the newsletter.
func renderNewsletterText(view newsletterView) string {
	var chunks []string
	add := func(chunk string) {
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
	}
	add(view.SiteName)
	for _, paragraph := range view.Intro {
		add(plainInlineText(paragraph))
	}
	if len(view.Highlights) > 0 {
		lines := []string{underlinedHeading("Highlights", '-')}
		for _, highlight := range view.Highlights {
			title := plainInlineText(highlight.Title)
			text := plainInlineText(highlight.Text)
			switch {
			case title != "" && text != "":
				lines = append(lines, "- "+title+": "+text)
			case title != "":
				lines = append(lines, "- "+title)
			default:
				lines = append(lines, "- "+text)
			}
		}
		add(strings.Join(lines, "\n"))
	}
	if view.WebURL != "" {
		add("Read on the web: " + view.WebURL)
	}
	add(strings.Repeat("=", plainTextRuleWidth))
	add(underlinedHeading(view.Post.Title, '='))
	add(view.Post.Subtitle)
	if view.Author != "" {
		add("By " + view.Author)
	}
	if len(view.Post.Tags) > 0 {
		add("Tags: " + strings.Join(view.Post.Tags, ", "))
	}
	for _, section := range view.Post.Sections {
		if len(section.Heading) > 0 {
			add(underlinedHeading(plainInlineText(section.Heading), '-'))
		}
		for _, block := range section.Blocks {
			for _, chunk := range plainTextBlock(block) {
				add(chunk)
			}
		}
	}
	if len(view.Post.References) > 0 {
		lines := []string{underlinedHeading("References", '-')}
		for index, reference := range view.Post.References {
			entry := reference.URL
			if reference.Label != reference.URL {
				entry = reference.Label + " (" + reference.URL + ")"
			}
			lines = append(lines, strconv.Itoa(index+1)+". "+entry)
		}
		add(strings.Join(lines, "\n"))
	}
	if len(view.Closing) > 0 {
		add(strings.Repeat("=", plainTextRuleWidth))
		for _, paragraph := range view.Closing {
			add(plainInlineText(paragraph))
		}
	}
	if len(view.Footer) > 0 {
		footerParagraphs := make([]string, 0, len(view.Footer))
		for _, paragraph := range view.Footer {
			footerParagraphs = append(footerParagraphs, plainInlineText(paragraph))
		}
		add("-- \n" + strings.Join(footerParagraphs, "\n\n"))
	}
	return strings.Join(chunks, "\n\n") + "\n"
}

// plainTextBlock renders one blog block as plain-text chunks.
func plainTextBlock(block blogBlockView) []string {
	var chunks []string
	for _, paragraph := range block.Paragraphs {
		chunks = append(chunks, plainInlineText(paragraph))
	}
	switch block.Kind {
	case string(model.BlogBlockBullets):
		lines := make([]string, 0, len(block.Items))
		for _, item := range block.Items {
			lines = append(lines, "- "+plainInlineText(item))
		}
		chunks = append(chunks, strings.Join(lines, "\n"))
	case string(model.BlogBlockCode):
		lines := strings.Split(block.Code, "\n")
		for index, line := range lines {
			if line != "" {
				lines[index] = "    " + line
			}
		}
		chunks = append(chunks, strings.Join(lines, "\n"))
	case string(model.BlogBlockQuote):
		for index, chunk := range chunks {
			chunks[index] = "> " + strings.ReplaceAll(chunk, "\n", "\n> ")
		}
	case string(model.BlogBlockCallout):
		if len(chunks) > 0 {
			chunks[0] = "Note: " + chunks[0]
		}
	}
	return chunks
}

// underlinedHeading returns heading followed by a line of marker characters
// as long as the heading, up to plainTextHeadingUnderlineMaxLen.
func underlinedHeading(heading string, marker byte) string {
	if heading == "" {
		return ""
	}
	width := runeCount(heading)
	if width > plainTextHeadingUnderlineMaxLen {
		width = plainTextHeadingUnderlineMaxLen
	}
	return heading + "\n" + strings.Repeat(string(marker), width)
}
