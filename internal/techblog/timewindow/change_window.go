// Package timewindow resolves and describes the research interval of one
// newsletter request. Every function is pure and deterministic: callers pass
// times in, nothing reads the clock, and identical input always yields
// byte-identical output, so Dex Steps that use these helpers can be retried
// safely. All times are normalized to UTC.
package timewindow

import (
	"strconv"
	"strings"
	"time"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

// maximumRepresentableLookbackDays is the largest whole number of days whose
// length fits in a time.Duration (about 292 years). Lookback values above it
// are saturated so that day arithmetic can never overflow.
const maximumRepresentableLookbackDays = int(int64(1<<63-1) / int64(24*time.Hour))

// changeWindowDateSeparator is the spaced en dash placed between the first and
// last date of a described change window.
const changeWindowDateSeparator = " – "

// ResolveChangeWindow returns the research interval that ends at until and
// spans the resolved number of whole days.
//
// requestedLookbackDays <= 0 means the request named no range, so
// defaultLookbackDays is used instead. A defaultLookbackDays below 1 is treated
// as 1 and a maxLookbackDays below 1 is treated as 1. The resolved day count is
// clamped to [1, maxLookbackDays]; a maxLookbackDays above the largest
// whole-day time.Duration (106751 days) is treated as that limit.
//
// Until is until.UTC() (which also drops any monotonic clock reading), Since is
// exactly Until minus the resolved days times 24 hours, and LookbackDays is the
// resolved day count. Because the arithmetic is done in UTC, daylight saving
// transitions in the caller's location never change the interval length.
func ResolveChangeWindow(until time.Time, requestedLookbackDays int, defaultLookbackDays int, maxLookbackDays int) model.ChangeWindow {
	upperBound := maxLookbackDays
	if upperBound < 1 {
		upperBound = 1
	}
	if upperBound > maximumRepresentableLookbackDays {
		upperBound = maximumRepresentableLookbackDays
	}

	resolvedDays := requestedLookbackDays
	if resolvedDays <= 0 {
		resolvedDays = defaultLookbackDays
		if resolvedDays < 1 {
			resolvedDays = 1
		}
	}
	if resolvedDays > upperBound {
		resolvedDays = upperBound
	}

	untilUTC := until.UTC()
	return model.ChangeWindow{
		Since:        untilUTC.Add(-time.Duration(resolvedDays) * 24 * time.Hour),
		Until:        untilUTC,
		LookbackDays: resolvedDays,
	}
}

// DescribeChangeWindow returns human-readable text for a change window, for
// example "Sep 19 – Sep 26, 2026 (past 7 days)". Both endpoints are rendered as
// UTC calendar dates separated by a spaced en dash. When the endpoints fall in
// different years both years are shown ("Dec 28, 2025 – Jan 4, 2026 (past 7
// days)"); when they fall on the same date only that date is shown. The
// parenthetical uses the singular "past 1 day" for one day and is omitted when
// LookbackDays is below 1.
func DescribeChangeWindow(window model.ChangeWindow) string {
	since := window.Since.UTC()
	until := window.Until.UTC()

	var description strings.Builder
	switch {
	case isSameCalendarDate(since, until):
		description.WriteString(until.Format("Jan 2, 2006"))
	case since.Year() == until.Year():
		description.WriteString(since.Format("Jan 2"))
		description.WriteString(changeWindowDateSeparator)
		description.WriteString(until.Format("Jan 2, 2006"))
	default:
		description.WriteString(since.Format("Jan 2, 2006"))
		description.WriteString(changeWindowDateSeparator)
		description.WriteString(until.Format("Jan 2, 2006"))
	}

	switch {
	case window.LookbackDays == 1:
		description.WriteString(" (past 1 day)")
	case window.LookbackDays > 1:
		description.WriteString(" (past ")
		description.WriteString(strconv.Itoa(window.LookbackDays))
		description.WriteString(" days)")
	}
	return description.String()
}

func isSameCalendarDate(first time.Time, second time.Time) bool {
	firstYear, firstMonth, firstDay := first.Date()
	secondYear, secondMonth, secondDay := second.Date()
	return firstYear == secondYear && firstMonth == secondMonth && firstDay == secondDay
}
