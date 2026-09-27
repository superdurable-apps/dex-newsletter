package prompts

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

// Evidence bounding parameters.
const (
	// defaultMaximumEvidenceCharacters applies when the configuration carries
	// no positive MaxEvidenceCharactersPerRepository.
	defaultMaximumEvidenceCharacters = 60000
	// retainedFileListPullRequests is how many of the newest pull requests
	// keep their changed-file lists through the "older file lists" step.
	retainedFileListPullRequests = 3
	// retainedFullCommitMessages is how many of the newest commits keep their
	// full message through the "commit messages beyond N" step.
	retainedFullCommitMessages = 20
	// truncatedPullRequestBodyRunes is the description length kept by the
	// description-truncation step.
	truncatedPullRequestBodyRunes = 500
	// maximumCommitSubjectRunes bounds a commit message shortened to its
	// first line.
	maximumCommitSubjectRunes = 200
)

// evidenceDocument is the evidence section body. Pull requests and commits
// are ordered newest first so that the oldest, lowest-priority content is
// always at the end of each list.
type evidenceDocument struct {
	Repository   string                `json:"repository"`
	PullRequests []pullRequestDocument `json:"pullRequests"`
	Commits      []commitDocument      `json:"commits"`
	Notes        []string              `json:"notes"`
}

type pullRequestDocument struct {
	Number        int                  `json:"number"`
	Title         string               `json:"title"`
	URL           string               `json:"url"`
	Author        string               `json:"author,omitempty"`
	MergedAt      string               `json:"mergedAt,omitempty"`
	Labels        []string             `json:"labels,omitempty"`
	Body          string               `json:"body,omitempty"`
	BodyTruncated bool                 `json:"bodyTruncated,omitempty"`
	Files         []fileChangeDocument `json:"files,omitempty"`
}

type fileChangeDocument struct {
	Filename       string `json:"filename"`
	Status         string `json:"status,omitempty"`
	Additions      int    `json:"additions"`
	Deletions      int    `json:"deletions"`
	Patch          string `json:"patch,omitempty"`
	PatchTruncated bool   `json:"patchTruncated,omitempty"`
}

type commitDocument struct {
	SHA         string `json:"sha"`
	URL         string `json:"url"`
	Author      string `json:"author,omitempty"`
	CommittedAt string `json:"committedAt,omitempty"`
	Message     string `json:"message,omitempty"`
}

// boundedEvidence is the encoded evidence section body and a trusted
// description of what was removed to fit the budget.
type boundedEvidence struct {
	encodedJSON       string
	characters        int
	maximumCharacters int
	withinBudget      bool
	reductionNotes    []string
}

// boundRepositoryEvidence encodes evidence in at most maximumCharacters
// characters (runes), removing the lowest-priority content first:
//
//  1. file patches, oldest pull request first;
//  2. changed-file lists of older pull requests (the newest three keep them);
//  3. commit messages beyond the newest twenty, shortened to their first line;
//  4. pull request descriptions, truncated to 500 characters, oldest first;
//
// and then, only if still necessary, descriptions entirely, the remaining
// file lists, the remaining commit messages, whole commits, whole pull
// requests (always oldest first), and finally the research notes. Each step
// stops as soon as the evidence fits. withinBudget is false only when even
// the empty skeleton exceeds the budget.
func boundRepositoryEvidence(evidence model.RepositoryChangeEvidence, maximumCharacters int) boundedEvidence {
	if maximumCharacters < 1 {
		maximumCharacters = defaultMaximumEvidenceCharacters
	}
	sizer := newEvidenceSizer(newEvidenceDocument(evidence))
	reductions := []struct {
		apply    func() int
		describe func(count int) string
	}{
		{
			apply: func() int { return sizer.reducePullRequests(maximumCharacters, 0, pullRequestWithoutPatches) },
			describe: func(count int) string {
				return fmt.Sprintf("file patches were omitted for the %d oldest pull requests that had them", count)
			},
		},
		{
			apply: func() int {
				return sizer.reducePullRequests(maximumCharacters, retainedFileListPullRequests, pullRequestWithoutFileList)
			},
			describe: func(count int) string {
				return fmt.Sprintf("changed-file lists were omitted for the %d oldest pull requests", count)
			},
		},
		{
			apply: func() int {
				return sizer.reduceCommits(maximumCharacters, retainedFullCommitMessages, commitWithSubjectOnly)
			},
			describe: func(count int) string {
				return fmt.Sprintf("commit messages were shortened to their first line for the %d oldest commits", count)
			},
		},
		{
			apply: func() int { return sizer.reducePullRequests(maximumCharacters, 0, pullRequestWithTruncatedBody) },
			describe: func(count int) string {
				return fmt.Sprintf("descriptions were truncated to %d characters for the %d oldest pull requests", truncatedPullRequestBodyRunes, count)
			},
		},
		{
			apply: func() int { return sizer.reducePullRequests(maximumCharacters, 0, pullRequestWithoutBody) },
			describe: func(count int) string {
				return fmt.Sprintf("descriptions were omitted for the %d oldest pull requests", count)
			},
		},
		{
			apply: func() int { return sizer.reducePullRequests(maximumCharacters, 0, pullRequestWithoutFileList) },
			describe: func(count int) string {
				return fmt.Sprintf("changed-file lists were omitted for %d more pull requests", count)
			},
		},
		{
			apply: func() int { return sizer.reduceCommits(maximumCharacters, 0, commitWithSubjectOnly) },
			describe: func(count int) string {
				return fmt.Sprintf("commit messages were shortened to their first line for %d more commits", count)
			},
		},
		{
			apply:    func() int { return sizer.dropOldestCommits(maximumCharacters) },
			describe: func(count int) string { return fmt.Sprintf("the %d oldest commits were omitted", count) },
		},
		{
			apply:    func() int { return sizer.dropOldestPullRequests(maximumCharacters) },
			describe: func(count int) string { return fmt.Sprintf("the %d oldest pull requests were omitted", count) },
		},
		{
			apply:    func() int { return sizer.omitNotes() },
			describe: func(int) string { return "research notes were omitted" },
		},
	}
	var notes []string
	for _, reduction := range reductions {
		if sizer.total() <= maximumCharacters {
			break
		}
		if count := reduction.apply(); count > 0 {
			notes = append(notes, reduction.describe(count))
		}
	}
	encoded := encodeUntrustedJSON(sizer.document)
	characters := utf8.RuneCountInString(encoded)
	return boundedEvidence{
		encodedJSON:       encoded,
		characters:        characters,
		maximumCharacters: maximumCharacters,
		withinBudget:      characters <= maximumCharacters,
		reductionNotes:    notes,
	}
}

// newEvidenceDocument projects evidence into its section document, sorted
// newest first, without sharing any slice with the input. Every string has
// its invisible formatting characters and stray variation selectors removed
// (see removeHiddenCharacters), so hidden text such as Unicode tag
// characters in a pull request description never reaches the model; line
// breaks are kept.
func newEvidenceDocument(evidence model.RepositoryChangeEvidence) evidenceDocument {
	pullRequests := slices.Clone(evidence.PullRequests)
	slices.SortStableFunc(pullRequests, compareNewestPullRequestFirst)
	commits := slices.Clone(evidence.Commits)
	slices.SortStableFunc(commits, compareNewestCommitFirst)

	document := evidenceDocument{
		Repository:   removeHiddenCharacters(formatRepositoryReference(evidence.Repository)),
		PullRequests: make([]pullRequestDocument, 0, len(pullRequests)),
		Commits:      make([]commitDocument, 0, len(commits)),
		Notes:        removeHiddenCharactersFromEach(evidence.Notes),
	}
	for _, pullRequest := range pullRequests {
		labels := removeHiddenCharactersFromEach(pullRequest.Labels)
		slices.Sort(labels)
		var files []fileChangeDocument
		if len(pullRequest.Files) > 0 {
			files = make([]fileChangeDocument, 0, len(pullRequest.Files))
			for _, file := range pullRequest.Files {
				files = append(files, fileChangeDocument{
					Filename:       removeHiddenCharacters(file.Filename),
					Status:         removeHiddenCharacters(file.Status),
					Additions:      file.Additions,
					Deletions:      file.Deletions,
					Patch:          removeHiddenCharacters(file.Patch),
					PatchTruncated: file.PatchTruncated,
				})
			}
		}
		document.PullRequests = append(document.PullRequests, pullRequestDocument{
			Number:        pullRequest.Number,
			Title:         removeHiddenCharacters(pullRequest.Title),
			URL:           removeHiddenCharacters(pullRequest.URL),
			Author:        removeHiddenCharacters(pullRequest.AuthorLogin),
			MergedAt:      formatOptionalTimestamp(pullRequest.MergedAt),
			Labels:        labels,
			Body:          removeHiddenCharacters(pullRequest.Body),
			BodyTruncated: pullRequest.BodyTruncated,
			Files:         files,
		})
	}
	for _, commit := range commits {
		document.Commits = append(document.Commits, commitDocument{
			SHA:         removeHiddenCharacters(commit.SHA),
			URL:         removeHiddenCharacters(commit.URL),
			Author:      removeHiddenCharacters(commit.AuthorLogin),
			CommittedAt: formatOptionalTimestamp(commit.CommittedAt),
			Message:     removeHiddenCharacters(commit.Message),
		})
	}
	return document
}

// removeHiddenCharactersFromEach applies removeHiddenCharacters to a copy of
// values. The result is never nil.
func removeHiddenCharactersFromEach(values []string) []string {
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		cleaned = append(cleaned, removeHiddenCharacters(value))
	}
	return cleaned
}

func compareNewestPullRequestFirst(first model.PullRequestEvidence, second model.PullRequestEvidence) int {
	if order := second.MergedAt.Compare(first.MergedAt); order != 0 {
		return order
	}
	if order := cmp.Compare(second.Number, first.Number); order != 0 {
		return order
	}
	if order := cmp.Compare(first.URL, second.URL); order != 0 {
		return order
	}
	return cmp.Compare(first.Title, second.Title)
}

func compareNewestCommitFirst(first model.CommitEvidence, second model.CommitEvidence) int {
	if order := second.CommittedAt.Compare(first.CommittedAt); order != 0 {
		return order
	}
	if order := cmp.Compare(first.SHA, second.SHA); order != 0 {
		return order
	}
	return cmp.Compare(first.URL, second.URL)
}

// formatOptionalTimestamp renders RFC 3339 UTC, or "" for the zero time.
func formatOptionalTimestamp(timestamp time.Time) string {
	if timestamp.IsZero() {
		return ""
	}
	return timestamp.UTC().Format(time.RFC3339)
}

// evidenceSizer tracks the exact encoded size of an evidenceDocument without
// re-encoding the whole document after every change. encoding/json encodes a
// slice as "[" + elements joined by "," + "]" with each element encoded
// exactly as on its own, so the total is the size of the document with empty
// lists plus, for each non-empty list, the element sizes and separators.
type evidenceSizer struct {
	document           evidenceDocument
	pullRequestSizes   []int
	commitSizes        []int
	pullRequestSizeSum int
	commitSizeSum      int
	skeletonSize       int
}

func newEvidenceSizer(document evidenceDocument) *evidenceSizer {
	sizer := &evidenceSizer{document: document}
	for _, pullRequest := range document.PullRequests {
		size := encodedRuneCount(pullRequest)
		sizer.pullRequestSizes = append(sizer.pullRequestSizes, size)
		sizer.pullRequestSizeSum += size
	}
	for _, commit := range document.Commits {
		size := encodedRuneCount(commit)
		sizer.commitSizes = append(sizer.commitSizes, size)
		sizer.commitSizeSum += size
	}
	sizer.refreshSkeletonSize()
	return sizer
}

func encodedRuneCount(value any) int {
	return utf8.RuneCountInString(encodeUntrustedJSON(value))
}

// refreshSkeletonSize measures the document with both lists empty.
func (sizer *evidenceSizer) refreshSkeletonSize() {
	skeleton := sizer.document
	skeleton.PullRequests = []pullRequestDocument{}
	skeleton.Commits = []commitDocument{}
	sizer.skeletonSize = encodedRuneCount(skeleton)
}

// total is the exact rune count of encodeUntrustedJSON(sizer.document).
func (sizer *evidenceSizer) total() int {
	return sizer.skeletonSize +
		encodedListContentSize(len(sizer.pullRequestSizes), sizer.pullRequestSizeSum) +
		encodedListContentSize(len(sizer.commitSizes), sizer.commitSizeSum)
}

func encodedListContentSize(count int, sizeSum int) int {
	if count == 0 {
		return 0
	}
	return sizeSum + count - 1
}

// reducePullRequests applies reduce to pull requests from the oldest down to
// index newestRetained, until the document fits. A change is kept only when
// it strictly shrinks the element. It returns how many were changed.
func (sizer *evidenceSizer) reducePullRequests(maximumCharacters int, newestRetained int, reduce func(pullRequestDocument) (pullRequestDocument, bool)) int {
	changed := 0
	for index := len(sizer.document.PullRequests) - 1; index >= newestRetained && sizer.total() > maximumCharacters; index-- {
		candidate, reduced := reduce(sizer.document.PullRequests[index])
		if !reduced {
			continue
		}
		size := encodedRuneCount(candidate)
		if size >= sizer.pullRequestSizes[index] {
			continue
		}
		sizer.document.PullRequests[index] = candidate
		sizer.pullRequestSizeSum += size - sizer.pullRequestSizes[index]
		sizer.pullRequestSizes[index] = size
		changed++
	}
	return changed
}

// reduceCommits is reducePullRequests for commits.
func (sizer *evidenceSizer) reduceCommits(maximumCharacters int, newestRetained int, reduce func(commitDocument) (commitDocument, bool)) int {
	changed := 0
	for index := len(sizer.document.Commits) - 1; index >= newestRetained && sizer.total() > maximumCharacters; index-- {
		candidate, reduced := reduce(sizer.document.Commits[index])
		if !reduced {
			continue
		}
		size := encodedRuneCount(candidate)
		if size >= sizer.commitSizes[index] {
			continue
		}
		sizer.document.Commits[index] = candidate
		sizer.commitSizeSum += size - sizer.commitSizes[index]
		sizer.commitSizes[index] = size
		changed++
	}
	return changed
}

// dropOldestCommits removes commits from the end until the document fits.
func (sizer *evidenceSizer) dropOldestCommits(maximumCharacters int) int {
	dropped := 0
	for last := len(sizer.document.Commits) - 1; last >= 0 && sizer.total() > maximumCharacters; last-- {
		sizer.commitSizeSum -= sizer.commitSizes[last]
		sizer.commitSizes = sizer.commitSizes[:last]
		sizer.document.Commits = sizer.document.Commits[:last]
		dropped++
	}
	return dropped
}

// dropOldestPullRequests removes pull requests from the end until the
// document fits.
func (sizer *evidenceSizer) dropOldestPullRequests(maximumCharacters int) int {
	dropped := 0
	for last := len(sizer.document.PullRequests) - 1; last >= 0 && sizer.total() > maximumCharacters; last-- {
		sizer.pullRequestSizeSum -= sizer.pullRequestSizes[last]
		sizer.pullRequestSizes = sizer.pullRequestSizes[:last]
		sizer.document.PullRequests = sizer.document.PullRequests[:last]
		dropped++
	}
	return dropped
}

// omitNotes clears the research notes and reports 1 when there were any.
func (sizer *evidenceSizer) omitNotes() int {
	if len(sizer.document.Notes) == 0 {
		return 0
	}
	sizer.document.Notes = []string{}
	sizer.refreshSkeletonSize()
	return 1
}

// pullRequestWithoutPatches drops every file patch. The file slice is
// rebuilt so the previous document is never modified.
func pullRequestWithoutPatches(pullRequest pullRequestDocument) (pullRequestDocument, bool) {
	if !slices.ContainsFunc(pullRequest.Files, func(file fileChangeDocument) bool { return file.Patch != "" || file.PatchTruncated }) {
		return pullRequest, false
	}
	files := make([]fileChangeDocument, len(pullRequest.Files))
	for index, file := range pullRequest.Files {
		file.Patch = ""
		file.PatchTruncated = false
		files[index] = file
	}
	pullRequest.Files = files
	return pullRequest, true
}

func pullRequestWithoutFileList(pullRequest pullRequestDocument) (pullRequestDocument, bool) {
	if pullRequest.Files == nil {
		return pullRequest, false
	}
	pullRequest.Files = nil
	return pullRequest, true
}

func pullRequestWithTruncatedBody(pullRequest pullRequestDocument) (pullRequestDocument, bool) {
	if utf8.RuneCountInString(pullRequest.Body) <= truncatedPullRequestBodyRunes {
		return pullRequest, false
	}
	pullRequest.Body = truncateRunes(pullRequest.Body, truncatedPullRequestBodyRunes)
	pullRequest.BodyTruncated = true
	return pullRequest, true
}

func pullRequestWithoutBody(pullRequest pullRequestDocument) (pullRequestDocument, bool) {
	if pullRequest.Body == "" && !pullRequest.BodyTruncated {
		return pullRequest, false
	}
	pullRequest.Body = ""
	pullRequest.BodyTruncated = false
	return pullRequest, true
}

// commitWithSubjectOnly keeps the first line of the message, bounded.
func commitWithSubjectOnly(commit commitDocument) (commitDocument, bool) {
	subject, _, _ := strings.Cut(commit.Message, "\n")
	subject = truncateRunes(strings.TrimSpace(subject), maximumCommitSubjectRunes)
	if subject == commit.Message {
		return commit, false
	}
	commit.Message = subject
	return commit, true
}
