package subscribers

import (
	"net/mail"
	"reflect"
	"strings"
	"testing"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/config"
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

// defaultTestLayout mirrors the default newsletter configuration.
func defaultTestLayout() SheetLayout {
	return SheetLayout{
		EmailColumnHeader:        "email",
		StatusColumnHeader:       "status",
		UnsubscribedStatusValues: []string{"unsubscribed", "opted-out", "bounced"},
		MaxRecipients:            500,
	}
}

// layoutWithMaxRecipients returns the default test layout with another cap.
func layoutWithMaxRecipients(maxRecipients int) SheetLayout {
	layout := defaultTestLayout()
	layout.MaxRecipients = maxRecipients
	return layout
}

// emailOnlyTestLayout returns the default test layout with another cap and
// status filtering explicitly turned off, for sheets with only an email
// column.
func emailOnlyTestLayout(maxRecipients int) SheetLayout {
	layout := layoutWithMaxRecipients(maxRecipients)
	layout.StatusColumnHeader = ""
	return layout
}

func TestSheetLayoutFromConfiguration(t *testing.T) {
	testCases := []struct {
		name          string
		configuration config.NewsletterConfiguration
		want          SheetLayout
	}{
		{
			name:          "default configuration",
			configuration: config.Default().Newsletter,
			want:          defaultTestLayout(),
		},
		{
			name: "custom headers and cap",
			configuration: config.NewsletterConfiguration{
				EmailColumnHeader:        "E-mail Address",
				StatusColumnHeader:       "Subscription",
				UnsubscribedStatusValues: []string{"no"},
				MaxRecipients:            3,
				Footer:                   "ignored",
			},
			want: SheetLayout{
				EmailColumnHeader:        "E-mail Address",
				StatusColumnHeader:       "Subscription",
				UnsubscribedStatusValues: []string{"no"},
				MaxRecipients:            3,
			},
		},
		{
			name:          "nil unsubscribed values stay nil",
			configuration: config.NewsletterConfiguration{EmailColumnHeader: "email", MaxRecipients: 1},
			want:          SheetLayout{EmailColumnHeader: "email", MaxRecipients: 1},
		},
		{
			name:          "empty unsubscribed values stay empty",
			configuration: config.NewsletterConfiguration{EmailColumnHeader: "email", UnsubscribedStatusValues: []string{}, MaxRecipients: 1},
			want:          SheetLayout{EmailColumnHeader: "email", UnsubscribedStatusValues: []string{}, MaxRecipients: 1},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := SheetLayoutFromConfiguration(testCase.configuration)
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("SheetLayoutFromConfiguration() = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

func TestSheetLayoutFromConfigurationCopiesStatusValues(t *testing.T) {
	configuration := config.Default().Newsletter
	layout := SheetLayoutFromConfiguration(configuration)
	configuration.UnsubscribedStatusValues[0] = "changed"
	if layout.UnsubscribedStatusValues[0] != "unsubscribed" {
		t.Fatalf("layout shares the configuration slice: got %q", layout.UnsubscribedStatusValues[0])
	}
}

func TestBuildSubscriberListLayoutErrors(t *testing.T) {
	rows := [][]string{{"email"}, {"a@example.com"}}
	testCases := []struct {
		name        string
		layout      SheetLayout
		wantMessage string
	}{
		{name: "zero max recipients", layout: layoutWithMaxRecipients(0), wantMessage: "max recipients must be positive, got 0"},
		{name: "negative max recipients", layout: layoutWithMaxRecipients(-5), wantMessage: "max recipients must be positive, got -5"},
		{
			name:        "empty email column header",
			layout:      SheetLayout{EmailColumnHeader: "", MaxRecipients: 1},
			wantMessage: "email column header is blank",
		},
		{
			name:        "whitespace email column header",
			layout:      SheetLayout{EmailColumnHeader: " \t" + noBreakSpace, MaxRecipients: 1},
			wantMessage: "email column header is blank",
		},
		{
			name:        "status header equals email header ignoring case and spaces",
			layout:      SheetLayout{EmailColumnHeader: "Email", StatusColumnHeader: " EMAIL ", MaxRecipients: 1},
			wantMessage: "must differ from email column header",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := BuildSubscriberList(rows, testCase.layout)
			if err == nil {
				t.Fatalf("BuildSubscriberList() error = nil, want %q", testCase.wantMessage)
			}
			if !strings.Contains(err.Error(), testCase.wantMessage) {
				t.Fatalf("BuildSubscriberList() error = %q, want it to contain %q", err, testCase.wantMessage)
			}
			if !reflect.DeepEqual(got, model.SubscriberList{}) {
				t.Fatalf("BuildSubscriberList() list = %#v, want zero value on error", got)
			}
		})
	}
}

func TestBuildSubscriberListHeaderDetection(t *testing.T) {
	testCases := []struct {
		name        string
		rows        [][]string
		layout      SheetLayout
		want        model.SubscriberList
		wantMessage string
	}{
		{
			name: "standard header with status",
			rows: [][]string{
				{"email", "status"},
				{"a@example.com", "active"},
				{"b@example.com", "unsubscribed"},
			},
			layout: defaultTestLayout(),
			want:   model.SubscriberList{Recipients: []string{"a@example.com"}, RowsRead: 2, UnsubscribedCount: 1},
		},
		{
			name: "headers in other case and order with padding",
			rows: [][]string{
				{"Name", " STATUS ", "\tEmail" + noBreakSpace},
				{"Ann", "Opted-Out", "ann@example.com"},
				{"Bo", "subscribed", "bo@example.com"},
			},
			layout: defaultTestLayout(),
			want:   model.SubscriberList{Recipients: []string{"bo@example.com"}, RowsRead: 2, UnsubscribedCount: 1},
		},
		{
			name: "byte order mark and zero width space around header",
			rows: [][]string{
				{byteOrderMark + "email" + zeroWidthSpace, "status"},
				{"a@example.com", "bounced"},
				{"b@example.com"},
			},
			layout: defaultTestLayout(),
			want:   model.SubscriberList{Recipients: []string{"b@example.com"}, RowsRead: 2, UnsubscribedCount: 1},
		},
		{
			name: "blank rows before header are skipped",
			rows: [][]string{
				nil,
				{},
				{"", "  ", "\t"},
				{"email", "status"},
				{"a@example.com"},
			},
			layout: defaultTestLayout(),
			want:   model.SubscriberList{Recipients: []string{"a@example.com"}, RowsRead: 1},
		},
		{
			name: "duplicate email headers use the leftmost column",
			rows: [][]string{
				{"email", "Email"},
				{"first@example.com", "second@example.com"},
			},
			layout: emailOnlyTestLayout(10),
			want:   model.SubscriberList{Recipients: []string{"first@example.com"}, RowsRead: 1},
		},
		{
			name: "duplicate status headers use the leftmost column",
			rows: [][]string{
				{"email", "status", "STATUS"},
				{"a@example.com", "active", "unsubscribed"},
				{"b@example.com", "unsubscribed", "active"},
			},
			layout: defaultTestLayout(),
			want:   model.SubscriberList{Recipients: []string{"a@example.com"}, RowsRead: 2, UnsubscribedCount: 1},
		},
		{
			name: "status column left of email column",
			rows: [][]string{
				{"status", "email"},
				{"unsubscribed", "a@example.com"},
				{"", "b@example.com"},
			},
			layout: defaultTestLayout(),
			want:   model.SubscriberList{Recipients: []string{"b@example.com"}, RowsRead: 2, UnsubscribedCount: 1},
		},
		{
			name: "blank status header in layout disables filtering",
			rows: [][]string{
				{"email", "status"},
				{"a@example.com", "unsubscribed"},
			},
			layout: SheetLayout{EmailColumnHeader: "email", StatusColumnHeader: "  ", UnsubscribedStatusValues: []string{"unsubscribed"}, MaxRecipients: 10},
			want:   model.SubscriberList{Recipients: []string{"a@example.com"}, RowsRead: 1},
		},
		{
			name: "custom header names",
			rows: [][]string{
				{"Subscriber E-mail", "Subscription"},
				{"a@example.com", "NO"},
				{"b@example.com", "yes"},
			},
			layout: SheetLayout{EmailColumnHeader: "subscriber e-mail", StatusColumnHeader: "subscription", UnsubscribedStatusValues: []string{"no"}, MaxRecipients: 10},
			want:   model.SubscriberList{Recipients: []string{"b@example.com"}, RowsRead: 2, UnsubscribedCount: 1},
		},
		{
			name: "no header sheet with status filtering off uses column zero and every row",
			rows: [][]string{
				{"a@example.com", "unsubscribed"},
				{"b@example.com"},
				{"not an address"},
			},
			layout: emailOnlyTestLayout(10),
			want:   model.SubscriberList{Recipients: []string{"a@example.com", "b@example.com"}, RowsRead: 3, InvalidAddresses: 1},
		},
		{
			name: "no header sheet after blank rows with padded first address",
			rows: [][]string{
				{},
				{" "},
				{"  First@Example.com  "},
				{"second@example.com"},
			},
			layout: emailOnlyTestLayout(10),
			want:   model.SubscriberList{Recipients: []string{"first@example.com", "second@example.com"}, RowsRead: 2},
		},
		{
			name: "no header sheet ignores later header lookalike rows",
			rows: [][]string{
				{"a@example.com"},
				{"email", "status"},
				{"b@example.com", "unsubscribed"},
			},
			layout: emailOnlyTestLayout(10),
			want:   model.SubscriberList{Recipients: []string{"a@example.com", "b@example.com"}, RowsRead: 3, InvalidAddresses: 1},
		},
		{
			name:   "header row only",
			rows:   [][]string{{"email", "status"}},
			layout: defaultTestLayout(),
			want:   model.SubscriberList{Recipients: []string{}},
		},
		{
			name:   "nil rows",
			rows:   nil,
			layout: defaultTestLayout(),
			want:   model.SubscriberList{Recipients: []string{}},
		},
		{
			name:   "only blank rows",
			rows:   [][]string{{}, {"", ""}, {" " + noBreakSpace + " "}},
			layout: defaultTestLayout(),
			want:   model.SubscriberList{Recipients: []string{}},
		},
		{
			name:        "first row has neither header nor address",
			rows:        [][]string{{"name", "address"}, {"Ann", "a@example.com"}},
			layout:      defaultTestLayout(),
			wantMessage: `row 1, the first non-blank row, has no header cell equal to "email"`,
		},
		{
			name:        "header appears only after a title row",
			rows:        [][]string{{}, {"Newsletter subscribers"}, {"email"}, {"a@example.com"}},
			layout:      defaultTestLayout(),
			wantMessage: "no email column: row 2",
		},
		{
			name:        "first cell is a display name address",
			rows:        [][]string{{"Ann <a@example.com>"}},
			layout:      defaultTestLayout(),
			wantMessage: "no email column",
		},
		{
			name:        "first cell is a header injection attempt",
			rows:        [][]string{{"a@example.com\r\nBcc: x@example.com"}},
			layout:      defaultTestLayout(),
			wantMessage: "no email column",
		},
		{
			name:        "address in second column without header",
			rows:        [][]string{{"Ann", "a@example.com"}},
			layout:      defaultTestLayout(),
			wantMessage: "no email column",
		},
		{
			name:        "header with only a status column",
			rows:        [][]string{{"status"}, {"active"}},
			layout:      defaultTestLayout(),
			wantMessage: "no email column",
		},
		{
			name:        "email header inside a longer cell does not match",
			rows:        [][]string{{"email address"}, {"a@example.com"}},
			layout:      defaultTestLayout(),
			wantMessage: "no email column",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := BuildSubscriberList(testCase.rows, testCase.layout)
			if testCase.wantMessage != "" {
				if err == nil {
					t.Fatalf("BuildSubscriberList() = %#v, want error containing %q", got, testCase.wantMessage)
				}
				if !strings.Contains(err.Error(), testCase.wantMessage) {
					t.Fatalf("BuildSubscriberList() error = %q, want it to contain %q", err, testCase.wantMessage)
				}
				return
			}
			if err != nil {
				t.Fatalf("BuildSubscriberList() error = %v", err)
			}
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("BuildSubscriberList() = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

// TestBuildSubscriberListRequiresConfiguredStatusColumn locks in the fail-closed
// rule: a configured status column that the sheet does not have is an error,
// never a silently disabled unsubscribe filter. Only a blank status column
// header sends without status filtering.
func TestBuildSubscriberListRequiresConfiguredStatusColumn(t *testing.T) {
	const missingHeaderMessage = `subscriber sheet has no status column: header row 1 has no cell equal to "status"`
	const headerlessMessage = `row 1, the first non-blank row, holds an address instead of a header row, so the status column "status" cannot be located`
	testCases := []struct {
		name        string
		rows        [][]string
		layout      SheetLayout
		want        model.SubscriberList
		wantMessage string
	}{
		{
			name:        "status column renamed by an operator",
			rows:        [][]string{{"email", "Subscription status"}, {"a@example.com", "unsubscribed"}},
			layout:      defaultTestLayout(),
			wantMessage: missingHeaderMessage,
		},
		{
			name:        "status column renamed with an underscore",
			rows:        [][]string{{"email", "subscription_status"}, {"a@example.com", "unsubscribed"}},
			layout:      defaultTestLayout(),
			wantMessage: missingHeaderMessage,
		},
		{
			name:        "status header that is only a prefix",
			rows:        [][]string{{"email", "status (optional)"}, {"a@example.com", "unsubscribed"}},
			layout:      defaultTestLayout(),
			wantMessage: missingHeaderMessage,
		},
		{
			name:        "status column under another name",
			rows:        [][]string{{"email", "state"}, {"a@example.com", "unsubscribed"}},
			layout:      defaultTestLayout(),
			wantMessage: missingHeaderMessage,
		},
		{
			name:        "status column deleted",
			rows:        [][]string{{"email"}, {"a@example.com"}},
			layout:      defaultTestLayout(),
			wantMessage: missingHeaderMessage,
		},
		{
			name:        "header row only without status column",
			rows:        [][]string{{"email", "name"}},
			layout:      defaultTestLayout(),
			wantMessage: missingHeaderMessage,
		},
		{
			name:        "header row after blank rows reports its row number",
			rows:        [][]string{{}, {" "}, {"Email", "State"}, {"a@example.com", "unsubscribed"}},
			layout:      defaultTestLayout(),
			wantMessage: `header row 3 has no cell equal to "status"`,
		},
		{
			name:        "padded custom status header is reported trimmed",
			rows:        [][]string{{"email", "Status"}, {"a@example.com", "no"}},
			layout:      SheetLayout{EmailColumnHeader: "email", StatusColumnHeader: "  Subscription  ", UnsubscribedStatusValues: []string{"no"}, MaxRecipients: 10},
			wantMessage: `header row 1 has no cell equal to "Subscription"`,
		},
		{
			name:        "header row deleted from a sheet with a status column",
			rows:        [][]string{{"a@example.com", "unsubscribed"}, {"b@example.com", "active"}},
			layout:      defaultTestLayout(),
			wantMessage: headerlessMessage,
		},
		{
			name:        "headerless single column sheet with status filtering configured",
			rows:        [][]string{{"a@example.com"}, {"b@example.com"}},
			layout:      defaultTestLayout(),
			wantMessage: headerlessMessage,
		},
		{
			name:        "headerless sheet after blank rows reports its row number",
			rows:        [][]string{{}, {"", " "}, {"a@example.com", "unsubscribed"}},
			layout:      defaultTestLayout(),
			wantMessage: "row 3, the first non-blank row, holds an address instead of a header row",
		},
		{
			name:   "blank status header sends a header sheet without filtering",
			rows:   [][]string{{"email", "state"}, {"a@example.com", "unsubscribed"}},
			layout: emailOnlyTestLayout(10),
			want:   model.SubscriberList{Recipients: []string{"a@example.com"}, RowsRead: 1},
		},
		{
			name:   "whitespace status header sends a headerless sheet without filtering",
			rows:   [][]string{{"a@example.com", "unsubscribed"}, {"b@example.com", "active"}},
			layout: SheetLayout{EmailColumnHeader: "email", StatusColumnHeader: " \t" + noBreakSpace, UnsubscribedStatusValues: []string{"unsubscribed"}, MaxRecipients: 10},
			want:   model.SubscriberList{Recipients: []string{"a@example.com", "b@example.com"}, RowsRead: 2},
		},
		{
			name:   "status column present is applied",
			rows:   [][]string{{"email", "Subscription status"}, {"a@example.com", "unsubscribed"}, {"b@example.com", "active"}},
			layout: SheetLayout{EmailColumnHeader: "email", StatusColumnHeader: "subscription status", UnsubscribedStatusValues: []string{"unsubscribed"}, MaxRecipients: 10},
			want:   model.SubscriberList{Recipients: []string{"b@example.com"}, RowsRead: 2, UnsubscribedCount: 1},
		},
		{
			name:   "sheet without any non-blank row is still empty",
			rows:   [][]string{{}, {" "}},
			layout: defaultTestLayout(),
			want:   model.SubscriberList{Recipients: []string{}},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := BuildSubscriberList(testCase.rows, testCase.layout)
			if testCase.wantMessage != "" {
				if err == nil {
					t.Fatalf("BuildSubscriberList() = %#v, want error containing %q", got, testCase.wantMessage)
				}
				if !strings.Contains(err.Error(), testCase.wantMessage) {
					t.Fatalf("BuildSubscriberList() error = %q, want it to contain %q", err, testCase.wantMessage)
				}
				if !strings.Contains(err.Error(), "set the status column header to blank") {
					t.Fatalf("BuildSubscriberList() error = %q, want it to name the explicit opt-out", err)
				}
				if strings.Contains(err.Error(), "example.com") {
					t.Fatalf("BuildSubscriberList() error = %q echoes a sheet address", err)
				}
				if !reflect.DeepEqual(got, model.SubscriberList{}) {
					t.Fatalf("BuildSubscriberList() list = %#v, want zero value on error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("BuildSubscriberList() error = %v", err)
			}
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("BuildSubscriberList() = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

func TestBuildSubscriberListAddressValidation(t *testing.T) {
	maximumLocalPart := strings.Repeat("a", 64)
	// 63 + 1 + 63 + 1 + 57 + 4 = 189 characters, so the address is 254.
	longestDomain := strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 57) + ".com"
	longestAddress := maximumLocalPart + "@" + longestDomain
	tooLongAddress := maximumLocalPart + "@" + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 58) + ".com"
	if len(longestAddress) != 254 || len(tooLongAddress) != 255 {
		t.Fatalf("length fixtures are %d and %d, want 254 and 255", len(longestAddress), len(tooLongAddress))
	}

	testCases := []struct {
		name string
		cell string
		// want is the accepted recipient, or "" when the cell is invalid.
		want string
	}{
		// Accepted addresses.
		{name: "simple", cell: "a@example.com", want: "a@example.com"},
		{name: "mixed case is lowercased", cell: "Alice.Smith@Example.COM", want: "alice.smith@example.com"},
		{name: "surrounding spaces and tabs trimmed", cell: " \t a@example.com \t ", want: "a@example.com"},
		{name: "surrounding CR LF trimmed", cell: "\r\na@example.com\r\n", want: "a@example.com"},
		{name: "surrounding no-break spaces trimmed", cell: noBreakSpace + "a@example.com" + narrowNoBreakSpace, want: "a@example.com"},
		{name: "surrounding byte order mark and zero width space trimmed", cell: byteOrderMark + "a@example.com" + zeroWidthSpace, want: "a@example.com"},
		{name: "plus tag kept", cell: "a+news@example.com", want: "a+news@example.com"},
		{name: "apostrophe local part", cell: "o'neil@example.com", want: "o'neil@example.com"},
		{name: "dotted local part", cell: "first.last@example.com", want: "first.last@example.com"},
		{name: "subdomains", cell: "a@mail.eu.example.co.uk", want: "a@mail.eu.example.co.uk"},
		{name: "hyphenated domain", cell: "a@my-company.example", want: "a@my-company.example"},
		{name: "punycode domain", cell: "a@xn--bcher-kva.example", want: "a@xn--bcher-kva.example"},
		{name: "numeric labels with alphabetic top level", cell: "a@123.example.com", want: "a@123.example.com"},
		{name: "maximum local part", cell: maximumLocalPart + "@example.com", want: maximumLocalPart + "@example.com"},
		{name: "maximum domain label", cell: "a@" + strings.Repeat("b", 63) + ".com", want: "a@" + strings.Repeat("b", 63) + ".com"},
		{name: "maximum total length", cell: longestAddress, want: longestAddress},

		// Header injection and multiple-recipient attempts.
		{name: "CRLF Bcc injection", cell: "a@example.com\r\nBcc: x@example.com"},
		{name: "LF injection", cell: "a@example.com\nBcc: x@example.com"},
		{name: "CR injection", cell: "a@example.com\rBcc: x@example.com"},
		{name: "encoded CRLF stays literal and invalid", cell: "a@example.com%0d%0aBcc:x@example.com"},
		{name: "NUL inside", cell: "a\x00@example.com"},
		{name: "trailing NUL", cell: "a@example.com\x00"},
		{name: "display name", cell: "Name <a@example.com>"},
		{name: "quoted display name", cell: `"Name" <a@example.com>`},
		{name: "angle brackets only", cell: "<a@example.com>"},
		{name: "trailing angle bracket", cell: "a@example.com>"},
		{name: "comma list", cell: "a@example.com, c@example.org"},
		{name: "comma list without space", cell: "a@example.com,c@example.org"},
		{name: "semicolon list", cell: "a@example.com;c@example.org"},
		{name: "group syntax", cell: "friends: a@example.com;"},
		{name: "empty group", cell: "undisclosed-recipients:;"},
		{name: "mailto prefix", cell: "mailto:a@example.com"},
		{name: "comment", cell: "a(comment)@example.com"},
		{name: "trailing comment", cell: "a@example.com (Ann)"},
		{name: "quoted local part", cell: `"a b"@example.com`},
		{name: "backslash", cell: `a\@b@example.com`},
		{name: "domain literal", cell: "a@[192.0.2.1]"},

		// Whitespace and control characters inside.
		{name: "space inside local part", cell: "a b@example.com"},
		{name: "space before at", cell: "a @example.com"},
		{name: "tab inside", cell: "a\t@example.com"},
		{name: "no-break space inside", cell: "a" + noBreakSpace + "b@example.com"},
		{name: "zero width space inside", cell: "a" + zeroWidthSpace + "@example.com"},
		{name: "DEL inside", cell: "a\x7f@example.com"},
		{name: "escape inside", cell: "a\x1b@example.com"},

		// Structural problems.
		{name: "no at sign", cell: "a.example.com"},
		{name: "two at signs", cell: "a@b@example.com"},
		{name: "empty local part", cell: "@example.com"},
		{name: "empty domain", cell: "a@"},
		{name: "single label domain", cell: "a@localhost"},
		{name: "leading dot local part", cell: ".a@example.com"},
		{name: "trailing dot local part", cell: "a.@example.com"},
		{name: "consecutive dots local part", cell: "a..b@example.com"},
		{name: "leading dot domain", cell: "a@.example.com"},
		{name: "trailing dot domain", cell: "a@example.com."},
		{name: "consecutive dots domain", cell: "a@example..com"},
		{name: "leading hyphen label", cell: "a@-example.com"},
		{name: "trailing hyphen label", cell: "a@example-.com"},
		{name: "underscore in domain", cell: "a@ex_ample.com"},
		{name: "numeric top level label", cell: "a@example.123"},
		{name: "IPv4 domain", cell: "a@192.0.2.1"},
		{name: "local part too long", cell: strings.Repeat("a", 65) + "@example.com"},
		{name: "domain label too long", cell: "a@" + strings.Repeat("b", 64) + ".com"},
		{name: "address too long", cell: tooLongAddress},

		// Unicode.
		{name: "non ASCII local part", cell: "josé@example.com"},
		{name: "non ASCII domain", cell: "a@exämple.com"},
		{name: "CJK local part", cell: "用户@example.com"},
		{name: "fullwidth at sign", cell: "a" + fullwidthCommercialAt + "example.com"},
		{name: "Cyrillic look-alike letter", cell: cyrillicSmallLetterA + "@example.com"},
		{name: "right to left override", cell: "a" + rightToLeftOverride + "@example.com"},
		{name: "invalid UTF-8", cell: "a\xff@example.com"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			rows := [][]string{{"email"}, {testCase.cell}}
			got, err := BuildSubscriberList(rows, emailOnlyTestLayout(10))
			if err != nil {
				t.Fatalf("BuildSubscriberList() error = %v", err)
			}
			want := model.SubscriberList{Recipients: []string{}, RowsRead: 1, InvalidAddresses: 1}
			if testCase.want != "" {
				want = model.SubscriberList{Recipients: []string{testCase.want}, RowsRead: 1}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("BuildSubscriberList(%q) = %#v, want %#v", testCase.cell, got, want)
			}
		})
	}
}

func TestBuildSubscriberListCounting(t *testing.T) {
	testCases := []struct {
		name   string
		rows   [][]string
		layout SheetLayout
		want   model.SubscriberList
	}{
		{
			name: "ragged rows and blank cells",
			rows: [][]string{
				{"email", "name", "status"},
				{"a@example.com"},
				{"b@example.com", "Bo"},
				{"c@example.com", "Cy", "unsubscribed"},
				{},
				{"", "No Email", "active"},
				{"   ", "Spaces"},
				{"d@example.com", "Di", "", "extra", "cells"},
			},
			layout: defaultTestLayout(),
			want: model.SubscriberList{
				Recipients:        []string{"a@example.com", "b@example.com", "d@example.com"},
				RowsRead:          6,
				UnsubscribedCount: 1,
			},
		},
		{
			name: "status values match trimmed and case-insensitively",
			rows: [][]string{
				{"email", "status"},
				{"a@example.com", " UnSubscribed "},
				{"b@example.com", "OPTED-OUT"},
				{"c@example.com", "\tBounced" + noBreakSpace},
				{"d@example.com", "unsubscribed later"},
				{"e@example.com", "Subscribed"},
			},
			layout: defaultTestLayout(),
			want: model.SubscriberList{
				Recipients:        []string{"d@example.com", "e@example.com"},
				RowsRead:          5,
				UnsubscribedCount: 3,
			},
		},
		{
			name: "padded unsubscribed values in layout are trimmed",
			rows: [][]string{
				{"email", "status"},
				{"a@example.com", "left"},
				{"b@example.com", "stayed"},
			},
			layout: SheetLayout{EmailColumnHeader: "email", StatusColumnHeader: "status", UnsubscribedStatusValues: []string{"  LEFT  "}, MaxRecipients: 10},
			want:   model.SubscriberList{Recipients: []string{"b@example.com"}, RowsRead: 2, UnsubscribedCount: 1},
		},
		{
			name: "no unsubscribed values filters nothing",
			rows: [][]string{
				{"email", "status"},
				{"a@example.com", "unsubscribed"},
			},
			layout: SheetLayout{EmailColumnHeader: "email", StatusColumnHeader: "status", MaxRecipients: 10},
			want:   model.SubscriberList{Recipients: []string{"a@example.com"}, RowsRead: 1},
		},
		{
			name: "blank unsubscribed value matches blank and missing status",
			rows: [][]string{
				{"email", "status"},
				{"a@example.com", "confirmed"},
				{"b@example.com", ""},
				{"c@example.com"},
			},
			layout: SheetLayout{EmailColumnHeader: "email", StatusColumnHeader: "status", UnsubscribedStatusValues: []string{""}, MaxRecipients: 10},
			want:   model.SubscriberList{Recipients: []string{"a@example.com"}, RowsRead: 3, UnsubscribedCount: 2},
		},
		{
			name: "case-insensitive duplicates keep the first spelling lowercased",
			rows: [][]string{
				{"email"},
				{"Alice@Example.com"},
				{"alice@example.com"},
				{" ALICE@EXAMPLE.COM "},
				{"bob@example.com"},
			},
			layout: emailOnlyTestLayout(500),
			want: model.SubscriberList{
				Recipients:     []string{"alice@example.com", "bob@example.com"},
				RowsRead:       4,
				DuplicateCount: 2,
			},
		},
		{
			name: "provider aliases are distinct addresses",
			rows: [][]string{
				{"email"},
				{"alice@example.com"},
				{"a.lice@example.com"},
				{"alice+news@example.com"},
			},
			layout: emailOnlyTestLayout(500),
			want: model.SubscriberList{
				Recipients: []string{"alice@example.com", "a.lice@example.com", "alice+news@example.com"},
				RowsRead:   3,
			},
		},
		{
			name: "first-seen order is preserved",
			rows: [][]string{
				{"email"},
				{"c@example.com"},
				{"a@example.com"},
				{"b@example.com"},
				{"a@example.com"},
			},
			layout: emailOnlyTestLayout(500),
			want: model.SubscriberList{
				Recipients:     []string{"c@example.com", "a@example.com", "b@example.com"},
				RowsRead:       4,
				DuplicateCount: 1,
			},
		},
		{
			name: "unsubscribe after a subscribed duplicate wins",
			rows: [][]string{
				{"email", "status"},
				{"a@example.com", "active"},
				{"b@example.com", "active"},
				{"A@example.com", "unsubscribed"},
			},
			layout: defaultTestLayout(),
			want: model.SubscriberList{
				Recipients:        []string{"b@example.com"},
				RowsRead:          3,
				UnsubscribedCount: 2,
			},
		},
		{
			name: "unsubscribe before a subscribed duplicate wins",
			rows: [][]string{
				{"email", "status"},
				{"a@example.com", "unsubscribed"},
				{"a@example.com", "active"},
				{"a@example.com"},
			},
			layout: defaultTestLayout(),
			want: model.SubscriberList{
				Recipients:        []string{},
				RowsRead:          3,
				UnsubscribedCount: 3,
			},
		},
		{
			name: "all unsubscribed is not an error",
			rows: [][]string{
				{"email", "status"},
				{"a@example.com", "unsubscribed"},
				{"b@example.com", "bounced"},
				{"c@example.com", "opted-out"},
			},
			layout: defaultTestLayout(),
			want:   model.SubscriberList{Recipients: []string{}, RowsRead: 3, UnsubscribedCount: 3},
		},
		{
			name: "all invalid is not an error",
			rows: [][]string{
				{"email"},
				{"Name <a@example.com>"},
				{"a@example.com, b@example.com"},
				{"a@example.com\r\nBcc: x@example.com"},
			},
			layout: emailOnlyTestLayout(500),
			want:   model.SubscriberList{Recipients: []string{}, RowsRead: 3, InvalidAddresses: 3},
		},
		{
			name: "invalid unsubscribed row counts as invalid",
			rows: [][]string{
				{"email", "status"},
				{"not-an-address", "unsubscribed"},
			},
			layout: defaultTestLayout(),
			want:   model.SubscriberList{Recipients: []string{}, RowsRead: 1, InvalidAddresses: 1},
		},
		{
			name: "repeated header row counts as invalid address",
			rows: [][]string{
				{"email"},
				{"a@example.com"},
				{"Email"},
			},
			layout: emailOnlyTestLayout(500),
			want:   model.SubscriberList{Recipients: []string{"a@example.com"}, RowsRead: 2, InvalidAddresses: 1},
		},
		{
			name: "cap truncates unique addresses and later duplicates count as duplicates",
			rows: [][]string{
				{"email"},
				{"a@example.com"},
				{"b@example.com"},
				{"c@example.com"},
				{"d@example.com"},
				{"C@example.com"},
				{"a@example.com"},
			},
			layout: emailOnlyTestLayout(2),
			want: model.SubscriberList{
				Recipients:     []string{"a@example.com", "b@example.com"},
				RowsRead:       6,
				DuplicateCount: 2,
				TruncatedCount: 2,
			},
		},
		{
			name: "cap exactly reached truncates nothing",
			rows: [][]string{
				{"email"},
				{"a@example.com"},
				{"b@example.com"},
			},
			layout: emailOnlyTestLayout(2),
			want:   model.SubscriberList{Recipients: []string{"a@example.com", "b@example.com"}, RowsRead: 2},
		},
		{
			name: "cap of one",
			rows: [][]string{
				{"email"},
				{"a@example.com"},
				{"b@example.com"},
			},
			layout: emailOnlyTestLayout(1),
			want:   model.SubscriberList{Recipients: []string{"a@example.com"}, RowsRead: 2, TruncatedCount: 1},
		},
		{
			name: "invalid, unsubscribed, and duplicate rows do not use cap slots",
			rows: [][]string{
				{"email", "status"},
				{"bad address"},
				{"x@example.com", "unsubscribed"},
				{"a@example.com"},
				{"A@EXAMPLE.COM"},
				{"", "active"},
				{""},
				{"b@example.com"},
				{"c@example.com"},
			},
			layout: layoutWithMaxRecipients(2),
			want: model.SubscriberList{
				Recipients:        []string{"a@example.com", "b@example.com"},
				RowsRead:          7,
				InvalidAddresses:  1,
				DuplicateCount:    1,
				UnsubscribedCount: 1,
				TruncatedCount:    1,
			},
		},
		{
			name: "no header sheet counts every non-blank row",
			rows: [][]string{
				{"a@example.com"},
				{},
				{"a@example.com"},
				{"b@example.com"},
				{"bad"},
			},
			layout: emailOnlyTestLayout(1),
			want: model.SubscriberList{
				Recipients:       []string{"a@example.com"},
				RowsRead:         4,
				InvalidAddresses: 1,
				DuplicateCount:   1,
				TruncatedCount:   1,
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := BuildSubscriberList(testCase.rows, testCase.layout)
			if err != nil {
				t.Fatalf("BuildSubscriberList() error = %v", err)
			}
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("BuildSubscriberList() = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

func TestBuildSubscriberListLargeSheetCap(t *testing.T) {
	rows := [][]string{{"email", "status"}}
	for index := 0; index < 2500; index++ {
		address := "user" + strings.Repeat("x", index%7) + string(rune('a'+index%26)) + "@example.com"
		rows = append(rows, []string{address})
	}
	layout := SheetLayoutFromConfiguration(config.Default().Newsletter)
	got, err := BuildSubscriberList(rows, layout)
	if err != nil {
		t.Fatalf("BuildSubscriberList() error = %v", err)
	}
	unique := 7 * 26
	if unique > layout.MaxRecipients {
		t.Fatalf("fixture has %d unique addresses, want at most the cap %d", unique, layout.MaxRecipients)
	}
	if len(got.Recipients) != unique || got.DuplicateCount != 2500-unique || got.TruncatedCount != 0 || got.RowsRead != 2500 {
		t.Fatalf("BuildSubscriberList() = %d recipients, %d duplicates, %d truncated, %d rows read", len(got.Recipients), got.DuplicateCount, got.TruncatedCount, got.RowsRead)
	}

	capped := layout
	capped.MaxRecipients = 50
	got, err = BuildSubscriberList(rows, capped)
	if err != nil {
		t.Fatalf("BuildSubscriberList() error = %v", err)
	}
	if len(got.Recipients) != 50 || got.TruncatedCount != unique-50 || got.DuplicateCount != 2500-unique {
		t.Fatalf("capped BuildSubscriberList() = %d recipients, %d truncated, %d duplicates", len(got.Recipients), got.TruncatedCount, got.DuplicateCount)
	}
}

func TestBuildSubscriberListIsDeterministicAndDoesNotMutateInput(t *testing.T) {
	rows := [][]string{
		{"", ""},
		{" Status ", "EMAIL"},
		{"unsubscribed", "Z@Example.com"},
		{"", " Y@example.com "},
		{"active", "y@EXAMPLE.com"},
		{"active", "Name <x@example.com>"},
		{"", "w@example.com"},
	}
	rowsBefore := copyRows(rows)
	layout := SheetLayout{
		EmailColumnHeader:        " Email ",
		StatusColumnHeader:       " status ",
		UnsubscribedStatusValues: []string{" Unsubscribed "},
		MaxRecipients:            1,
	}
	statusValuesBefore := append([]string(nil), layout.UnsubscribedStatusValues...)

	first, err := BuildSubscriberList(rows, layout)
	if err != nil {
		t.Fatalf("BuildSubscriberList() error = %v", err)
	}
	second, err := BuildSubscriberList(rows, layout)
	if err != nil {
		t.Fatalf("BuildSubscriberList() second call error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("BuildSubscriberList() is not deterministic: %#v then %#v", first, second)
	}
	want := model.SubscriberList{
		Recipients:        []string{"y@example.com"},
		RowsRead:          5,
		InvalidAddresses:  1,
		DuplicateCount:    1,
		UnsubscribedCount: 1,
		TruncatedCount:    1,
	}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("BuildSubscriberList() = %#v, want %#v", first, want)
	}
	if !reflect.DeepEqual(rows, rowsBefore) {
		t.Fatalf("BuildSubscriberList() modified rows: %q, want %q", rows, rowsBefore)
	}
	if !reflect.DeepEqual(layout.UnsubscribedStatusValues, statusValuesBefore) {
		t.Fatalf("BuildSubscriberList() modified layout status values: %q", layout.UnsubscribedStatusValues)
	}
}

func TestBuildSubscriberListRecipientsNeverNil(t *testing.T) {
	testCases := []struct {
		name string
		rows [][]string
	}{
		{name: "nil rows", rows: nil},
		{name: "blank rows", rows: [][]string{{}, {""}}},
		{name: "header only", rows: [][]string{{"email", "status"}}},
		{name: "all invalid", rows: [][]string{{"email", "status"}, {"bad"}}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := BuildSubscriberList(testCase.rows, defaultTestLayout())
			if err != nil {
				t.Fatalf("BuildSubscriberList() error = %v", err)
			}
			if got.Recipients == nil {
				t.Fatalf("BuildSubscriberList() Recipients = nil, want empty non-nil slice")
			}
		})
	}
}

// FuzzBuildSubscriberList checks the invariants that must hold for any cell
// content: every recipient is a lowercase bare addr-spec that net/mail parses
// back to itself, recipients are unique and within the cap, and every data row
// is accounted for at most once.
func FuzzBuildSubscriberList(f *testing.F) {
	seeds := []struct{ email, status string }{
		{"a@example.com", ""},
		{"A@Example.com", "unsubscribed"},
		{"a@example.com\r\nBcc: x@example.com", "active"},
		{"Name <a@example.com>", ""},
		{"a@example.com, c@example.org", ""},
		{"josé@example.com", ""},
		{"\"a b\"@example.com", ""},
		{"a@[192.0.2.1]", ""},
		{" " + byteOrderMark + "a@example.com" + zeroWidthSpace + " ", " Bounced "},
		{"", "unsubscribed"},
	}
	for _, seed := range seeds {
		f.Add(seed.email, seed.status)
	}
	f.Fuzz(func(t *testing.T, emailCell string, statusCell string) {
		rows := [][]string{
			{"email", "status"},
			{emailCell, statusCell},
			{"b@example.com"},
			{emailCell},
		}
		layout := layoutWithMaxRecipients(2)
		got, err := BuildSubscriberList(rows, layout)
		if err != nil {
			t.Fatalf("BuildSubscriberList() error = %v", err)
		}
		again, err := BuildSubscriberList(rows, layout)
		if err != nil || !reflect.DeepEqual(got, again) {
			t.Fatalf("BuildSubscriberList() is not deterministic: %#v then %#v (%v)", got, again, err)
		}
		if len(got.Recipients) > layout.MaxRecipients {
			t.Fatalf("%d recipients exceed cap %d", len(got.Recipients), layout.MaxRecipients)
		}
		accounted := len(got.Recipients) + got.InvalidAddresses + got.DuplicateCount + got.UnsubscribedCount + got.TruncatedCount
		if accounted > got.RowsRead || got.RowsRead > 3 {
			t.Fatalf("counts %#v account for %d rows of %d read", got, accounted, got.RowsRead)
		}
		seen := map[string]bool{}
		for _, recipient := range got.Recipients {
			if seen[recipient] {
				t.Fatalf("duplicate recipient %q", recipient)
			}
			seen[recipient] = true
			if recipient != strings.ToLower(recipient) {
				t.Fatalf("recipient %q is not lowercase", recipient)
			}
			if strings.ContainsAny(recipient, "\r\n\x00,;<> \t") || strings.Count(recipient, "@") != 1 || len(recipient) > 254 {
				t.Fatalf("recipient %q violates the address policy", recipient)
			}
			parsed, err := mail.ParseAddress(recipient)
			if err != nil || parsed.Name != "" || parsed.Address != recipient {
				t.Fatalf("recipient %q does not round-trip through net/mail: %#v, %v", recipient, parsed, err)
			}
			_, domain, _ := strings.Cut(recipient, "@")
			if !strings.Contains(domain, ".") {
				t.Fatalf("recipient %q has a domain without a dot", recipient)
			}
		}
	})
}

// copyRows returns a deep copy of the sheet rows.
func copyRows(rows [][]string) [][]string {
	copied := make([][]string, len(rows))
	for index, row := range rows {
		copied[index] = append([]string(nil), row...)
	}
	return copied
}
