package api

import (
	"context"
	"errors"

	"github.com/superdurable-apps/dex-newsletter/internal/api/generated"
	"github.com/superdurable-apps/dex-newsletter/internal/blogpost"
	"github.com/superdurable-apps/dex-newsletter/internal/content"
)

// Drafts is the pre-publish editor boundary.
type Drafts interface {
	Get(ctx context.Context, flowID, token string) (blogpost.EditableDraft, error)
	Preview(ctx context.Context, flowID, token string, blog content.BlogDraft) (blogpost.DraftPreview, error)
	Save(ctx context.Context, flowID, token string, input blogpost.SaveDraftEditsInput) (blogpost.DraftEditResult, error)
	Approve(ctx context.Context, flowID, token string, baseVersion int64) (blogpost.DraftEditResult, error)
}

const (
	forbiddenMessage   = "This editor link is invalid. Use the link from the Slack review thread or Dex Web."
	notFoundMessage    = "This blog post run was not found."
	unavailableMessage = "The editor is unavailable right now. Try again in a moment."
)

func (handler *Handler) GetDraft(ctx context.Context, params generated.GetDraftParams) (generated.GetDraftRes, error) {
	view, err := handler.drafts.Get(ctx, params.RunId, params.Token)
	switch {
	case err == nil:
		return editorView(params.RunId, view), nil
	case errors.Is(err, blogpost.ErrInvalidEditorLink):
		response := generated.GetDraftForbidden(errorResponse("invalid_editor_link", forbiddenMessage))
		return &response, nil
	case errors.Is(err, blogpost.ErrUnknownRun):
		response := generated.GetDraftNotFound(errorResponse("unknown_run", notFoundMessage))
		return &response, nil
	}
	handler.logger.Error("load draft failed", "error", err.Error())
	response := generated.GetDraftServiceUnavailable(errorResponse("editor_unavailable", unavailableMessage))
	return &response, nil
}

func (handler *Handler) PreviewDraft(ctx context.Context, request *generated.DraftPreviewRequest, params generated.PreviewDraftParams) (generated.PreviewDraftRes, error) {
	preview, err := handler.drafts.Preview(ctx, params.RunId, request.Token, blogDraft(request.Blog))
	switch {
	case err == nil:
		response := &generated.DraftPreview{Valid: preview.Valid, BlogHtml: preview.BlogHTML, EmailSubject: preview.EmailSubject, EmailHtml: preview.EmailHTML}
		if preview.Message != "" {
			response.Message = generated.NewOptString(preview.Message)
		}
		return response, nil
	case errors.Is(err, blogpost.ErrInvalidEditorLink):
		response := generated.PreviewDraftForbidden(errorResponse("invalid_editor_link", forbiddenMessage))
		return &response, nil
	case errors.Is(err, blogpost.ErrUnknownRun):
		response := generated.PreviewDraftNotFound(errorResponse("unknown_run", notFoundMessage))
		return &response, nil
	}
	handler.logger.Error("preview draft failed", "error", err.Error())
	response := generated.PreviewDraftServiceUnavailable(errorResponse("editor_unavailable", unavailableMessage))
	return &response, nil
}

func (handler *Handler) SaveDraft(ctx context.Context, request *generated.DraftSaveRequest, params generated.SaveDraftParams) (generated.SaveDraftRes, error) {
	result, err := handler.drafts.Save(ctx, params.RunId, request.Token, blogpost.SaveDraftEditsInput{
		BaseVersion: request.BaseVersion, Blog: blogDraft(request.Blog),
	})
	switch {
	case err == nil && result.Outcome == blogpost.OutcomeSaved:
		return &generated.DraftChangeResult{Outcome: generated.DraftChangeResultOutcomeSaved, DraftVersion: result.DraftVersion}, nil
	case err == nil && result.Outcome == blogpost.OutcomeInvalid:
		response := generated.SaveDraftBadRequest(errorResponse("invalid_draft", result.Message))
		return &response, nil
	case err == nil:
		response := generated.SaveDraftConflict(errorResponse(result.Outcome, result.Message))
		return &response, nil
	case errors.Is(err, blogpost.ErrInvalidEditorLink):
		response := generated.SaveDraftForbidden(errorResponse("invalid_editor_link", forbiddenMessage))
		return &response, nil
	case errors.Is(err, blogpost.ErrUnknownRun):
		response := generated.SaveDraftNotFound(errorResponse("unknown_run", notFoundMessage))
		return &response, nil
	}
	handler.logger.Error("save draft failed", "error", err.Error())
	response := generated.SaveDraftServiceUnavailable(errorResponse("editor_unavailable", unavailableMessage))
	return &response, nil
}

func (handler *Handler) ApproveDraft(ctx context.Context, request *generated.DraftApprovalRequest, params generated.ApproveDraftParams) (generated.ApproveDraftRes, error) {
	result, err := handler.drafts.Approve(ctx, params.RunId, request.Token, request.BaseVersion)
	switch {
	case err == nil && result.Outcome == blogpost.OutcomeApproved:
		return &generated.DraftChangeResult{Outcome: generated.DraftChangeResultOutcomeApproved, DraftVersion: result.DraftVersion}, nil
	case err == nil:
		response := generated.ApproveDraftConflict(errorResponse(result.Outcome, result.Message))
		return &response, nil
	case errors.Is(err, blogpost.ErrInvalidEditorLink):
		response := generated.ApproveDraftForbidden(errorResponse("invalid_editor_link", forbiddenMessage))
		return &response, nil
	case errors.Is(err, blogpost.ErrUnknownRun):
		response := generated.ApproveDraftNotFound(errorResponse("unknown_run", notFoundMessage))
		return &response, nil
	}
	handler.logger.Error("approve draft failed", "error", err.Error())
	response := generated.ApproveDraftServiceUnavailable(errorResponse("editor_unavailable", unavailableMessage))
	return &response, nil
}

func editorView(runID string, view blogpost.EditableDraft) *generated.DraftEditorView {
	return &generated.DraftEditorView{
		RunId: runID, Status: view.Status, Editable: view.Editable, DraftVersion: view.DraftVersion, RevisionCount: view.RevisionCount,
		Blog: generatedBlogDraft(view.Blog), BlogHtml: view.BlogHTML, EmailSubject: view.EmailSubject, EmailHtml: view.EmailHTML,
	}
}

func blogDraft(draft generated.BlogDraft) content.BlogDraft {
	converted := content.BlogDraft{Title: draft.Title, Subtitle: draft.Subtitle, Slug: draft.Slug, Summary: draft.Summary, Closing: draft.Closing}
	for _, section := range draft.Sections {
		converted.Sections = append(converted.Sections, content.BlogSection{Heading: section.Heading, Paragraphs: section.Paragraphs, Bullets: section.Bullets})
	}
	for _, highlight := range draft.Highlights {
		converted.Highlights = append(converted.Highlights, content.BlogHighlight{Title: highlight.Title, Description: highlight.Description, URL: highlight.URL})
	}
	return converted
}

func generatedBlogDraft(draft content.BlogDraft) generated.BlogDraft {
	converted := generated.BlogDraft{
		Title: draft.Title, Subtitle: draft.Subtitle, Slug: draft.Slug, Summary: draft.Summary, Closing: draft.Closing,
		Sections: []generated.BlogSection{}, Highlights: []generated.BlogHighlight{},
	}
	for _, section := range draft.Sections {
		converted.Sections = append(converted.Sections, generated.BlogSection{Heading: section.Heading, Paragraphs: orEmpty(section.Paragraphs), Bullets: orEmpty(section.Bullets)})
	}
	for _, highlight := range draft.Highlights {
		converted.Highlights = append(converted.Highlights, generated.BlogHighlight{Title: highlight.Title, Description: highlight.Description, URL: highlight.URL})
	}
	return converted
}

func orEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
