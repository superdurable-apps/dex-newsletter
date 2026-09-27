package timewindow

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // Embeds the IANA database so DST test locations load everywhere.

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

func TestResolveChangeWindowResolvesLookbackDays(t *testing.T) {
	until := time.Date(2026, time.September, 26, 17, 20, 34, 100000, time.UTC)
	testCases := []struct {
		name         string
		requested    int
		defaultDays  int
		maximumDays  int
		expectedDays int
	}{
		{"requested within bounds", 14, 7, 90, 14},
		{"requested one day", 1, 7, 90, 1},
		{"requested equals maximum", 90, 7, 90, 90},
		{"zero means not specified", 0, 7, 90, 7},
		{"negative means not specified", -5, 7, 90, 7},
		{"most negative means not specified", math.MinInt, 7, 90, 7},
		{"requested above maximum is clamped", 120, 7, 90, 90},
		{"huge request is clamped", math.MaxInt, 7, 90, 90},
		{"default above maximum is clamped", 0, 200, 90, 90},
		{"zero default is treated as one", 0, 0, 90, 1},
		{"negative default is treated as one", 0, -3, 90, 1},
		{"zero maximum is treated as one", 30, 7, 0, 1},
		{"negative maximum is treated as one", 30, 7, -10, 1},
		{"negative maximum and unspecified request", 0, 7, math.MinInt, 1},
		{"every bound invalid", -1, -1, -1, 1},
		{"default ignored when request given", 3, 60, 90, 3},
		{"maximum of one", 5, 5, 1, 1},
		{"leap-year length", 366, 7, 366, 366},
		{"huge maximum saturates at representable days", math.MaxInt, 7, math.MaxInt, maximumRepresentableLookbackDays},
		{"huge default saturates at representable days", 0, math.MaxInt, math.MaxInt, maximumRepresentableLookbackDays},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			window := ResolveChangeWindow(until, testCase.requested, testCase.defaultDays, testCase.maximumDays)
			if window.LookbackDays != testCase.expectedDays {
				t.Fatalf("LookbackDays = %d, want %d", window.LookbackDays, testCase.expectedDays)
			}
			if !window.Until.Equal(until) {
				t.Fatalf("Until = %s, want %s", window.Until, until)
			}
			expectedSince := until.Add(-time.Duration(testCase.expectedDays) * 24 * time.Hour)
			if !window.Since.Equal(expectedSince) {
				t.Fatalf("Since = %s, want %s", window.Since, expectedSince)
			}
			if !window.Since.Before(window.Until) {
				t.Fatalf("Since %s is not before Until %s", window.Since, window.Until)
			}
			if window.Since.Location() != time.UTC || window.Until.Location() != time.UTC {
				t.Fatalf("window is not UTC: since %v, until %v", window.Since.Location(), window.Until.Location())
			}
		})
	}
}

func TestResolveChangeWindowUsesExactDaysAcrossCalendarEdges(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load America/New_York: %v", err)
	}
	london, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatalf("load Europe/London: %v", err)
	}
	testCases := []struct {
		name          string
		until         time.Time
		days          int
		expectedSince time.Time
		expectedUntil time.Time
	}{
		{
			name:          "spring-forward week in New York is still 168 hours",
			until:         time.Date(2026, time.March, 10, 12, 0, 0, 0, newYork),
			days:          7,
			expectedSince: time.Date(2026, time.March, 3, 16, 0, 0, 0, time.UTC),
			expectedUntil: time.Date(2026, time.March, 10, 16, 0, 0, 0, time.UTC),
		},
		{
			name:          "fall-back day in New York is still 24 hours",
			until:         time.Date(2026, time.November, 1, 12, 0, 0, 0, newYork),
			days:          1,
			expectedSince: time.Date(2026, time.October, 31, 17, 0, 0, 0, time.UTC),
			expectedUntil: time.Date(2026, time.November, 1, 17, 0, 0, 0, time.UTC),
		},
		{
			name:          "London clock change inside a two-week window",
			until:         time.Date(2026, time.April, 5, 9, 30, 0, 0, london),
			days:          14,
			expectedSince: time.Date(2026, time.March, 22, 8, 30, 0, 0, time.UTC),
			expectedUntil: time.Date(2026, time.April, 5, 8, 30, 0, 0, time.UTC),
		},
		{
			name:          "fixed positive offset is normalized",
			until:         time.Date(2026, time.September, 27, 13, 30, 0, 0, time.FixedZone("UTC+14", 14*60*60)),
			days:          7,
			expectedSince: time.Date(2026, time.September, 19, 23, 30, 0, 0, time.UTC),
			expectedUntil: time.Date(2026, time.September, 26, 23, 30, 0, 0, time.UTC),
		},
		{
			name:          "leap day is counted in a leap year",
			until:         time.Date(2024, time.March, 1, 0, 0, 0, 0, time.UTC),
			days:          1,
			expectedSince: time.Date(2024, time.February, 29, 0, 0, 0, 0, time.UTC),
			expectedUntil: time.Date(2024, time.March, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:          "week containing a leap day",
			until:         time.Date(2024, time.March, 3, 10, 0, 0, 0, time.UTC),
			days:          7,
			expectedSince: time.Date(2024, time.February, 25, 10, 0, 0, 0, time.UTC),
			expectedUntil: time.Date(2024, time.March, 3, 10, 0, 0, 0, time.UTC),
		},
		{
			name:          "no leap day in a common year",
			until:         time.Date(2025, time.March, 1, 0, 0, 0, 0, time.UTC),
			days:          1,
			expectedSince: time.Date(2025, time.February, 28, 0, 0, 0, 0, time.UTC),
			expectedUntil: time.Date(2025, time.March, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:          "century non-leap year 2100",
			until:         time.Date(2100, time.March, 1, 0, 0, 0, 0, time.UTC),
			days:          1,
			expectedSince: time.Date(2100, time.February, 28, 0, 0, 0, 0, time.UTC),
			expectedUntil: time.Date(2100, time.March, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:          "366 days across a leap year",
			until:         time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
			days:          366,
			expectedSince: time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC),
			expectedUntil: time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:          "year boundary",
			until:         time.Date(2026, time.January, 4, 8, 0, 0, 0, time.UTC),
			days:          7,
			expectedSince: time.Date(2025, time.December, 28, 8, 0, 0, 0, time.UTC),
			expectedUntil: time.Date(2026, time.January, 4, 8, 0, 0, 0, time.UTC),
		},
		{
			name:          "nanoseconds are preserved",
			until:         time.Date(2026, time.September, 26, 0, 0, 0, 123456789, time.UTC),
			days:          2,
			expectedSince: time.Date(2026, time.September, 24, 0, 0, 0, 123456789, time.UTC),
			expectedUntil: time.Date(2026, time.September, 26, 0, 0, 0, 123456789, time.UTC),
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			window := ResolveChangeWindow(testCase.until, testCase.days, 7, 366)
			if window.LookbackDays != testCase.days {
				t.Fatalf("LookbackDays = %d, want %d", window.LookbackDays, testCase.days)
			}
			if window.Since != testCase.expectedSince {
				t.Fatalf("Since = %s, want %s", window.Since.Format(time.RFC3339Nano), testCase.expectedSince.Format(time.RFC3339Nano))
			}
			if window.Until != testCase.expectedUntil {
				t.Fatalf("Until = %s, want %s", window.Until.Format(time.RFC3339Nano), testCase.expectedUntil.Format(time.RFC3339Nano))
			}
			if length := window.Until.Sub(window.Since); length != time.Duration(testCase.days)*24*time.Hour {
				t.Fatalf("window length = %s, want exactly %d days", length, testCase.days)
			}
		})
	}
}

func TestResolveChangeWindowIsByteIdenticalForIdenticalInput(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load America/New_York: %v", err)
	}
	until := time.Date(2026, time.September, 26, 13, 20, 34, 100000, newYork)
	first, err := json.Marshal(ResolveChangeWindow(until, 0, 7, 90))
	if err != nil {
		t.Fatalf("marshal first window: %v", err)
	}
	second, err := json.Marshal(ResolveChangeWindow(until, 0, 7, 90))
	if err != nil {
		t.Fatalf("marshal second window: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("identical input produced different JSON:\n%s\n%s", first, second)
	}
	const expected = `{"since":"2026-09-19T17:20:34.0001Z","until":"2026-09-26T17:20:34.0001Z","lookbackDays":7}`
	if string(first) != expected {
		t.Fatalf("JSON = %s, want %s", first, expected)
	}
}

func TestResolveChangeWindowRoundTripsSlackTimestamp(t *testing.T) {
	messageTime, err := ParseSlackTimestamp("1727371234.000100")
	if err != nil {
		t.Fatalf("ParseSlackTimestamp returned error: %v", err)
	}
	window := ResolveChangeWindow(messageTime, 0, 7, 90)
	if got, want := DescribeChangeWindow(window), "Sep 19 – Sep 26, 2024 (past 7 days)"; got != want {
		t.Fatalf("DescribeChangeWindow = %q, want %q", got, want)
	}
}

func TestDescribeChangeWindow(t *testing.T) {
	kiritimati := time.FixedZone("UTC+14", 14*60*60)
	pagoPago := time.FixedZone("UTC-11", -11*60*60)
	testCases := []struct {
		name     string
		window   model.ChangeWindow
		expected string
	}{
		{
			name:     "same year",
			window:   windowEnding(time.Date(2026, time.September, 26, 17, 20, 34, 0, time.UTC), 7),
			expected: "Sep 19 – Sep 26, 2026 (past 7 days)",
		},
		{
			name:     "different years show both years",
			window:   windowEnding(time.Date(2026, time.January, 4, 12, 0, 0, 0, time.UTC), 7),
			expected: "Dec 28, 2025 – Jan 4, 2026 (past 7 days)",
		},
		{
			name:     "singular day",
			window:   windowEnding(time.Date(2026, time.September, 26, 9, 0, 0, 0, time.UTC), 1),
			expected: "Sep 25 – Sep 26, 2026 (past 1 day)",
		},
		{
			name:     "two days are plural",
			window:   windowEnding(time.Date(2026, time.September, 26, 9, 0, 0, 0, time.UTC), 2),
			expected: "Sep 24 – Sep 26, 2026 (past 2 days)",
		},
		{
			name:     "across months",
			window:   windowEnding(time.Date(2026, time.September, 26, 9, 0, 0, 0, time.UTC), 30),
			expected: "Aug 27 – Sep 26, 2026 (past 30 days)",
		},
		{
			name:     "single-digit dates are not padded",
			window:   windowEnding(time.Date(2026, time.September, 8, 9, 0, 0, 0, time.UTC), 7),
			expected: "Sep 1 – Sep 8, 2026 (past 7 days)",
		},
		{
			name:     "leap day",
			window:   windowEnding(time.Date(2024, time.March, 1, 6, 0, 0, 0, time.UTC), 1),
			expected: "Feb 29 – Mar 1, 2024 (past 1 day)",
		},
		{
			name:     "exactly midnight",
			window:   windowEnding(time.Date(2026, time.September, 26, 0, 0, 0, 0, time.UTC), 7),
			expected: "Sep 19 – Sep 26, 2026 (past 7 days)",
		},
		{
			name:     "last nanosecond of a day",
			window:   windowEnding(time.Date(2026, time.September, 26, 23, 59, 59, 999999999, time.UTC), 7),
			expected: "Sep 19 – Sep 26, 2026 (past 7 days)",
		},
		{
			name:     "local dates ahead of UTC use UTC dates",
			window:   model.ChangeWindow{Since: time.Date(2026, time.September, 20, 13, 30, 0, 0, kiritimati), Until: time.Date(2026, time.September, 27, 13, 30, 0, 0, kiritimati), LookbackDays: 7},
			expected: "Sep 19 – Sep 26, 2026 (past 7 days)",
		},
		{
			name:     "local dates behind UTC use UTC dates",
			window:   model.ChangeWindow{Since: time.Date(2026, time.December, 31, 14, 0, 0, 0, pagoPago), Until: time.Date(2027, time.January, 7, 14, 0, 0, 0, pagoPago), LookbackDays: 7},
			expected: "Jan 1 – Jan 8, 2027 (past 7 days)",
		},
		{
			name:     "maximum configured lookback",
			window:   windowEnding(time.Date(2026, time.September, 26, 9, 0, 0, 0, time.UTC), 90),
			expected: "Jun 28 – Sep 26, 2026 (past 90 days)",
		},
		{
			name:     "full leap year",
			window:   windowEnding(time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC), 366),
			expected: "Jan 1, 2024 – Jan 1, 2025 (past 366 days)",
		},
		{
			name:     "multi-year span",
			window:   windowEnding(time.Date(2026, time.September, 26, 9, 0, 0, 0, time.UTC), 1826),
			expected: "Sep 26, 2021 – Sep 26, 2026 (past 1826 days)",
		},
		{
			name:     "same date shows one date",
			window:   model.ChangeWindow{Since: time.Date(2026, time.September, 26, 1, 0, 0, 0, time.UTC), Until: time.Date(2026, time.September, 26, 20, 0, 0, 0, time.UTC), LookbackDays: 1},
			expected: "Sep 26, 2026 (past 1 day)",
		},
		{
			name:     "zero lookback days omits the parenthetical",
			window:   model.ChangeWindow{Since: time.Date(2026, time.September, 19, 0, 0, 0, 0, time.UTC), Until: time.Date(2026, time.September, 26, 0, 0, 0, 0, time.UTC)},
			expected: "Sep 19 – Sep 26, 2026",
		},
		{
			name:     "negative lookback days omits the parenthetical",
			window:   model.ChangeWindow{Since: time.Date(2026, time.September, 19, 0, 0, 0, 0, time.UTC), Until: time.Date(2026, time.September, 26, 0, 0, 0, 0, time.UTC), LookbackDays: -7},
			expected: "Sep 19 – Sep 26, 2026",
		},
		{
			name:     "zero-value window",
			window:   model.ChangeWindow{},
			expected: "Jan 1, 0001",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			description := DescribeChangeWindow(testCase.window)
			if description != testCase.expected {
				t.Fatalf("DescribeChangeWindow = %q, want %q", description, testCase.expected)
			}
			if again := DescribeChangeWindow(testCase.window); again != description {
				t.Fatalf("DescribeChangeWindow is not deterministic: %q then %q", description, again)
			}
		})
	}
}

func TestDescribeChangeWindowUsesSpacedEnDash(t *testing.T) {
	description := DescribeChangeWindow(windowEnding(time.Date(2026, time.September, 26, 9, 0, 0, 0, time.UTC), 7))
	if !strings.Contains(description, " – ") {
		t.Fatalf("description %q does not contain a spaced en dash (U+2013)", description)
	}
	if strings.Contains(description, "-") || strings.Contains(description, "—") {
		t.Fatalf("description %q contains a hyphen or em dash", description)
	}
}

func TestDescribeChangeWindowOfResolvedWindows(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load America/New_York: %v", err)
	}
	testCases := []struct {
		name      string
		until     time.Time
		requested int
		expected  string
	}{
		{"default week", time.Date(2026, time.September, 26, 10, 0, 0, 0, time.UTC), 0, "Sep 19 – Sep 26, 2026 (past 7 days)"},
		{"two weeks across a year", time.Date(2026, time.January, 10, 10, 0, 0, 0, time.UTC), 14, "Dec 27, 2025 – Jan 10, 2026 (past 14 days)"},
		{"clamped request", time.Date(2026, time.September, 26, 10, 0, 0, 0, time.UTC), 1000, "Jun 28 – Sep 26, 2026 (past 90 days)"},
		{"New York evening is the next UTC date", time.Date(2026, time.March, 9, 21, 0, 0, 0, newYork), 7, "Mar 3 – Mar 10, 2026 (past 7 days)"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			window := ResolveChangeWindow(testCase.until, testCase.requested, 7, 90)
			if description := DescribeChangeWindow(window); description != testCase.expected {
				t.Fatalf("DescribeChangeWindow = %q, want %q", description, testCase.expected)
			}
		})
	}
}

// windowEnding builds a UTC change window of whole days ending at until.
func windowEnding(until time.Time, days int) model.ChangeWindow {
	return model.ChangeWindow{
		Since:        until.Add(-time.Duration(days) * 24 * time.Hour),
		Until:        until,
		LookbackDays: days,
	}
}
