package techblog

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/config"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
	"github.com/superdurable/dex-connectors-library/connectors/google/gemini"
	"github.com/superdurable/dex-connectors-library/sdkgo"
	"github.com/superdurable/dex/sdk-go/dex"
)

// LanguageModelGenerationFlowType is the stable Flow type of the
// provider-neutral language-model seam.
const LanguageModelGenerationFlowType = "LanguageModelGenerationFlow"

// GeminiConnectionName is the static Dex Web connection name for the Gemini
// API key.
const GeminiConnectionName = "gemini-api"

const generateContentWithGeminiStepType = "GenerateContentWithGemini"

var (
	// dex:indexed-attribute attribute-key:generation-purpose index-key:generation-purpose index-type:keyword value-type:string description:"Language-model stage served by this run"
	generationPurpose = dex.DefineAttribute[string](
		"generation-purpose",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword}),
	)
	// dex:indexed-attribute attribute-key:generation-status index-key:generation-status index-type:keyword value-type:string description:"Provider-neutral generation outcome"
	generationStatus = dex.DefineAttribute[string](
		"generation-status",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword}),
	)
	generationRequest     = dex.DefineAttribute[model.GenerationRequest]("generation-request")
	generationProvider    = dex.DefineAttribute[string]("generation-provider")
	generationModel       = dex.DefineAttribute[string]("generation-model")
	generationTotalTokens = dex.DefineAttribute[int64]("generation-total-tokens")
)

// LanguageModelGenerationFlow runs one provider-neutral generation request
// through the configured provider's Connector Step. Adding a provider means
// adding its Connector Step and completion Step here and listing it in
// config.SupportedLanguageModelProviders; calling Flows do not change.
type LanguageModelGenerationFlow struct {
	dex.FlowDefaults
	geminiConnection gemini.Connection
}

// NewLanguageModelGenerationFlow constructs the Flow with its provider
// connections.
func NewLanguageModelGenerationFlow(geminiConnection gemini.Connection) *LanguageModelGenerationFlow {
	return &LanguageModelGenerationFlow{geminiConnection: geminiConnection}
}

// GetFlowType returns the stable Flow type.
func (*LanguageModelGenerationFlow) GetFlowType() string { return LanguageModelGenerationFlowType }

// GetSteps registers every Step reachable from RouteGenerationRequest.
func (flow *LanguageModelGenerationFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(RouteGenerationRequest{}),
		dex.DefineStep(gemini.NewGenerateContentStep(gemini.GenerateContentStepConfig[model.GenerationRequest]{
			StepType:       generateContentWithGeminiStepType,
			ConnectionName: GeminiConnectionName,
			Annotations: sdkgo.StepAnnotations{
				GroupID: "generation", GroupLabel: "Generation",
				Explanation: "Generate the requested content with Gemini.",
			},
			Connection:          flow.geminiConnection,
			MapToOperationInput: mapGenerationRequestToGeminiRequest,
			Generated:           sdkgo.GoTo(CompleteGeminiGeneration{}),
			Truncated:           sdkgo.GoTo(CompleteGeminiGeneration{}),
			Blocked:             sdkgo.GoTo(CompleteGeminiGeneration{}),
			ProviderRejected:    sdkgo.GoTo(CompleteGeminiGeneration{}),
			InvalidResponse:     sdkgo.GoTo(CompleteGeminiGeneration{}),
			Defect:              sdkgo.GoTo(CompleteGeminiGeneration{}),
		})),
		dex.DefineStep(CompleteGeminiGeneration{}),
	}
}

// GetRPCs registers the Dex Web read models.
func (flow *LanguageModelGenerationFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

// GetPersistenceSchema registers every Attribute.
func (*LanguageModelGenerationFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{
		generationPurpose, generationStatus, generationRequest, generationProvider,
		generationModel, generationTotalTokens,
	}}
}

// dex:field attribute-key:generation-provider value-type:string editable:false description:"Language-model provider"
// dex:field attribute-key:generation-model value-type:string editable:false description:"Model"
func (*LanguageModelGenerationFlow) GetDexSummary(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	provider, err := optionalValue(generationProvider.Get(ctx))
	if err != nil {
		return nil, err
	}
	modelName, err := optionalValue(generationModel.Get(ctx))
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"generation-provider": provider,
		"generation-model":    modelName,
	}}, nil
}

// dex:field attribute-key:generation-purpose value-type:string editable:false description:"Stage" ui-slot:title
// dex:field attribute-key:generation-status value-type:string editable:false description:"Outcome" ui-slot:status
// dex:field attribute-key:generation-provider value-type:string editable:false description:"Provider"
// dex:field attribute-key:generation-model value-type:string editable:false description:"Model"
// dex:field attribute-key:generation-total-tokens value-type:int64 editable:false description:"Total tokens"
func (*LanguageModelGenerationFlow) GetDexDisplay(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	purpose, err := optionalValue(generationPurpose.Get(ctx))
	if err != nil {
		return nil, err
	}
	status, err := optionalValue(generationStatus.Get(ctx))
	if err != nil {
		return nil, err
	}
	provider, err := optionalValue(generationProvider.Get(ctx))
	if err != nil {
		return nil, err
	}
	modelName, err := optionalValue(generationModel.Get(ctx))
	if err != nil {
		return nil, err
	}
	totalTokens, err := optionalValue(generationTotalTokens.Get(ctx))
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"generation-purpose":      purpose,
		"generation-status":       status,
		"generation-provider":     provider,
		"generation-model":        modelName,
		"generation-total-tokens": totalTokens,
	}}, nil
}

// RouteGenerationRequest persists the request and selects the provider's
// Connector Step.
//
// dex:group group-id:generation group-label:"Generation"
// dex:explanation text:"Record the generation request and route it to the configured provider."
type RouteGenerationRequest struct {
	dex.StepDefaultsNoWaitFor[model.GenerationRequest]
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (RouteGenerationRequest) GetStepType() string { return "RouteGenerationRequest" }

// Execute routes the request by provider.
func (RouteGenerationRequest) Execute(ctx dex.Context, request model.GenerationRequest) (*dex.StepDecision, error) {
	if err := generationRequest.Set(ctx, request); err != nil {
		return nil, err
	}
	if err := generationPurpose.Set(ctx, request.Purpose); err != nil {
		return nil, err
	}
	if err := generationProvider.Set(ctx, request.Provider); err != nil {
		return nil, err
	}
	if err := generationModel.Set(ctx, request.Model); err != nil {
		return nil, err
	}
	if _, err := decodeResponseJSONSchema(request.ResponseJSONSchema); err != nil {
		result := model.GenerationResult{
			Purpose: request.Purpose, Status: model.GenerationDefect,
			Provider: request.Provider, Model: request.Model,
			FailureMessage: "the response JSON schema is not a JSON object: " + safeErrorText(err),
		}
		if err := generationStatus.Set(ctx, string(result.Status)); err != nil {
			return nil, err
		}
		return dex.GracefulComplete(result), nil
	}
	switch strings.TrimSpace(request.Provider) {
	case config.ProviderGemini:
		return dex.GoTo(sdkgo.StepRef[model.GenerationRequest](generateContentWithGeminiStepType), request), nil
	default:
		result := model.GenerationResult{
			Purpose: request.Purpose, Status: model.GenerationUnsupportedProvider,
			Provider: request.Provider, Model: request.Model,
			FailureMessage: "no Connector Step is registered for this language-model provider",
		}
		if err := generationStatus.Set(ctx, string(result.Status)); err != nil {
			return nil, err
		}
		return dex.GracefulComplete(result), nil
	}
}

// CompleteGeminiGeneration maps the Gemini operation result to the
// provider-neutral GenerationResult.
//
// dex:group group-id:generation group-label:"Generation"
// dex:explanation text:"Complete with the provider-neutral result of the Gemini generation."
type CompleteGeminiGeneration struct {
	dex.StepDefaultsNoWaitFor[gemini.GenerateContentResult]
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (CompleteGeminiGeneration) GetStepType() string { return "CompleteGeminiGeneration" }

// Execute completes the Flow with the mapped result.
func (CompleteGeminiGeneration) Execute(ctx dex.Context, operationResult gemini.GenerateContentResult) (*dex.StepDecision, error) {
	request, err := generationRequest.Get(ctx)
	if err != nil {
		return nil, err
	}
	result := mapGeminiResultToGenerationResult(request, operationResult)
	if err := generationStatus.Set(ctx, string(result.Status)); err != nil {
		return nil, err
	}
	if err := generationTotalTokens.Set(ctx, int64(result.Usage.TotalTokens)); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(result), nil
}

func mapGenerationRequestToGeminiRequest(request model.GenerationRequest) gemini.GenerateContentRequest {
	geminiRequest := gemini.GenerateContentRequest{
		Model:             request.Model,
		SystemInstruction: request.SystemInstruction,
		Contents: []gemini.Content{{
			Role:  "user",
			Parts: []gemini.Part{{Text: request.Prompt}},
		}},
		Temperature:     request.Temperature,
		MaxOutputTokens: request.MaxOutputTokens,
		ThinkingBudget:  request.ThinkingBudget,
	}
	// RouteGenerationRequest has already rejected invalid schema text.
	if schema, err := decodeResponseJSONSchema(request.ResponseJSONSchema); err == nil && schema != nil {
		geminiRequest.ResponseMIMEType = "application/json"
		geminiRequest.ResponseJSONSchema = schemaWithoutGeminiRejectedBounds(schema)
	}
	return geminiRequest
}

// geminiRejectedSchemaBounds are JSON Schema length, count, and range bounds.
// Gemini structured output rejects schemas that combine enough of them with
// HTTP 400 INVALID_ARGUMENT ("Request contains an invalid argument"), observed
// 2026-09-26 on gemini-3.5-flash and gemini-3.5-flash-lite. The prompts
// parsers enforce every bound after generation, so they are safe to drop.
var geminiRejectedSchemaBounds = map[string]bool{
	"maxLength": true, "minLength": true, "maxItems": true, "minItems": true, "minimum": true, "maximum": true,
}

// schemaWithoutGeminiRejectedBounds copies a schema node without those bounds.
// Property names are never treated as keywords.
func schemaWithoutGeminiRejectedBounds(node map[string]any) map[string]any {
	copied := make(map[string]any, len(node))
	for keyword, value := range node {
		if geminiRejectedSchemaBounds[keyword] {
			continue
		}
		switch keyword {
		case "properties":
			properties, isMap := value.(map[string]any)
			if !isMap {
				copied[keyword] = value
				continue
			}
			copiedProperties := make(map[string]any, len(properties))
			for name, child := range properties {
				if childSchema, isSchema := child.(map[string]any); isSchema {
					copiedProperties[name] = schemaWithoutGeminiRejectedBounds(childSchema)
				} else {
					copiedProperties[name] = child
				}
			}
			copied[keyword] = copiedProperties
		case "items":
			if itemSchema, isSchema := value.(map[string]any); isSchema {
				copied[keyword] = schemaWithoutGeminiRejectedBounds(itemSchema)
			} else {
				copied[keyword] = value
			}
		default:
			copied[keyword] = value
		}
	}
	return copied
}

// decodeResponseJSONSchema decodes schema text; empty text means free-form
// output and returns a nil schema.
func decodeResponseJSONSchema(schemaText string) (map[string]any, error) {
	if strings.TrimSpace(schemaText) == "" {
		return nil, nil
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(schemaText), &schema); err != nil {
		return nil, err
	}
	if schema == nil {
		return nil, fmt.Errorf("schema is null")
	}
	return schema, nil
}

func mapGeminiResultToGenerationResult(request model.GenerationRequest, operationResult gemini.GenerateContentResult) model.GenerationResult {
	response := operationResult.Value
	result := model.GenerationResult{
		Purpose:      request.Purpose,
		Provider:     config.ProviderGemini,
		Model:        request.Model,
		FinishReason: response.FinishReason,
		Usage: model.GenerationUsage{
			PromptTokens:   response.Usage.PromptTokens,
			OutputTokens:   response.Usage.CandidateTokens,
			ThoughtsTokens: response.Usage.ThoughtsTokens,
			TotalTokens:    response.Usage.TotalTokens,
		},
	}
	if response.ModelVersion != "" {
		result.Model = response.ModelVersion
	}
	switch operationResult.Branch {
	case gemini.GenerateContentBranchGenerated:
		result.Status = model.GenerationSucceeded
		result.Text = response.Text
	case gemini.GenerateContentBranchTruncated:
		result.Status = model.GenerationTruncated
		result.Text = response.Text
		result.FailureMessage = "the model stopped at the output token limit"
	case gemini.GenerateContentBranchBlocked:
		result.Status = model.GenerationBlocked
		result.FailureMessage = "the provider blocked the prompt or response (" + firstNonEmpty(response.BlockReason, response.FinishReason) + ")"
	case gemini.GenerateContentBranchProviderRejected:
		result.Status = model.GenerationRejected
	case gemini.GenerateContentBranchInvalidResponse:
		result.Status = model.GenerationInvalidResponse
	default:
		result.Status = model.GenerationDefect
	}
	if result.FailureMessage == "" && operationResult.Failure != nil {
		result.FailureMessage = string(operationResult.Failure.Kind) + ": " + operationResult.Failure.Message
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "unspecified"
}

var _ dex.Flow = (*LanguageModelGenerationFlow)(nil)
var _ dex.Step[model.GenerationRequest] = RouteGenerationRequest{}
var _ dex.Step[gemini.GenerateContentResult] = CompleteGeminiGeneration{}
var _ dex.RPC[dex.None, map[string]any] = (*LanguageModelGenerationFlow)(nil).GetDexSummary
var _ dex.RPC[dex.None, map[string]any] = (*LanguageModelGenerationFlow)(nil).GetDexDisplay
