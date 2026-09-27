package prompts

import (
	"encoding/json"
	"strings"
)

// Delimited prompt section tags. Each tag appears exactly once as an opening
// line and once as a closing line in a prompt, and nowhere else.
const (
	sectionRequest           = "request"
	sectionRepositoryCatalog = "repository-catalog"
	sectionResearchRequest   = "research-request"
	sectionEvidence          = "evidence"
	sectionRepositoryDigests = "repository-digests"
	sectionPublication       = "publication"
	sectionResearchBrief     = "research-brief"
	sectionPreviousDraft     = "previous-draft"
	sectionEditorFeedback    = "editor-feedback"
	sectionBlogPost          = "blog-post"
)

// encodeUntrustedJSON encodes a section body as compact JSON. encoding/json
// escapes <, >, and & inside strings (HTML escaping is on for json.Marshal)
// and escapes every control character, so the result never contains a raw
// angle bracket or line break and cannot close or open a section. Struct
// fields keep declaration order and map keys are sorted, so the encoding is
// deterministic. Only types that cannot fail to encode are passed here;
// "null" is the defensive result if encoding ever fails.
func encodeUntrustedJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "null"
	}
	return string(encoded)
}

// jsonEscapedCharacter returns the escape sequence the section encoder uses
// for one character, such as the six characters for '<'.
func jsonEscapedCharacter(character rune) string {
	return strings.Trim(encodeUntrustedJSON(string(character)), `"`)
}

// writeDelimitedSection writes one section whose body is value encoded as
// untrusted JSON.
func writeDelimitedSection(prompt *strings.Builder, tag string, value any) {
	writeEncodedSection(prompt, tag, encodeUntrustedJSON(value))
}

// writeEncodedSection writes one section around a body that was already
// produced by encodeUntrustedJSON.
func writeEncodedSection(prompt *strings.Builder, tag string, encodedJSON string) {
	prompt.WriteString("<")
	prompt.WriteString(tag)
	prompt.WriteString(">\n")
	prompt.WriteString(encodedJSON)
	prompt.WriteString("\n</")
	prompt.WriteString(tag)
	prompt.WriteString(">\n\n")
}

// writeParagraph writes trusted prompt prose followed by a blank line.
func writeParagraph(prompt *strings.Builder, text string) {
	prompt.WriteString(text)
	prompt.WriteString("\n\n")
}

// writeBulletList writes a trusted heading and one "- " line per item.
func writeBulletList(prompt *strings.Builder, heading string, items []string) {
	prompt.WriteString(heading)
	for _, item := range items {
		prompt.WriteString("\n- ")
		prompt.WriteString(item)
	}
	prompt.WriteString("\n\n")
}

// finishPrompt returns the prompt without trailing blank lines.
func finishPrompt(prompt *strings.Builder) string {
	return strings.TrimRight(prompt.String(), "\n")
}

// researchFocusDocument is the research-request section body: what the
// requester asked for, as interpreted from untrusted text.
type researchFocusDocument struct {
	Repository      string   `json:"repository,omitempty"`
	Topic           string   `json:"topic"`
	Instructions    string   `json:"instructions"`
	Audience        string   `json:"audience,omitempty"`
	PathHints       []string `json:"pathHints,omitempty"`
	SelectionReason string   `json:"selectionReason,omitempty"`
}
