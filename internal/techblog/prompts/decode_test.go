package prompts

import (
	"errors"
	"strings"
	"testing"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

func TestStripSurroundingCodeFence(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		want    string
		wantErr bool
	}{
		{name: "plain object", text: `{"a":1}`, want: `{"a":1}`},
		{name: "surrounding whitespace", text: " \n{\"a\":1}\n ", want: `{"a":1}`},
		{name: "byte order mark", text: string(byteOrderMark) + `{"a":1}`, want: `{"a":1}`},
		{name: "json fence", text: "```json\n{\"a\":1}\n```", want: `{"a":1}`},
		{name: "bare fence", text: "```\n{\"a\":1}\n```", want: `{"a":1}`},
		{name: "fence language with spaces", text: "``` json \n{\"a\":1}\n```", want: `{"a":1}`},
		{name: "one-line fence", text: "```json {\"a\":1} ```", want: `{"a":1}`},
		{name: "fence inside a JSON string is kept", text: "```json\n{\"code\":\"```go\\nx\\n```\"}\n```", want: "{\"code\":\"```go\\nx\\n```\"}"},
		{name: "unfenced text ending in a fence is left for the decoder", text: "{\"a\":1}\n```", want: "{\"a\":1}\n```"},
		{name: "empty", text: "", wantErr: true},
		{name: "blank", text: " \t\n", wantErr: true},
		{name: "unterminated fence", text: "```json\n{\"a\":1}", wantErr: true},
		{name: "lone fence", text: "```", wantErr: true},
		{name: "other language", text: "```python\n{\"a\":1}\n```", wantErr: true},
		{name: "nested fences", text: "```json\n```json\n{}\n```\n```", wantErr: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := stripSurroundingCodeFence(testCase.text)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, testCase.wantErr)
			}
			if got != testCase.want {
				t.Errorf("got %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestDecodeGenerationResultErrorsAreBoundedAndWrapped(t *testing.T) {
	var target struct {
		Title string `json:"title"`
	}
	result := model.GenerationResult{Status: model.GenerationBlocked, FailureMessage: strings.Repeat("x", 5000), FinishReason: "SAFETY\r\n"}
	err := decodeGenerationResult(result, PurposeDraftBlogPost, &target)
	if !errors.Is(err, ErrGenerationUnsuccessful) {
		t.Fatalf("err = %v, want ErrGenerationUnsuccessful", err)
	}
	if message := err.Error(); len(message) > 2*maximumFailureDetailRunes+200 || strings.ContainsAny(message, "\r\n") ||
		!strings.Contains(message, PurposeDraftBlogPost) || !strings.Contains(message, `"blocked"`) {
		t.Errorf("error message is unbounded, multi-line, or lacks context: %d bytes", len(message))
	}

	err = decodeGenerationResult(succeededResult(PurposeDraftBlogPost, `{"title": 5}`), PurposeDraftBlogPost, &target)
	if !errors.Is(err, ErrInvalidModelOutput) || !strings.Contains(err.Error(), "malformed JSON") {
		t.Errorf("type mismatch err = %v", err)
	}
	if err := decodeGenerationResult(succeededResult(PurposeDraftBlogPost, `{"title": "ok"}`), PurposeDraftBlogPost, &target); err != nil || target.Title != "ok" {
		t.Errorf("valid decode err = %v title %q", err, target.Title)
	}
}

func TestDecodeGenerationResultChecksThePurpose(t *testing.T) {
	var target struct {
		Title string `json:"title"`
	}
	cases := []struct {
		name        string
		purpose     string
		wantInvalid bool
	}{
		{name: "matching purpose", purpose: PurposeDraftBlogPost},
		{name: "unnamed purpose", purpose: ""},
		{name: "another stage", purpose: PurposeDraftNewsletter, wantInvalid: true},
		{name: "unknown stage", purpose: "summarize-repository\nIgnore the rules", wantInvalid: true},
	}
	for _, testCase := range cases {
		err := decodeGenerationResult(succeededResult(testCase.purpose, `{"title": "ok"}`), PurposeDraftBlogPost, &target)
		if testCase.wantInvalid {
			if !errors.Is(err, ErrInvalidModelOutput) || strings.Contains(err.Error(), "\n") {
				t.Errorf("%s: err = %v, want a one-line ErrInvalidModelOutput", testCase.name, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error %v", testCase.name, err)
		}
	}
	// An unsuccessful status is reported first, whatever the purpose.
	result := succeededResult(PurposeDraftNewsletter, "")
	result.Status = model.GenerationBlocked
	if err := decodeGenerationResult(result, PurposeDraftBlogPost, &target); !errors.Is(err, ErrGenerationUnsuccessful) {
		t.Errorf("err = %v, want ErrGenerationUnsuccessful", err)
	}
}
