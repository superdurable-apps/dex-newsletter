package techblog

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

func TestGeminiMappingDropsSchemaBoundsGeminiRejects(t *testing.T) {
	schema := `{"type":"object","required":["maxLength","items"],"properties":{
		"maxLength":{"type":"string","maxLength":10,"description":"a property named like a keyword"},
		"items":{"type":"array","maxItems":3,"minItems":1,"items":{"type":"string","minLength":1,"maxLength":400}},
		"count":{"type":"integer","minimum":0,"maximum":9,"enum":[1,2]}}}`
	request := mapGenerationRequestToGeminiRequest(model.GenerationRequest{Model: "gemini-test", Prompt: "p", ResponseJSONSchema: schema})
	want := map[string]any{
		"type": "object", "required": []any{"maxLength", "items"},
		"properties": map[string]any{
			"maxLength": map[string]any{"type": "string", "description": "a property named like a keyword"},
			"items":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"count":     map[string]any{"type": "integer", "enum": []any{float64(1), float64(2)}},
		},
	}
	if !reflect.DeepEqual(request.ResponseJSONSchema, want) {
		got, _ := json.Marshal(request.ResponseJSONSchema)
		t.Fatalf("schema sent to Gemini = %s", got)
	}
	if request.ResponseMIMEType != "application/json" {
		t.Fatalf("MIME type = %q", request.ResponseMIMEType)
	}
	if free := mapGenerationRequestToGeminiRequest(model.GenerationRequest{Model: "gemini-test", Prompt: "p"}); free.ResponseJSONSchema != nil || free.ResponseMIMEType != "" {
		t.Fatalf("free-text request carried a schema: %+v", free)
	}
}
