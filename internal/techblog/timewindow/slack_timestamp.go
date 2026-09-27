package timewindow

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// earliestSlackTimestampSeconds is 2013-01-01T00:00:00Z. Slack launched in
	// 2013, so an earlier message time is corrupt input.
	earliestSlackTimestampSeconds = 1356998400
	// firstRejectedSlackTimestampSeconds is 2101-01-01T00:00:00Z, the first
	// instant after the year 2100.
	firstRejectedSlackTimestampSeconds = 4133980800
	// maximumSlackTimestampFractionDigits is the microsecond precision Slack uses.
	maximumSlackTimestampFractionDigits = 6
	// maximumSlackTimestampLength bounds accepted input; a valid timestamp has at
	// most 10 whole-second digits, a dot, and 6 fraction digits.
	maximumSlackTimestampLength = 32
	// maximumQuotedSlackTimestampBytes bounds how much untrusted input is echoed
	// into an error message.
	maximumQuotedSlackTimestampBytes = maximumSlackTimestampLength
)

// ParseSlackTimestamp converts a Slack message timestamp such as
// "1727371234.000100" (Unix seconds, a dot, and microseconds) to a UTC time
// with microsecond precision.
//
// The whole-second part must be one or more ASCII digits. The optional
// fractional part must be a dot followed by one to six ASCII digits and is read
// as a decimal fraction of a second, so ".5" is 500000 microseconds; a dot with
// no digits and more than six digits are rejected. Signs, whitespace,
// exponents, separators, and non-ASCII digits are rejected. Times before
// 2013-01-01T00:00:00Z or after the year 2100 are rejected as absurd.
func ParseSlackTimestamp(timestamp string) (time.Time, error) {
	if timestamp == "" {
		return time.Time{}, errors.New("slack timestamp is empty")
	}
	if len(timestamp) > maximumSlackTimestampLength {
		return time.Time{}, fmt.Errorf("slack timestamp %s is longer than %d bytes", quoteSlackTimestampForError(timestamp), maximumSlackTimestampLength)
	}
	if timestamp[0] == '-' {
		return time.Time{}, fmt.Errorf("slack timestamp %s is negative", quoteSlackTimestampForError(timestamp))
	}

	wholeSecondsText, fractionText, hasFraction := strings.Cut(timestamp, ".")
	if wholeSecondsText == "" {
		return time.Time{}, fmt.Errorf("slack timestamp %s has no whole-second digits", quoteSlackTimestampForError(timestamp))
	}
	if !isASCIIDigits(wholeSecondsText) {
		return time.Time{}, fmt.Errorf("slack timestamp %s is not numeric", quoteSlackTimestampForError(timestamp))
	}
	if hasFraction {
		if fractionText == "" {
			return time.Time{}, fmt.Errorf("slack timestamp %s has a dot but no fractional digits", quoteSlackTimestampForError(timestamp))
		}
		if !isASCIIDigits(fractionText) {
			return time.Time{}, fmt.Errorf("slack timestamp %s has a non-numeric fractional part", quoteSlackTimestampForError(timestamp))
		}
		if len(fractionText) > maximumSlackTimestampFractionDigits {
			return time.Time{}, fmt.Errorf("slack timestamp %s has more than %d fractional digits", quoteSlackTimestampForError(timestamp), maximumSlackTimestampFractionDigits)
		}
	}

	wholeSeconds, err := strconv.ParseInt(wholeSecondsText, 10, 64)
	if err != nil || wholeSeconds < earliestSlackTimestampSeconds || wholeSeconds >= firstRejectedSlackTimestampSeconds {
		return time.Time{}, fmt.Errorf("slack timestamp %s is outside 2013-01-01 through 2100-12-31 UTC", quoteSlackTimestampForError(timestamp))
	}

	microseconds := int64(0)
	if hasFraction {
		paddedFraction := fractionText + strings.Repeat("0", maximumSlackTimestampFractionDigits-len(fractionText))
		microseconds, err = strconv.ParseInt(paddedFraction, 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("slack timestamp %s has a non-numeric fractional part", quoteSlackTimestampForError(timestamp))
		}
	}
	return time.Unix(wholeSeconds, microseconds*int64(time.Microsecond)).UTC(), nil
}

func isASCIIDigits(text string) bool {
	for index := 0; index < len(text); index++ {
		if text[index] < '0' || text[index] > '9' {
			return false
		}
	}
	return true
}

func quoteSlackTimestampForError(timestamp string) string {
	if len(timestamp) <= maximumQuotedSlackTimestampBytes {
		return strconv.Quote(timestamp)
	}
	return strconv.Quote(timestamp[:maximumQuotedSlackTimestampBytes]) + "..."
}
