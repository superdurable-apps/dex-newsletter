package notices

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// sensitiveTextRedaction replaces every match of pattern with replacement,
// which may reference pattern's capture groups. The pattern runs only when the
// lowercased text contains one of triggers, a cheap necessary condition for a
// match that skips most patterns on ordinary text.
type sensitiveTextRedaction struct {
	triggers    []string
	pattern     *regexp.Regexp
	replacement string
}

const (
	// redactedMarker replaces a secret-like value.
	redactedMarker = "[redacted]"
	// redactedEmailMarker replaces an email address.
	redactedEmailMarker = "[redacted email]"
)

// emailLocalPartPattern matches an unquoted email local part, including
// internationalized letters.
const emailLocalPartPattern = `[\p{L}\p{N}\p{M}!#$%&'*+=?^_{}~.\-]+`

// emailQuotedLocalPartPattern matches a quoted email local part such as
// "john doe". Its first character may not be whitespace, ':' or ',' so the gap
// between two JSON strings, as in "handle": "@octocat.dev", is not taken for
// one.
const emailQuotedLocalPartPattern = `"[^"\s:,][^"\n]{0,63}"`

// emailDomainPattern matches the domain of an email address: one or more
// labels followed by an alphabetic or punycode top-level label, so version
// pins such as sdk-go@v0.12.1 or core@17.0.0 are not mistaken for addresses.
const emailDomainPattern = `(?:[\p{L}\p{N}\p{M}\-]+\.)+(?:(?i:xn--[a-z0-9\-]+)|\p{L}[\p{L}\p{M}]+)`

// credentialNamePrefixPattern matches the identifier text, possibly empty, in
// front of a credential word: DB_ in DB_PASSWORD, x-goog- in x-goog-api-key,
// or github in githubToken. It never needs a word boundary before the
// credential word, so snake_case, UPPER_SNAKE, kebab-case, dotted, and
// camelCase names are all covered.
const credentialNamePrefixPattern = `[A-Za-z0-9_.\-]*`

// quotedCredentialValuePatterns are the first two alternatives of every
// credential value: a double- or single-quoted string, so a passphrase with
// spaces is redacted whole. The quotes are capture groups 2 to 5 of the
// enclosing pattern, which credentialValueReplacement puts back.
const quotedCredentialValuePatterns = `(")[^"\n]*(")|(')[^'\n]*(')`

// credentialValueReplacement keeps a credential's name, separator, and quotes
// (group 1 and groups 2 to 5) and replaces its value.
const credentialValueReplacement = "${1}${2}${4}" + redactedMarker + "${3}${5}"

// sensitiveTextRedactions is the best-effort, ordered scrub applied to every
// dynamic value so notices never carry email addresses or credentials, for
// example an API key echoed inside a provider error URL or a .env line quoted
// in a configuration error. The patterns are compiled once and never
// modified; RE2 matching keeps every pass linear in the input length. The
// scrub favors precision on ordinary prose ("token refresh", "key: the new
// flow", "max_tokens=500") and covers credential assignments, credential
// headers and JSON fields, cookies, credentials embedded in URLs, well-known
// token formats, and email addresses (see also redactDotlessEmailAddresses).
// Replacements are redaction markers or text already present, and no marker
// can complete a later pattern, so the triggers of the original text stay a
// valid necessary condition for every pass.
var sensitiveTextRedactions = []sensitiveTextRedaction{
	// PEM private key blocks, including a block cut off before its END line.
	{
		triggers:    []string{"private key-----"},
		pattern:     regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----(?s:.*?)(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|\z)`),
		replacement: "[redacted private key]",
	},
	// Credentials embedded in a URL authority, such as https://user:pass@host.
	{
		triggers:    []string{"://"},
		pattern:     regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.\-]*://)[^\s/?#@]+@`),
		replacement: "${1}" + redactedMarker + "@",
	},
	// Credential headers and YAML or JSON fields, such as
	// "Authorization: Bearer x", "db_password: x", "SLACK_BOT_TOKEN: x", or
	// {"token": "x"}. A bare token or secret key counts only when quoted, so
	// prose such as "Token: the new refresh flow" is kept.
	{
		triggers: []string{"authorization", "api", "access", "secret", "private", "passw", "token"},
		pattern: regexp.MustCompile(`(?i)((?:` + credentialNamePrefixPattern + `(?:authorization|api[_\-]?key|access[_\-]?key|secret[_\-]?key|private[_\-]?key|password|passwd)` +
			`|[A-Za-z0-9_.\-]+(?:token|secret)|"(?:token|secret))"?[ \t]*:[ \t]*)` +
			`(?:` + quotedCredentialValuePatterns + `|["']?(?:(?:bearer|basic|token)[ \t]+)?[^\s"',;]+)`),
		replacement: credentialValueReplacement,
	},
	// Cookie headers, such as "Cookie: session=x; theme=y". The value must hold
	// a name=value pair, so prose such as "cookie: consent banner" is kept.
	{
		triggers:    []string{"cookie"},
		pattern:     regexp.MustCompile(`(?i)(` + credentialNamePrefixPattern + `cookie"?[ \t]*:[ \t]*"?)[^\s;,="]+=[^\s;,"]*(?:;[ \t]*[^\s;,"]+)*`),
		replacement: "${1}" + redactedMarker,
	},
	// Credential assignments and query parameters, such as ?key=x,
	// token=x, DB_PASSWORD=x, GITHUB_TOKEN = "x", or X-Amz-Signature=x. The
	// generic names key and sig count only on their own, so primary_key=5 and
	// monkey=banana are kept; a value starting with '=' is a comparison.
	{
		triggers: []string{"="},
		pattern: regexp.MustCompile(`(?i)((?:` + credentialNamePrefixPattern +
			`(?:api[_\-]?key|access[_\-]?key|secret[_\-]?key|private[_\-]?key|signing[_\-]?key|secret|token|password|passwd|pwd|credentials?|signature|authorization|cookie)` +
			`|(?:^|[^A-Za-z0-9_.\-])(?:key|sig))[ \t]*=[ \t]*)` +
			`(?:` + quotedCredentialValuePatterns + `|["']?(?:(?:bearer|basic)[ \t]+)?[^\s&#"'<>=][^\s&#"'<>]*)`),
		replacement: credentialValueReplacement,
	},
	// Bearer tokens outside a header.
	{
		triggers:    []string{"bearer"},
		pattern:     regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9._~+/\-]{8,}=*`),
		replacement: "${1}" + redactedMarker,
	},
	// Slack bot, user, and app tokens, and incoming webhook URLs.
	{
		triggers:    []string{"xox", "xapp-"},
		pattern:     regexp.MustCompile(`\b(?:xox[a-z]|xapp)-[A-Za-z0-9\-]{8,}`),
		replacement: redactedMarker,
	},
	{
		triggers:    []string{"hooks.slack.com/"},
		pattern:     regexp.MustCompile(`(?i)hooks\.slack\.com/(?:services|workflows|triggers)/[A-Za-z0-9/_\-]+`),
		replacement: "hooks.slack.com/" + redactedMarker,
	},
	// GitHub personal access, OAuth, app, and fine-grained tokens.
	{
		triggers:    []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_"},
		pattern:     regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})`),
		replacement: redactedMarker,
	},
	// Google API keys, OAuth client secrets, OAuth access tokens, and OAuth
	// refresh tokens. The Process uses Google OAuth for Sheets and Gmail.
	{
		triggers:    []string{"aiza"},
		pattern:     regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{30,}`),
		replacement: redactedMarker,
	},
	{
		triggers:    []string{"gocspx-"},
		pattern:     regexp.MustCompile(`\bGOCSPX-[0-9A-Za-z_\-]{20,}`),
		replacement: redactedMarker,
	},
	{
		triggers:    []string{"ya29."},
		pattern:     regexp.MustCompile(`\bya29\.[0-9A-Za-z_.\-]{10,}`),
		replacement: redactedMarker,
	},
	{
		triggers:    []string{"1//"},
		pattern:     regexp.MustCompile(`\b1//[0-9A-Za-z_\-]{20,}`),
		replacement: redactedMarker,
	},
	// OpenAI- and Anthropic-style secret keys.
	{
		triggers:    []string{"sk-"},
		pattern:     regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{16,}`),
		replacement: redactedMarker,
	},
	// AWS access key IDs.
	{
		triggers:    []string{"akia", "asia"},
		pattern:     regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),
		replacement: redactedMarker,
	},
	// JSON Web Tokens.
	{
		triggers:    []string{"eyj"},
		pattern:     regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]*`),
		replacement: redactedMarker,
	},
	// Email addresses with a dotted domain, including quoted and
	// internationalized local parts, internationalized domains, and a doubled
	// '@' typo.
	{
		triggers:    []string{"@"},
		pattern:     regexp.MustCompile(`(?:` + emailQuotedLocalPartPattern + `|` + emailLocalPartPattern + `)@+` + emailDomainPattern),
		replacement: redactedEmailMarker,
	},
	// Percent-encoded email addresses, such as bob%40example.com in a URL.
	{
		triggers:    []string{"%40"},
		pattern:     regexp.MustCompile(`(?i)[\p{L}\p{N}\p{M}._+\-]+%40` + emailDomainPattern),
		replacement: redactedEmailMarker,
	},
}

// dotlessEmailAddressPattern matches a local part (group 1) and '@' followed
// by one domain label with no dot (group 2), such as jane.doe@gmail or
// bob@localhost: mistyped or internal addresses, typical of spreadsheet cells,
// that emailDomainPattern does not cover. It runs after the dotted email
// pattern, so a complete address is already redacted.
var dotlessEmailAddressPattern = regexp.MustCompile(`(` + emailLocalPartPattern + `)@+([\p{L}\p{N}\p{M}\-]+)`)

// redactSensitiveText returns text with every email address and secret-like
// token replaced by a redaction marker.
func redactSensitiveText(text string) string {
	// Replacements never complete a later pattern (see
	// sensitiveTextRedactions), so one lowercased copy stays a valid
	// necessary condition for every pattern.
	lowercased := strings.ToLower(text)
	for _, redaction := range sensitiveTextRedactions {
		if containsAnySubstring(lowercased, redaction.triggers) {
			text = redaction.pattern.ReplaceAllString(text, redaction.replacement)
		}
	}
	if strings.Contains(text, "@") {
		text = redactDotlessEmailAddresses(text)
	}
	return text
}

// redactDotlessEmailAddresses replaces every dotlessEmailAddressPattern match
// that is not a version pin or Git reference with redactedEmailMarker.
func redactDotlessEmailAddresses(text string) string {
	var builder strings.Builder
	written := 0
	for _, match := range dotlessEmailAddressPattern.FindAllStringSubmatchIndex(text, -1) {
		if isVersionOrReferenceAtSign(text, match) {
			continue
		}
		builder.WriteString(text[written:match[0]])
		builder.WriteString(redactedEmailMarker)
		written = match[1]
	}
	if written == 0 {
		return text
	}
	builder.WriteString(text[written:])
	return builder.String()
}

// isVersionOrReferenceAtSign reports whether one dotlessEmailAddressPattern
// match in text is a package version pin, container digest, or Git reference
// rather than an address: a module or action path such as
// actions/setup-go@main, a label that does not start with a letter
// (core@17), a version (sdk@v4, sdk@v0.12.1, pkg@x.1), a commit (repo@4f2a9c1),
// or a well-known version or ref name (pkg@latest, image@sha256).
func isVersionOrReferenceAtSign(text string, match []int) bool {
	localPart := text[match[2]:match[3]]
	label := text[match[4]:match[5]]
	if match[0] > 0 && text[match[0]-1] == '/' && isPackageName(localPart) {
		return true
	}
	first, _ := utf8.DecodeRuneInString(label)
	if !unicode.IsLetter(first) {
		return true
	}
	if len(label) > 1 && (label[0] == 'v' || label[0] == 'V') && isASCIIDigit(label[1]) {
		return true
	}
	if following := text[match[5]:]; len(following) > 1 && following[0] == '.' && isASCIIDigit(following[1]) {
		return true
	}
	if isCommitHash(label) {
		return true
	}
	name, _, _ := strings.Cut(strings.ToLower(label), "-")
	return isVersionOrReferenceName(name)
}

// isPackageName reports whether text holds only the ASCII letters, digits,
// '.', '_', and '-' of a package, module, or repository name.
func isPackageName(text string) bool {
	for index := 0; index < len(text); index++ {
		character := text[index]
		if !isASCIIDigit(character) && !('a' <= character && character <= 'z') && !('A' <= character && character <= 'Z') && character != '.' && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

// isCommitHash reports whether label looks like an abbreviated or full Git
// commit hash: 7 to 40 lowercase hexadecimal characters, at least one a digit.
func isCommitHash(label string) bool {
	if len(label) < 7 || len(label) > 40 {
		return false
	}
	hasDigit := false
	for index := 0; index < len(label); index++ {
		character := label[index]
		switch {
		case isASCIIDigit(character):
			hasDigit = true
		case 'a' <= character && character <= 'f':
		default:
			return false
		}
	}
	return hasDigit
}

// isVersionOrReferenceName reports whether a lowercased name after '@' is a
// well-known dist-tag, branch, digest algorithm, or package-spec protocol.
func isVersionOrReferenceName(name string) bool {
	switch name {
	case "latest", "next", "canary", "beta", "alpha", "rc", "stable", "lts", "nightly", "edge", "preview",
		"experimental", "snapshot", "current", "release", "main", "master", "trunk", "head", "dev", "develop",
		"sha1", "sha256", "sha384", "sha512", "npm", "workspace", "file", "link", "git", "github", "patch":
		return true
	}
	return false
}

// isASCIIDigit reports whether character is '0' through '9'.
func isASCIIDigit(character byte) bool {
	return '0' <= character && character <= '9'
}

// containsAnySubstring reports whether text contains at least one of
// substrings.
func containsAnySubstring(text string, substrings []string) bool {
	for _, substring := range substrings {
		if strings.Contains(text, substring) {
			return true
		}
	}
	return false
}
