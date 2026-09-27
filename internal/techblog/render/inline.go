package render

import "strings"

// inlineSegmentKind is the kind of one inline markup segment.
type inlineSegmentKind int

const (
	// inlineText is literal text.
	inlineText inlineSegmentKind = iota
	// inlineCode is a code span.
	inlineCode
	// inlineLink is a link to an allowed web URL.
	inlineLink
)

// inlineSegment is one parsed piece of inline markup. Exported field and
// method names make the segment usable from html/template.
type inlineSegment struct {
	// Kind selects how the segment renders.
	Kind inlineSegmentKind
	// Text is the literal text or the code span content.
	Text string
	// URL is the link destination; it is set only for links and always
	// satisfies isAllowedWebURL.
	URL string
	// Label is the link label; it holds only text and code segments.
	Label []inlineSegment
}

// IsCode reports whether the segment is a code span.
func (segment inlineSegment) IsCode() bool { return segment.Kind == inlineCode }

// IsLink reports whether the segment is a link.
func (segment inlineSegment) IsLink() bool { return segment.Kind == inlineLink }

// inlineStructure records, for one text, which delimiters pair up. All maps
// are keyed and valued by byte offsets into the text.
type inlineStructure struct {
	// codeSpanClosers maps the start of an opening backtick run to the start
	// of its closing run of equal length.
	codeSpanClosers map[int]int
	// bracketPartners maps a "[" outside code spans to its matching "]".
	bracketPartners map[int]int
	// parenthesisPartners maps a "(" to its matching ")" when no character
	// that may not appear in a link destination lies between them.
	parenthesisPartners map[int]int
}

// parseInline splits text into literal text, code spans, and, when
// allowLinks is set, links. It runs in time linear in len(text).
//
// Code spans follow CommonMark: a backtick run opens a span that the next run
// of the same length closes, the content has line feeds turned into spaces,
// and one space is stripped from each end when both ends have one and the
// content is not all spaces. A run with no closer is literal.
//
// A link is "[" label "]" "(" destination ")", where the brackets match with
// nesting outside code spans and the parentheses match with nesting inside a
// destination free of whitespace, control, non-ASCII, and backtick bytes. A
// link whose destination is not an allowed web URL renders as its literal
// source text. A blank label is replaced by the URL. Labels hold only text and
// code spans.
func parseInline(text string, allowLinks bool) []inlineSegment {
	if text == "" {
		return nil
	}
	if !strings.ContainsAny(text, "`[") {
		return []inlineSegment{{Kind: inlineText, Text: text}}
	}
	structure := scanInlineStructure(text)
	var segments []inlineSegment
	var literal strings.Builder
	flushLiteral := func() {
		if literal.Len() > 0 {
			segments = append(segments, inlineSegment{Kind: inlineText, Text: literal.String()})
			literal.Reset()
		}
	}
	for index := 0; index < len(text); {
		character := text[index]
		if character == '`' {
			runLength := backtickRunLength(text, index)
			if closer, found := structure.codeSpanClosers[index]; found {
				flushLiteral()
				segments = append(segments, inlineSegment{Kind: inlineCode, Text: codeSpanContent(text[index+runLength : closer])})
				index = closer + runLength
				continue
			}
			literal.WriteString(text[index : index+runLength])
			index += runLength
			continue
		}
		if character == '[' && allowLinks {
			if link, end, matched := matchLink(text, index, structure); matched {
				if link.Kind == inlineLink {
					flushLiteral()
					segments = append(segments, link)
				} else {
					literal.WriteString(text[index:end])
				}
				index = end
				continue
			}
		}
		literal.WriteByte(character)
		index++
	}
	flushLiteral()
	return segments
}

// matchLink tries to read a link starting at the "[" at offset start. When
// the text has link syntax there, matched is true and end is the offset just
// past the closing ")"; the segment is a link when the destination is an
// allowed web URL and a literal text segment otherwise.
func matchLink(text string, start int, structure inlineStructure) (inlineSegment, int, bool) {
	labelEnd, found := structure.bracketPartners[start]
	if !found || labelEnd+1 >= len(text) || text[labelEnd+1] != '(' {
		return inlineSegment{}, 0, false
	}
	destinationStart := labelEnd + 2
	destinationEnd, found := structure.parenthesisPartners[labelEnd+1]
	if !found || destinationEnd-destinationStart > maxWebURLBytes {
		return inlineSegment{}, 0, false
	}
	end := destinationEnd + 1
	destination := text[destinationStart:destinationEnd]
	if !isAllowedWebURL(destination) {
		return inlineSegment{Kind: inlineText, Text: text[start:end]}, end, true
	}
	labelText := text[start+1 : labelEnd]
	label := parseInline(labelText, false)
	if strings.TrimSpace(labelText) == "" {
		label = []inlineSegment{{Kind: inlineText, Text: destination}}
	}
	return inlineSegment{Kind: inlineLink, URL: destination, Label: label}, end, true
}

// scanInlineStructure pairs the delimiters of text in two linear passes.
func scanInlineStructure(text string) inlineStructure {
	structure := inlineStructure{
		codeSpanClosers:     map[int]int{},
		bracketPartners:     map[int]int{},
		parenthesisPartners: map[int]int{},
	}

	// Find every maximal backtick run and, for each, the next run of the same
	// length.
	type backtickRun struct{ start, length int }
	var runs []backtickRun
	runIndexByStart := map[int]int{}
	for index := 0; index < len(text); {
		if text[index] != '`' {
			index++
			continue
		}
		length := backtickRunLength(text, index)
		runIndexByStart[index] = len(runs)
		runs = append(runs, backtickRun{start: index, length: length})
		index += length
	}
	nextRunOfSameLength := make([]int, len(runs))
	latestRunByLength := map[int]int{}
	for runIndex := len(runs) - 1; runIndex >= 0; runIndex-- {
		nextRunOfSameLength[runIndex] = -1
		if next, found := latestRunByLength[runs[runIndex].length]; found {
			nextRunOfSameLength[runIndex] = next
		}
		latestRunByLength[runs[runIndex].length] = runIndex
	}

	// Resolve code spans left to right and match brackets outside them.
	var openBrackets []int
	for index := 0; index < len(text); {
		switch text[index] {
		case '`':
			run := runs[runIndexByStart[index]]
			if next := nextRunOfSameLength[runIndexByStart[index]]; next >= 0 {
				structure.codeSpanClosers[index] = runs[next].start
				index = runs[next].start + run.length
			} else {
				index += run.length
			}
			continue
		case '[':
			openBrackets = append(openBrackets, index)
		case ']':
			if count := len(openBrackets); count > 0 {
				structure.bracketPartners[openBrackets[count-1]] = index
				openBrackets = openBrackets[:count-1]
			}
		}
		index++
	}

	// Match parentheses that could delimit a link destination.
	var openParentheses []int
	for index := 0; index < len(text); index++ {
		character := text[index]
		switch {
		case character == '(':
			openParentheses = append(openParentheses, index)
		case character == ')':
			if count := len(openParentheses); count > 0 {
				structure.parenthesisPartners[openParentheses[count-1]] = index
				openParentheses = openParentheses[:count-1]
			}
		case character <= ' ' || character >= 0x7f || character == '`':
			openParentheses = openParentheses[:0]
		}
	}
	return structure
}

// backtickRunLength returns the length of the backtick run starting at start.
func backtickRunLength(text string, start int) int {
	end := start
	for end < len(text) && text[end] == '`' {
		end++
	}
	return end - start
}

// codeSpanContent applies the CommonMark code span content rules.
func codeSpanContent(content string) string {
	content = strings.ReplaceAll(content, "\n", " ")
	if len(content) >= 2 && content[0] == ' ' && content[len(content)-1] == ' ' && strings.Trim(content, " ") != "" {
		content = content[1 : len(content)-1]
	}
	return content
}

// plainInlineText renders segments as plain text: code spans keep their
// backticks and links read "label (url)", or just the URL when the label is
// the URL.
func plainInlineText(segments []inlineSegment) string {
	var builder strings.Builder
	for _, segment := range segments {
		switch segment.Kind {
		case inlineCode:
			builder.WriteString("`" + segment.Text + "`")
		case inlineLink:
			label := plainInlineText(segment.Label)
			if label == segment.URL {
				builder.WriteString(segment.URL)
			} else {
				builder.WriteString(label + " (" + segment.URL + ")")
			}
		default:
			builder.WriteString(segment.Text)
		}
	}
	return builder.String()
}
