package render

import (
	"html/template"
	"strings"
	"time"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

// maxBlogDocumentBytes bounds the rendered blog artifact. Escaping and
// per-element markup make a document larger than its text, so the bound is
// checked on the rendered bytes; it leaves ample room for posts at the text
// limit with ordinary markup density.
const maxBlogDocumentBytes = 2 << 20

// blogContentSecurityPolicy forbids every resource except inline styles and
// data: images, so the artifact cannot load or leak anything when opened.
const blogContentSecurityPolicy = "default-src 'none'; style-src 'unsafe-inline'; img-src data:"

// blogStyleSheet is the complete inline style sheet of the blog artifact. It
// must never reference a URL.
const blogStyleSheet = `:root{color-scheme:light dark;--background:#ffffff;--text:#1f2328;--muted:#5b6470;--accent:#0a5bd3;--rule:#dde2e8;--code-background:#f5f7f9;--code-text:#1f2328;--callout-background:#eef4fd;--callout-rule:#0a5bd3;--quote-rule:#c8d0d9;--tag-background:#eef1f4}
@media (prefers-color-scheme:dark){:root{--background:#0f1216;--text:#e4e7eb;--muted:#9aa4b0;--accent:#72b0ff;--rule:#2a3039;--code-background:#161b22;--code-text:#e6edf3;--callout-background:#14202f;--callout-rule:#72b0ff;--quote-rule:#3b434e;--tag-background:#1b2129}}
*,*::before,*::after{box-sizing:border-box}
html{-webkit-text-size-adjust:100%;text-size-adjust:100%}
body{margin:0;background:var(--background);color:var(--text);font-family:system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"Helvetica Neue",Arial,"Noto Sans",sans-serif;font-size:1.0625rem;line-height:1.7;-webkit-font-smoothing:antialiased;text-rendering:optimizeLegibility}
.page{max-width:68ch;margin:0 auto;padding:3.5rem 1.25rem 4rem}
.site-name{margin:0 0 2.25rem;font-size:.8125rem;font-weight:600;letter-spacing:.08em;text-transform:uppercase;color:var(--muted)}
.post-header{margin:0 0 2.5rem;padding:0 0 2rem;border-bottom:1px solid var(--rule)}
h1{margin:0;font-size:2.5rem;line-height:1.15;letter-spacing:-.02em;font-weight:750;text-wrap:balance}
.subtitle{margin:.875rem 0 0;font-size:1.25rem;line-height:1.5;color:var(--muted)}
.byline{margin:1.25rem 0 0;font-size:.9375rem;color:var(--muted)}
.tags{display:flex;flex-wrap:wrap;gap:.5rem;margin:1rem 0 0;padding:0;list-style:none}
.tags li{margin:0;padding:.125rem .625rem;border-radius:999px;background:var(--tag-background);font-size:.8125rem;color:var(--muted)}
h2{margin:2.75rem 0 1rem;font-size:1.5rem;line-height:1.3;letter-spacing:-.01em;font-weight:700;text-wrap:balance}
p{margin:0 0 1.25rem}
ul,ol{margin:0 0 1.25rem;padding-left:1.5rem}
li{margin:.375rem 0}
li::marker{color:var(--muted)}
a{color:var(--accent);text-decoration:underline;text-decoration-thickness:1px;text-underline-offset:.18em;overflow-wrap:anywhere}
a:hover{text-decoration-thickness:2px}
code{font-family:ui-monospace,SFMono-Regular,"SF Mono",Menlo,Consolas,"Liberation Mono",monospace;font-size:.875em;padding:.12em .36em;border-radius:5px;background:var(--code-background);color:var(--code-text);overflow-wrap:anywhere}
pre{margin:0 0 1.5rem;padding:1rem 1.125rem;overflow-x:auto;border:1px solid var(--rule);border-radius:8px;background:var(--code-background);line-height:1.55;tab-size:4;-moz-tab-size:4}
pre code{padding:0;border-radius:0;background:none;font-size:.875rem;white-space:pre;overflow-wrap:normal}
blockquote{margin:0 0 1.5rem;padding:.125rem 0 .125rem 1.25rem;border-left:3px solid var(--quote-rule);color:var(--muted);font-style:italic}
.callout{margin:0 0 1.5rem;padding:1rem 1.25rem;border-left:4px solid var(--callout-rule);border-radius:0 8px 8px 0;background:var(--callout-background)}
blockquote p:last-child,.callout p:last-child{margin-bottom:0}
.references{margin:3rem 0 0;padding:1.5rem 0 0;border-top:1px solid var(--rule);font-size:.9375rem}
.references h2{margin:0 0 .75rem;font-size:1.125rem}
.site-footer{margin:3.5rem 0 0;padding:1.5rem 0 0;border-top:1px solid var(--rule);font-size:.875rem;color:var(--muted)}
.site-footer p{margin:0 0 .5rem}
@media (max-width:40rem){body{font-size:1rem}.page{padding-top:2.25rem}h1{font-size:2rem}h2{font-size:1.3125rem}}
@media print{:root{--background:#ffffff;--text:#000000;--muted:#444444;--accent:#000000}.page{max-width:none;padding:0}pre{white-space:pre-wrap}}`

// blogDocumentTemplateText is the html/template source of the blog artifact.
const blogDocumentTemplateText = `{{define "label"}}{{range .}}{{if .IsCode}}<code>{{.Text}}</code>{{else}}{{.Text}}{{end}}{{end}}{{end}}
{{- define "inline"}}{{range .}}{{if .IsCode}}<code>{{.Text}}</code>{{else if .IsLink}}<a href="{{.URL}}" rel="noopener">{{template "label" .Label}}</a>{{else}}{{.Text}}{{end}}{{end}}{{end}}
{{- define "paragraphs"}}{{range .}}<p>{{template "inline" .}}</p>
{{end}}{{end}}
{{- define "block"}}
{{- if eq .Kind "bullets"}}{{template "paragraphs" .Paragraphs}}<ul>
{{range .Items}}<li>{{template "inline" .}}</li>
{{end}}</ul>
{{else if eq .Kind "code"}}{{template "paragraphs" .Paragraphs}}<pre><code{{if .Language}} class="language-{{.Language}}"{{end}}>{{.Code}}</code></pre>
{{else if eq .Kind "quote"}}<blockquote>
{{template "paragraphs" .Paragraphs}}</blockquote>
{{else if eq .Kind "callout"}}<aside class="callout">
{{template "paragraphs" .Paragraphs}}</aside>
{{else}}{{template "paragraphs" .Paragraphs}}{{end}}
{{- end}}
{{- define "document"}}<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="` + blogContentSecurityPolicy + `">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<meta name="color-scheme" content="light dark">
<title>{{.DocumentTitle}}</title>
{{if .Post.Summary}}<meta name="description" content="{{.Post.Summary}}">
{{end}}<style>
` + blogStyleSheet + `
</style>
</head>
<body>
<div class="page">
<header class="post-header">
{{if .SiteName}}<p class="site-name">{{.SiteName}}</p>
{{end}}<h1>{{.Post.Title}}</h1>
{{if .Post.Subtitle}}<p class="subtitle">{{.Post.Subtitle}}</p>
{{end}}{{if or .Author .DateText}}<p class="byline">{{if .Author}}By {{.Author}}{{end}}{{if and .Author .DateText}} · {{end}}{{if .DateText}}<time datetime="{{.DateISO}}">{{.DateText}}</time>{{end}}</p>
{{end}}{{if .Post.Tags}}<ul class="tags">
{{range .Post.Tags}}<li>{{.}}</li>
{{end}}</ul>
{{end}}</header>
<main>
<article>
{{range .Post.Sections}}<section>
{{if .Heading}}<h2>{{template "label" .Heading}}</h2>
{{end}}{{range .Blocks}}{{template "block" .}}{{end}}</section>
{{end}}</article>
{{if .Post.References}}<section class="references">
<h2>References</h2>
<ol>
{{range .Post.References}}<li><a href="{{.URL}}" rel="noopener">{{.Label}}</a></li>
{{end}}</ol>
</section>
{{end}}</main>
<footer class="site-footer">
{{if .SiteName}}<p>{{.SiteName}}</p>
{{end}}{{if .PublishedURL}}<p><a href="{{.PublishedURL}}" rel="noopener">Permanent link to this post</a></p>
{{end}}</footer>
</div>
</body>
</html>
{{end}}`

// blogDocumentView is the data of the blog artifact template.
type blogDocumentView struct {
	DocumentTitle string
	SiteName      string
	Author        string
	DateText      string
	DateISO       string
	PublishedURL  string
	Post          blogPostView
}

// RenderBlogHTML normalizes post and renders it as a complete, self-contained
// HTML5 document: an inline style sheet with light and dark color schemes, a
// Content-Security-Policy that forbids every external resource, a header
// with the site name, title, subtitle, "By {Author} · {Month D, YYYY}" byline
// from generatedAt in UTC, and tags, one article section per blog section,
// the references, and a footer. A zero generatedAt omits the date. All text is
// escaped by html/template; the only markup produced from post text is the
// inline markup described in the package documentation. It returns the error
// of NormalizeBlogPost when the post is invalid, and an error when the
// document would exceed 2 MiB.
func RenderBlogHTML(post model.BlogPost, presentation BlogPresentation, generatedAt time.Time) (string, error) {
	normalized, err := NormalizeBlogPost(post)
	if err != nil {
		return "", err
	}
	presentation = normalizedPresentation(presentation)
	view := blogDocumentView{
		DocumentTitle: normalized.Title,
		SiteName:      presentation.SiteName,
		Author:        presentation.Author,
		PublishedURL:  PublishedBlogURL(normalized, presentation),
		Post:          newBlogPostView(normalized),
	}
	if presentation.SiteName != "" {
		view.DocumentTitle = normalized.Title + " · " + presentation.SiteName
	}
	if !generatedAt.IsZero() {
		utc := generatedAt.UTC()
		view.DateText = utc.Format("January 2, 2006")
		view.DateISO = utc.Format("2006-01-02")
	}
	document, err := executeTemplate("blog", blogDocumentTemplateText, view)
	if err != nil {
		return "", err
	}
	if err := checkByteLimit("blog HTML document", document, maxBlogDocumentBytes); err != nil {
		return "", err
	}
	return document, nil
}

// executeTemplate parses templateText and executes its "document" template
// with data. The template is parsed on every call so no parsed or escaped
// template state is shared between calls.
func executeTemplate(name string, templateText string, data any) (string, error) {
	parsed, err := template.New(name).Parse(templateText)
	if err != nil {
		return "", err
	}
	var output strings.Builder
	if err := parsed.ExecuteTemplate(&output, "document", data); err != nil {
		return "", err
	}
	return output.String(), nil
}
