package subscribers

import (
	"net/mail"
	"strings"
)

const (
	// maximumAddressLength is the longest address that fits an RFC 5321
	// forward-path.
	maximumAddressLength = 254
	// maximumLocalPartLength is the RFC 5321 limit on the local part.
	maximumLocalPartLength = 64
	// maximumDomainLabelLength is the RFC 1035 limit on one domain label.
	maximumDomainLabelLength = 63
)

// forbiddenAddressCharacters are the RFC 5322 specials, other than '@' and
// '.', that never appear in a bare dot-atom addr-spec. Rejecting them before
// parsing keeps display names, lists, groups, comments, quoted strings, and
// domain literals out regardless of how permissive net/mail is.
const forbiddenAddressCharacters = `"(),:;<>[\]`

// canonicalEmailAddress validates one trimmed cell as a single bare addr-spec
// under the package address policy and returns its lowercased form.
func canonicalEmailAddress(candidate string) (string, bool) {
	if candidate == "" || len(candidate) > maximumAddressLength {
		return "", false
	}
	for index := 0; index < len(candidate); index++ {
		character := candidate[index]
		// Reject space, every ASCII control character (including CR, LF, NUL,
		// and tab), DEL, and every byte of a non-ASCII character.
		if character <= ' ' || character >= 0x7F {
			return "", false
		}
		if strings.IndexByte(forbiddenAddressCharacters, character) >= 0 {
			return "", false
		}
	}
	if strings.Count(candidate, "@") != 1 {
		return "", false
	}
	parsedAddress, err := mail.ParseAddress(candidate)
	if err != nil || parsedAddress.Name != "" || parsedAddress.Address != candidate {
		return "", false
	}
	localPart, domain, _ := strings.Cut(candidate, "@")
	if !isValidLocalPart(localPart) || !isValidDomain(domain) {
		return "", false
	}
	return strings.ToLower(candidate), true
}

// isValidLocalPart reports whether the local part is a non-empty dot-atom
// within the RFC 5321 length limit. Its characters were checked by the caller.
func isValidLocalPart(localPart string) bool {
	if localPart == "" || len(localPart) > maximumLocalPartLength {
		return false
	}
	return !strings.HasPrefix(localPart, ".") && !strings.HasSuffix(localPart, ".") && !strings.Contains(localPart, "..")
}

// isValidDomain reports whether the domain is a hostname with at least two
// labels of ASCII letters, digits, and inner hyphens, whose final label is not
// entirely numeric, so IP addresses and single-label hosts are rejected. The
// address length limit already keeps the domain under the 253-character DNS
// limit.
func isValidDomain(domain string) bool {
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if !isValidDomainLabel(label) {
			return false
		}
	}
	return !isAllDigits(labels[len(labels)-1])
}

// isValidDomainLabel reports whether one label is 1 to 63 ASCII letters,
// digits, or hyphens that neither starts nor ends with a hyphen.
func isValidDomainLabel(label string) bool {
	if label == "" || len(label) > maximumDomainLabelLength {
		return false
	}
	if label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for index := 0; index < len(label); index++ {
		character := label[index]
		isLetter := ('a' <= character && character <= 'z') || ('A' <= character && character <= 'Z')
		isDigit := '0' <= character && character <= '9'
		if !isLetter && !isDigit && character != '-' {
			return false
		}
	}
	return true
}

// isAllDigits reports whether the non-empty text consists only of ASCII
// digits.
func isAllDigits(text string) bool {
	for index := 0; index < len(text); index++ {
		if text[index] < '0' || text[index] > '9' {
			return false
		}
	}
	return text != ""
}
