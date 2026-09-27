package render

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

// Size bounds for a normalized blog post. Characters are Unicode code points.
const (
	maxTitleCharacters          = 200
	maxSubtitleCharacters       = 400
	maxSummaryCharacters        = 1000
	maxTagCount                 = 20
	maxTagCharacters            = 60
	maxSectionCount             = 40
	maxBlocksPerSection         = 60
	maxItemsPerBulletsBlock     = 100
	maxHeadingCharacters        = 200
	maxReferenceCount           = 100
	maxReferenceLabelCharacters = 500
	maxTotalTextCharacters      = 200000
	maxWebURLBytes              = 2048
	maxCodeLanguageBytes        = 32
	maxSlugBytes                = 80
	minimumSlugWordCutBytes     = 40
	fallbackBlogSlug            = "tech-blog-post"
)

// NormalizeBlogPost validates a language-model blog draft and returns its
// canonical form.
//
// Every string is cleaned and trimmed. Title, subtitle, summary, tags,
// headings, bullet items, and reference labels become single-line text;
// block text keeps paragraph breaks; code keeps its indentation. Empty blocks,
// sections, items, tags, and references are dropped before any length limit
// is checked, leading "#" marks are removed from tags, duplicate tags
// (case-insensitively) and duplicate reference URLs are dropped, references
// keep only absolute http or https URLs, and an unlabeled reference is
// labeled with its URL. Block types
// are matched case-insensitively; see the package documentation for how
// unknown and incomplete blocks are handled. Code languages are reduced to
// lowercase [a-z0-9+#-]. The slug is reduced to lowercase [a-z0-9-] with
// single dashes and at most 80 bytes, derived from the title when the draft
// slug is blank or has no usable characters, and "tech-blog-post" when the
// title has none either.
//
// It returns an error when the title is blank or longer than 200 characters,
// when no section has a non-empty block, or when the post exceeds a size
// bound: 40 sections, 60 blocks per section, 100 items per bullets block, 20
// tags, 100 references, per-field length limits, or 200,000 characters of
// text in total. The input is never modified, and the result is idempotent.
func NormalizeBlogPost(post model.BlogPost) (model.BlogPost, error) {
	title := singleLineText(post.Title)
	if title == "" {
		return model.BlogPost{}, fmt.Errorf("blog post title is required")
	}
	if err := checkCharacterLimit("blog post title", title, maxTitleCharacters); err != nil {
		return model.BlogPost{}, err
	}
	subtitle := singleLineText(post.Subtitle)
	if err := checkCharacterLimit("blog post subtitle", subtitle, maxSubtitleCharacters); err != nil {
		return model.BlogPost{}, err
	}
	summary := singleLineText(post.Summary)
	if err := checkCharacterLimit("blog post summary", summary, maxSummaryCharacters); err != nil {
		return model.BlogPost{}, err
	}
	tags, err := normalizeTags(post.Tags)
	if err != nil {
		return model.BlogPost{}, err
	}
	sections, err := normalizeSections(post.Sections)
	if err != nil {
		return model.BlogPost{}, err
	}
	references, err := normalizeReferences(post.References)
	if err != nil {
		return model.BlogPost{}, err
	}
	normalized := model.BlogPost{
		Title:      title,
		Subtitle:   subtitle,
		Slug:       resolveBlogSlug(post.Slug, title),
		Summary:    summary,
		Tags:       tags,
		Sections:   sections,
		References: references,
	}
	if total := blogPostTextCharacters(normalized); total > maxTotalTextCharacters {
		return model.BlogPost{}, fmt.Errorf("blog post has %d characters of text; the limit is %d", total, maxTotalTextCharacters)
	}
	return normalized, nil
}

// checkCharacterLimit reports an error when value is longer than limit
// characters.
func checkCharacterLimit(field string, value string, limit int) error {
	if count := runeCount(value); count > limit {
		return fmt.Errorf("%s has %d characters; the limit is %d", field, count, limit)
	}
	return nil
}

// checkByteLimit reports an error when the rendered form of document is
// longer than limit bytes.
func checkByteLimit(document string, rendered string, limit int) error {
	if size := len(rendered); size > limit {
		return fmt.Errorf("%s renders to %d bytes; the limit is %d, so shorten the post or use less inline markup", document, size, limit)
	}
	return nil
}

// normalizeTags trims tags, removes every leading "#" mark together with any
// whitespace around the marks, drops blank and case-insensitively duplicate
// tags, and enforces the tag bounds. Marks and whitespace are stripped
// together so that a normalized tag never starts with either, which keeps
// normalization idempotent.
func normalizeTags(rawTags []string) ([]string, error) {
	tags := make([]string, 0, len(rawTags))
	seen := make(map[string]bool, len(rawTags))
	for _, rawTag := range rawTags {
		tag := strings.TrimLeftFunc(singleLineText(rawTag), isTagPrefixCharacter)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if seen[key] {
			continue
		}
		seen[key] = true
		if err := checkCharacterLimit(fmt.Sprintf("blog post tag %q", truncateForMessage(tag)), tag, maxTagCharacters); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	if len(tags) > maxTagCount {
		return nil, fmt.Errorf("blog post has %d tags; the limit is %d", len(tags), maxTagCount)
	}
	return tags, nil
}

// isTagPrefixCharacter reports whether character is stripped from the start
// of a tag: a "#" mark or whitespace.
func isTagPrefixCharacter(character rune) bool {
	return character == '#' || unicode.IsSpace(character)
}

// normalizeSections normalizes every section, drops sections without blocks,
// and requires at least one remaining section. A dropped section is never
// checked against the heading limit.
func normalizeSections(rawSections []model.BlogSection) ([]model.BlogSection, error) {
	sections := make([]model.BlogSection, 0, len(rawSections))
	for sectionIndex, rawSection := range rawSections {
		blocks := make([]model.BlogBlock, 0, len(rawSection.Blocks))
		for _, rawBlock := range rawSection.Blocks {
			block, kept, err := normalizeBlock(rawBlock)
			if err != nil {
				return nil, fmt.Errorf("blog post section %d: %w", sectionIndex+1, err)
			}
			if kept {
				blocks = append(blocks, block)
			}
		}
		if len(blocks) == 0 {
			continue
		}
		if len(blocks) > maxBlocksPerSection {
			return nil, fmt.Errorf("blog post section %d has %d blocks; the limit is %d", sectionIndex+1, len(blocks), maxBlocksPerSection)
		}
		heading := singleLineText(rawSection.Heading)
		if err := checkCharacterLimit(fmt.Sprintf("blog post section %d heading", sectionIndex+1), heading, maxHeadingCharacters); err != nil {
			return nil, err
		}
		sections = append(sections, model.BlogSection{Heading: heading, Blocks: blocks})
	}
	if len(sections) == 0 {
		return nil, fmt.Errorf("blog post needs at least one section with a non-empty block")
	}
	if len(sections) > maxSectionCount {
		return nil, fmt.Errorf("blog post has %d sections; the limit is %d", len(sections), maxSectionCount)
	}
	return sections, nil
}

// normalizeBlock returns the canonical form of one block and whether it is
// kept. Only the fields meaningful for the resulting type are retained.
func normalizeBlock(rawBlock model.BlogBlock) (model.BlogBlock, bool, error) {
	blockType := model.BlogBlockType(strings.ToLower(strings.TrimSpace(string(rawBlock.Type))))
	text := proseText(rawBlock.Text)
	switch blockType {
	case model.BlogBlockBullets:
		items := make([]string, 0, len(rawBlock.Items))
		for _, rawItem := range rawBlock.Items {
			if item := singleLineText(rawItem); item != "" {
				items = append(items, item)
			}
		}
		if len(items) == 0 {
			return paragraphBlock(text)
		}
		if len(items) > maxItemsPerBulletsBlock {
			return model.BlogBlock{}, false, fmt.Errorf("bullets block has %d items; the limit is %d", len(items), maxItemsPerBulletsBlock)
		}
		return model.BlogBlock{Type: model.BlogBlockBullets, Text: text, Items: items}, true, nil
	case model.BlogBlockCode:
		code := codeText(rawBlock.Code)
		if code == "" {
			return paragraphBlock(text)
		}
		return model.BlogBlock{
			Type:     model.BlogBlockCode,
			Text:     text,
			Items:    []string{},
			Language: sanitizeCodeLanguage(rawBlock.Language),
			Code:     code,
		}, true, nil
	case model.BlogBlockParagraph, model.BlogBlockQuote, model.BlogBlockCallout:
		if text == "" {
			return model.BlogBlock{}, false, nil
		}
		return model.BlogBlock{Type: blockType, Text: text, Items: []string{}}, true, nil
	default:
		return paragraphBlock(text)
	}
}

// paragraphBlock returns a paragraph block holding text, or no block when
// text is empty.
func paragraphBlock(text string) (model.BlogBlock, bool, error) {
	if text == "" {
		return model.BlogBlock{}, false, nil
	}
	return model.BlogBlock{Type: model.BlogBlockParagraph, Text: text, Items: []string{}}, true, nil
}

// normalizeReferences keeps references with allowed web URLs, drops duplicate
// URLs, labels unlabeled references with their URL, and enforces the
// reference bounds. A label equal to the URL is bounded by the URL limit
// rather than the label limit, so an unlabeled reference with a long URL is
// kept, and normalizing it again keeps it too.
func normalizeReferences(rawReferences []model.SourceReference) ([]model.SourceReference, error) {
	references := make([]model.SourceReference, 0, len(rawReferences))
	seen := make(map[string]bool, len(rawReferences))
	for _, rawReference := range rawReferences {
		link := strings.TrimSpace(rawReference.URL)
		if !isAllowedWebURL(link) || seen[link] {
			continue
		}
		seen[link] = true
		label := singleLineText(rawReference.Label)
		if label == "" {
			label = link
		}
		if label != link {
			if err := checkCharacterLimit(fmt.Sprintf("blog post reference %q label", truncateForMessage(link)), label, maxReferenceLabelCharacters); err != nil {
				return nil, err
			}
		}
		references = append(references, model.SourceReference{Label: label, URL: link})
	}
	if len(references) > maxReferenceCount {
		return nil, fmt.Errorf("blog post has %d references; the limit is %d", len(references), maxReferenceCount)
	}
	return references, nil
}

// blogPostTextCharacters counts every character of text a normalized post
// carries.
func blogPostTextCharacters(post model.BlogPost) int {
	total := runeCount(post.Title) + runeCount(post.Subtitle) + runeCount(post.Summary) + runeCount(post.Slug)
	for _, tag := range post.Tags {
		total += runeCount(tag)
	}
	for _, section := range post.Sections {
		total += runeCount(section.Heading)
		for _, block := range section.Blocks {
			total += runeCount(block.Text) + runeCount(block.Language) + runeCount(block.Code)
			for _, item := range block.Items {
				total += runeCount(item)
			}
		}
	}
	for _, reference := range post.References {
		total += runeCount(reference.Label) + runeCount(reference.URL)
	}
	return total
}

// resolveBlogSlug returns the sanitized draft slug, or the slug derived from
// title when the draft slug sanitizes to nothing, or fallbackBlogSlug.
func resolveBlogSlug(draftSlug string, title string) string {
	if slug := sanitizeSlug(draftSlug); slug != "" {
		return slug
	}
	if slug := sanitizeSlug(title); slug != "" {
		return slug
	}
	return fallbackBlogSlug
}

// sanitizeSlug lowercases ASCII letters, keeps ASCII letters and digits,
// removes apostrophes, turns every other run of characters into one dash,
// trims dashes, and shortens the result to maxSlugBytes, preferring to cut at
// a word boundary.
func sanitizeSlug(value string) string {
	var builder strings.Builder
	pendingDash := false
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
		case character >= 'A' && character <= 'Z':
			character += 'a' - 'A'
		case character == '\'' || character == 0x2019:
			continue
		default:
			pendingDash = builder.Len() > 0
			continue
		}
		if pendingDash {
			builder.WriteByte('-')
			pendingDash = false
		}
		builder.WriteRune(character)
	}
	slug := builder.String()
	if len(slug) <= maxSlugBytes {
		return slug
	}
	cut := slug[:maxSlugBytes]
	if slug[maxSlugBytes] != '-' {
		if lastDash := strings.LastIndexByte(cut, '-'); lastDash >= minimumSlugWordCutBytes {
			cut = cut[:lastDash]
		}
	}
	return strings.TrimRight(cut, "-")
}

// sanitizeCodeLanguage lowercases a code language name and keeps only
// [a-z0-9+#-], trimming dashes and bounding its length.
func sanitizeCodeLanguage(value string) string {
	var builder strings.Builder
	for _, character := range strings.ToLower(value) {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '+' || character == '#' || character == '-' {
			builder.WriteRune(character)
		}
	}
	language := builder.String()
	if len(language) > maxCodeLanguageBytes {
		language = language[:maxCodeLanguageBytes]
	}
	return strings.Trim(language, "-")
}

// isAllowedWebURL reports whether value is an absolute http or https URL of
// at most maxWebURLBytes printable ASCII bytes, without characters that are
// unsafe in URLs, user information, or an empty host.
func isAllowedWebURL(value string) bool {
	if value == "" || len(value) > maxWebURLBytes {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character <= ' ' || character >= 0x7f || strings.IndexByte("\"<>\\^`{|}", character) >= 0 {
			return false
		}
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return parsed.Opaque == "" && parsed.User == nil && parsed.Hostname() != ""
}

// truncateForMessage shortens untrusted text quoted in an error message.
func truncateForMessage(value string) string {
	const limit = 40
	if runeCount(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit]) + "..."
}
