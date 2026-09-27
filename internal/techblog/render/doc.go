// Package render turns a language-model blog draft into the deliverables of
// the tech blog newsletter Process: a normalized model.BlogPost, a
// self-contained HTML5 blog artifact, and a newsletter email with HTML and
// plain-text bodies.
//
// Every string handled here is untrusted: blog and newsletter copy comes from
// a language model that read Slack and GitHub content. The package therefore
// normalizes before it renders, renders through html/template so every piece
// of model text is contextually escaped, and admits only two kinds of inline
// markup. The package performs no I/O and reads no clock or randomness, so
// identical input always produces byte-identical output and a retried Dex
// Step recomputes exactly the same artifact.
//
// # Normalization
//
// NormalizeBlogPost is the single gate for blog content. It strips control
// characters (keeping tab and line feed), converts CR and CRLF to LF, removes
// Unicode bidirectional embedding, override, and isolate controls, trims every
// string, drops empty blocks, sections, items, tags, and references, and
// rejects posts that exceed the size bounds. Normalization is idempotent:
// normalizing a normalized post returns it unchanged. RenderBlogHTML and
// RenderNewsletter normalize their input themselves, so callers cannot render
// an unnormalized post by accident.
//
// Block types are matched case-insensitively against the model.BlogBlock*
// constants. An unknown type becomes a paragraph when it has text and is
// dropped otherwise. A bullets block without items and a code block without
// code are handled the same way. Bullets and code blocks keep their Text as an
// optional lead-in paragraph rendered before the list or listing.
//
// # Inline markup
//
// Block text, bullet items, newsletter intro, highlight text, closing, and
// footer support exactly two inline forms:
//
//   - `code` spans, delimited by equal-length backtick runs as in CommonMark,
//     so a span opened by two backticks may contain a single backtick;
//   - [label](url) links, where url is an absolute http or https URL made of
//     printable ASCII without user information. The label may contain code
//     spans but never another link.
//
// Section headings and highlight titles support code spans only. Titles,
// subtitles, tags, and reference labels are plain text. Unbalanced markup
// renders literally, and a well-formed link whose destination is not an
// allowed URL (javascript:, data:, vbscript:, relative, scheme-relative, and
// so on) renders as its literal source text. There are no escapes, no
// emphasis, no raw HTML, and no bare-URL autolinking.
//
// # Output
//
// The blog document references no external resource of any kind: its only
// styles are an inline style element, and a restrictive Content-Security-
// Policy forbids everything except inline styles and data: images. The
// newsletter HTML is a table-based 600 pixel layout with inline style
// attributes only, wrapped in an Outlook-only conditional table because
// Outlook for Windows ignores max-width, and its plain-text body carries the
// same content with links written as "label (url)". Rendered documents are
// bounded in bytes as well as in text, because escaping and per-element
// inline styles can make markup-dense copy render many times larger than its
// text.
package render
