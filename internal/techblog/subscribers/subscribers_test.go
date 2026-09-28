package subscribers

import (
	"net/mail"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

// Invisible and look-alike characters are spelled as rune conversions so the
// fixtures stay reviewable as plain ASCII source.
const (
	noBreakSpace          = string(rune(0x00A0))
	narrowNoBreakSpace    = string(rune(0x202F))
	byteOrderMark         = string(rune(0xFEFF))
	zeroWidthSpace        = string(rune(0x200B))
	rightToLeftOverride   = string(rune(0x202E))
	fullwidthCommercialAt = string(rune(0xFF20))
	cyrillicSmallLetterA  = string(rune(0x0430))
)

func TestCanonicalAddress(t *testing.T) {
	maximumLocalPart := strings.Repeat("a", 64)
	// 63 + 1 + 63 + 1 + 57 + 4 = 189 characters, so the address is 254.
	longestDomain := strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 57) + ".com"
	longestAddress := maximumLocalPart + "@" + longestDomain
	tooLongAddress := maximumLocalPart + "@" + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 58) + ".com"
	if len(longestAddress) != 254 || len(tooLongAddress) != 255 {
		t.Fatalf("length fixtures are %d and %d, want 254 and 255", len(longestAddress), len(tooLongAddress))
	}

	testCases := []struct {
		name  string
		input string
		// want is the canonical address, or "" when the input is invalid.
		want string
	}{
		// Accepted addresses.
		{name: "simple", input: "a@example.com", want: "a@example.com"},
		{name: "mixed case is lowercased", input: "Alice.Smith@Example.COM", want: "alice.smith@example.com"},
		{name: "surrounding spaces and tabs trimmed", input: " \t a@example.com \t ", want: "a@example.com"},
		{name: "surrounding CR LF trimmed", input: "\r\na@example.com\r\n", want: "a@example.com"},
		{name: "surrounding no-break spaces trimmed", input: noBreakSpace + "a@example.com" + narrowNoBreakSpace, want: "a@example.com"},
		{name: "surrounding byte order mark and zero width space trimmed", input: byteOrderMark + "a@example.com" + zeroWidthSpace, want: "a@example.com"},
		{name: "plus tag kept", input: "a+news@example.com", want: "a+news@example.com"},
		{name: "apostrophe local part", input: "o'neil@example.com", want: "o'neil@example.com"},
		{name: "dotted local part", input: "first.last@example.com", want: "first.last@example.com"},
		{name: "subdomains", input: "a@mail.eu.example.co.uk", want: "a@mail.eu.example.co.uk"},
		{name: "hyphenated domain", input: "a@my-company.example", want: "a@my-company.example"},
		{name: "punycode domain", input: "a@xn--bcher-kva.example", want: "a@xn--bcher-kva.example"},
		{name: "numeric labels with alphabetic top level", input: "a@123.example.com", want: "a@123.example.com"},
		{name: "maximum local part", input: maximumLocalPart + "@example.com", want: maximumLocalPart + "@example.com"},
		{name: "maximum domain label", input: "a@" + strings.Repeat("b", 63) + ".com", want: "a@" + strings.Repeat("b", 63) + ".com"},
		{name: "maximum total length", input: longestAddress, want: longestAddress},

		// Header injection and multiple-recipient attempts.
		{name: "CRLF Bcc injection", input: "a@example.com\r\nBcc: x@example.com"},
		{name: "LF injection", input: "a@example.com\nBcc: x@example.com"},
		{name: "CR injection", input: "a@example.com\rBcc: x@example.com"},
		{name: "encoded CRLF stays literal and invalid", input: "a@example.com%0d%0aBcc:x@example.com"},
		{name: "NUL inside", input: "a\x00@example.com"},
		{name: "trailing NUL", input: "a@example.com\x00"},
		{name: "display name", input: "Name <a@example.com>"},
		{name: "quoted display name", input: `"Name" <a@example.com>`},
		{name: "angle brackets only", input: "<a@example.com>"},
		{name: "trailing angle bracket", input: "a@example.com>"},
		{name: "comma list", input: "a@example.com, c@example.org"},
		{name: "comma list without space", input: "a@example.com,c@example.org"},
		{name: "semicolon list", input: "a@example.com;c@example.org"},
		{name: "group syntax", input: "friends: a@example.com;"},
		{name: "empty group", input: "undisclosed-recipients:;"},
		{name: "mailto prefix", input: "mailto:a@example.com"},
		{name: "comment", input: "a(comment)@example.com"},
		{name: "trailing comment", input: "a@example.com (Ann)"},
		{name: "quoted local part", input: `"a b"@example.com`},
		{name: "backslash", input: `a\@b@example.com`},
		{name: "domain literal", input: "a@[192.0.2.1]"},

		// Whitespace and control characters inside.
		{name: "space inside local part", input: "a b@example.com"},
		{name: "space before at", input: "a @example.com"},
		{name: "tab inside", input: "a\t@example.com"},
		{name: "no-break space inside", input: "a" + noBreakSpace + "b@example.com"},
		{name: "zero width space inside", input: "a" + zeroWidthSpace + "@example.com"},
		{name: "DEL inside", input: "a\x7f@example.com"},
		{name: "escape inside", input: "a\x1b@example.com"},

		// Structural problems.
		{name: "no at sign", input: "a.example.com"},
		{name: "two at signs", input: "a@b@example.com"},
		{name: "empty local part", input: "@example.com"},
		{name: "empty domain", input: "a@"},
		{name: "single label domain", input: "a@localhost"},
		{name: "leading dot local part", input: ".a@example.com"},
		{name: "trailing dot local part", input: "a.@example.com"},
		{name: "consecutive dots local part", input: "a..b@example.com"},
		{name: "leading dot domain", input: "a@.example.com"},
		{name: "trailing dot domain", input: "a@example.com."},
		{name: "consecutive dots domain", input: "a@example..com"},
		{name: "leading hyphen label", input: "a@-example.com"},
		{name: "trailing hyphen label", input: "a@example-.com"},
		{name: "underscore in domain", input: "a@ex_ample.com"},
		{name: "numeric top level label", input: "a@example.123"},
		{name: "IPv4 domain", input: "a@192.0.2.1"},
		{name: "local part too long", input: strings.Repeat("a", 65) + "@example.com"},
		{name: "domain label too long", input: "a@" + strings.Repeat("b", 64) + ".com"},
		{name: "address too long", input: tooLongAddress},

		// Unicode.
		{name: "non ASCII local part", input: "josé@example.com"},
		{name: "non ASCII domain", input: "a@exämple.com"},
		{name: "CJK local part", input: "用户@example.com"},
		{name: "fullwidth at sign", input: "a" + fullwidthCommercialAt + "example.com"},
		{name: "Cyrillic look-alike letter", input: cyrillicSmallLetterA + "@example.com"},
		{name: "right to left override", input: "a" + rightToLeftOverride + "@example.com"},
		{name: "invalid UTF-8", input: "a\xff@example.com"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, valid := CanonicalAddress(testCase.input)
			if valid != (testCase.want != "") || got != testCase.want {
				t.Fatalf("CanonicalAddress(%q) = %q, %v; want %q, %v", testCase.input, got, valid, testCase.want, testCase.want != "")
			}
		})
	}
}

func TestBuildDeliveryList(t *testing.T) {
	testCases := []struct {
		name          string
		addresses     []string
		maxRecipients int
		want          model.SubscriberList
	}{
		{name: "nil list", addresses: nil, maxRecipients: 5, want: model.SubscriberList{Recipients: []string{}}},
		{name: "empty list", addresses: []string{}, maxRecipients: 5, want: model.SubscriberList{Recipients: []string{}}},
		{
			name:          "stored order is kept",
			addresses:     []string{"c@example.com", "a@example.com", "b@example.com"},
			maxRecipients: 5,
			want:          model.SubscriberList{Recipients: []string{"c@example.com", "a@example.com", "b@example.com"}},
		},
		{
			name:          "addresses are canonicalized",
			addresses:     []string{" Reader@Example.COM "},
			maxRecipients: 5,
			want:          model.SubscriberList{Recipients: []string{"reader@example.com"}},
		},
		{
			name:          "invalid addresses are counted and skipped",
			addresses:     []string{"a@example.com", "Name <b@example.com>", "c@example.com\r\nBcc: d@example.com", ""},
			maxRecipients: 5,
			want:          model.SubscriberList{Recipients: []string{"a@example.com"}, InvalidAddresses: 3},
		},
		{
			name:          "duplicates compare canonically",
			addresses:     []string{"a@example.com", "A@EXAMPLE.com", "b@example.com", "a@example.com"},
			maxRecipients: 5,
			want:          model.SubscriberList{Recipients: []string{"a@example.com", "b@example.com"}, DuplicateCount: 2},
		},
		{
			name:          "cap truncates later addresses",
			addresses:     []string{"a@example.com", "b@example.com", "c@example.com", "b@example.com"},
			maxRecipients: 2,
			want:          model.SubscriberList{Recipients: []string{"a@example.com", "b@example.com"}, DuplicateCount: 1, TruncatedCount: 1},
		},
		{
			name:          "a truncated address repeated counts as a duplicate",
			addresses:     []string{"a@example.com", "c@example.com", "c@example.com"},
			maxRecipients: 1,
			want:          model.SubscriberList{Recipients: []string{"a@example.com"}, TruncatedCount: 1, DuplicateCount: 1},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var original []string
			if testCase.addresses != nil {
				original = append([]string{}, testCase.addresses...)
			}
			got, err := BuildDeliveryList(testCase.addresses, testCase.maxRecipients)
			if err != nil {
				t.Fatalf("BuildDeliveryList() error = %v", err)
			}
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("BuildDeliveryList() = %#v, want %#v", got, testCase.want)
			}
			if !reflect.DeepEqual(testCase.addresses, original) {
				t.Fatalf("BuildDeliveryList() modified its input: %q, want %q", testCase.addresses, original)
			}
		})
	}
}

func TestBuildDeliveryListRejectsNonPositiveCap(t *testing.T) {
	for _, maxRecipients := range []int{0, -1} {
		if _, err := BuildDeliveryList([]string{"a@example.com"}, maxRecipients); err == nil {
			t.Fatalf("BuildDeliveryList(maxRecipients=%d) returned no error", maxRecipients)
		}
	}
}

func TestBuildDeliveryListLargeListCap(t *testing.T) {
	addresses := make([]string, 0, 2500)
	for index := 0; index < 2500; index++ {
		addresses = append(addresses, "reader"+strings.Repeat("x", index%7)+"."+strconv.Itoa(index)+"@example.com")
	}
	got, err := BuildDeliveryList(addresses, 2000)
	if err != nil {
		t.Fatalf("BuildDeliveryList() error = %v", err)
	}
	if len(got.Recipients) != 2000 || got.TruncatedCount != 500 || got.InvalidAddresses != 0 || got.DuplicateCount != 0 {
		t.Fatalf("BuildDeliveryList() kept %d, truncated %d, invalid %d, duplicate %d; want 2000, 500, 0, 0",
			len(got.Recipients), got.TruncatedCount, got.InvalidAddresses, got.DuplicateCount)
	}
}

func FuzzCanonicalAddress(f *testing.F) {
	for _, seed := range []string{
		"a@example.com",
		"A@Example.com",
		"a@example.com\r\nBcc: x@example.com",
		"Name <a@example.com>",
		"a@example.com, c@example.org",
		"josé@example.com",
		"\"a b\"@example.com",
		"a@[192.0.2.1]",
		" " + byteOrderMark + "a@example.com" + zeroWidthSpace + " ",
		"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, candidate string) {
		address, valid := CanonicalAddress(candidate)
		if !valid {
			if address != "" {
				t.Fatalf("CanonicalAddress(%q) rejected the input but returned %q", candidate, address)
			}
			return
		}
		if again, validAgain := CanonicalAddress(address); !validAgain || again != address {
			t.Fatalf("CanonicalAddress is not idempotent: %q then %q (%v)", address, again, validAgain)
		}
		if address != strings.ToLower(address) {
			t.Fatalf("address %q is not lowercase", address)
		}
		if strings.ContainsAny(address, "\r\n\x00,;<> \t") || strings.Count(address, "@") != 1 || len(address) > 254 {
			t.Fatalf("address %q violates the address policy", address)
		}
		parsed, err := mail.ParseAddress(address)
		if err != nil || parsed.Name != "" || parsed.Address != address {
			t.Fatalf("address %q does not round-trip through net/mail: %#v, %v", address, parsed, err)
		}
		_, domain, _ := strings.Cut(address, "@")
		if !strings.Contains(domain, ".") {
			t.Fatalf("address %q has a domain without a dot", address)
		}
	})
}
