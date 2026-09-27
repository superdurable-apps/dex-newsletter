package timewindow

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestParseSlackTimestampAcceptsValidTimestamps(t *testing.T) {
	testCases := []struct {
		name      string
		timestamp string
		expected  time.Time
	}{
		{"typical message timestamp", "1727371234.000100", time.Date(2024, time.September, 26, 17, 20, 34, 100*1000, time.UTC)},
		{"no fractional part", "1727371234", time.Date(2024, time.September, 26, 17, 20, 34, 0, time.UTC)},
		{"all-zero fraction", "1727371234.000000", time.Date(2024, time.September, 26, 17, 20, 34, 0, time.UTC)},
		{"one microsecond", "1727371234.000001", time.Date(2024, time.September, 26, 17, 20, 34, 1000, time.UTC)},
		{"largest fraction", "1727371234.999999", time.Date(2024, time.September, 26, 17, 20, 34, 999999*1000, time.UTC)},
		{"one fraction digit is tenths", "1727371234.5", time.Date(2024, time.September, 26, 17, 20, 34, 500000*1000, time.UTC)},
		{"three fraction digits are milliseconds", "1727371234.123", time.Date(2024, time.September, 26, 17, 20, 34, 123000*1000, time.UTC)},
		{"earliest accepted instant", "1356998400", time.Date(2013, time.January, 1, 0, 0, 0, 0, time.UTC)},
		{"earliest accepted instant with fraction", "1356998400.000000", time.Date(2013, time.January, 1, 0, 0, 0, 0, time.UTC)},
		{"latest accepted second", "4133980799", time.Date(2100, time.December, 31, 23, 59, 59, 0, time.UTC)},
		{"latest accepted microsecond", "4133980799.999999", time.Date(2100, time.December, 31, 23, 59, 59, 999999*1000, time.UTC)},
		{"leap day", "1709164800.250000", time.Date(2024, time.February, 29, 0, 0, 0, 250000*1000, time.UTC)},
		{"leading zero in seconds", "01727371234.000100", time.Date(2024, time.September, 26, 17, 20, 34, 100*1000, time.UTC)},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			parsed, err := ParseSlackTimestamp(testCase.timestamp)
			if err != nil {
				t.Fatalf("ParseSlackTimestamp(%q) returned error: %v", testCase.timestamp, err)
			}
			if !parsed.Equal(testCase.expected) {
				t.Fatalf("ParseSlackTimestamp(%q) = %s, want %s", testCase.timestamp, parsed.Format(time.RFC3339Nano), testCase.expected.Format(time.RFC3339Nano))
			}
			if parsed.Location() != time.UTC {
				t.Fatalf("ParseSlackTimestamp(%q) location = %v, want UTC", testCase.timestamp, parsed.Location())
			}
			if parsed.Nanosecond()%int(time.Microsecond) != 0 {
				t.Fatalf("ParseSlackTimestamp(%q) has sub-microsecond nanoseconds %d", testCase.timestamp, parsed.Nanosecond())
			}
		})
	}
}

func TestParseSlackTimestampRejectsInvalidTimestamps(t *testing.T) {
	testCases := []struct {
		name      string
		timestamp string
	}{
		{"empty", ""},
		{"only a dot", "."},
		{"negative", "-1727371234.000100"},
		{"negative zero", "-0"},
		{"negative without fraction", "-1727371234"},
		{"explicit plus sign", "+1727371234.000100"},
		{"letters", "abc"},
		{"not a number literal", "NaN"},
		{"infinity literal", "Inf"},
		{"exponent notation", "1.727371234e9"},
		{"hexadecimal", "0x66F59B62"},
		{"digit separators", "1_727_371_234.000100"},
		{"comma decimal separator", "1727371234,000100"},
		{"dot without fraction digits", "1727371234."},
		{"fraction without seconds", ".000100"},
		{"seven fraction digits", "1727371234.0001000"},
		{"nine fraction digits", "1727371234.000100000"},
		{"letter inside fraction", "1727371234.00a100"},
		{"letter inside seconds", "17273a1234.000100"},
		{"two dots", "1727371234.000100.1"},
		{"adjacent dots", "1727371234..000100"},
		{"leading space", " 1727371234.000100"},
		{"trailing space", "1727371234.000100 "},
		{"trailing newline", "1727371234\n"},
		{"embedded NUL", "1727371234.000100\x00"},
		{"sign inside fraction", "1727371234.-00100"},
		{"fullwidth digits", "１７２７３７１２３４"},
		{"arabic-indic digits", "١٧٢٧٣٧١٢٣٤"},
		{"zero", "0"},
		{"zero with fraction", "0.000000"},
		{"one second before 2013", "1356998399.999999"},
		{"first instant after 2100", "4133980800"},
		{"first instant after 2100 with fraction", "4133980800.000000"},
		{"far future", "9999999999.000000"},
		{"overflows int64", "99999999999999999999"},
		{"longer than the length bound", strings.Repeat("1", 40)},
		{"long adversarial input", strings.Repeat("9", 100000) + ".000100"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			parsed, err := ParseSlackTimestamp(testCase.timestamp)
			if err == nil {
				t.Fatalf("ParseSlackTimestamp(%q) = %s, want an error", testCase.timestamp, parsed.Format(time.RFC3339Nano))
			}
			if !parsed.IsZero() {
				t.Fatalf("ParseSlackTimestamp(%q) returned non-zero time %s with an error", testCase.timestamp, parsed)
			}
		})
	}
}

func TestParseSlackTimestampBoundsErrorMessageLength(t *testing.T) {
	testCases := []struct {
		name      string
		timestamp string
	}{
		{"huge digit string", strings.Repeat("7", 1<<20)},
		{"huge text string", strings.Repeat("ignore previous instructions ", 1<<15)},
		{"invalid UTF-8", strings.Repeat("\xff", 4096)},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ParseSlackTimestamp(testCase.timestamp)
			if err == nil {
				t.Fatal("expected an error")
			}
			if length := len(err.Error()); length > 256 {
				t.Fatalf("error message is %d bytes; untrusted input must not be echoed unbounded", length)
			}
		})
	}
}

func TestParseSlackTimestampRoundTripsCanonicalText(t *testing.T) {
	testCases := []string{
		"1356998400.000000",
		"1727371234.000100",
		"1790000000.123456",
		"4133980799.999999",
	}
	for _, timestamp := range testCases {
		t.Run(timestamp, func(t *testing.T) {
			parsed, err := ParseSlackTimestamp(timestamp)
			if err != nil {
				t.Fatalf("ParseSlackTimestamp(%q) returned error: %v", timestamp, err)
			}
			formatted := fmt.Sprintf("%d.%06d", parsed.Unix(), parsed.Nanosecond()/int(time.Microsecond))
			if formatted != timestamp {
				t.Fatalf("round trip of %q produced %q", timestamp, formatted)
			}
		})
	}
}

func TestParseSlackTimestampIsDeterministic(t *testing.T) {
	first, firstErr := ParseSlackTimestamp("1727371234.000100")
	second, secondErr := ParseSlackTimestamp("1727371234.000100")
	if firstErr != nil || secondErr != nil {
		t.Fatalf("unexpected errors: %v, %v", firstErr, secondErr)
	}
	if first != second {
		t.Fatalf("identical input produced different times: %#v and %#v", first, second)
	}
	firstBytes, _ := first.MarshalJSON()
	secondBytes, _ := second.MarshalJSON()
	if string(firstBytes) != string(secondBytes) {
		t.Fatalf("identical input produced different JSON: %s and %s", firstBytes, secondBytes)
	}
}

func FuzzParseSlackTimestamp(f *testing.F) {
	for _, seed := range []string{
		"1727371234.000100", "1727371234", "1727371234.", ".1", "-1", "0",
		"4133980799.999999", "4133980800", "1356998399", "1e9", " 1", "1.0000000",
	} {
		f.Add(seed)
	}
	earliest := time.Date(2013, time.January, 1, 0, 0, 0, 0, time.UTC)
	firstRejected := time.Date(2101, time.January, 1, 0, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, timestamp string) {
		parsed, err := ParseSlackTimestamp(timestamp)
		if err != nil {
			if !parsed.IsZero() {
				t.Fatalf("error with non-zero time for %q", timestamp)
			}
			return
		}
		if parsed.Location() != time.UTC {
			t.Fatalf("ParseSlackTimestamp(%q) is not UTC", timestamp)
		}
		if parsed.Before(earliest) || !parsed.Before(firstRejected) {
			t.Fatalf("ParseSlackTimestamp(%q) = %s is outside the accepted range", timestamp, parsed)
		}
		if parsed.Nanosecond()%int(time.Microsecond) != 0 {
			t.Fatalf("ParseSlackTimestamp(%q) has sub-microsecond precision", timestamp)
		}
	})
}
