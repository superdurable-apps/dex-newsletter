package blogpost

import (
	"context"
	"errors"

	"github.com/superdurable-apps/dex-newsletter/internal/content"

	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	// ErrInvalidEditorLink rejects a missing or forged editor token before any Dex call.
	ErrInvalidEditorLink = errors.New("this editor link is invalid")
	// ErrUnknownRun reports a run that does not exist.
	ErrUnknownRun = errors.New("this blog post run was not found")
)

// EditorService is the application boundary for the pre-publish editor.
type EditorService struct {
	client *dex.Client
	flow   *Flow
	links  EditorLinks
}

func NewEditorService(client *dex.Client, flow *Flow, links EditorLinks) *EditorService {
	return &EditorService{client: client, flow: flow, links: links}
}

// Get returns the draft and its published rendering.
func (service *EditorService) Get(ctx context.Context, flowID, token string) (EditableDraft, error) {
	if !service.links.Verify(flowID, token) {
		return EditableDraft{}, ErrInvalidEditorLink
	}
	var view EditableDraft
	if err := service.client.InvokeRPC(ctx, flowID, service.flow.GetDraftForEditing, nil, &view); err != nil {
		return EditableDraft{}, knownRunError(err)
	}
	return view, nil
}

// Preview renders unsaved edits.
func (service *EditorService) Preview(ctx context.Context, flowID, token string, blog content.BlogDraft, newsletter content.NewsletterDraft) (DraftPreview, error) {
	if !service.links.Verify(flowID, token) {
		return DraftPreview{}, ErrInvalidEditorLink
	}
	var preview DraftPreview
	input := PreviewDraftEditsInput{Blog: blog, Newsletter: newsletter}
	if err := service.client.InvokeRPC(ctx, flowID, service.flow.PreviewDraftEdits, input, &preview); err != nil {
		return DraftPreview{}, knownRunError(err)
	}
	return preview, nil
}

// Save stores the edits as the next draft version.
func (service *EditorService) Save(ctx context.Context, flowID, token string, input SaveDraftEditsInput) (DraftEditResult, error) {
	if !service.links.Verify(flowID, token) {
		return DraftEditResult{}, ErrInvalidEditorLink
	}
	var result DraftEditResult
	err := service.client.InvokeRPC(ctx, flowID, service.flow.SaveDraftEdits, input, &result)
	return service.reconcile(ctx, flowID, result, err)
}

// Approve approves baseVersion and starts delivery.
func (service *EditorService) Approve(ctx context.Context, flowID, token string, baseVersion int64) (DraftEditResult, error) {
	if !service.links.Verify(flowID, token) {
		return DraftEditResult{}, ErrInvalidEditorLink
	}
	var result DraftEditResult
	err := service.client.InvokeRPC(ctx, flowID, service.flow.ApproveEditedDraft, ApproveEditedDraftInput{BaseVersion: baseVersion}, &result)
	return service.reconcile(ctx, flowID, result, err)
}

// reconcile turns a closed run into "not in review". Locked RPCs need an active run and report a
// completed one as not active or not found, while the read-only view still answers for it.
func (service *EditorService) reconcile(ctx context.Context, flowID string, result DraftEditResult, err error) (DraftEditResult, error) {
	var inactive *dex.FlowNotActiveError
	var missing *dex.FlowNotFoundError
	if err == nil || (!errors.As(err, &inactive) && !errors.As(err, &missing)) {
		return result, err
	}
	var view EditableDraft
	if viewErr := service.client.InvokeRPC(ctx, flowID, service.flow.GetDraftForEditing, nil, &view); viewErr != nil {
		return DraftEditResult{}, knownRunError(viewErr)
	}
	return DraftEditResult{Outcome: OutcomeNotInReview, DraftVersion: view.DraftVersion, Message: "This draft is " + view.Status + ", so it can no longer be changed."}, nil
}

// knownRunError maps a missing run to ErrUnknownRun. The Client reports it as not found or, for
// every RPC, as not active; a read-only RPC still answers for a completed run, so on the view
// and preview paths either error means the run does not exist.
func knownRunError(err error) error {
	var missing *dex.FlowNotFoundError
	var inactive *dex.FlowNotActiveError
	if errors.As(err, &missing) || errors.As(err, &inactive) {
		return ErrUnknownRun
	}
	return err
}
