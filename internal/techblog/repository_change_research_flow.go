package techblog

import (
	"errors"
	"fmt"
	"strings"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/config"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/prompts"
	github "github.com/superdurable/dex-connectors-library/connectors/github"
	"github.com/superdurable/dex-connectors-library/sdkgo"
	"github.com/superdurable/dex/sdk-go/dex"
)

// RepositoryChangeResearchFlowType is the stable Flow type of the
// per-repository research SubFlow.
const RepositoryChangeResearchFlowType = "RepositoryChangeResearchFlow"

// GitHubConnectionName is the static Dex Web connection name for GitHub.
const GitHubConnectionName = "github-account"

const (
	listMergedPullRequestsStepType = "ListMergedPullRequests"
	listPullRequestFilesStepType   = "ListPullRequestFiles"
	listRepositoryCommitsStepType  = "ListRepositoryCommits"

	githubMaximumPageSize       = 100
	pullRequestFilesPageSize    = 100
	maximumCommitMessageRunes   = 1000
	maximumFallbackHighlights   = 8
	maximumFallbackSummaryRunes = 400
	maximumCommitPathQueries    = 3
)

// Research status values shown in Dex Web.
const (
	researchStatusCollectingPullRequests = "collecting-pull-requests"
	researchStatusCollectingFiles        = "collecting-pull-request-files"
	researchStatusCollectingCommits      = "collecting-commits"
	researchStatusSummarizing            = "summarizing"
	researchStatusCompleted              = "completed"
)

var (
	// dex:indexed-attribute attribute-key:research-repository index-key:research-repository index-type:keyword value-type:string description:"Researched repository"
	researchRepository = dex.DefineAttribute[string](
		"research-repository",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword}),
	)
	// dex:indexed-attribute attribute-key:research-status index-key:research-status index-type:keyword value-type:string description:"Repository research progress"
	researchStatus = dex.DefineAttribute[string](
		"research-status",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword}),
	)
	researchRequest           = dex.DefineAttribute[model.RepositoryResearchRequest]("research-request")
	researchEvidence          = dex.DefineAttribute[model.RepositoryChangeEvidence]("research-evidence")
	pullRequestDetailCursor   = dex.DefineAttribute[int64]("pull-request-detail-cursor")
	commitPathCursor          = dex.DefineAttribute[int64]("commit-path-cursor")
	researchPullRequestCount  = dex.DefineAttribute[int64]("research-pull-request-count")
	researchCommitCount       = dex.DefineAttribute[int64]("research-commit-count")
	researchDigestAttribute   = dex.DefineAttribute[model.RepositoryChangeDigest]("research-digest")
	researchDigestDescription = dex.DefineAttribute[string]("research-digest-summary")
)

// MergedPullRequestPageRequest is the Step input for one page of merged pull
// requests.
type MergedPullRequestPageRequest struct {
	Repository model.RepositoryReference `json:"repository"`
	Window     model.ChangeWindow        `json:"window"`
	Page       int                       `json:"page"`
	PageSize   int                       `json:"pageSize"`
}

// PullRequestFilesPageRequest is the Step input for one page of files changed
// by one pull request.
type PullRequestFilesPageRequest struct {
	Repository model.RepositoryReference `json:"repository"`
	Number     int                       `json:"number"`
	Page       int                       `json:"page"`
	PageSize   int                       `json:"pageSize"`
}

// CommitPageRequest is the Step input for one page of commits.
type CommitPageRequest struct {
	Repository model.RepositoryReference `json:"repository"`
	Window     model.ChangeWindow        `json:"window"`
	Page       int                       `json:"page"`
	PageSize   int                       `json:"pageSize"`
	Path       string                    `json:"path,omitempty"`
}

// RepositoryChangeResearchFlow collects bounded evidence of recent changes in
// one repository and summarizes it through LanguageModelGenerationFlow.
type RepositoryChangeResearchFlow struct {
	dex.FlowDefaults
	configuration    config.ProcessConfiguration
	githubConnection github.Connection
	languageModel    *LanguageModelGenerationFlow
}

// NewRepositoryChangeResearchFlow constructs the Flow.
func NewRepositoryChangeResearchFlow(
	configuration config.ProcessConfiguration,
	githubConnection github.Connection,
	languageModel *LanguageModelGenerationFlow,
) *RepositoryChangeResearchFlow {
	if languageModel == nil {
		panic("language-model generation Flow is required")
	}
	return &RepositoryChangeResearchFlow{
		configuration: configuration, githubConnection: githubConnection, languageModel: languageModel,
	}
}

// GetFlowType returns the stable Flow type.
func (*RepositoryChangeResearchFlow) GetFlowType() string { return RepositoryChangeResearchFlowType }

// GetSteps registers every reachable Step.
func (flow *RepositoryChangeResearchFlow) GetSteps() []dex.StepDef {
	research := flow.configuration.Research
	return []dex.StepDef{
		dex.DefineStartStep(StartRepositoryResearch{research: research}),
		dex.DefineStep(github.NewListMergedPullRequestsStep(github.ListMergedPullRequestsStepConfig[MergedPullRequestPageRequest]{
			StepType:       listMergedPullRequestsStepType,
			ConnectionName: GitHubConnectionName,
			Annotations: sdkgo.StepAnnotations{
				GroupID: "pull-requests", GroupLabel: "Pull requests",
				Explanation: "List pull requests merged inside the change window.",
			},
			Connection: flow.githubConnection,
			MapToOperationInput: func(request MergedPullRequestPageRequest) github.ListMergedPullRequestsInput {
				return github.ListMergedPullRequestsInput{
					Owner: request.Repository.Owner, Repository: request.Repository.Name,
					MergedAfter: request.Window.Since, MergedBefore: request.Window.Until,
					PageSize: request.PageSize, Page: request.Page,
				}
			},
			Listed:               sdkgo.GoTo(RecordMergedPullRequestPage{research: research}),
			NotFound:             sdkgo.GoTo(RecordRepositoryNotFound{}),
			InsufficientScope:    sdkgo.GoTo(RecordMergedPullRequestFailure{research: research}),
			AuthorizationRevoked: sdkgo.GoTo(RecordMergedPullRequestFailure{research: research}),
			ProviderRejected:     sdkgo.GoTo(RecordMergedPullRequestFailure{research: research}),
			InvalidResponse:      sdkgo.GoTo(RecordMergedPullRequestFailure{research: research}),
			Defect:               sdkgo.GoTo(RecordMergedPullRequestFailure{research: research}),
		})),
		dex.DefineStep(RecordMergedPullRequestPage{research: research}),
		dex.DefineStep(RecordMergedPullRequestFailure{research: research}),
		dex.DefineStep(RecordRepositoryNotFound{}),
		dex.DefineStep(github.NewListPullRequestFilesStep(github.ListPullRequestFilesStepConfig[PullRequestFilesPageRequest]{
			StepType:       listPullRequestFilesStepType,
			ConnectionName: GitHubConnectionName,
			Annotations: sdkgo.StepAnnotations{
				GroupID: "pull-requests", GroupLabel: "Pull requests",
				Explanation: "List the files changed by one merged pull request.",
			},
			Connection: flow.githubConnection,
			MapToOperationInput: func(request PullRequestFilesPageRequest) github.ListPullRequestFilesInput {
				return github.ListPullRequestFilesInput{
					Owner: request.Repository.Owner, Repository: request.Repository.Name,
					Number: request.Number, PageSize: request.PageSize, Page: request.Page,
				}
			},
			Listed:               sdkgo.GoTo(RecordPullRequestFilePage{research: research}),
			NotFound:             sdkgo.GoTo(RecordPullRequestFilesFailure{research: research}),
			InsufficientScope:    sdkgo.GoTo(RecordPullRequestFilesFailure{research: research}),
			AuthorizationRevoked: sdkgo.GoTo(RecordPullRequestFilesFailure{research: research}),
			ProviderRejected:     sdkgo.GoTo(RecordPullRequestFilesFailure{research: research}),
			InvalidResponse:      sdkgo.GoTo(RecordPullRequestFilesFailure{research: research}),
			Defect:               sdkgo.GoTo(RecordPullRequestFilesFailure{research: research}),
		})),
		dex.DefineStep(RecordPullRequestFilePage{research: research}),
		dex.DefineStep(RecordPullRequestFilesFailure{research: research}),
		dex.DefineStep(github.NewListCommitsStep(github.ListCommitsStepConfig[CommitPageRequest]{
			StepType:       listRepositoryCommitsStepType,
			ConnectionName: GitHubConnectionName,
			Annotations: sdkgo.StepAnnotations{
				GroupID: "commits", GroupLabel: "Commits",
				Explanation: "List commits made inside the change window.",
			},
			Connection: flow.githubConnection,
			MapToOperationInput: func(request CommitPageRequest) github.ListCommitsInput {
				return github.ListCommitsInput{
					Owner: request.Repository.Owner, Repository: request.Repository.Name,
					Since: request.Window.Since, Until: request.Window.Until, Path: request.Path,
					PageSize: request.PageSize, Page: request.Page,
				}
			},
			Listed:               sdkgo.GoTo(RecordCommitPage{research: research}),
			NotFound:             sdkgo.GoTo(RecordCommitFailure{research: research}),
			InsufficientScope:    sdkgo.GoTo(RecordCommitFailure{research: research}),
			AuthorizationRevoked: sdkgo.GoTo(RecordCommitFailure{research: research}),
			ProviderRejected:     sdkgo.GoTo(RecordCommitFailure{research: research}),
			InvalidResponse:      sdkgo.GoTo(RecordCommitFailure{research: research}),
			Defect:               sdkgo.GoTo(RecordCommitFailure{research: research}),
		})),
		dex.DefineStep(RecordCommitPage{research: research}),
		dex.DefineStep(RecordCommitFailure{research: research}),
		dex.DefineStep(PrepareRepositorySummary{configuration: flow.configuration}),
		dex.DefineStep(SummarizeRepositoryChanges{languageModel: flow.languageModel}),
	}
}

// GetRPCs registers the Dex Web read models.
func (flow *RepositoryChangeResearchFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

// GetPersistenceSchema registers every Attribute.
func (*RepositoryChangeResearchFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{
		researchRepository, researchStatus, researchRequest, researchEvidence, pullRequestDetailCursor, commitPathCursor,
		researchPullRequestCount, researchCommitCount, researchDigestAttribute, researchDigestDescription,
	}}
}

// dex:field attribute-key:research-pull-request-count value-type:int64 editable:false description:"Merged pull requests"
// dex:field attribute-key:research-commit-count value-type:int64 editable:false description:"Commits"
func (*RepositoryChangeResearchFlow) GetDexSummary(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	pullRequestCount, err := optionalValue(researchPullRequestCount.Get(ctx))
	if err != nil {
		return nil, err
	}
	commitCount, err := optionalValue(researchCommitCount.Get(ctx))
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"research-pull-request-count": pullRequestCount,
		"research-commit-count":       commitCount,
	}}, nil
}

// dex:field attribute-key:research-repository value-type:string editable:false description:"Repository" ui-slot:title
// dex:field attribute-key:research-status value-type:string editable:false description:"Research progress" ui-slot:status
// dex:field attribute-key:research-digest-summary value-type:string editable:false description:"Summary" ui-slot:subtitle
// dex:field attribute-key:research-pull-request-count value-type:int64 editable:false description:"Merged pull requests"
// dex:field attribute-key:research-commit-count value-type:int64 editable:false description:"Commits"
// dex:field attribute-key:research-digest value-type:json editable:false description:"Repository change digest"
func (*RepositoryChangeResearchFlow) GetDexDisplay(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	repository, err := optionalValue(researchRepository.Get(ctx))
	if err != nil {
		return nil, err
	}
	status, err := optionalValue(researchStatus.Get(ctx))
	if err != nil {
		return nil, err
	}
	summary, err := optionalValue(researchDigestDescription.Get(ctx))
	if err != nil {
		return nil, err
	}
	pullRequestCount, err := optionalValue(researchPullRequestCount.Get(ctx))
	if err != nil {
		return nil, err
	}
	commitCount, err := optionalValue(researchCommitCount.Get(ctx))
	if err != nil {
		return nil, err
	}
	digest, err := optionalValue(researchDigestAttribute.Get(ctx))
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"research-repository":         repository,
		"research-status":             status,
		"research-digest-summary":     summary,
		"research-pull-request-count": pullRequestCount,
		"research-commit-count":       commitCount,
		"research-digest":             digest,
	}}, nil
}

// dex:group group-id:pull-requests group-label:"Pull requests"
// dex:explanation text:"Record the research request and list the first page of merged pull requests."
type StartRepositoryResearch struct {
	dex.StepDefaultsNoWaitFor[model.RepositoryResearchRequest]
	research config.ResearchConfiguration
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (StartRepositoryResearch) GetStepType() string { return "StartRepositoryResearch" }

// Execute initializes durable research state.
func (step StartRepositoryResearch) Execute(ctx dex.Context, request model.RepositoryResearchRequest) (*dex.StepDecision, error) {
	repository := request.Selection.Repository
	if err := researchRequest.Set(ctx, request); err != nil {
		return nil, err
	}
	if err := researchRepository.Set(ctx, repository.Owner+"/"+repository.Name); err != nil {
		return nil, err
	}
	if err := researchEvidence.Set(ctx, model.RepositoryChangeEvidence{Repository: repository}); err != nil {
		return nil, err
	}
	if err := setResearchCounts(ctx, 0, 0); err != nil {
		return nil, err
	}
	if err := commitPathCursor.Set(ctx, 0); err != nil {
		return nil, err
	}
	if err := researchStatus.Set(ctx, researchStatusCollectingPullRequests); err != nil {
		return nil, err
	}
	return dex.GoTo(sdkgo.StepRef[MergedPullRequestPageRequest](listMergedPullRequestsStepType), MergedPullRequestPageRequest{
		Repository: repository, Window: request.Window, Page: 1,
		PageSize: min(step.research.MaxMergedPullRequestsPerRepository, githubMaximumPageSize),
	}), nil
}

// dex:group group-id:pull-requests group-label:"Pull requests"
// dex:explanation text:"Store one page of merged pull requests and fetch the next page or the changed files."
type RecordMergedPullRequestPage struct {
	dex.StepDefaultsNoWaitFor[github.ListMergedPullRequestsResult]
	research config.ResearchConfiguration
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (RecordMergedPullRequestPage) GetStepType() string { return "RecordMergedPullRequestPage" }

// Execute appends the page within the configured bound.
func (step RecordMergedPullRequestPage) Execute(ctx dex.Context, result github.ListMergedPullRequestsResult) (*dex.StepDecision, error) {
	request, evidence, err := loadResearchState(ctx)
	if err != nil {
		return nil, err
	}
	limit := step.research.MaxMergedPullRequestsPerRepository
	evidence.PullRequestsListed = true
	for _, pullRequest := range result.Value.PullRequests {
		if len(evidence.PullRequests) >= limit {
			break
		}
		if containsPullRequest(evidence.PullRequests, pullRequest.Number) {
			continue
		}
		evidence.PullRequests = append(evidence.PullRequests, model.PullRequestEvidence{
			Number: pullRequest.Number, Title: pullRequest.Title, Body: pullRequest.Body,
			BodyTruncated: pullRequest.BodyTruncated, URL: pullRequest.URL, AuthorLogin: pullRequest.AuthorLogin,
			MergedAt: pullRequest.MergedAt, Labels: pullRequest.Labels,
		})
	}
	if result.Value.IncompleteResults {
		evidence.Notes = appendNote(evidence.Notes, "GitHub search reported incomplete results for merged pull requests.")
	}
	if err := saveEvidence(ctx, evidence); err != nil {
		return nil, err
	}
	if result.Value.NextPage > 0 && len(evidence.PullRequests) < limit {
		return dex.GoTo(sdkgo.StepRef[MergedPullRequestPageRequest](listMergedPullRequestsStepType), MergedPullRequestPageRequest{
			Repository: request.Selection.Repository, Window: request.Window, Page: result.Value.NextPage,
			PageSize: min(limit, githubMaximumPageSize),
		}), nil
	}
	if result.Value.NextPage > 0 {
		evidence.Notes = appendNote(evidence.Notes, fmt.Sprintf("Only the %d most recent merged pull requests were researched.", limit))
		if err := saveEvidence(ctx, evidence); err != nil {
			return nil, err
		}
	}
	if err := researchStatus.Set(ctx, researchStatusCollectingFiles); err != nil {
		return nil, err
	}
	filesRequest, hasPullRequest, err := nextPullRequestFilesRequest(ctx, step.research, request, evidence, 0)
	if err != nil {
		return nil, err
	}
	if hasPullRequest {
		return dex.GoTo(sdkgo.StepRef[PullRequestFilesPageRequest](listPullRequestFilesStepType), filesRequest), nil
	}
	if err := researchStatus.Set(ctx, researchStatusCollectingCommits); err != nil {
		return nil, err
	}
	return dex.GoTo(sdkgo.StepRef[CommitPageRequest](listRepositoryCommitsStepType), firstCommitPageRequest(step.research, request)), nil
}

// dex:group group-id:pull-requests group-label:"Pull requests"
// dex:explanation text:"Note that merged pull requests could not be listed and continue with commits."
type RecordMergedPullRequestFailure struct {
	dex.StepDefaultsNoWaitFor[github.ListMergedPullRequestsResult]
	research config.ResearchConfiguration
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (RecordMergedPullRequestFailure) GetStepType() string { return "RecordMergedPullRequestFailure" }

// Execute records the failure and continues with partial research.
func (step RecordMergedPullRequestFailure) Execute(ctx dex.Context, result github.ListMergedPullRequestsResult) (*dex.StepDecision, error) {
	request, evidence, err := loadResearchState(ctx)
	if err != nil {
		return nil, err
	}
	evidence.Notes = appendNote(evidence.Notes, "Merged pull requests could not be listed ("+describeQueryFailure(string(result.Branch), result.Failure)+").")
	if err := saveEvidence(ctx, evidence); err != nil {
		return nil, err
	}
	if err := researchStatus.Set(ctx, researchStatusCollectingCommits); err != nil {
		return nil, err
	}
	return dex.GoTo(sdkgo.StepRef[CommitPageRequest](listRepositoryCommitsStepType), firstCommitPageRequest(step.research, request)), nil
}

// dex:group group-id:pull-requests group-label:"Pull requests"
// dex:explanation text:"Complete with a not-found digest when the repository does not exist or is not visible."
type RecordRepositoryNotFound struct {
	dex.StepDefaultsNoWaitFor[github.ListMergedPullRequestsResult]
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (RecordRepositoryNotFound) GetStepType() string { return "RecordRepositoryNotFound" }

// Execute completes the research without evidence.
func (RecordRepositoryNotFound) Execute(ctx dex.Context, _ github.ListMergedPullRequestsResult) (*dex.StepDecision, error) {
	request, err := researchRequest.Get(ctx)
	if err != nil {
		return nil, err
	}
	digest := model.RepositoryChangeDigest{
		Repository: request.Selection.Repository, Status: model.RepositoryNotFound,
		Summary: "The repository was not found or is not visible to the GitHub connection.",
	}
	if err := recordRepositoryDigest(ctx, digest); err != nil {
		return nil, err
	}
	if err := researchStatus.Set(ctx, researchStatusCompleted); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(digest), nil
}

// dex:group group-id:pull-requests group-label:"Pull requests"
// dex:explanation text:"Store one page of changed files and continue with the next page or pull request."
type RecordPullRequestFilePage struct {
	dex.StepDefaultsNoWaitFor[github.ListPullRequestFilesResult]
	research config.ResearchConfiguration
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (RecordPullRequestFilePage) GetStepType() string { return "RecordPullRequestFilePage" }

// Execute attaches bounded file changes to the current pull request.
func (step RecordPullRequestFilePage) Execute(ctx dex.Context, result github.ListPullRequestFilesResult) (*dex.StepDecision, error) {
	request, evidence, err := loadResearchState(ctx)
	if err != nil {
		return nil, err
	}
	cursor, err := pullRequestDetailCursor.Get(ctx)
	if err != nil {
		return nil, err
	}
	if cursor < 0 || int(cursor) >= len(evidence.PullRequests) {
		return nil, fmt.Errorf("pull request detail cursor %d is outside %d pull requests", cursor, len(evidence.PullRequests))
	}
	pullRequest := &evidence.PullRequests[cursor]
	for _, file := range result.Value.Files {
		if len(pullRequest.Files) >= step.research.MaxFilesPerPullRequest {
			break
		}
		patch, patchTruncated := truncateRunes(file.Patch, step.research.MaxPatchCharactersPerFile)
		pullRequest.Files = append(pullRequest.Files, model.FileChangeEvidence{
			Filename: file.Filename, Status: file.Status, Additions: file.Additions, Deletions: file.Deletions,
			Patch: patch, PatchTruncated: patchTruncated || file.PatchTruncated,
		})
	}
	if err := saveEvidence(ctx, evidence); err != nil {
		return nil, err
	}
	if result.Value.NextPage > 0 && len(pullRequest.Files) < step.research.MaxFilesPerPullRequest {
		return dex.GoTo(sdkgo.StepRef[PullRequestFilesPageRequest](listPullRequestFilesStepType), PullRequestFilesPageRequest{
			Repository: request.Selection.Repository, Number: pullRequest.Number,
			Page: result.Value.NextPage, PageSize: pullRequestFilesPageSize,
		}), nil
	}
	filesRequest, hasPullRequest, err := nextPullRequestFilesRequest(ctx, step.research, request, evidence, cursor+1)
	if err != nil {
		return nil, err
	}
	if hasPullRequest {
		return dex.GoTo(sdkgo.StepRef[PullRequestFilesPageRequest](listPullRequestFilesStepType), filesRequest), nil
	}
	if err := researchStatus.Set(ctx, researchStatusCollectingCommits); err != nil {
		return nil, err
	}
	return dex.GoTo(sdkgo.StepRef[CommitPageRequest](listRepositoryCommitsStepType), firstCommitPageRequest(step.research, request)), nil
}

// dex:group group-id:pull-requests group-label:"Pull requests"
// dex:explanation text:"Note that one pull request's files could not be listed and continue with the next."
type RecordPullRequestFilesFailure struct {
	dex.StepDefaultsNoWaitFor[github.ListPullRequestFilesResult]
	research config.ResearchConfiguration
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (RecordPullRequestFilesFailure) GetStepType() string { return "RecordPullRequestFilesFailure" }

// Execute records the failure and advances the cursor.
func (step RecordPullRequestFilesFailure) Execute(ctx dex.Context, result github.ListPullRequestFilesResult) (*dex.StepDecision, error) {
	request, evidence, err := loadResearchState(ctx)
	if err != nil {
		return nil, err
	}
	cursor, err := pullRequestDetailCursor.Get(ctx)
	if err != nil {
		return nil, err
	}
	if cursor >= 0 && int(cursor) < len(evidence.PullRequests) {
		evidence.Notes = appendNote(evidence.Notes, fmt.Sprintf(
			"Files for pull request #%d could not be listed (%s).",
			evidence.PullRequests[cursor].Number, describeQueryFailure(string(result.Branch), result.Failure),
		))
		if err := saveEvidence(ctx, evidence); err != nil {
			return nil, err
		}
	}
	filesRequest, hasPullRequest, err := nextPullRequestFilesRequest(ctx, step.research, request, evidence, cursor+1)
	if err != nil {
		return nil, err
	}
	if hasPullRequest {
		return dex.GoTo(sdkgo.StepRef[PullRequestFilesPageRequest](listPullRequestFilesStepType), filesRequest), nil
	}
	if err := researchStatus.Set(ctx, researchStatusCollectingCommits); err != nil {
		return nil, err
	}
	return dex.GoTo(sdkgo.StepRef[CommitPageRequest](listRepositoryCommitsStepType), firstCommitPageRequest(step.research, request)), nil
}

// dex:group group-id:commits group-label:"Commits"
// dex:explanation text:"Store one page of commits and fetch the next page or summarize the evidence."
type RecordCommitPage struct {
	dex.StepDefaultsNoWaitFor[github.ListCommitsResult]
	research config.ResearchConfiguration
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (RecordCommitPage) GetStepType() string { return "RecordCommitPage" }

// Execute appends commits within the configured bound.
func (step RecordCommitPage) Execute(ctx dex.Context, result github.ListCommitsResult) (*dex.StepDecision, error) {
	request, evidence, err := loadResearchState(ctx)
	if err != nil {
		return nil, err
	}
	pathIndex, err := commitPathCursor.Get(ctx)
	if err != nil {
		return nil, err
	}
	limit := step.research.MaxCommitsPerRepository
	evidence.CommitsListed = true
	for _, commit := range result.Value.Commits {
		if len(evidence.Commits) >= limit {
			break
		}
		if containsCommit(evidence.Commits, commit.SHA) {
			continue
		}
		message, _ := truncateRunes(commit.Message, maximumCommitMessageRunes)
		evidence.Commits = append(evidence.Commits, model.CommitEvidence{
			SHA: commit.SHA, Message: message, AuthorLogin: firstNonBlank(commit.AuthorLogin, commit.AuthorName),
			URL: commit.URL, CommittedAt: commit.CommittedAt,
		})
	}
	if err := saveEvidence(ctx, evidence); err != nil {
		return nil, err
	}
	paths := commitPaths(request)
	if result.Value.NextPage > 0 && len(evidence.Commits) < limit {
		return dex.GoTo(sdkgo.StepRef[CommitPageRequest](listRepositoryCommitsStepType), CommitPageRequest{
			Repository: request.Selection.Repository, Window: request.Window, Path: paths[pathIndex],
			Page: result.Value.NextPage, PageSize: min(limit, githubMaximumPageSize),
		}), nil
	}
	nextPathIndex := pathIndex + 1
	if int(nextPathIndex) < len(paths) && len(evidence.Commits) < limit {
		if err := commitPathCursor.Set(ctx, nextPathIndex); err != nil {
			return nil, err
		}
		return dex.GoTo(sdkgo.StepRef[CommitPageRequest](listRepositoryCommitsStepType), commitPageRequestForPath(step.research, request, paths[nextPathIndex])), nil
	}
	return dex.GoTo(PrepareRepositorySummary{}, ResearchStageEntry{}), nil
}

// dex:group group-id:commits group-label:"Commits"
// dex:explanation text:"Note that commits could not be listed and summarize the remaining evidence."
type RecordCommitFailure struct {
	dex.StepDefaultsNoWaitFor[github.ListCommitsResult]
	research config.ResearchConfiguration
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (RecordCommitFailure) GetStepType() string { return "RecordCommitFailure" }

// Execute records the failure and continues.
func (step RecordCommitFailure) Execute(ctx dex.Context, result github.ListCommitsResult) (*dex.StepDecision, error) {
	request, evidence, err := loadResearchState(ctx)
	if err != nil {
		return nil, err
	}
	pathIndex, err := commitPathCursor.Get(ctx)
	if err != nil {
		return nil, err
	}
	paths := commitPaths(request)
	scope := "the repository"
	if int(pathIndex) < len(paths) && paths[pathIndex] != "" {
		scope = paths[pathIndex]
	}
	evidence.Notes = appendNote(evidence.Notes, "Commits for "+scope+" could not be listed ("+describeQueryFailure(string(result.Branch), result.Failure)+").")
	if err := saveEvidence(ctx, evidence); err != nil {
		return nil, err
	}
	nextPathIndex := pathIndex + 1
	if int(nextPathIndex) < len(paths) {
		if err := commitPathCursor.Set(ctx, nextPathIndex); err != nil {
			return nil, err
		}
		return dex.GoTo(sdkgo.StepRef[CommitPageRequest](listRepositoryCommitsStepType), commitPageRequestForPath(step.research, request, paths[nextPathIndex])), nil
	}
	return dex.GoTo(PrepareRepositorySummary{}, ResearchStageEntry{}), nil
}

// ResearchStageEntry is the empty Step input for research stages that read all
// state from Attributes.
type ResearchStageEntry struct{}

// dex:group group-id:summary group-label:"Summary"
// dex:explanation text:"Build the repository summary request, or complete without one when nothing changed."
type PrepareRepositorySummary struct {
	dex.StepDefaultsNoWaitFor[ResearchStageEntry]
	configuration config.ProcessConfiguration
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (PrepareRepositorySummary) GetStepType() string { return "PrepareRepositorySummary" }

// Execute routes to the language-model summary.
func (step PrepareRepositorySummary) Execute(ctx dex.Context, _ ResearchStageEntry) (*dex.StepDecision, error) {
	request, evidence, err := loadResearchState(ctx)
	if err != nil {
		return nil, err
	}
	if !evidence.PullRequestsListed && !evidence.CommitsListed {
		digest := model.RepositoryChangeDigest{
			Repository: evidence.Repository, Status: model.RepositoryResearchFailed,
			Summary: "GitHub research failed: neither merged pull requests nor commits could be listed.",
			Notes:   evidence.Notes,
		}
		if err := recordRepositoryDigest(ctx, digest); err != nil {
			return nil, err
		}
		if err := researchStatus.Set(ctx, researchStatusCompleted); err != nil {
			return nil, err
		}
		return dex.GracefulComplete(digest), nil
	}
	if len(evidence.PullRequests) == 0 && len(evidence.Commits) == 0 {
		digest := model.RepositoryChangeDigest{
			Repository: evidence.Repository, Status: model.RepositoryResearched,
			Summary: "No merged pull requests or commits were found inside the change window.",
			Notes:   evidence.Notes,
		}
		if len(evidence.Notes) > 0 {
			digest.Status = model.RepositoryResearchIncomplete
		}
		if err := recordRepositoryDigest(ctx, digest); err != nil {
			return nil, err
		}
		if err := researchStatus.Set(ctx, researchStatusCompleted); err != nil {
			return nil, err
		}
		return dex.GracefulComplete(digest), nil
	}
	if err := researchStatus.Set(ctx, researchStatusSummarizing); err != nil {
		return nil, err
	}
	return dex.GoTo(SummarizeRepositoryChanges{}, prompts.BuildRepositorySummaryRequest(step.configuration, request, evidence)), nil
}

// dex:group group-id:summary group-label:"Summary"
// dex:explanation text:"Wait for the language model to summarize the repository evidence, then complete with the digest."
type SummarizeRepositoryChanges struct {
	dex.StepDefaults
	languageModel *LanguageModelGenerationFlow
}

// GetStepType returns the stable unqualified Step type that Dex Web uses.
func (SummarizeRepositoryChanges) GetStepType() string { return "SummarizeRepositoryChanges" }

// WaitFor starts the generation SubFlow.
func (step SummarizeRepositoryChanges) WaitFor(_ dex.Context, request model.GenerationRequest) (*dex.Wait, error) {
	return dex.Until(dex.SubFlow(step.languageModel, request)), nil
}

// Execute parses the digest, falling back to an evidence listing when the
// model output is unusable so that one repository never blocks the request.
func (SummarizeRepositoryChanges) Execute(ctx dex.Context, _ model.GenerationRequest) (*dex.StepDecision, error) {
	request, evidence, err := loadResearchState(ctx)
	if err != nil {
		return nil, err
	}
	generation, err := decodeGenerationSubFlowResult(ctx, 0)
	var digest model.RepositoryChangeDigest
	if err == nil {
		digest, err = prompts.ParseRepositoryChangeDigest(generation, request, evidence)
	}
	if err != nil {
		digest = fallbackRepositoryDigest(evidence, err)
	}
	if err := recordRepositoryDigest(ctx, digest); err != nil {
		return nil, err
	}
	if err := researchStatus.Set(ctx, researchStatusCompleted); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(digest), nil
}

// nextPullRequestFilesRequest returns the files request for the pull request
// at cursor, or false when every detailed pull request has been visited.
func nextPullRequestFilesRequest(
	ctx dex.Context,
	research config.ResearchConfiguration,
	request model.RepositoryResearchRequest,
	evidence model.RepositoryChangeEvidence,
	cursor int64,
) (PullRequestFilesPageRequest, bool, error) {
	if err := pullRequestDetailCursor.Set(ctx, cursor); err != nil {
		return PullRequestFilesPageRequest{}, false, err
	}
	detailLimit := min(research.MaxPullRequestsWithFileDetails, len(evidence.PullRequests))
	if int(cursor) >= detailLimit {
		return PullRequestFilesPageRequest{}, false, nil
	}
	return PullRequestFilesPageRequest{
		Repository: request.Selection.Repository, Number: evidence.PullRequests[cursor].Number,
		Page: 1, PageSize: pullRequestFilesPageSize,
	}, true, nil
}

func firstCommitPageRequest(research config.ResearchConfiguration, request model.RepositoryResearchRequest) CommitPageRequest {
	return commitPageRequestForPath(research, request, commitPaths(request)[0])
}

func commitPageRequestForPath(research config.ResearchConfiguration, request model.RepositoryResearchRequest, path string) CommitPageRequest {
	return CommitPageRequest{
		Repository: request.Selection.Repository, Window: request.Window, Path: path,
		Page: 1, PageSize: min(research.MaxCommitsPerRepository, githubMaximumPageSize),
	}
}

// commitPaths returns the codebase areas to list commits for: up to
// maximumCommitPathQueries path hints, or the whole repository ("") when the
// selection names none. The result is never empty.
func commitPaths(request model.RepositoryResearchRequest) []string {
	paths := make([]string, 0, maximumCommitPathQueries)
	for _, hint := range request.Selection.PathHints {
		path := strings.Trim(strings.TrimSpace(hint), "/")
		if path == "" || strings.Contains(path, "..") || containsText(paths, path) {
			continue
		}
		paths = append(paths, path)
		if len(paths) == maximumCommitPathQueries {
			break
		}
	}
	if len(paths) == 0 {
		return []string{""}
	}
	return paths
}

func containsText(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

func containsCommit(commits []model.CommitEvidence, sha string) bool {
	for _, commit := range commits {
		if commit.SHA == sha {
			return true
		}
	}
	return false
}

func recordRepositoryDigest(ctx dex.Context, digest model.RepositoryChangeDigest) error {
	summary, _ := truncateRunes(digest.Summary, maximumFallbackSummaryRunes)
	return errors.Join(researchDigestAttribute.Set(ctx, digest), researchDigestDescription.Set(ctx, summary))
}

func fallbackRepositoryDigest(evidence model.RepositoryChangeEvidence, cause error) model.RepositoryChangeDigest {
	digest := model.RepositoryChangeDigest{
		Repository: evidence.Repository, Status: model.RepositoryResearchIncomplete,
		Relevant:         len(evidence.PullRequests) > 0 || len(evidence.Commits) > 0,
		Summary:          "An automatic summary was unavailable, so the merged pull requests are listed as found.",
		PullRequestCount: len(evidence.PullRequests), CommitCount: len(evidence.Commits),
		Notes: appendNote(evidence.Notes, "Summary unavailable: "+safeErrorText(cause)),
	}
	for _, pullRequest := range evidence.PullRequests {
		if len(digest.Highlights) >= maximumFallbackHighlights {
			break
		}
		whatChanged, _ := truncateRunes(strings.TrimSpace(pullRequest.Body), maximumFallbackSummaryRunes)
		digest.Highlights = append(digest.Highlights, model.ChangeHighlight{
			Title: pullRequest.Title, WhatChanged: whatChanged,
			References: []model.SourceReference{{Label: fmt.Sprintf("PR #%d", pullRequest.Number), URL: pullRequest.URL}},
		})
	}
	return digest
}

func loadResearchState(ctx dex.Context) (model.RepositoryResearchRequest, model.RepositoryChangeEvidence, error) {
	request, err := researchRequest.Get(ctx)
	if err != nil {
		return model.RepositoryResearchRequest{}, model.RepositoryChangeEvidence{}, err
	}
	evidence, err := researchEvidence.Get(ctx)
	if err != nil {
		return model.RepositoryResearchRequest{}, model.RepositoryChangeEvidence{}, err
	}
	return request, evidence, nil
}

func saveEvidence(ctx dex.Context, evidence model.RepositoryChangeEvidence) error {
	if err := researchEvidence.Set(ctx, evidence); err != nil {
		return err
	}
	return setResearchCounts(ctx, len(evidence.PullRequests), len(evidence.Commits))
}

func setResearchCounts(ctx dex.Context, pullRequests int, commits int) error {
	if err := researchPullRequestCount.Set(ctx, int64(pullRequests)); err != nil {
		return err
	}
	return researchCommitCount.Set(ctx, int64(commits))
}

func containsPullRequest(pullRequests []model.PullRequestEvidence, number int) bool {
	for _, pullRequest := range pullRequests {
		if pullRequest.Number == number {
			return true
		}
	}
	return false
}

func appendNote(notes []string, note string) []string {
	for _, existing := range notes {
		if existing == note {
			return notes
		}
	}
	return append(append([]string(nil), notes...), note)
}

func describeQueryFailure(branch string, failure *sdkgo.Failure) string {
	if failure == nil || failure.Kind == "" {
		return branch
	}
	return branch + ": " + string(failure.Kind)
}

// decodeGenerationSubFlowResult decodes one LanguageModelGenerationFlow result
// and reports a failed or unsuccessful generation as an error.
func decodeGenerationSubFlowResult(ctx dex.Context, index int) (model.GenerationResult, error) {
	flowResult, err := dex.SubFlowResult(ctx, index)
	if err != nil {
		return model.GenerationResult{}, err
	}
	if flowResult.Status != dex.FlowCompleted {
		return model.GenerationResult{}, fmt.Errorf("language-model generation ended with status %s", flowResult.Status)
	}
	var generation model.GenerationResult
	if err := flowResult.DecodeSingleOutput(&generation); err != nil {
		return model.GenerationResult{}, fmt.Errorf("decode language-model generation result: %w", err)
	}
	if generation.Status != model.GenerationSucceeded {
		message := string(generation.Status)
		if generation.FailureMessage != "" {
			message += ": " + generation.FailureMessage
		}
		return generation, fmt.Errorf("language-model generation did not succeed (%s)", message)
	}
	return generation, nil
}

var _ dex.Flow = (*RepositoryChangeResearchFlow)(nil)
var _ dex.Step[model.RepositoryResearchRequest] = StartRepositoryResearch{}
var _ dex.Step[github.ListMergedPullRequestsResult] = RecordMergedPullRequestPage{}
var _ dex.Step[github.ListMergedPullRequestsResult] = RecordMergedPullRequestFailure{}
var _ dex.Step[github.ListMergedPullRequestsResult] = RecordRepositoryNotFound{}
var _ dex.Step[github.ListPullRequestFilesResult] = RecordPullRequestFilePage{}
var _ dex.Step[github.ListPullRequestFilesResult] = RecordPullRequestFilesFailure{}
var _ dex.Step[github.ListCommitsResult] = RecordCommitPage{}
var _ dex.Step[github.ListCommitsResult] = RecordCommitFailure{}
var _ dex.Step[ResearchStageEntry] = PrepareRepositorySummary{}
var _ dex.Step[model.GenerationRequest] = SummarizeRepositoryChanges{}
var _ dex.RPC[dex.None, map[string]any] = (*RepositoryChangeResearchFlow)(nil).GetDexSummary
var _ dex.RPC[dex.None, map[string]any] = (*RepositoryChangeResearchFlow)(nil).GetDexDisplay
