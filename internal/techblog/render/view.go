package render

import (
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

// blogPostView is a normalized blog post with its inline markup parsed, ready
// for the HTML templates and the plain-text writer.
type blogPostView struct {
	Title      string
	Subtitle   string
	Summary    string
	Tags       []string
	Sections   []blogSectionView
	References []model.SourceReference
}

// blogSectionView is one section of a blogPostView.
type blogSectionView struct {
	// Heading holds text and code segments; it is empty for an unheaded
	// section.
	Heading []inlineSegment
	Blocks  []blogBlockView
}

// blogBlockView is one block of a blogSectionView.
type blogBlockView struct {
	// Kind is one of the model.BlogBlock* values.
	Kind string
	// Paragraphs is the block text split into paragraphs: the body of a
	// paragraph, quote, or callout, or the lead-in of bullets and code.
	Paragraphs [][]inlineSegment
	// Items holds the bullet items.
	Items [][]inlineSegment
	// Language is the sanitized code language, possibly empty.
	Language string
	// Code is the code listing.
	Code string
}

// newBlogPostView parses the inline markup of a normalized post.
func newBlogPostView(post model.BlogPost) blogPostView {
	view := blogPostView{
		Title:      post.Title,
		Subtitle:   post.Subtitle,
		Summary:    post.Summary,
		Tags:       post.Tags,
		References: post.References,
	}
	for _, section := range post.Sections {
		sectionView := blogSectionView{Heading: parseInline(section.Heading, false)}
		for _, block := range section.Blocks {
			blockView := blogBlockView{
				Kind:       string(block.Type),
				Paragraphs: parseProseParagraphs(block.Text),
				Language:   block.Language,
				Code:       block.Code,
			}
			for _, item := range block.Items {
				blockView.Items = append(blockView.Items, parseInline(item, true))
			}
			sectionView.Blocks = append(sectionView.Blocks, blockView)
		}
		view.Sections = append(view.Sections, sectionView)
	}
	return view
}

// parseProseParagraphs splits prose text into paragraphs and parses the
// inline markup of each, links allowed.
func parseProseParagraphs(text string) [][]inlineSegment {
	var paragraphs [][]inlineSegment
	for _, paragraph := range proseParagraphs(text) {
		paragraphs = append(paragraphs, parseInline(paragraph, true))
	}
	return paragraphs
}
