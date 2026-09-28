package notices

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// maximumMessageRunes bounds every notice. Slack truncates long text and
	// recommends keeping messages well under 4,000 characters.
	maximumMessageRunes = 3500
	// minimumInputCapBytes and inputCapBytesPerOutputRune size the cap on one
	// untrusted value before it is scanned (see inputCapBytes), so the
	// sanitizing and redaction passes stay cheap for any input.
	minimumInputCapBytes       = 4096
	inputCapBytesPerOutputRune = 8
	// maximumLinkTargetRunes bounds the escaped URL inside one Slack link.
	maximumLinkTargetRunes = 600
	// maximumLinkedFlowIDBytes is the longest Flow ID placed in a Dex Web
	// link. Longer IDs are shown, truncated, as code instead of a link that
	// could never resolve.
	maximumLinkedFlowIDBytes = 200
	// maximumQuotedLines bounds the lines of one quoted block.
	maximumQuotedLines = 12

	// ellipsis marks a truncated dynamic value.
	ellipsis = "…"
	// messageTruncationMarker ends a message cut by boundMessage. Budgets keep
	// every notice below maximumMessageRunes, so it never appears in practice.
	messageTruncationMarker = "… (message shortened)"
	// zeroWidthSpace (U+200B) follows every '@' in dynamic text so Slack can
	// never resolve it as a user, user-group, or @channel/@here/@everyone
	// mention, even when the message is posted with link_names enabled.
	zeroWidthSpace = "\xe2\x80\x8b"
	// codeSpanBacktickReplacement stands in for a backtick inside a code span,
	// because Slack mrkdwn has no way to escape one.
	codeSpanBacktickReplacement = "'"
	// linkLabelPipeReplacement (U+00A6 BROKEN BAR) stands in for '|' inside a
	// link label, because Slack mrkdwn has no way to escape one.
	linkLabelPipeReplacement = "¦"
	// unsafeLinkTargetCharacters may not appear in a linked URL: they would
	// end the link early, split it, or are not valid unescaped in a URL.
	unsafeLinkTargetCharacters = "<>|\"`\\^{}"
)

// textMode selects the Slack mrkdwn context one escaped value is written into.
type textMode int

const (
	// plainTextMode is ordinary message text.
	plainTextMode textMode = iota
	// codeSpanMode is text between single backticks.
	codeSpanMode
	// linkLabelMode is the label part of a <url|label> link.
	linkLabelMode
)

// slackMessage accumulates the lines of one notice.
type slackMessage struct {
	lines []string
}

// appendLine adds one line, skipping empty lines.
func (message *slackMessage) appendLine(line string) {
	if line != "" {
		message.lines = append(message.lines, line)
	}
}

// appendField adds a "*Label:* value" line when value is not empty. The label
// must be static text.
func (message *slackMessage) appendField(label string, value string) {
	if value != "" {
		message.appendLine("*" + label + ":* " + value)
	}
}

// appendQuotedField adds a "*Label:*" line followed by a quoted block when the
// block is not empty. The label must be static text.
func (message *slackMessage) appendQuotedField(label string, quotedBlock string) {
	if quotedBlock != "" {
		message.appendLine("*" + label + ":*")
		message.appendLine(quotedBlock)
	}
}

// render joins the lines into the final message.
func (message *slackMessage) render() string {
	return boundMessage(strings.Join(message.lines, "\n"))
}

// boundMessage is a last-resort guard that keeps a message within
// maximumMessageRunes. It cuts only at line boundaries so it never splits an
// escape sequence, a code span, or a link, and appends
// messageTruncationMarker.
func boundMessage(message string) string {
	if utf8.RuneCountInString(message) <= maximumMessageRunes {
		return message
	}
	budget := maximumMessageRunes - utf8.RuneCountInString("\n"+messageTruncationMarker)
	cut := -1
	runesBefore := 0
	for index, character := range message {
		if runesBefore > budget {
			break
		}
		if character == '\n' {
			cut = index
		}
		runesBefore++
	}
	if cut <= 0 {
		return messageTruncationMarker
	}
	return message[:cut] + "\n" + messageTruncationMarker
}

// singleLineText returns an untrusted value as Slack-safe text on one line of
// at most limit runes after escaping.
func singleLineText(value string, limit int) string {
	text, truncated := normalizedSingleLine(value, limit)
	if text == "" && !truncated {
		return ""
	}
	return escapeBounded(text, limit, plainTextMode, truncated)
}

// linkLabelText returns an untrusted value as a Slack-safe link label of at
// most limit runes after escaping, or "" when the value is blank.
func linkLabelText(value string, limit int) string {
	text, truncated := normalizedSingleLine(value, limit)
	if text == "" {
		return ""
	}
	return escapeBounded(text, limit, linkLabelMode, truncated)
}

// codeSpan returns an untrusted value as a Slack code span of at most limit
// runes, backticks included, or "" when the value is blank.
func codeSpan(value string, limit int) string {
	text, truncated := normalizedSingleLine(value, limit)
	return codeSpanFromNormalizedText(text, truncated, limit)
}

// codeSpanFromNormalizedText wraps already normalized single-line text in a
// code span of at most limit runes, backticks included.
func codeSpanFromNormalizedText(text string, truncated bool, limit int) string {
	if text == "" || limit < 3 {
		return ""
	}
	return "`" + escapeBounded(text, limit-2, codeSpanMode, truncated) + "`"
}

// quotedText returns an untrusted, possibly multi-line value as a Slack quoted
// block. The escaped text is at most limit runes; each of the at most
// maximumQuotedLines lines adds its two-rune "> " prefix. It returns "" when
// the value is blank.
func quotedText(value string, limit int) string {
	lines, truncated := normalizedLines(value, limit)
	if len(lines) > maximumQuotedLines {
		lines = lines[:maximumQuotedLines]
		truncated = true
		for len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	}
	if len(lines) == 0 && !truncated {
		return ""
	}
	escaped := escapeBounded(strings.Join(lines, "\n"), limit, plainTextMode, truncated)
	if escaped == "" {
		return ""
	}
	quotedLines := strings.Split(escaped, "\n")
	for index, line := range quotedLines {
		if line == "" {
			quotedLines[index] = ">"
		} else {
			quotedLines[index] = "> " + line
		}
	}
	return strings.Join(quotedLines, "\n")
}

// normalizedSingleLine cleans an untrusted value bound for at most limit
// output runes and collapses every run of whitespace, line breaks included,
// into one space. It reports whether input was dropped by the input cap.
func normalizedSingleLine(value string, limit int) (string, bool) {
	text, truncated := cleanedText(value, limit)
	return strings.Join(strings.Fields(text), " "), truncated
}

// normalizedLines cleans an untrusted value bound for at most limit output
// runes into lines with collapsed inner whitespace, at most one blank line in
// a row, and no leading or trailing blank lines. It reports whether input was
// dropped by the input cap.
func normalizedLines(value string, limit int) ([]string, bool) {
	text, truncated := cleanedText(value, limit)
	var lines []string
	previousLineBlank := true
	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.Join(strings.Fields(rawLine), " ")
		if line == "" {
			if !previousLineBlank {
				lines = append(lines, "")
			}
			previousLineBlank = true
			continue
		}
		lines = append(lines, line)
		previousLineBlank = false
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, truncated
}

// cleanedText caps, sanitizes, and redacts one untrusted value bound for at
// most limit output runes. The result holds only '\n' line breaks, ' ' spaces,
// and visible characters. It reports whether input was dropped by the cap.
func cleanedText(value string, limit int) (string, bool) {
	capped, truncated := cappedInput(value, inputCapBytes(limit))
	return redactSensitiveText(sanitizedText(capped)), truncated
}

// inputCapBytes is how much of an untrusted value is scanned for at most limit
// output runes: enough for the output to fill its limit unless the value is
// mostly whitespace, invisible characters, or redacted text, in which case the
// output is merely shorter and still marked truncated.
func inputCapBytes(limit int) int {
	return max(minimumInputCapBytes, limit*inputCapBytesPerOutputRune)
}

// cappedInput keeps at most capBytes of value after skipping the leading
// whitespace, control, and format characters that normalization drops anyway,
// so padding never uses up the cap. A longer value is cut back to its last
// token boundary (see isTokenBoundaryRune) so no partial token, such as the
// first half of an email address or key, survives to evade redaction; a
// prefix without any boundary is one token and is dropped entirely.
func cappedInput(value string, capBytes int) (string, bool) {
	value = strings.TrimLeftFunc(value, isDroppedLeadingRune)
	if len(value) <= capBytes {
		return value, false
	}
	end := max(capBytes, 0)
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	prefix := value[:end]
	cut := strings.LastIndexFunc(prefix, isTokenBoundaryRune)
	if cut < 0 {
		return "", true
	}
	return withoutOpenURLAuthority(prefix[:cut]), true
}

// isDroppedLeadingRune reports whether character contributes nothing at the
// start of a value: whitespace, which normalization trims, and control and
// format characters, which sanitizedText drops.
func isDroppedLeadingRune(character rune) bool {
	return unicode.IsSpace(character) || unicode.Is(unicode.Cc, character) || unicode.Is(unicode.Cf, character)
}

// isTokenBoundaryRune reports whether character can never be part of an email
// address or secret-like token the redaction patterns look for: whitespace, or
// non-ASCII punctuation or symbols such as CJK punctuation ("，", "。"),
// fullwidth forms, emoji, and U+FFFD for invalid UTF-8. Letters, digits,
// marks, ASCII punctuation, and the control and format characters that
// sanitizedText drops (joining their neighbors) are not boundaries.
func isTokenBoundaryRune(character rune) bool {
	return unicode.IsSpace(character) || (character >= utf8.RuneSelf && (unicode.IsPunct(character) || unicode.IsSymbol(character)))
}

// withoutOpenURLAuthority drops a trailing "://" and everything after it when
// the URL authority is still open, as in "see https://user:pa", because the
// URL credential pattern recognizes a login only by the '@' that ends it.
func withoutOpenURLAuthority(text string) string {
	schemeSeparator := strings.LastIndex(text, "://")
	if schemeSeparator < 0 || strings.IndexFunc(text[schemeSeparator+len("://"):], endsURLAuthority) >= 0 {
		return text
	}
	return text[:schemeSeparator]
}

// endsURLAuthority reports whether character ends the authority of a URL.
func endsURLAuthority(character rune) bool {
	return unicode.IsSpace(character) || strings.ContainsRune("/?#@", character)
}

// showsNoContent reports whether escaped markup, possibly a quoted block,
// shows nothing but quote markers and a truncation ellipsis, so the notice
// should use its static fallback wording instead.
func showsNoContent(markup string) bool {
	content := strings.TrimLeft(markup, "> ")
	return content == "" || content == ellipsis
}

// sanitizedText replaces invalid UTF-8, turns every line break into '\n' and
// every other whitespace character into ' ', and drops control and format
// characters such as NUL, zero-width characters, and bidirectional overrides.
func sanitizedText(value string) string {
	value = strings.ToValidUTF8(value, string(utf8.RuneError))
	value = strings.ReplaceAll(value, "\r\n", "\n")
	var builder strings.Builder
	builder.Grow(len(value))
	for _, character := range value {
		switch {
		case isLineBreak(character):
			builder.WriteByte('\n')
		case unicode.IsSpace(character):
			builder.WriteByte(' ')
		case unicode.Is(unicode.Cc, character), unicode.Is(unicode.Cf, character):
		default:
			builder.WriteRune(character)
		}
	}
	return builder.String()
}

// isLineBreak reports whether character ends a line: LF, CR, VT, FF, NEL
// (U+0085), LINE SEPARATOR (U+2028), or PARAGRAPH SEPARATOR (U+2029).
func isLineBreak(character rune) bool {
	switch character {
	case '\n', '\r', '\v', '\f', 0x85, 0x2028, 0x2029:
		return true
	}
	return false
}

// escapeBounded escapes normalized text for one Slack mrkdwn context and
// bounds the escaped result to limit runes. Truncation happens only between
// escaped characters, so an entity such as "&amp;" is never split, and ends
// with an ellipsis. truncated forces the ellipsis for text that was already
// shortened.
func escapeBounded(text string, limit int, mode textMode, truncated bool) string {
	if limit < 1 {
		return ""
	}
	if !truncated {
		escapedLength := 0
		for _, character := range text {
			escapedLength += utf8.RuneCountInString(escapedRune(character, mode))
			if escapedLength > limit {
				truncated = true
				break
			}
		}
	}
	var builder strings.Builder
	if !truncated {
		for _, character := range text {
			builder.WriteString(escapedRune(character, mode))
		}
		return builder.String()
	}
	budget := limit - utf8.RuneCountInString(ellipsis)
	used := 0
	for _, character := range text {
		escaped := escapedRune(character, mode)
		length := utf8.RuneCountInString(escaped)
		if used+length > budget {
			break
		}
		builder.WriteString(escaped)
		used += length
	}
	return strings.TrimRight(builder.String(), " \n") + ellipsis
}

// escapedRune returns the Slack mrkdwn spelling of one character in mode.
func escapedRune(character rune, mode textMode) string {
	switch character {
	case '&':
		return "&amp;"
	case '<':
		return "&lt;"
	case '>':
		return "&gt;"
	case '@':
		return "@" + zeroWidthSpace
	case '`':
		if mode == codeSpanMode {
			return codeSpanBacktickReplacement
		}
	case '|':
		if mode == linkLabelMode {
			return linkLabelPipeReplacement
		}
	}
	return string(character)
}

// slackLinkTarget validates rawURL as an absolute http(s) URL of printable
// ASCII with no credentials, no secret-like or email-like content, and no
// character that could break Slack link syntax, and returns it escaped for the
// target part of a <url|label> link.
func slackLinkTarget(rawURL string) (string, bool) {
	if rawURL == "" || len(rawURL) > maximumLinkTargetRunes {
		return "", false
	}
	for index := 0; index < len(rawURL); index++ {
		character := rawURL[index]
		if character <= ' ' || character >= 0x7F || strings.IndexByte(unsafeLinkTargetCharacters, character) >= 0 {
			return "", false
		}
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Opaque != "" || parsed.User != nil || parsed.Hostname() == "" {
		return "", false
	}
	if redactSensitiveText(rawURL) != rawURL {
		return "", false
	}
	escaped := strings.ReplaceAll(rawURL, "&", "&amp;")
	if len(escaped) > maximumLinkTargetRunes {
		return "", false
	}
	return escaped, true
}

// slackLink returns a <url|label> link to a validated URL, or "" when rawURL
// is not linkable. The label must already be escaped with linkLabelMode.
func slackLink(rawURL string, escapedLabel string) string {
	target, linkable := slackLinkTarget(strings.TrimSpace(rawURL))
	if !linkable || escapedLabel == "" {
		return ""
	}
	return "<" + target + "|" + escapedLabel + ">"
}

// dexWebRunURL builds the Dex Web v2 run page URL for runReference. It reports
// false when any field is blank, the Flow ID is too long to link, or the base
// URL carries a query or fragment that the run path cannot follow.
func dexWebRunURL(runReference RunReference) (string, bool) {
	baseURL := strings.TrimRight(strings.TrimSpace(runReference.DexWebURL), "/")
	flowType := strings.TrimSpace(runReference.FlowType)
	flowID := runReference.FlowID
	if baseURL == "" || flowType == "" || strings.TrimSpace(flowID) == "" || len(flowID) > maximumLinkedFlowIDBytes {
		return "", false
	}
	if strings.ContainsAny(baseURL, "?#") {
		return "", false
	}
	return baseURL + "/v2/run/" + url.PathEscape(flowType) + "/" + url.PathEscape(flowID), true
}

// runReferenceMarkup returns a Dex Web link labeled with the static label when
// runReference yields a valid link, otherwise "Flow `ID`", or "" when the
// Flow ID is blank. It reports whether the markup is a link.
func runReferenceMarkup(runReference RunReference, label string) (string, bool) {
	if runURL, built := dexWebRunURL(runReference); built {
		if link := slackLink(runURL, label); link != "" {
			return link, true
		}
	}
	flowIDSpan := codeSpan(runReference.FlowID, maximumFlowIDRunes)
	if flowIDSpan == "" {
		return "", false
	}
	return "Flow " + flowIDSpan, false
}
