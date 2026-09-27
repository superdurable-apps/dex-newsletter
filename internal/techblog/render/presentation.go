package render

import (
	"net/url"
	"strings"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/config"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

// BlogPresentation is the site-level presentation shared by the blog artifact
// and the newsletter email.
type BlogPresentation struct {
	// SiteName is the blog name shown above the post title. It is plain text.
	SiteName string
	// Author is the byline author. A blank Author omits "By ..." from the
	// byline. It is plain text.
	Author string
	// PublicBaseURL is the absolute http or https URL under which the blog
	// artifact is published. A blank value means the post has no public URL.
	PublicBaseURL string
}

// BlogPresentationFromConfiguration returns the presentation named by the
// blog configuration, with every field trimmed.
func BlogPresentationFromConfiguration(configuration config.BlogConfiguration) BlogPresentation {
	return BlogPresentation{
		SiteName:      strings.TrimSpace(configuration.SiteName),
		Author:        strings.TrimSpace(configuration.Author),
		PublicBaseURL: strings.TrimSpace(configuration.PublicBaseURL),
	}
}

// PublishedBlogURL returns the public URL of the post: PublicBaseURL without
// trailing slashes, a slash, the post slug, and ".html". It returns "" when
// PublicBaseURL is blank, and also when it is not an absolute http or https
// URL of printable ASCII without user information, query, or fragment, so an
// unsafe or malformed base can never become a link.
//
// The slug is the one NormalizeBlogPost would assign, so the URL is safe and
// stable even for a post that has not been normalized.
func PublishedBlogURL(post model.BlogPost, presentation BlogPresentation) string {
	base := strings.TrimRight(strings.TrimSpace(presentation.PublicBaseURL), "/")
	if base == "" || !isAllowedWebURL(base) {
		return ""
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(base, "#") {
		return ""
	}
	return base + "/" + BlogArtifactFileName(post)
}

// BlogArtifactFileName returns the file name of the blog artifact: the post
// slug followed by ".html". The slug is the one NormalizeBlogPost would
// assign, so the name is always lowercase [a-z0-9-] and can never traverse
// directories, even for a post that has not been normalized.
func BlogArtifactFileName(post model.BlogPost) string {
	return resolveBlogSlug(post.Slug, singleLineText(post.Title)) + ".html"
}

// normalizedPresentation returns presentation with its display fields reduced
// to single-line clean text and its base URL trimmed.
func normalizedPresentation(presentation BlogPresentation) BlogPresentation {
	return BlogPresentation{
		SiteName:      singleLineText(presentation.SiteName),
		Author:        singleLineText(presentation.Author),
		PublicBaseURL: strings.TrimSpace(presentation.PublicBaseURL),
	}
}
