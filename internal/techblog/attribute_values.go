package techblog

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/superdurable/dex/sdk-go/dex"
)

// optionalValue normalizes an Attribute read that may happen before the
// Attribute is first written. Call it as optionalValue(attribute.Get(ctx)) so
// the Attribute access stays visible to the Flow Definition Graph analyzer.
func optionalValue[T any](value T, err error) (T, error) {
	var missing *dex.AttributeNotFoundError
	if errors.As(err, &missing) {
		var zero T
		return zero, nil
	}
	return value, err
}

func truncateRunes(value string, limit int) (string, bool) {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value, false
	}
	runes := []rune(value)
	return string(runes[:limit]), true
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func safeErrorText(err error) string {
	if err == nil {
		return "unknown error"
	}
	text, _ := truncateRunes(err.Error(), 300)
	return text
}
