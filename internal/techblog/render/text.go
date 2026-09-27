package render

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	nextLine           rune = 0x0085
	lineSeparator      rune = 0x2028
	paragraphSeparator rune = 0x2029
	byteOrderMark      rune = 0xFEFF
)

// cleanText makes untrusted text safe to store and render: invalid UTF-8
// bytes become U+FFFD, CR and CRLF become LF, U+2028 and U+2029 become LF,
// and every other control character except tab is removed together with
// bidirectional embedding, override, and isolate controls and the byte order
// mark.
func cleanText(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	previousWasCarriageReturn := false
	for _, character := range value {
		if previousWasCarriageReturn {
			previousWasCarriageReturn = false
			if character == '\n' {
				continue
			}
		}
		switch {
		case character == '\r':
			builder.WriteByte('\n')
			previousWasCarriageReturn = true
		case character == '\n' || character == '\t':
			builder.WriteRune(character)
		case character == lineSeparator || character == paragraphSeparator:
			builder.WriteByte('\n')
		case unicode.IsControl(character), isBidirectionalControl(character), character == byteOrderMark:
			// Dropped: invisible characters that can reorder or hide text.
		default:
			builder.WriteRune(character)
		}
	}
	return builder.String()
}

// isBidirectionalControl reports whether character is a Unicode bidirectional
// embedding, override, or isolate control (the "Trojan Source" characters).
func isBidirectionalControl(character rune) bool {
	return (character >= 0x202A && character <= 0x202E) || (character >= 0x2066 && character <= 0x2069)
}

// singleLineText cleans value and collapses every run of whitespace, line
// breaks included, into one space, trimming both ends.
func singleLineText(value string) string {
	return strings.Join(strings.Fields(cleanText(value)), " ")
}

// proseText cleans value, trims every line, collapses runs of blank lines into
// one blank line, and trims leading and trailing blank lines. Blank lines
// separate paragraphs; single line feeds stay inside a paragraph.
func proseText(value string) string {
	lines := strings.Split(cleanText(value), "\n")
	kept := make([]string, 0, len(lines))
	previousBlank := true
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if !previousBlank {
				kept = append(kept, "")
			}
			previousBlank = true
			continue
		}
		kept = append(kept, line)
		previousBlank = false
	}
	for len(kept) > 0 && kept[len(kept)-1] == "" {
		kept = kept[:len(kept)-1]
	}
	return strings.Join(kept, "\n")
}

// proseParagraphs splits text produced by proseText into its paragraphs.
func proseParagraphs(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n\n")
}

// codeText cleans a code listing without disturbing its indentation: trailing
// whitespace is removed from every line, and leading and trailing blank lines
// are dropped.
func codeText(value string) string {
	lines := strings.Split(cleanText(value), "\n")
	for index, line := range lines {
		lines[index] = strings.TrimRightFunc(line, unicode.IsSpace)
	}
	start := 0
	for start < len(lines) && lines[start] == "" {
		start++
	}
	end := len(lines)
	for end > start && lines[end-1] == "" {
		end--
	}
	return strings.Join(lines[start:end], "\n")
}

// runeCount returns the number of characters in value.
func runeCount(value string) int {
	return utf8.RuneCountInString(value)
}
