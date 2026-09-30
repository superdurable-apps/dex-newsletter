package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/superdurable-apps/dex-newsletter/internal/api/generated"
	"github.com/superdurable-apps/dex-newsletter/internal/blogpost"
	"github.com/superdurable-apps/dex-newsletter/internal/content"
)

const (
	testRunID = "blog-post-T-C1-1.0"
	testToken = "0123456789abcdef0123456789abcdef"
)

// fakeDrafts records each call and answers with the configured result or error.
type fakeDrafts struct {
	view    blogpost.EditableDraft
	preview blogpost.DraftPreview
	result  blogpost.DraftEditResult
	err     error

	flowID, token string
	blog          content.BlogDraft
	newsletter    content.NewsletterDraft
	saved         blogpost.SaveDraftEditsInput
	baseVersion   int64
}

func (drafts *fakeDrafts) Get(_ context.Context, flowID, token string) (blogpost.EditableDraft, error) {
	drafts.flowID, drafts.token = flowID, token
	return drafts.view, drafts.err
}

func (drafts *fakeDrafts) Preview(_ context.Context, flowID, token string, blog content.BlogDraft, newsletter content.NewsletterDraft) (blogpost.DraftPreview, error) {
	drafts.flowID, drafts.token, drafts.blog, drafts.newsletter = flowID, token, blog, newsletter
	return drafts.preview, drafts.err
}

func (drafts *fakeDrafts) Save(_ context.Context, flowID, token string, input blogpost.SaveDraftEditsInput) (blogpost.DraftEditResult, error) {
	drafts.flowID, drafts.token, drafts.saved = flowID, token, input
	return drafts.result, drafts.err
}

func (drafts *fakeDrafts) Approve(_ context.Context, flowID, token string, baseVersion int64) (blogpost.DraftEditResult, error) {
	drafts.flowID, drafts.token, drafts.baseVersion = flowID, token, baseVersion
	return drafts.result, drafts.err
}

type fakeNewsletter struct{}

func (fakeNewsletter) Subscribe(context.Context, string) (bool, error) { return false, nil }

func (fakeNewsletter) Unsubscribe(context.Context, string, string) (bool, error) { return false, nil }

func draftHandler(drafts *fakeDrafts) *Handler {
	return &Handler{drafts: drafts, newsletter: fakeNewsletter{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func requestBlog() generated.BlogDraft {
	return generated.BlogDraft{
		Title: "Connectors ship", Subtitle: "Retries and more", Slug: "connectors-ship", Summary: "The short version.", Closing: "Thanks.",
		Sections: []generated.BlogSection{
			{Heading: "What changed", Paragraphs: []string{"First.", "Second."}, Bullets: []string{"Retry on 429"}},
			{Heading: "Next", Paragraphs: []string{"Soon."}, Bullets: []string{"a", "b"}},
		},
		Highlights: []generated.BlogHighlight{
			{Title: "Retry", Description: "Fewer failures.", URL: "https://example.com/acme/connectors/pull/1"},
			{Title: "Docs", Description: "New guide."},
		},
	}
}

func requestNewsletter() generated.NewsletterDraft {
	return generated.NewsletterDraft{
		Subject: "This week at Acme", Preheader: "In short", Intro: "Hello readers.", Closing: "Read the post.",
		Items: []generated.NewsletterItem{{Title: "Retry", Summary: "Fewer failures."}, {Title: "Docs", Summary: "New guide."}},
	}
}

func contentBlog() content.BlogDraft {
	return content.BlogDraft{
		Title: "Connectors ship", Subtitle: "Retries and more", Slug: "connectors-ship", Summary: "The short version.", Closing: "Thanks.",
		Sections: []content.BlogSection{
			{Heading: "What changed", Paragraphs: []string{"First.", "Second."}, Bullets: []string{"Retry on 429"}},
			{Heading: "Next", Paragraphs: []string{"Soon."}, Bullets: []string{"a", "b"}},
		},
		Highlights: []content.BlogHighlight{
			{Title: "Retry", Description: "Fewer failures.", URL: "https://example.com/acme/connectors/pull/1"},
			{Title: "Docs", Description: "New guide."},
		},
	}
}

func contentNewsletter() content.NewsletterDraft {
	return content.NewsletterDraft{
		Subject: "This week at Acme", Preheader: "In short", Intro: "Hello readers.", Closing: "Read the post.",
		Items: []content.NewsletterItem{{Title: "Retry", Summary: "Fewer failures."}, {Title: "Docs", Summary: "New guide."}},
	}
}

// errorResponseOf returns the HTTP status each generated error response type encodes to, and its body.
func errorResponseOf(t *testing.T, response any) (int, generated.ErrorResponse) {
	t.Helper()
	switch typed := response.(type) {
	case *generated.GetDraftForbidden:
		return http.StatusForbidden, generated.ErrorResponse(*typed)
	case *generated.GetDraftNotFound:
		return http.StatusNotFound, generated.ErrorResponse(*typed)
	case *generated.GetDraftServiceUnavailable:
		return http.StatusServiceUnavailable, generated.ErrorResponse(*typed)
	case *generated.PreviewDraftForbidden:
		return http.StatusForbidden, generated.ErrorResponse(*typed)
	case *generated.PreviewDraftNotFound:
		return http.StatusNotFound, generated.ErrorResponse(*typed)
	case *generated.PreviewDraftServiceUnavailable:
		return http.StatusServiceUnavailable, generated.ErrorResponse(*typed)
	case *generated.SaveDraftBadRequest:
		return http.StatusBadRequest, generated.ErrorResponse(*typed)
	case *generated.SaveDraftForbidden:
		return http.StatusForbidden, generated.ErrorResponse(*typed)
	case *generated.SaveDraftNotFound:
		return http.StatusNotFound, generated.ErrorResponse(*typed)
	case *generated.SaveDraftConflict:
		return http.StatusConflict, generated.ErrorResponse(*typed)
	case *generated.SaveDraftServiceUnavailable:
		return http.StatusServiceUnavailable, generated.ErrorResponse(*typed)
	case *generated.ApproveDraftForbidden:
		return http.StatusForbidden, generated.ErrorResponse(*typed)
	case *generated.ApproveDraftNotFound:
		return http.StatusNotFound, generated.ErrorResponse(*typed)
	case *generated.ApproveDraftConflict:
		return http.StatusConflict, generated.ErrorResponse(*typed)
	case *generated.ApproveDraftServiceUnavailable:
		return http.StatusServiceUnavailable, generated.ErrorResponse(*typed)
	}
	t.Fatalf("response %T is not an error response", response)
	return 0, generated.ErrorResponse{}
}

// serviceErrors are the service failures every editor operation maps to the same responses.
var serviceErrors = []struct {
	name   string
	err    error
	status int
	code   string
}{
	{"invalid link", blogpost.ErrInvalidEditorLink, http.StatusForbidden, "invalid_editor_link"},
	{"unknown run", fmt.Errorf("load draft: %w", blogpost.ErrUnknownRun), http.StatusNotFound, "unknown_run"},
	{"Dex unavailable", errors.New("dial tcp dex.example.com:443: connection refused"), http.StatusServiceUnavailable, "editor_unavailable"},
}

func assertServiceError(t *testing.T, response any, status int, code string) {
	t.Helper()
	gotStatus, body := errorResponseOf(t, response)
	if gotStatus != status || body.Error != code || body.Message == "" || strings.Contains(body.Message, "dex.example.com") {
		t.Fatalf("response = %d %+v, want %d %q with a user-facing message", gotStatus, body, status, code)
	}
}

func TestGetDraft(t *testing.T) {
	drafts := &fakeDrafts{view: blogpost.EditableDraft{
		Status: "awaiting-review", Editable: true, DraftVersion: 3, RevisionCount: 1,
		Blog: contentBlog(), Newsletter: contentNewsletter(), BlogHTML: "<html>blog</html>", NewsletterHTML: "<html>email</html>",
	}}
	response, err := draftHandler(drafts).GetDraft(context.Background(), generated.GetDraftParams{RunId: testRunID, Token: testToken})
	if err != nil {
		t.Fatal(err)
	}
	view, ok := response.(*generated.DraftEditorView)
	if !ok {
		t.Fatalf("response = %T", response)
	}
	want := &generated.DraftEditorView{
		RunId: testRunID, Status: "awaiting-review", Editable: true, DraftVersion: 3, RevisionCount: 1,
		Blog: requestBlog(), Newsletter: requestNewsletter(), BlogHtml: "<html>blog</html>", NewsletterHtml: "<html>email</html>",
	}
	if !reflect.DeepEqual(view, want) {
		t.Fatalf("view = %+v\nwant   %+v", view, want)
	}
	if drafts.flowID != testRunID || drafts.token != testToken {
		t.Fatalf("service called with %q, %q", drafts.flowID, drafts.token)
	}

	for _, testCase := range serviceErrors {
		t.Run(testCase.name, func(t *testing.T) {
			response, err := draftHandler(&fakeDrafts{err: testCase.err}).GetDraft(context.Background(), generated.GetDraftParams{RunId: testRunID, Token: testToken})
			if err != nil {
				t.Fatal(err)
			}
			assertServiceError(t, response, testCase.status, testCase.code)
		})
	}
}

func TestGetDraftReturnsEmptyArrays(t *testing.T) {
	drafts := &fakeDrafts{view: blogpost.EditableDraft{
		Status: "sent", DraftVersion: 2,
		Blog:       content.BlogDraft{Title: "T", Sections: []content.BlogSection{{Heading: "H"}}},
		Newsletter: content.NewsletterDraft{Subject: "S"},
	}}
	response, err := draftHandler(drafts).GetDraft(context.Background(), generated.GetDraftParams{RunId: testRunID, Token: testToken})
	if err != nil {
		t.Fatal(err)
	}
	view := response.(*generated.DraftEditorView)
	section := view.Blog.Sections[0]
	if view.Editable || section.Paragraphs == nil || section.Bullets == nil || view.Blog.Highlights == nil || view.Newsletter.Items == nil {
		t.Fatalf("view = %+v", view)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"paragraphs":[]`, `"bullets":[]`, `"highlights":[]`, `"items":[]`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("view JSON lacks %s: %s", want, encoded)
		}
	}

	response, err = draftHandler(&fakeDrafts{view: blogpost.EditableDraft{Status: "researching"}}).GetDraft(context.Background(), generated.GetDraftParams{RunId: testRunID, Token: testToken})
	if err != nil {
		t.Fatal(err)
	}
	if view := response.(*generated.DraftEditorView); view.Blog.Sections == nil || view.Blog.Highlights == nil || view.Newsletter.Items == nil {
		t.Fatalf("a run without a draft returned nil arrays: %+v", view)
	}
}

func TestPreviewDraft(t *testing.T) {
	drafts := &fakeDrafts{preview: blogpost.DraftPreview{Valid: true, BlogHTML: "<html>blog</html>", NewsletterHTML: "<html>email</html>"}}
	request := &generated.DraftPreviewRequest{Token: testToken, Blog: requestBlog(), Newsletter: requestNewsletter()}
	response, err := draftHandler(drafts).PreviewDraft(context.Background(), request, generated.PreviewDraftParams{RunId: testRunID})
	if err != nil {
		t.Fatal(err)
	}
	want := &generated.DraftPreview{Valid: true, BlogHtml: "<html>blog</html>", NewsletterHtml: "<html>email</html>"}
	if !reflect.DeepEqual(response, want) {
		t.Fatalf("preview = %+v", response)
	}
	if drafts.flowID != testRunID || drafts.token != testToken || !reflect.DeepEqual(drafts.blog, contentBlog()) || !reflect.DeepEqual(drafts.newsletter, contentNewsletter()) {
		t.Fatalf("service called with %q, %q, %+v, %+v", drafts.flowID, drafts.token, drafts.blog, drafts.newsletter)
	}

	invalid := &fakeDrafts{preview: blogpost.DraftPreview{Message: "Title is required"}}
	response, err = draftHandler(invalid).PreviewDraft(context.Background(), request, generated.PreviewDraftParams{RunId: testRunID})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(response, &generated.DraftPreview{Message: generated.NewOptString("Title is required")}) {
		t.Fatalf("invalid preview = %+v", response)
	}

	for _, testCase := range serviceErrors {
		t.Run(testCase.name, func(t *testing.T) {
			response, err := draftHandler(&fakeDrafts{err: testCase.err}).PreviewDraft(context.Background(), request, generated.PreviewDraftParams{RunId: testRunID})
			if err != nil {
				t.Fatal(err)
			}
			assertServiceError(t, response, testCase.status, testCase.code)
		})
	}
}

func TestPreviewDraftAcceptsEmptyLists(t *testing.T) {
	drafts := &fakeDrafts{preview: blogpost.DraftPreview{Message: "keep at least one section"}}
	request := &generated.DraftPreviewRequest{Token: testToken, Blog: generated.BlogDraft{Title: "T"}, Newsletter: generated.NewsletterDraft{Subject: "S"}}
	if _, err := draftHandler(drafts).PreviewDraft(context.Background(), request, generated.PreviewDraftParams{RunId: testRunID}); err != nil {
		t.Fatal(err)
	}
	if drafts.blog.Title != "T" || len(drafts.blog.Sections) != 0 || len(drafts.blog.Highlights) != 0 || drafts.newsletter.Subject != "S" || len(drafts.newsletter.Items) != 0 {
		t.Fatalf("service called with %+v, %+v", drafts.blog, drafts.newsletter)
	}
}

func TestSaveDraft(t *testing.T) {
	request := &generated.DraftSaveRequest{Token: testToken, BaseVersion: 4, Blog: requestBlog(), Newsletter: requestNewsletter()}
	drafts := &fakeDrafts{result: blogpost.DraftEditResult{Outcome: blogpost.OutcomeSaved, DraftVersion: 5}}
	response, err := draftHandler(drafts).SaveDraft(context.Background(), request, generated.SaveDraftParams{RunId: testRunID})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(response, &generated.DraftChangeResult{Outcome: generated.DraftChangeResultOutcomeSaved, DraftVersion: 5}) {
		t.Fatalf("response = %#v", response)
	}
	wantInput := blogpost.SaveDraftEditsInput{BaseVersion: 4, Blog: contentBlog(), Newsletter: contentNewsletter()}
	if drafts.flowID != testRunID || drafts.token != testToken || !reflect.DeepEqual(drafts.saved, wantInput) {
		t.Fatalf("service called with %q, %q, %+v", drafts.flowID, drafts.token, drafts.saved)
	}

	refusals := []struct {
		outcome, message string
		status           int
		code             string
	}{
		{blogpost.OutcomeInvalid, "Title is required", http.StatusBadRequest, "invalid_draft"},
		{blogpost.OutcomeDraftChanged, "The draft changed since you opened it. Reload to see the new version.", http.StatusConflict, "draft_changed"},
		{blogpost.OutcomeNotInReview, "This draft is sent, so it can no longer be changed.", http.StatusConflict, "not_in_review"},
	}
	for _, refusal := range refusals {
		t.Run(refusal.outcome, func(t *testing.T) {
			drafts := &fakeDrafts{result: blogpost.DraftEditResult{Outcome: refusal.outcome, Message: refusal.message, DraftVersion: 4}}
			response, err := draftHandler(drafts).SaveDraft(context.Background(), request, generated.SaveDraftParams{RunId: testRunID})
			if err != nil {
				t.Fatal(err)
			}
			status, body := errorResponseOf(t, response)
			if status != refusal.status || body != (generated.ErrorResponse{Error: refusal.code, Message: refusal.message}) {
				t.Fatalf("response = %d %+v", status, body)
			}
		})
	}
	for _, testCase := range serviceErrors {
		t.Run(testCase.name, func(t *testing.T) {
			response, err := draftHandler(&fakeDrafts{err: testCase.err}).SaveDraft(context.Background(), request, generated.SaveDraftParams{RunId: testRunID})
			if err != nil {
				t.Fatal(err)
			}
			assertServiceError(t, response, testCase.status, testCase.code)
		})
	}
}

func TestApproveDraft(t *testing.T) {
	request := &generated.DraftApprovalRequest{Token: testToken, BaseVersion: 5}
	drafts := &fakeDrafts{result: blogpost.DraftEditResult{Outcome: blogpost.OutcomeApproved, DraftVersion: 5}}
	response, err := draftHandler(drafts).ApproveDraft(context.Background(), request, generated.ApproveDraftParams{RunId: testRunID})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(response, &generated.DraftChangeResult{Outcome: generated.DraftChangeResultOutcomeApproved, DraftVersion: 5}) {
		t.Fatalf("response = %#v", response)
	}
	if drafts.flowID != testRunID || drafts.token != testToken || drafts.baseVersion != 5 {
		t.Fatalf("service called with %q, %q, %d", drafts.flowID, drafts.token, drafts.baseVersion)
	}

	for _, outcome := range []string{blogpost.OutcomeDraftChanged, blogpost.OutcomeNotInReview} {
		t.Run(outcome, func(t *testing.T) {
			drafts := &fakeDrafts{result: blogpost.DraftEditResult{Outcome: outcome, Message: "Reload and review it before approving.", DraftVersion: 6}}
			response, err := draftHandler(drafts).ApproveDraft(context.Background(), request, generated.ApproveDraftParams{RunId: testRunID})
			if err != nil {
				t.Fatal(err)
			}
			status, body := errorResponseOf(t, response)
			if status != http.StatusConflict || body != (generated.ErrorResponse{Error: outcome, Message: "Reload and review it before approving."}) {
				t.Fatalf("response = %d %+v", status, body)
			}
		})
	}
	for _, testCase := range serviceErrors {
		t.Run(testCase.name, func(t *testing.T) {
			response, err := draftHandler(&fakeDrafts{err: testCase.err}).ApproveDraft(context.Background(), request, generated.ApproveDraftParams{RunId: testRunID})
			if err != nil {
				t.Fatal(err)
			}
			assertServiceError(t, response, testCase.status, testCase.code)
		})
	}
}

// TestDraftRoutesOverHTTP checks the generated router, decoders, and encoders end to end.
func TestDraftRoutesOverHTTP(t *testing.T) {
	blogJSON := `{"title":"T","subtitle":"","slug":"t","summary":"","closing":"","sections":[{"heading":"H","paragraphs":["p"],"bullets":[]}],"highlights":[]}`
	newsletterJSON := `{"subject":"S","preheader":"","intro":"","items":[],"closing":""}`
	saveBody := `{"token":"` + testToken + `","baseVersion":2,"blog":` + blogJSON + `,"newsletter":` + newsletterJSON + `}`
	cases := []struct {
		name, method, path, body string
		drafts                   *fakeDrafts
		status                   int
		contains                 []string
	}{
		{"get", http.MethodGet, "/api/drafts/" + testRunID + "?token=" + testToken, "",
			&fakeDrafts{view: blogpost.EditableDraft{Status: "awaiting-review", Editable: true, DraftVersion: 2, Blog: content.BlogDraft{Title: "T", Sections: []content.BlogSection{{Heading: "H"}}}}},
			http.StatusOK, []string{`"runId":"` + testRunID + `"`, `"draftVersion":2`, `"paragraphs":[]`, `"highlights":[]`, `"items":[]`}},
		{"get forbidden", http.MethodGet, "/api/drafts/" + testRunID + "?token=forged", "",
			&fakeDrafts{err: blogpost.ErrInvalidEditorLink}, http.StatusForbidden, []string{`"error":"invalid_editor_link"`}},
		{"get unknown", http.MethodGet, "/api/drafts/" + testRunID + "?token=" + testToken, "",
			&fakeDrafts{err: blogpost.ErrUnknownRun}, http.StatusNotFound, []string{`"error":"unknown_run"`}},
		{"preview invalid", http.MethodPost, "/api/drafts/" + testRunID + "/preview", `{"token":"` + testToken + `","blog":` + blogJSON + `,"newsletter":` + newsletterJSON + `}`,
			&fakeDrafts{preview: blogpost.DraftPreview{Message: "Title is required"}}, http.StatusOK, []string{`"valid":false`, `"message":"Title is required"`}},
		{"save", http.MethodPut, "/api/drafts/" + testRunID, saveBody,
			&fakeDrafts{result: blogpost.DraftEditResult{Outcome: blogpost.OutcomeSaved, DraftVersion: 3}}, http.StatusOK, []string{`"outcome":"saved"`, `"draftVersion":3`}},
		{"save invalid", http.MethodPut, "/api/drafts/" + testRunID, saveBody,
			&fakeDrafts{result: blogpost.DraftEditResult{Outcome: blogpost.OutcomeInvalid, Message: "Title is required"}}, http.StatusBadRequest, []string{`"error":"invalid_draft"`, `"message":"Title is required"`}},
		{"save changed", http.MethodPut, "/api/drafts/" + testRunID, saveBody,
			&fakeDrafts{result: blogpost.DraftEditResult{Outcome: blogpost.OutcomeDraftChanged, Message: "Reload."}}, http.StatusConflict, []string{`"error":"draft_changed"`}},
		{"save unavailable", http.MethodPut, "/api/drafts/" + testRunID, saveBody,
			&fakeDrafts{err: errors.New("dex down")}, http.StatusServiceUnavailable, []string{`"error":"editor_unavailable"`}},
		{"approve", http.MethodPost, "/api/drafts/" + testRunID + "/approval", `{"token":"` + testToken + `","baseVersion":3}`,
			&fakeDrafts{result: blogpost.DraftEditResult{Outcome: blogpost.OutcomeApproved, DraftVersion: 3}}, http.StatusOK, []string{`"outcome":"approved"`}},
		{"approve closed", http.MethodPost, "/api/drafts/" + testRunID + "/approval", `{"token":"` + testToken + `","baseVersion":3}`,
			&fakeDrafts{result: blogpost.DraftEditResult{Outcome: blogpost.OutcomeNotInReview, Message: "This draft is sent."}}, http.StatusConflict, []string{`"error":"not_in_review"`}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server, err := NewHandler(fakeNewsletter{}, testCase.drafts, ApplicationInfo{Name: "Acme Notes"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			var body io.Reader
			if testCase.body != "" {
				body = strings.NewReader(testCase.body)
			}
			request := httptest.NewRequest(testCase.method, testCase.path, body)
			if body != nil {
				request.Header.Set("Content-Type", "application/json")
			}
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, request)
			if recorder.Code != testCase.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, testCase.status, recorder.Body)
			}
			for _, want := range testCase.contains {
				if !strings.Contains(recorder.Body.String(), want) {
					t.Errorf("body lacks %s: %s", want, recorder.Body)
				}
			}
			if testCase.drafts.flowID != testRunID {
				t.Fatalf("service called for run %q", testCase.drafts.flowID)
			}
		})
	}
}
