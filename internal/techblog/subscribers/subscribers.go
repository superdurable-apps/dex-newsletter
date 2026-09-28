// Package subscribers validates newsletter subscriber addresses and turns the
// stored subscriber list into a bounded delivery list.
//
// Every address is untrusted input: it arrives from the public subscription
// form. The package performs no I/O and reads no clock or randomness, so the
// same input always produces the same output. A retried Dex Step therefore
// recomputes the same audience snapshot.
//
// # Address policy
//
// A subscriber must be a single bare RFC 5322 addr-spec in printable ASCII:
// a dot-atom local part of at most 64 characters, exactly one '@', and a
// hostname domain with at least two labels, for a total of at most 254
// characters. Display names, groups, lists, comments, quoted local parts,
// domain literals, and any whitespace, control, or non-ASCII character are
// rejected rather than repaired, so input such as
// "a@example.com\r\nBcc: b@example.com" or "Name <a@example.com>" can never
// reach a message header. Internationalized addresses are rejected on purpose:
// they need SMTPUTF8 end to end, admit invisible and look-alike characters,
// and have no single case mapping for deduplication.
//
// Leading and trailing white space and the invisible characters that paste
// and autofill commonly leave at the edges are trimmed first; nothing inside
// the address is removed.
//
// Accepted addresses are lowercased in full, local part included. Local parts
// are case-sensitive in RFC 5321 section 2.4, but mainstream mailbox
// providers treat them case-insensitively, and lowercasing makes
// "Alice@Example.com" and "alice@example.com" one subscriber with one
// canonical spelling. Provider-specific aliases such as dots or "+tag"
// suffixes are not merged.
package subscribers

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

// CanonicalAddress trims the candidate's edges and returns its lowercase
// canonical form when it satisfies the address policy.
func CanonicalAddress(candidate string) (string, bool) {
	return canonicalEmailAddress(trimAddress(candidate))
}

// BuildDeliveryList re-validates the stored subscriber addresses and returns
// them as recipients in stored order, together with the count of every
// address it set aside. It returns an error only when maxRecipients is not
// positive. Recipients is never nil and the input is not modified.
//
// Each address lands in exactly one of these outcomes, checked in order:
//
//   - it is not a valid address (see the package documentation):
//     InvalidAddresses;
//   - its canonical form already appeared earlier: DuplicateCount;
//   - the list already holds maxRecipients addresses: TruncatedCount;
//   - otherwise its canonical form is appended to Recipients.
//
// The subscriber list Flow stores only canonical, unique addresses, so the
// first three outcomes are a defence against a list edited outside the
// application rather than an expected path.
func BuildDeliveryList(addresses []string, maxRecipients int) (model.SubscriberList, error) {
	if maxRecipients <= 0 {
		return model.SubscriberList{}, fmt.Errorf("subscriber list: max recipients must be positive, got %d", maxRecipients)
	}
	list := model.SubscriberList{Recipients: []string{}}
	seen := map[string]bool{}
	for _, candidate := range addresses {
		address, valid := CanonicalAddress(candidate)
		switch {
		case !valid:
			list.InvalidAddresses++
		case seen[address]:
			list.DuplicateCount++
		case len(list.Recipients) >= maxRecipients:
			seen[address] = true
			list.TruncatedCount++
		default:
			seen[address] = true
			list.Recipients = append(list.Recipients, address)
		}
	}
	return list, nil
}

// trimAddress removes leading and trailing Unicode white space and the
// invisible characters that paste and autofill commonly leave at the edges:
// the byte order mark, zero-width space and joiners, and the word joiner.
// Characters inside the address are never removed.
func trimAddress(candidate string) string {
	return strings.TrimFunc(candidate, isIgnorableEdgeRune)
}

// isIgnorableEdgeRune reports whether trimAddress removes the rune.
func isIgnorableEdgeRune(character rune) bool {
	switch character {
	case '\uFEFF', '\u200B', '\u200C', '\u200D', '\u2060':
		return true
	}
	return unicode.IsSpace(character)
}
