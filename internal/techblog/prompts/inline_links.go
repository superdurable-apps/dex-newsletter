package prompts

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Inline link sanitization.
//
// The renderer turns [label](url) in blog block text, bullet items, and
// newsletter copy into live hyperlinks, and email clients turn bare web
// addresses into links as well, with or without a scheme. Language-model
// output, and the GitHub and Slack text it was written from, is untrusted, so
// every free-text field these parsers return is passed through
// sanitizeInlineMarkupLine or sanitizeInlineMarkupProse. They keep a link or a
// bare web address only when its destination is a citable source of the
// stage, rewritten to the canonical URL; any other link is reduced to its
// label and any other bare web address is removed.
//
// A bare web address is an http(s) URL (bareURLLength) or a scheme-less
// address (schemelessWebAddressLength): a www.-prefixed host such as
// www.example.com, or a host whose last label looks like a top-level domain
// followed by a path, such as example.com/login. A scheme-less address is
// citable when its host and path match a citable URL (citableAddressKey).
// Hosts without a path (example.com) are not recognized, because they cannot
// be told apart from file names such as README.md, and neither are version
// numbers, file names, or relative paths such as internal/techblog/model.
//
// Inside code spans, bare web addresses are removed the same way, except in
// published blog text (inlineMarkupRules.keepCodeSpans), whose code spans
// render as code and must not be altered; a Go import path such as
// github.com/superdurable/dex there is kept, as it is in code blocks, which
// these functions never see. Elsewhere a backtick protects nothing, because
// plain-text email shows code spans as literal text an email client can link.
// Research notes therefore never carry an uncited web address anywhere, so
// text from GitHub cannot hand the blog writer an address to copy, and
// newsletter copy carries none at all.
//
// Link recognition mirrors the renderer's inline parser exactly: code spans
// are resolved first, brackets are matched outside code spans, parentheses are
// matched inside runs free of whitespace, control, non-ASCII, and backtick
// bytes, and a destination longer than 2048 bytes is not link syntax. It runs
// on text that is already normalized the way the renderer normalizes it (and
// the renderer's normalization leaves this package's output unchanged), and
// on one renderer paragraph at a time. Reducing a link to its label can expose
// new link syntax, because a label may itself look like a link, so rewriting
// repeats until a pass changes nothing. The result is a fixed point: the
// renderer finds exactly the links this scan kept. Pathological input that
// does not settle within maximumInlineMarkupPasses passes is dropped.

// inlineMarkupRules selects what inline link sanitization keeps.
type inlineMarkupRules struct {
	// citableSources are the only URLs that may remain, as links or as bare
	// URLs.
	citableSources citableSourceCatalog
	// keepCodeSpans leaves code spans verbatim, URLs included. Only published
	// blog text that the renderer parses for code spans sets it.
	keepCodeSpans bool
}

// strictInlineMarkupRules keeps only citable URLs, in code spans too.
func strictInlineMarkupRules(catalog citableSourceCatalog) inlineMarkupRules {
	return inlineMarkupRules{citableSources: catalog}
}

// publishedBlogTextRules keeps only citable URLs outside code spans and
// leaves code spans verbatim.
func publishedBlogTextRules(catalog citableSourceCatalog) inlineMarkupRules {
	return inlineMarkupRules{citableSources: catalog, keepCodeSpans: true}
}

const (
	// maximumInlineLinkDestinationBytes mirrors the renderer's URL bound; a
	// longer destination is not link syntax.
	maximumInlineLinkDestinationBytes = 2048
	// maximumInlineMarkupPasses bounds the fixed-point iteration. Ordinary
	// text settles in one or two passes; only deliberately constructed
	// markup needs more.
	maximumInlineMarkupPasses = 16
	// maximumReducedLabelDepth bounds how deeply one pass rewrites the labels
	// of reduced links nested inside the labels of other reduced links.
	// Deeper labels are copied and handled by the next pass.
	maximumReducedLabelDepth = 32
)

const (
	// bareURLTerminators end a bare URL. Besides keeping angle brackets and
	// quotes out, they guarantee that a kept bare URL contains no character
	// that takes part in inline markup structure.
	bareURLTerminators = "[]()`<>\"'{}|\\^"
	// bareURLTrailingPunctuation is sentence punctuation that is not part of
	// a URL at its end.
	bareURLTrailingPunctuation = ".,;:!?*_~…"
	// inlineUnsafeURLBytes may not appear in a URL placed into inline markup.
	inlineUnsafeURLBytes = "\"<>\\^`{|}[]()"
)

// sourceFileExtensions are common source, configuration, and data file
// extensions that are not delegated top-level domains, so no email client
// links name.ext/path. A host ending in one of them is a file name, as in
// "Node.js/Deno" or "go.mod/go.sum", not a web address. Extensions that are
// also real top-level domains (md, py, rs, sh, io, dev, zip, java, ...) are
// deliberately absent: an email client can link those.
var sourceFileExtensions = map[string]bool{
	"bash": true, "bak": true, "bzl": true, "cfg": true, "cjs": true, "clj": true, "conf": true, "cpp": true, "cs": true,
	"css": true, "csv": true, "cxx": true, "dart": true, "diff": true, "dll": true, "elm": true, "env": true, "erb": true,
	"erl": true, "ex": true, "exe": true, "exs": true, "gif": true, "git": true, "go": true, "gql": true, "gradle": true, "graphql": true,
	"gz": true, "hcl": true, "hpp": true, "hs": true, "htm": true, "html": true, "hxx": true, "ico": true, "ini": true,
	"ipynb": true, "jar": true, "jl": true, "jpeg": true, "jpg": true, "js": true, "json": true,
	"jsonl": true, "jsx": true, "kt": true, "kts": true, "less": true, "lock": true, "log": true, "lua": true, "mdx": true,
	"mjs": true, "mod": true, "nim": true, "patch": true, "pem": true, "php": true, "png": true, "proto": true, "pyc": true,
	"pyi": true, "rb": true, "rst": true, "sass": true, "scala": true, "scss": true, "sql": true, "sum": true, "svelte": true,
	"svg": true, "swift": true, "tar": true, "tex": true, "tgz": true, "tmp": true, "tmpl": true, "toml": true, "tpl": true,
	"ts": true, "tsv": true, "tsx": true, "txt": true, "vue": true, "wasm": true, "webp": true, "xml": true, "yaml": true,
	"yml": true, "zig": true, "zsh": true,
}

// sanitizeInlineMarkupLine is sanitizeSingleLine followed by inline link
// sanitization under rules. The result has at most maximumRunes runes (<= 0
// means unbounded).
func sanitizeInlineMarkupLine(text string, rules inlineMarkupRules, maximumRunes int) string {
	line := settleInlineMarkupLine(text, rules)
	if maximumRunes <= 0 || utf8.RuneCountInString(line) <= maximumRunes {
		return line
	}
	// Truncation can cut off a closing backtick or parenthesis and so expose
	// new markup, so the truncated text settles again.
	line = settleInlineMarkupLine(truncateAtWordBoundary(line, maximumRunes), rules)
	if utf8.RuneCountInString(line) <= maximumRunes {
		return line
	}
	// Only rewriting a newly exposed link or web address to a longer
	// canonical URL can grow the text. Without citable sources every rewrite
	// shrinks it.
	rules.citableSources = newCitableSourceCatalog()
	return settleInlineMarkupLine(truncateAtWordBoundary(line, maximumRunes), rules)
}

// settleInlineMarkupLine normalizes one line and rewrites its inline markup
// until nothing changes, or returns "" when it does not settle.
func settleInlineMarkupLine(text string, rules inlineMarkupRules) string {
	line := sanitizeSingleLine(text, 0)
	for pass := 0; pass < maximumInlineMarkupPasses; pass++ {
		rewritten, changed := rewriteInlineMarkup(line, rules)
		if !changed {
			return line
		}
		line = sanitizeSingleLine(rewritten, 0)
	}
	return ""
}

// sanitizeInlineMarkupProse normalizes multi-paragraph text with
// normalizeProse and sanitizes the inline links of each paragraph under
// rules, repeating until nothing changes. It returns "" when the text does
// not settle.
func sanitizeInlineMarkupProse(text string, rules inlineMarkupRules) string {
	prose := normalizeProse(text)
	for pass := 0; pass < maximumInlineMarkupPasses; pass++ {
		paragraphs := strings.Split(prose, "\n\n")
		changed := false
		for index, paragraph := range paragraphs {
			rewritten, paragraphChanged := rewriteInlineMarkup(paragraph, rules)
			paragraphs[index] = rewritten
			changed = changed || paragraphChanged
		}
		if !changed {
			return prose
		}
		prose = normalizeProse(strings.Join(paragraphs, "\n\n"))
	}
	return ""
}

// normalizeProse cleans multi-line text the way the renderer's prose
// normalization does, and slightly more: CR, CRLF, U+2028, and U+2029 become
// LF; other control characters except tab, invisible formatting characters,
// and stray variation selectors are removed; invalid UTF-8 becomes U+FFFD;
// every line is trimmed; runs of blank lines become one blank line; and
// leading and trailing blank lines are dropped. A blank line separates
// paragraphs. The function is idempotent.
func normalizeProse(text string) string {
	var cleaned strings.Builder
	cleaned.Grow(len(text))
	previousWasCarriageReturn := false
	previous := rune(-1)
	for _, character := range text {
		if previousWasCarriageReturn {
			previousWasCarriageReturn = false
			if character == '\n' {
				continue
			}
		}
		switch {
		case character == '\r':
			cleaned.WriteByte('\n')
			previousWasCarriageReturn = true
			previous = '\n'
		case character == 0x2028, character == 0x2029:
			cleaned.WriteByte('\n')
			previous = '\n'
		case character == '\n', character == '\t':
			cleaned.WriteRune(character)
			previous = character
		case unicode.IsControl(character), isInvisibleFormattingCharacter(character):
			// Dropped: invisible characters that can hide or reorder text.
		case isVariationSelector(character) && !acceptsVariationSelector(previous):
			// Dropped: a selector that does not modify a symbol only hides data.
		default:
			cleaned.WriteRune(character)
			previous = character
		}
	}
	lines := strings.Split(cleaned.String(), "\n")
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

// rewriteInlineMarkup makes one pass over one renderer paragraph, walking it
// exactly as the renderer's inline parser does. A link is kept, with its
// destination rewritten to the canonical spelling, only when the destination
// is citable and the label holds no bare web address (the plain-text email
// shows a label verbatim, so an address in it would become a bare one);
// otherwise it is replaced by its label, itself rewritten the same way (up to
// maximumReducedLabelDepth levels). A bare web address is replaced by its
// canonical URL when citable and removed otherwise. Code spans are copied
// verbatim under rules.keepCodeSpans; otherwise the bare web addresses inside
// them are treated the same way, and a span left blank by that is removed. It
// reports whether anything changed.
//
// Rewriting a reduced link's label is only a shortcut to convergence: a pass
// that reduces a link always reports a change, so the caller's fixed point is
// reached only by a pass that walked the text exactly as the renderer does.
func rewriteInlineMarkup(text string, rules inlineMarkupRules) (string, bool) {
	if !strings.ContainsAny(text, "`[") && !mayContainBareWebAddress(text) {
		return text, false
	}
	rewriter := inlineMarkupRewriter{text: text, structure: scanInlineMarkupStructure(text), rules: rules}
	rewriter.rewritten.Grow(len(text))
	rewriter.rewriteRange(0, len(text), 0)
	return rewriter.rewritten.String(), rewriter.changed
}

// inlineMarkupRewriter holds the state of one rewriteInlineMarkup pass.
type inlineMarkupRewriter struct {
	text      string
	structure inlineMarkupStructure
	rules     inlineMarkupRules
	addresses bareWebAddressScanner
	rewritten strings.Builder
	changed   bool
}

// rewriteRange rewrites text[start:end]. Constructs that would extend past
// end are treated as literal text; that only happens inside a reduced link's
// label, which the renderer never parses for links anyway.
func (rewriter *inlineMarkupRewriter) rewriteRange(start int, end int, labelDepth int) {
	text, rules := rewriter.text, rewriter.rules
	for index := start; index < end; {
		switch character := text[index]; {
		case character == '`':
			runLength := min(backtickRunLength(text, index), end-index)
			closer, closed := rewriter.structure.codeSpanClosers[index]
			if !closed || closer+runLength > end {
				rewriter.rewritten.WriteString(text[index : index+runLength])
				index += runLength
				continue
			}
			spanEnd := closer + runLength
			if rules.keepCodeSpans {
				rewriter.rewritten.WriteString(text[index:spanEnd])
			} else if content, contentChanged := rewriteBareWebAddresses(text[index+runLength:closer], rules.citableSources); !contentChanged {
				rewriter.rewritten.WriteString(text[index:spanEnd])
			} else {
				rewriter.changed = true
				if strings.TrimSpace(content) != "" {
					rewriter.rewritten.WriteString(text[index : index+runLength])
					rewriter.rewritten.WriteString(content)
					rewriter.rewritten.WriteString(text[closer:spanEnd])
				}
			}
			index = spanEnd
			continue
		case character == '[':
			if link, found := matchInlineLink(text, index, rewriter.structure); found && link.end <= end {
				canonicalURL, citable := resolveInlineURL(link.destination, rules.citableSources)
				switch {
				case citable && !containsBareWebAddress(text[link.labelStart:link.labelEnd]):
					rewriter.rewritten.WriteString(text[index:link.destinationStart])
					rewriter.rewritten.WriteString(canonicalURL)
					rewriter.rewritten.WriteByte(')')
					rewriter.changed = rewriter.changed || canonicalURL != link.destination
				case labelDepth < maximumReducedLabelDepth:
					rewriter.changed = true
					rewriter.rewriteRange(link.labelStart, link.labelEnd, labelDepth+1)
				default:
					rewriter.changed = true
					rewriter.rewritten.WriteString(text[link.labelStart:link.labelEnd])
				}
				index = link.end
				continue
			}
		}
		if length, addressChanged := rewriter.addresses.rewriteAt(text[:end], index, rules.citableSources, &rewriter.rewritten); length > 0 {
			rewriter.changed = rewriter.changed || addressChanged
			index += length
			continue
		}
		rewriter.rewritten.WriteByte(text[index])
		index++
	}
}

// rewriteBareWebAddresses replaces each citable bare web address of text
// with its canonical URL, removes every other bare web address, and leaves
// everything else alone. It reports whether anything changed.
func rewriteBareWebAddresses(text string, catalog citableSourceCatalog) (string, bool) {
	if !mayContainBareWebAddress(text) {
		return text, false
	}
	var rewritten strings.Builder
	rewritten.Grow(len(text))
	changed := false
	var addresses bareWebAddressScanner
	for index := 0; index < len(text); {
		if length, addressChanged := addresses.rewriteAt(text, index, catalog, &rewritten); length > 0 {
			changed = changed || addressChanged
			index += length
			continue
		}
		rewritten.WriteByte(text[index])
		index++
	}
	return rewritten.String(), changed
}

// bareWebAddressScanner finds the bare web addresses of one text in a single
// left-to-right walk.
type bareWebAddressScanner struct {
	// examinedHostEnd is the offset just past the last host examined for a
	// scheme-less address. No address starts inside a host that did not
	// start one, so skipping those offsets keeps the walk linear.
	examinedHostEnd int
}

// lengthAt returns the byte length of the bare web address starting at
// offset index of text, or 0 when none starts there, and whether it is an
// http(s) URL. Like a scheme, a "www." prefix starts an address even inside a
// word, so "awww.example.com" loses its www host too; any other scheme-less
// address is looked for only from the first label of a host (startsHostLabel).
// Offsets must be visited in increasing order.
func (scanner *bareWebAddressScanner) lengthAt(text string, index int) (int, bool) {
	if character := text[index]; character == 'h' || character == 'H' {
		if length := bareURLLength(text[index:]); length > 0 {
			return length, true
		}
	}
	if hasASCIIPrefixFold(text[index:], "www.") {
		if length, _ := schemelessWebAddressLength(text[index:]); length > 0 {
			return length, false
		}
	}
	if index < scanner.examinedHostEnd || !startsHostLabel(text, index) {
		return 0, false
	}
	length, hostLength := schemelessWebAddressLength(text[index:])
	scanner.examinedHostEnd = index + hostLength
	return length, false
}

// rewriteAt handles a bare web address starting at offset index of text: it
// writes the canonical URL when the address is citable and nothing
// otherwise. It returns the bytes consumed, 0 when no address starts there,
// and whether the output differs from the input. Offsets must be visited in
// increasing order.
func (scanner *bareWebAddressScanner) rewriteAt(text string, index int, catalog citableSourceCatalog, rewritten *strings.Builder) (int, bool) {
	length, hasScheme := scanner.lengthAt(text, index)
	if length == 0 {
		return 0, false
	}
	candidate := text[index : index+length]
	resolve := resolveInlineAddress
	if hasScheme {
		resolve = resolveInlineURL
	}
	if canonicalURL, citable := resolve(candidate, catalog); citable {
		rewritten.WriteString(canonicalURL)
		return length, canonicalURL != candidate
	}
	return length, true
}

// containsBareWebAddress reports whether text holds "://" or a bare web
// address anywhere, code spans included.
func containsBareWebAddress(text string) bool {
	if strings.Contains(text, "://") {
		return true
	}
	if !mayContainBareWebAddress(text) {
		return false
	}
	var scanner bareWebAddressScanner
	for index := 0; index < len(text); index++ {
		if length, _ := scanner.lengthAt(text, index); length > 0 {
			return true
		}
	}
	return false
}

// mayContainBareWebAddress is a cheap filter: it is false only when text
// certainly holds no bare web address.
func mayContainBareWebAddress(text string) bool {
	if strings.Contains(text, "://") {
		return true
	}
	return strings.Contains(text, ".") && (strings.Contains(text, "/") || containsASCIIFold(text, "www."))
}

// inlineLinkSyntax is one [label](destination) occurrence, with byte offsets
// into the scanned text.
type inlineLinkSyntax struct {
	labelStart       int
	labelEnd         int
	destination      string
	destinationStart int
	end              int
}

// matchInlineLink reads link syntax starting at the "[" at offset start,
// exactly as the renderer does.
func matchInlineLink(text string, start int, structure inlineMarkupStructure) (inlineLinkSyntax, bool) {
	labelEnd, found := structure.bracketPartners[start]
	if !found || labelEnd+1 >= len(text) || text[labelEnd+1] != '(' {
		return inlineLinkSyntax{}, false
	}
	destinationStart := labelEnd + 2
	destinationEnd, found := structure.parenthesisPartners[labelEnd+1]
	if !found || destinationEnd-destinationStart > maximumInlineLinkDestinationBytes {
		return inlineLinkSyntax{}, false
	}
	return inlineLinkSyntax{
		labelStart:       start + 1,
		labelEnd:         labelEnd,
		destination:      text[destinationStart:destinationEnd],
		destinationStart: destinationStart,
		end:              destinationEnd + 1,
	}, true
}

// resolveInlineURL returns the canonical spelling of a cited URL when it is a
// citable source whose canonical spelling is safe inside inline markup:
// printable ASCII without any of the bytes in inlineUnsafeURLBytes, so it can
// neither change how the surrounding markup pairs up nor fail the renderer's
// URL character check.
func resolveInlineURL(cited string, catalog citableSourceCatalog) (string, bool) {
	canonicalURL, _, found := catalog.resolveSource(cited)
	if !found || !isInlineSafeURL(canonicalURL) {
		return "", false
	}
	return canonicalURL, true
}

// resolveInlineAddress is resolveInlineURL for a scheme-less web address,
// matched by host and path.
func resolveInlineAddress(address string, catalog citableSourceCatalog) (string, bool) {
	canonicalURL, found := catalog.resolveAddress(address)
	if !found || !isInlineSafeURL(canonicalURL) {
		return "", false
	}
	return canonicalURL, true
}

// isInlineSafeURL reports whether a URL is non-empty printable ASCII without
// any of the bytes in inlineUnsafeURLBytes.
func isInlineSafeURL(canonicalURL string) bool {
	if canonicalURL == "" {
		return false
	}
	for index := 0; index < len(canonicalURL); index++ {
		if character := canonicalURL[index]; character <= ' ' || character >= 0x7f || strings.IndexByte(inlineUnsafeURLBytes, character) >= 0 {
			return false
		}
	}
	return true
}

// bareURLLength returns the byte length of the bare http or https URL at the
// start of text (scheme matched case-insensitively), or 0 when there is none.
// The URL ends before whitespace, control characters, and the characters in
// bareURLTerminators, and trailing sentence punctuation is not part of it. A
// scheme with nothing after it is not a URL.
func bareURLLength(text string) int {
	schemeLength := 0
	switch {
	case hasASCIIPrefixFold(text, "https://"):
		schemeLength = len("https://")
	case hasASCIIPrefixFold(text, "http://"):
		schemeLength = len("http://")
	default:
		return 0
	}
	end := extendBareURL(text, schemeLength)
	if end == schemeLength {
		return 0
	}
	return end
}

// extendBareURL returns the end of the bare URL whose first start bytes are
// text[:start]: it runs to whitespace, a control character, or a character in
// bareURLTerminators, and then gives back trailing sentence punctuation, but
// never ends before start.
func extendBareURL(text string, start int) int {
	end := start
	for end < len(text) {
		character, size := utf8.DecodeRuneInString(text[end:])
		if unicode.IsSpace(character) || unicode.IsControl(character) || strings.ContainsRune(bareURLTerminators, character) {
			break
		}
		end += size
	}
	for end > start {
		character, size := utf8.DecodeLastRuneInString(text[:end])
		if !strings.ContainsRune(bareURLTrailingPunctuation, character) {
			break
		}
		end -= size
	}
	return end
}

// schemelessWebAddressLength returns the byte length of the scheme-less web
// address at the start of text, or 0 when there is none, together with the
// byte length of the host examined (0 when text does not start with a host
// label). text starts with an address when it starts with a host of at least
// two dot-separated labels that either
//
//   - begins with the label "www" (www.example.com), or
//   - ends in a label shaped like a top-level domain (isWebTopLevelDomain)
//     and is followed, after an optional :port, by "/" (example.com/login).
//
// The address then extends like a bare URL (extendBareURL). The caller
// checks that no host label character directly precedes text.
func schemelessWebAddressLength(text string) (length int, hostLength int) {
	labels, firstLabel, lastLabel := 0, "", ""
	for index := 0; ; {
		labelStart := index
		for index < len(text) {
			character, size := utf8.DecodeRuneInString(text[index:])
			if !isHostLabelRune(character) {
				break
			}
			index += size
		}
		if index == labelStart {
			break
		}
		labels++
		if labels == 1 {
			firstLabel = text[labelStart:index]
		}
		lastLabel = text[labelStart:index]
		hostLength = index
		if index+1 >= len(text) || text[index] != '.' {
			break
		}
		index++
	}
	if labels < 2 {
		return 0, hostLength
	}
	if strings.EqualFold(firstLabel, "www") {
		return extendBareURL(text, hostLength), hostLength
	}
	if !isWebTopLevelDomain(lastLabel) {
		return 0, hostLength
	}
	pathStart := hostLength
	if pathStart < len(text) && text[pathStart] == ':' {
		digits := pathStart + 1
		for digits < len(text) && '0' <= text[digits] && text[digits] <= '9' {
			digits++
		}
		if digits > pathStart+1 {
			pathStart = digits
		}
	}
	if pathStart >= len(text) || text[pathStart] != '/' {
		return 0, hostLength
	}
	return extendBareURL(text, pathStart+1), hostLength
}

// isHostLabelRune reports characters that may appear in a host label: letters
// and digits of any script (internationalized hosts), combining marks, and
// the hyphen.
func isHostLabelRune(character rune) bool {
	return character == '-' || unicode.IsLetter(character) || unicode.IsDigit(character) || unicode.IsMark(character)
}

// startsHostLabel reports whether a host label character starts at offset
// index of text and no host label character directly precedes it, so a word
// is examined from its start: "xattacker.example/login" is one address. The
// labels after the first dot of a host are skipped through
// bareWebAddressScanner.examinedHostEnd, so "client.go/server" is not
// examined again at "go/server".
func startsHostLabel(text string, index int) bool {
	character, _ := utf8.DecodeRuneInString(text[index:])
	if !isHostLabelRune(character) {
		return false
	}
	if index == 0 {
		return true
	}
	previous, _ := utf8.DecodeLastRuneInString(text[:index])
	return !isHostLabelRune(previous)
}

// isWebTopLevelDomain reports whether label is shaped like a top-level
// domain an email client may link: at least two letters (of any script) or a
// punycode label, and not a source file extension (sourceFileExtensions).
// Numeric labels (v1.2.3/...) and one-letter labels (e.g./...) never are.
// Unknown and reserved names such as "example" count, so an unfamiliar
// top-level domain is never trusted.
func isWebTopLevelDomain(label string) bool {
	lower := strings.ToLower(label)
	if strings.HasPrefix(lower, "xn--") && len(lower) > len("xn--") {
		return true
	}
	if utf8.RuneCountInString(label) < 2 || sourceFileExtensions[lower] {
		return false
	}
	for index, character := range label {
		if !unicode.IsLetter(character) && (index == 0 || !unicode.IsMark(character)) {
			return false
		}
	}
	return true
}

// containsASCIIFold reports whether text contains the lowercase ASCII
// substring, ignoring ASCII case only.
func containsASCIIFold(text string, substring string) bool {
	for index := 0; index+len(substring) <= len(text); index++ {
		if hasASCIIPrefixFold(text[index:], substring) {
			return true
		}
	}
	return false
}

// hasASCIIPrefixFold reports whether text starts with the lowercase ASCII
// prefix, ignoring ASCII case only.
func hasASCIIPrefixFold(text string, prefix string) bool {
	if len(text) < len(prefix) {
		return false
	}
	for index := 0; index < len(prefix); index++ {
		character := text[index]
		if 'A' <= character && character <= 'Z' {
			character += 'a' - 'A'
		}
		if character != prefix[index] {
			return false
		}
	}
	return true
}

// inlineMarkupStructure mirrors the renderer's record of which inline
// delimiters pair up. All maps are keyed and valued by byte offsets.
type inlineMarkupStructure struct {
	// codeSpanClosers maps the start of an opening backtick run to the start
	// of its closing run of equal length.
	codeSpanClosers map[int]int
	// bracketPartners maps a "[" outside code spans to its matching "]".
	bracketPartners map[int]int
	// parenthesisPartners maps a "(" to its matching ")" when no byte that
	// may not appear in a link destination lies between them.
	parenthesisPartners map[int]int
}

// scanInlineMarkupStructure pairs the delimiters of text exactly as the
// renderer does, in linear time.
func scanInlineMarkupStructure(text string) inlineMarkupStructure {
	structure := inlineMarkupStructure{
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
