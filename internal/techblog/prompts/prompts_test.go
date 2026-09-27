package prompts

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/config"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

func TestPurposeConstantsAreStable(t *testing.T) {
	cases := []struct{ got, want string }{
		{PurposeInterpretRequest, "interpret-request"},
		{PurposeSummarizeRepository, "summarize-repository"},
		{PurposeSynthesizeResearch, "synthesize-research"},
		{PurposeDraftBlogPost, "draft-blog-post"},
		{PurposeDraftNewsletter, "draft-newsletter"},
	}
	for _, testCase := range cases {
		if testCase.got != testCase.want {
			t.Errorf("purpose = %q, want %q", testCase.got, testCase.want)
		}
	}
}

// builderCase builds one stage's request from a configuration and a flag
// that swaps every untrusted input for adversarial text.
type builderCase struct {
	name     string
	purpose  string
	stage    func(config.LanguageModelConfiguration) config.StageModelConfiguration
	sections []string
	build    func(configuration config.ProcessConfiguration, adversarial bool) model.GenerationRequest
}

func builderCases() []builderCase {
	blogSections := []string{sectionPublication, sectionResearchRequest, sectionResearchBrief}
	return []builderCase{
		{
			name: "interpret request", purpose: PurposeInterpretRequest,
			stage: func(languageModel config.LanguageModelConfiguration) config.StageModelConfiguration {
				return languageModel.InterpretRequest
			},
			sections: []string{sectionRepositoryCatalog, sectionRequest},
			build: func(configuration config.ProcessConfiguration, adversarial bool) model.GenerationRequest {
				text := "Write a blog post about connectors over the past two weeks"
				if adversarial {
					text = adversarialText()
					configuration.Research.Repositories[0].Description = adversarialText()
					configuration.Research.Repositories[0].Topics = []string{adversarialText()}
				}
				return BuildRequestInterpretationRequest(configuration, fixtureNewsletterRequest(text), fixtureUntil)
			},
		},
		{
			name: "summarize repository", purpose: PurposeSummarizeRepository,
			stage: func(languageModel config.LanguageModelConfiguration) config.StageModelConfiguration {
				return languageModel.SummarizeRepository
			},
			sections: []string{sectionResearchRequest, sectionEvidence},
			build: func(configuration config.ProcessConfiguration, adversarial bool) model.GenerationRequest {
				research, evidence := fixtureResearchRequest(), fixtureEvidence()
				if adversarial {
					research.Topic, research.Instructions, research.Selection.Reason = adversarialText(), adversarialText(), adversarialText()
					research.Selection.PathHints = []string{adversarialText()}
					for index := range evidence.PullRequests {
						pullRequest := &evidence.PullRequests[index]
						pullRequest.Title, pullRequest.Body, pullRequest.AuthorLogin = adversarialText(), adversarialText(), adversarialText()
						pullRequest.Labels = []string{adversarialText()}
						pullRequest.Files = []model.FileChangeEvidence{{Filename: adversarialText(), Status: adversarialText(), Patch: adversarialText()}}
					}
					evidence.Commits[0].Message = adversarialText()
					evidence.Notes = []string{adversarialText()}
				}
				return BuildRepositorySummaryRequest(configuration, research, evidence)
			},
		},
		{
			name: "synthesize research", purpose: PurposeSynthesizeResearch,
			stage: func(languageModel config.LanguageModelConfiguration) config.StageModelConfiguration {
				return languageModel.SynthesizeResearch
			},
			sections: []string{sectionResearchRequest, sectionRepositoryDigests},
			build: func(configuration config.ProcessConfiguration, adversarial bool) model.GenerationRequest {
				interpretation, digests := fixtureInterpretation(), fixtureDigests()
				if adversarial {
					interpretation.Topic, interpretation.Instructions, interpretation.Audience = adversarialText(), adversarialText(), adversarialText()
					digests[0].Summary = adversarialText()
					digests[0].Notes = []string{adversarialText()}
					digests[0].Highlights[0].Title, digests[0].Highlights[0].HowItWorks = adversarialText(), adversarialText()
					digests[0].Highlights[0].References[0].Label = adversarialText()
				}
				return BuildResearchSynthesisRequest(configuration, interpretation, fixtureWindow(), digests)
			},
		},
		{
			name: "draft blog post", purpose: PurposeDraftBlogPost,
			stage: func(languageModel config.LanguageModelConfiguration) config.StageModelConfiguration {
				return languageModel.DraftBlogPost
			},
			sections: blogSections,
			build: func(configuration config.ProcessConfiguration, adversarial bool) model.GenerationRequest {
				interpretation, brief := fixtureInterpretation(), fixtureBrief()
				if adversarial {
					configuration.Blog.StyleGuide, configuration.Blog.SiteName = adversarialText(), adversarialText()
					interpretation.Instructions = adversarialText()
					brief.Headline, brief.Overview = adversarialText(), adversarialText()
					brief.OpenQuestions = []string{adversarialText()}
				}
				return BuildBlogDraftRequest(configuration, interpretation, fixtureWindow(), brief, nil)
			},
		},
		{
			name: "revise blog post", purpose: PurposeDraftBlogPost,
			stage: func(languageModel config.LanguageModelConfiguration) config.StageModelConfiguration {
				return languageModel.DraftBlogPost
			},
			sections: append(append([]string{}, blogSections...), sectionPreviousDraft, sectionEditorFeedback),
			build: func(configuration config.ProcessConfiguration, adversarial bool) model.GenerationRequest {
				revision := &BlogRevision{RevisionNumber: 2, EditorFeedback: "Shorten the intro.", PreviousDraft: fixturePost()}
				if adversarial {
					revision.EditorFeedback = adversarialText()
					revision.PreviousDraft.Title = adversarialText()
					revision.PreviousDraft.Sections[0].Blocks[0].Text = adversarialText()
					revision.PreviousDraft.Sections[0].Blocks = append(revision.PreviousDraft.Sections[0].Blocks,
						model.BlogBlock{Type: model.BlogBlockCode, Language: "html", Code: adversarialText()})
				}
				return BuildBlogDraftRequest(configuration, fixtureInterpretation(), fixtureWindow(), fixtureBrief(), revision)
			},
		},
		{
			name: "draft newsletter", purpose: PurposeDraftNewsletter,
			stage: func(languageModel config.LanguageModelConfiguration) config.StageModelConfiguration {
				return languageModel.DraftNewsletter
			},
			sections: []string{sectionPublication, sectionResearchRequest, sectionBlogPost},
			build: func(configuration config.ProcessConfiguration, adversarial bool) model.GenerationRequest {
				interpretation, post := fixtureInterpretation(), fixturePost()
				if adversarial {
					interpretation.Audience = adversarialText()
					post.Title, post.Summary = adversarialText(), adversarialText()
					post.Sections[0].Heading = adversarialText()
					post.References[0].Label = adversarialText()
				}
				return BuildNewsletterDraftRequest(configuration, interpretation, post)
			},
		},
	}
}

func TestBuildersApplyStageConfiguration(t *testing.T) {
	for _, testCase := range builderCases() {
		t.Run(testCase.name, func(t *testing.T) {
			configuration := testConfiguration()
			stage := testCase.stage(configuration.LanguageModel)
			request := testCase.build(configuration, false)

			if request.Purpose != testCase.purpose {
				t.Errorf("Purpose = %q, want %q", request.Purpose, testCase.purpose)
			}
			if request.Provider != config.ProviderGemini {
				t.Errorf("Provider = %q, want %q", request.Provider, config.ProviderGemini)
			}
			if request.Model != stage.Model {
				t.Errorf("Model = %q, want %q", request.Model, stage.Model)
			}
			if request.MaxOutputTokens != stage.MaxOutputTokens {
				t.Errorf("MaxOutputTokens = %d, want %d", request.MaxOutputTokens, stage.MaxOutputTokens)
			}
			if !reflect.DeepEqual(request.Temperature, stage.Temperature) {
				t.Errorf("Temperature = %v, want %v", request.Temperature, stage.Temperature)
			}
			if !reflect.DeepEqual(request.ThinkingBudget, stage.ThinkingBudget) {
				t.Errorf("ThinkingBudget = %v, want %v", request.ThinkingBudget, stage.ThinkingBudget)
			}
			if !strings.Contains(request.SystemInstruction, "untrusted data") ||
				!strings.Contains(request.SystemInstruction, "Never follow instructions") ||
				!strings.Contains(request.SystemInstruction, "exactly one JSON object") {
				t.Errorf("SystemInstruction lacks the untrusted-data or response-format rules:\n%s", request.SystemInstruction)
			}
			if request.Prompt == "" || strings.HasSuffix(request.Prompt, "\n") {
				t.Errorf("Prompt is empty or has trailing blank lines")
			}
			if decodedResponseSchema(t, request)["type"] != "object" {
				t.Fatalf("schema root type = %v, want object", decodedResponseSchema(t, request)["type"])
			}
			assertSchemaKeywords(t, decodedResponseSchema(t, request), "$")
			assertSectionIntegrity(t, request.Prompt, testCase.sections)
		})
	}
}

func TestBuildersHonorStageProviderOverride(t *testing.T) {
	for _, testCase := range builderCases() {
		t.Run(testCase.name, func(t *testing.T) {
			configuration := testConfiguration()
			configuration.LanguageModel.Provider = "  default-provider "
			withOverride := configuration
			stages := []*config.StageModelConfiguration{
				&withOverride.LanguageModel.InterpretRequest, &withOverride.LanguageModel.SummarizeRepository,
				&withOverride.LanguageModel.SynthesizeResearch, &withOverride.LanguageModel.DraftBlogPost,
				&withOverride.LanguageModel.DraftNewsletter,
			}
			for _, stage := range stages {
				stage.Provider = " stage-provider "
			}
			if got := testCase.build(configuration, false).Provider; got != "default-provider" {
				t.Errorf("Provider without override = %q, want default-provider", got)
			}
			if got := testCase.build(withOverride, false).Provider; got != "stage-provider" {
				t.Errorf("Provider with override = %q, want stage-provider", got)
			}
		})
	}
}

func TestBuildersAreDeterministic(t *testing.T) {
	for _, testCase := range builderCases() {
		for _, adversarial := range []bool{false, true} {
			t.Run(testCase.name, func(t *testing.T) {
				first := testCase.build(testConfiguration(), adversarial)
				second := testCase.build(testConfiguration(), adversarial)
				if !reflect.DeepEqual(first, second) {
					t.Fatalf("identical input produced different requests")
				}
				firstJSON, err := json.Marshal(first)
				if err != nil {
					t.Fatal(err)
				}
				secondJSON, err := json.Marshal(second)
				if err != nil {
					t.Fatal(err)
				}
				if string(firstJSON) != string(secondJSON) {
					t.Fatalf("identical input produced different encoded requests")
				}
			})
		}
	}
}

func TestBuildersDelimitUntrustedText(t *testing.T) {
	for _, testCase := range builderCases() {
		t.Run(testCase.name, func(t *testing.T) {
			request := testCase.build(testConfiguration(), true)
			assertSectionIntegrity(t, request.Prompt, testCase.sections)
			if !strings.Contains(request.Prompt, injectionMarker) {
				t.Fatalf("adversarial text did not reach the prompt at all; the test is not exercising the sections")
			}
			if strings.Contains(promptOutsideSections(t, request.Prompt, testCase.sections), injectionMarker) {
				t.Errorf("untrusted text leaked into trusted prompt prose")
			}
			if strings.Contains(request.SystemInstruction, injectionMarker) {
				t.Errorf("untrusted text leaked into the system instruction")
			}
		})
	}
}

func TestBuildersDoNotAliasConfigurationOrSchemas(t *testing.T) {
	for _, testCase := range builderCases() {
		t.Run(testCase.name, func(t *testing.T) {
			configuration := testConfiguration()
			request := testCase.build(configuration, false)
			stage := testCase.stage(configuration.LanguageModel)
			if stage.Temperature != nil {
				*stage.Temperature = 1.99
				if *request.Temperature == 1.99 {
					t.Errorf("request Temperature aliases the configuration")
				}
			}
			if stage.ThinkingBudget != nil {
				*stage.ThinkingBudget = -1
				if *request.ThinkingBudget == -1 {
					t.Errorf("request ThinkingBudget aliases the configuration")
				}
			}
			first := decodedResponseSchema(t, request)
			first["type"] = "mutated"
			delete(first, "properties")
			if again := testCase.build(testConfiguration(), false); decodedResponseSchema(t, again)["type"] != "object" {
				t.Errorf("schemas share state between calls")
			}
		})
	}
}

func TestSystemInstructionDescribesTheActualEscapes(t *testing.T) {
	rules := untrustedSectionRules()
	for _, character := range []rune{'<', '>', '&'} {
		escape := jsonEscapedCharacter(character)
		if len(escape) != 6 || !strings.HasPrefix(escape, `\u00`) {
			t.Fatalf("escape for %q = %q, want a six-character JSON escape", character, escape)
		}
		if !strings.Contains(rules, escape) {
			t.Errorf("rules do not mention the escape %q", escape)
		}
	}
}

// parserCase runs one parser and carries a valid answer for it.
type parserCase struct {
	name      string
	purpose   string
	validText string
	parse     func(result model.GenerationResult) (any, error)
}

func parserCases(t *testing.T) []parserCase {
	return []parserCase{
		{
			name: "request interpretation", purpose: PurposeInterpretRequest,
			validText: jsonText(t, map[string]any{
				"understood": true, "clarificationQuestion": "", "topic": "connectors", "instructions": "", "audience": "",
				"lookbackDays": 14, "repositorySelections": []any{map[string]any{
					"repository": map[string]any{"owner": "superdurable", "name": "dex-connectors-library"},
					"pathHints":  []string{"connectors/"}, "reason": "Connectors.",
				}},
			}),
			parse: func(result model.GenerationResult) (any, error) {
				return ParseRequestInterpretation(result, fixtureNewsletterRequest("connectors please"), testConfiguration())
			},
		},
		{
			name: "repository digest", purpose: PurposeSummarizeRepository,
			validText: jsonText(t, map[string]any{"relevant": true, "summary": "Retries.", "highlights": []any{map[string]any{
				"title": "Retries", "whatChanged": "a", "howItWorks": "b", "whyItMatters": "c",
				"references": []any{map[string]any{"label": "PR", "url": connectorsPullRequest101URL}},
			}}}),
			parse: func(result model.GenerationResult) (any, error) {
				return ParseRepositoryChangeDigest(result, fixtureResearchRequest(), fixtureEvidence())
			},
		},
		{
			name: "research brief", purpose: PurposeSynthesizeResearch,
			validText: jsonText(t, map[string]any{"hasNotableChanges": true, "headline": "H", "overview": "O", "openQuestions": []string{},
				"highlights": []any{map[string]any{"title": "Retries", "whatChanged": "a", "howItWorks": "b", "whyItMatters": "c",
					"references": []any{map[string]any{"label": "PR", "url": connectorsPullRequest101URL}}}}}),
			parse: func(result model.GenerationResult) (any, error) { return ParseResearchBrief(result, fixtureDigests()) },
		},
		{
			name: "blog post", purpose: PurposeDraftBlogPost,
			validText: jsonText(t, fixturePost()),
			parse:     func(result model.GenerationResult) (any, error) { return ParseBlogPost(result, fixtureBrief()) },
		},
		{
			name: "newsletter draft", purpose: PurposeDraftNewsletter,
			validText: jsonText(t, map[string]any{"subject": "S", "preheader": "P", "intro": "I", "closing": "C",
				"highlights": []any{map[string]any{"title": "T", "text": "X"}}}),
			parse: func(result model.GenerationResult) (any, error) { return ParseNewsletterDraft(result) },
		},
	}
}

func TestParsersAcceptValidOutputAndOneSurroundingCodeFence(t *testing.T) {
	for _, testCase := range parserCases(t) {
		want, err := testCase.parse(succeededResult(testCase.purpose, testCase.validText))
		if err != nil {
			t.Fatalf("%s: valid output rejected: %v", testCase.name, err)
		}
		wrappings := map[string]string{
			"json fence":          "```json\n" + testCase.validText + "\n```",
			"bare fence":          "```\n" + testCase.validText + "\n```",
			"uppercase fence":     "  \n```JSON\n" + testCase.validText + "\n```\n  ",
			"one-line fence":      "```json" + testCase.validText + "```",
			"byte order mark":     string(byteOrderMark) + testCase.validText,
			"surrounding spacing": "\n\t " + testCase.validText + " \n",
		}
		for wrappingName, text := range wrappings {
			got, err := testCase.parse(succeededResult(testCase.purpose, text))
			if err != nil {
				t.Errorf("%s with %s: %v", testCase.name, wrappingName, err)
				continue
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s with %s: got %+v, want %+v", testCase.name, wrappingName, got, want)
			}
		}
	}
}

func TestParsersRejectUnsuccessfulGeneration(t *testing.T) {
	statuses := []model.GenerationStatus{
		model.GenerationTruncated, model.GenerationBlocked, model.GenerationRejected, model.GenerationInvalidResponse,
		model.GenerationDefect, model.GenerationUnsupportedProvider, "",
	}
	for _, testCase := range parserCases(t) {
		for _, status := range statuses {
			result := succeededResult(testCase.purpose, testCase.validText)
			result.Status = status
			result.FailureMessage = "provider said no\nwith a second line"
			result.FinishReason = "SAFETY"
			_, err := testCase.parse(result)
			if !errors.Is(err, ErrGenerationUnsuccessful) {
				t.Errorf("%s status %q: err = %v, want ErrGenerationUnsuccessful", testCase.name, status, err)
				continue
			}
			if errors.Is(err, ErrInvalidModelOutput) {
				t.Errorf("%s status %q: unsuccessful generation also reported as invalid output", testCase.name, status)
			}
			if message := err.Error(); !strings.Contains(message, string(status)) || !strings.Contains(message, "SAFETY") ||
				strings.Contains(message, "\n") {
				t.Errorf("%s status %q: error %q should name the status and finish reason on one line", testCase.name, status, message)
			}
		}
	}
}

func TestParsersRejectMalformedOutput(t *testing.T) {
	malformed := map[string]string{
		"empty":                   "",
		"whitespace":              " \n\t ",
		"prose":                   "Sure! Here is the JSON you asked for.",
		"unterminated object":     `{"title": "x"`,
		"top-level array":         `[{"title": "x"}]`,
		"top-level null":          "null",
		"top-level string":        `"text"`,
		"empty object":            "{}",
		"unknown field":           `{"unexpectedField": 1}`,
		"trailing object":         "{} {}",
		"trailing prose":          `{} and more`,
		"unterminated fence":      "```json\n{}",
		"other fence language":    "```yaml\n{}\n```",
		"double fence":            "```json\n```json\n{}\n```\n```",
		"empty fence":             "``````",
		"lone fence":              "```",
		"prose before JSON":       "Here: {}",
		"invalid escape sequence": `{"title": "\q"}`,
	}
	for _, testCase := range parserCases(t) {
		for name, text := range malformed {
			_, err := testCase.parse(succeededResult(testCase.purpose, text))
			if !errors.Is(err, ErrInvalidModelOutput) {
				t.Errorf("%s with %s: err = %v, want ErrInvalidModelOutput", testCase.name, name, err)
			}
			if errors.Is(err, ErrGenerationUnsuccessful) {
				t.Errorf("%s with %s: malformed output reported as unsuccessful generation", testCase.name, name)
			}
		}
	}
}

// decodedResponseSchema decodes a request's canonical JSON Schema text.
func decodedResponseSchema(t *testing.T, request model.GenerationRequest) map[string]any {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal([]byte(request.ResponseJSONSchema), &schema); err != nil {
		t.Fatalf("decode response schema: %v", err)
	}
	return normalizeDecodedSchema(schema).(map[string]any)
}

// normalizeDecodedSchema restores the Go shapes the schema builders use
// ([]string lists and int bounds) after a JSON round trip.
func normalizeDecodedSchema(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			typed[key] = normalizeDecodedSchema(child)
		}
		return typed
	case []any:
		strings := make([]string, 0, len(typed))
		for _, element := range typed {
			text, isText := element.(string)
			if !isText {
				for index := range typed {
					typed[index] = normalizeDecodedSchema(typed[index])
				}
				return typed
			}
			strings = append(strings, text)
		}
		return strings
	case float64:
		if typed == float64(int(typed)) {
			return int(typed)
		}
		return typed
	default:
		return value
	}
}
