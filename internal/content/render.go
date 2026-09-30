package content

import (
	"bytes"
	"fmt"
	"html/template"
	"regexp"
	"strings"
)

// Every model-written string is escaped by html/template; only backtick spans become <code>.

var inlineCode = regexp.MustCompile("`([^`\n]{1,200})`")

func richText(text string) template.HTML {
	escaped := template.HTMLEscapeString(text)
	return template.HTML(inlineCode.ReplaceAllString(escaped, "<code>$1</code>"))
}

// BlogPage is the context rendered into the self-contained blog artifact.
type BlogPage struct {
	Draft           BlogDraft
	PublicationName string
	Window          Window
	Repositories    []SelectedRepository
}

var blogTemplate = template.Must(template.New("blog").Funcs(template.FuncMap{"rich": richText}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Draft.Title}} · {{.PublicationName}}</title>
<meta name="description" content="{{.Draft.Subtitle}}">
<style>
:root{--ink:#16181d;--muted:#5c6370;--line:#e4e6eb;--accent:#3b5bdb;--paper:#fff;--wash:#f6f7f9}
@media (prefers-color-scheme:dark){:root{--ink:#e8eaee;--muted:#9aa1ad;--line:#2a2e36;--accent:#8ea4ff;--paper:#111317;--wash:#181b20}}
*{box-sizing:border-box}
body{margin:0;background:var(--paper);color:var(--ink);font:17px/1.7 -apple-system,BlinkMacSystemFont,"Segoe UI",Inter,Roboto,sans-serif}
main{max-width:720px;margin:0 auto;padding:56px 24px 96px}
.eyebrow{color:var(--accent);font-size:13px;font-weight:600;letter-spacing:.08em;text-transform:uppercase}
h1{font-size:40px;line-height:1.15;letter-spacing:-.02em;margin:12px 0 12px}
.subtitle{color:var(--muted);font-size:20px;margin:0 0 20px}
.meta{color:var(--muted);font-size:14px;border-bottom:1px solid var(--line);padding-bottom:24px;margin-bottom:32px}
.summary{background:var(--wash);border-left:3px solid var(--accent);padding:16px 20px;border-radius:6px}
h2{font-size:24px;letter-spacing:-.01em;margin:44px 0 8px}
code{font:14px/1.4 ui-monospace,SFMono-Regular,Menlo,monospace;background:var(--wash);border:1px solid var(--line);border-radius:4px;padding:1px 5px}
ul{padding-left:22px}
.highlights{display:grid;gap:12px;margin-top:16px}
.highlight{border:1px solid var(--line);border-radius:8px;padding:14px 16px}
.highlight strong{display:block}
.highlight p{margin:4px 0 0;color:var(--muted);font-size:15px}
a{color:var(--accent)}
footer{margin-top:56px;padding-top:24px;border-top:1px solid var(--line);color:var(--muted);font-size:14px}
</style>
</head>
<body>
<main>
<article>
<div class="eyebrow">{{.PublicationName}}</div>
<h1>{{.Draft.Title}}</h1>
{{if .Draft.Subtitle}}<p class="subtitle">{{.Draft.Subtitle}}</p>{{end}}
<div class="meta">Changes from {{.Window.Label}}{{if .Repositories}} · {{range $index, $repository := .Repositories}}{{if $index}}, {{end}}{{$repository.FullName}}{{end}}{{end}}</div>
{{if .Draft.Summary}}<p class="summary">{{rich .Draft.Summary}}</p>{{end}}
{{range .Draft.Sections}}<section>
<h2>{{.Heading}}</h2>
{{range .Paragraphs}}<p>{{rich .}}</p>
{{end}}{{if .Bullets}}<ul>{{range .Bullets}}<li>{{rich .}}</li>{{end}}</ul>{{end}}
</section>
{{end}}{{if .Draft.Highlights}}<section>
<h2>Highlights</h2>
<div class="highlights">{{range .Draft.Highlights}}<div class="highlight"><strong>{{if .URL}}<a href="{{.URL}}">{{.Title}}</a>{{else}}{{.Title}}{{end}}</strong>{{if .Description}}<p>{{rich .Description}}</p>{{end}}</div>{{end}}</div>
</section>
{{end}}{{if .Draft.Closing}}<p>{{rich .Draft.Closing}}</p>{{end}}
</article>
<footer>Written from merged pull requests and commits between {{.Window.Label}}.</footer>
</main>
</body>
</html>
`))

// RenderBlogHTML returns one self-contained HTML document with inline CSS and no scripts.
func RenderBlogHTML(page BlogPage) (string, error) {
	var buffer bytes.Buffer
	if err := blogTemplate.Execute(&buffer, page); err != nil {
		return "", fmt.Errorf("render blog HTML: %w", err)
	}
	return buffer.String(), nil
}

// Email is one recipient's copy of the post: the same content as the blog, in email formatting.
type Email struct {
	Draft           BlogDraft
	PublicationName string
	Window          Window
	PostURL         string
	UnsubscribeURL  string
}

// EmailSubject is the subject line of the post's email: the post title.
func EmailSubject(draft BlogDraft) string { return draft.Title }

var emailTemplate = template.Must(template.New("email").Funcs(template.FuncMap{"rich": richText}).Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.Draft.Title}}</title></head>
<body style="margin:0;background:#f6f7f9;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;color:#16181d">
{{if .Draft.Subtitle}}<span style="display:none;max-height:0;overflow:hidden">{{.Draft.Subtitle}}</span>{{end}}
<table role="presentation" width="100%" cellpadding="0" cellspacing="0"><tr><td align="center" style="padding:32px 12px">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:640px;background:#ffffff;border:1px solid #e4e6eb;border-radius:10px">
<tr><td style="padding:32px 32px 8px">
<div style="color:#3b5bdb;font-size:12px;font-weight:600;letter-spacing:.08em;text-transform:uppercase">{{.PublicationName}}</div>
<h1 style="font-size:26px;line-height:1.25;margin:10px 0 8px">{{.Draft.Title}}</h1>
{{if .Draft.Subtitle}}<p style="font-size:17px;line-height:1.5;color:#5c6370;margin:0 0 8px">{{.Draft.Subtitle}}</p>{{end}}
<p style="font-size:13px;color:#8a909b;margin:0 0 16px">Changes from {{.Window.Label}}</p>
{{if .Draft.Summary}}<p style="font-size:16px;line-height:1.6;margin:0 0 8px;padding:12px 16px;background:#f6f7f9;border-left:3px solid #3b5bdb">{{rich .Draft.Summary}}</p>{{end}}
</td></tr>
{{range .Draft.Sections}}<tr><td style="padding:8px 32px">
<h2 style="font-size:19px;line-height:1.3;margin:16px 0 8px">{{.Heading}}</h2>
{{range .Paragraphs}}<p style="font-size:16px;line-height:1.6;margin:0 0 12px">{{rich .}}</p>
{{end}}{{if .Bullets}}<ul style="font-size:16px;line-height:1.6;margin:0 0 12px;padding-left:22px">{{range .Bullets}}<li>{{rich .}}</li>{{end}}</ul>{{end}}
</td></tr>
{{end}}{{if .Draft.Highlights}}<tr><td style="padding:8px 32px">
<h2 style="font-size:19px;line-height:1.3;margin:16px 0 8px">Highlights</h2>
{{range .Draft.Highlights}}<div style="border:1px solid #e4e6eb;border-radius:8px;padding:12px 14px;margin:0 0 10px">
<div style="font-size:16px;font-weight:600">{{if .URL}}<a href="{{.URL}}" style="color:#3b5bdb">{{.Title}}</a>{{else}}{{.Title}}{{end}}</div>
{{if .Description}}<div style="font-size:15px;line-height:1.55;color:#5c6370;margin-top:4px">{{rich .Description}}</div>{{end}}
</div>{{end}}
</td></tr>
{{end}}<tr><td style="padding:8px 32px 32px">
{{if .Draft.Closing}}<p style="font-size:16px;line-height:1.6;margin:0 0 16px">{{rich .Draft.Closing}}</p>{{end}}
{{if .PostURL}}<a href="{{.PostURL}}" style="display:inline-block;background:#3b5bdb;color:#ffffff;text-decoration:none;font-weight:600;padding:10px 18px;border-radius:6px">Read it on the blog</a>{{end}}
</td></tr></table>
<p style="font-size:12px;color:#8a909b;margin:16px 0 0">You receive this because you subscribed to {{.PublicationName}}. <a href="{{.UnsubscribeURL}}" style="color:#8a909b">Unsubscribe</a></p>
</td></tr></table>
</body></html>
`))

// RenderEmailHTML renders the post as one recipient's HTML email.
func RenderEmailHTML(email Email) (string, error) {
	var buffer bytes.Buffer
	if err := emailTemplate.Execute(&buffer, email); err != nil {
		return "", fmt.Errorf("render email HTML: %w", err)
	}
	return buffer.String(), nil
}

// RenderEmailText is the plain-text alternative: the post's text, then the links.
func RenderEmailText(email Email) string {
	var text strings.Builder
	text.WriteString(RenderBlogText(email.Draft))
	if email.PostURL != "" {
		fmt.Fprintf(&text, "\nRead it on the blog: %s\n", email.PostURL)
	}
	fmt.Fprintf(&text, "\n--\nYou receive this because you subscribed to %s.\nUnsubscribe: %s\n", email.PublicationName, email.UnsubscribeURL)
	return text.String()
}

// RenderBlogText is a plain-text reading copy of the draft for the Dex Web review panel.
func RenderBlogText(draft BlogDraft) string {
	var text strings.Builder
	fmt.Fprintf(&text, "%s\n", draft.Title)
	if draft.Subtitle != "" {
		fmt.Fprintf(&text, "%s\n", draft.Subtitle)
	}
	if draft.Summary != "" {
		fmt.Fprintf(&text, "\n%s\n", draft.Summary)
	}
	for _, section := range draft.Sections {
		fmt.Fprintf(&text, "\n## %s\n", section.Heading)
		for _, paragraph := range section.Paragraphs {
			fmt.Fprintf(&text, "\n%s\n", paragraph)
		}
		for _, bullet := range section.Bullets {
			fmt.Fprintf(&text, "- %s\n", bullet)
		}
	}
	if len(draft.Highlights) > 0 {
		text.WriteString("\n## Highlights\n")
		for _, highlight := range draft.Highlights {
			fmt.Fprintf(&text, "- %s: %s", highlight.Title, highlight.Description)
			if highlight.URL != "" {
				fmt.Fprintf(&text, " (%s)", highlight.URL)
			}
			text.WriteString("\n")
		}
	}
	if draft.Closing != "" {
		fmt.Fprintf(&text, "\n%s\n", draft.Closing)
	}
	return text.String()
}
