package prompts

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

// Bounds on untrusted text copied into errors.
const (
	// maximumFailureDetailRunes bounds provider failure text.
	maximumFailureDetailRunes = 300
	// maximumPurposeDetailRunes bounds a mismatched result purpose.
	maximumPurposeDetailRunes = 60
)

// byteOrderMark is U+FEFF, tolerated before a response.
const byteOrderMark rune = 0xFEFF

// decodeGenerationResult checks that generation succeeded and that a result
// naming a purpose names this stage's, and strictly decodes its text into
// target: one JSON object, optionally wrapped in a single Markdown code fence,
// with no unknown fields and no trailing data.
func decodeGenerationResult(result model.GenerationResult, purpose string, target any) error {
	if result.Status != model.GenerationSucceeded {
		return fmt.Errorf("%w: %s ended with status %q%s",
			ErrGenerationUnsuccessful, purpose, result.Status, describeGenerationFailure(result))
	}
	if result.Purpose != "" && result.Purpose != purpose {
		return invalidModelOutput(purpose, fmt.Sprintf("the result belongs to stage %q",
			sanitizeSingleLine(result.Purpose, maximumPurposeDetailRunes)))
	}
	body, err := stripSurroundingCodeFence(result.Text)
	if err != nil {
		return invalidModelOutput(purpose, err.Error())
	}
	if !strings.HasPrefix(body, "{") {
		return invalidModelOutput(purpose, "response is not a JSON object")
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return invalidModelOutput(purpose, "malformed JSON: "+sanitizeSingleLine(err.Error(), maximumFailureDetailRunes))
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return invalidModelOutput(purpose, "unexpected data after the JSON object")
	}
	return nil
}

// describeGenerationFailure renders bounded, single-line failure details.
func describeGenerationFailure(result model.GenerationResult) string {
	var details []string
	if reason := sanitizeSingleLine(result.FinishReason, maximumFailureDetailRunes); reason != "" {
		details = append(details, "finish reason "+reason)
	}
	if message := sanitizeSingleLine(result.FailureMessage, maximumFailureDetailRunes); message != "" {
		details = append(details, message)
	}
	if len(details) == 0 {
		return ""
	}
	return ": " + strings.Join(details, "; ")
}

// stripSurroundingCodeFence trims whitespace and a byte-order mark and
// removes one Markdown code fence (``` or ```json) that surrounds the whole
// text. Fences inside JSON strings are untouched.
func stripSurroundingCodeFence(text string) (string, error) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), string(byteOrderMark)))
	if trimmed == "" {
		return "", errors.New("response is empty")
	}
	const fence = "```"
	if !strings.HasPrefix(trimmed, fence) {
		return trimmed, nil
	}
	if len(trimmed) < 2*len(fence) || !strings.HasSuffix(trimmed, fence) {
		return "", errors.New("unterminated Markdown code fence")
	}
	inner := trimmed[len(fence) : len(trimmed)-len(fence)]
	if newline := strings.IndexByte(inner, '\n'); newline >= 0 {
		language := strings.TrimSpace(inner[:newline])
		if language != "" && !strings.EqualFold(language, "json") {
			return "", fmt.Errorf("unexpected code fence language %q", truncateRunes(language, 40))
		}
		inner = inner[newline+1:]
	} else if len(inner) >= len("json") && strings.EqualFold(inner[:len("json")], "json") {
		inner = inner[len("json"):]
	}
	inner = strings.TrimSpace(inner)
	if strings.HasPrefix(inner, fence) {
		return "", errors.New("more than one surrounding Markdown code fence")
	}
	return inner, nil
}
