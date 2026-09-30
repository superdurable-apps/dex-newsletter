// Package content holds the pure parts of the blog pipeline: research evidence,
// language-model requests and their structured replies, and HTML rendering.
// Nothing here calls a provider or touches Dex state.
package content

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	github "github.com/superdurable/dex-connectors-library/connectors/github"
)

// Evidence bounds keep every prompt and Attribute a predictable size.
const (
	maxPullRequestBodyRunes = 1500
	maxPatchRunes           = 1200
	maxFilesPerPullRequest  = 15
	maxCommitMessageRunes   = 400
	maxCandidates           = 150
)

// Window is the inclusive time range a post covers.
type Window struct {
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
	Label string    `json:"label"`
}

// NewWindow labels a range such as "Sep 21 – Sep 28, 2026".
func NewWindow(since, until time.Time) Window {
	since, until = since.UTC(), until.UTC()
	label := since.Format("Jan 2") + " – " + until.Format("Jan 2, 2006")
	if since.Year() != until.Year() {
		label = since.Format("Jan 2, 2006") + " – " + until.Format("Jan 2, 2006")
	}
	return Window{Since: since, Until: until, Label: label}
}

// RepositoryCandidate is one public repository the model may choose.
type RepositoryCandidate struct {
	Owner       string     `json:"owner"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Language    string     `json:"language,omitempty"`
	Topics      []string   `json:"topics,omitempty"`
	PushedAt    *time.Time `json:"pushedAt,omitempty"`
	URL         string     `json:"url,omitempty"`
}

func (candidate RepositoryCandidate) FullName() string { return candidate.Owner + "/" + candidate.Name }

// CandidatesFromRepositories keeps active repositories pushed inside the window.
func CandidatesFromRepositories(owner string, repositories []github.Repository, window Window) []RepositoryCandidate {
	candidates := []RepositoryCandidate{}
	for _, repository := range repositories {
		if repository.Fork || repository.Archived || repository.Disabled || repository.Name == "" {
			continue
		}
		if repository.PushedAt != nil && repository.PushedAt.Before(window.Since) {
			continue
		}
		candidates = append(candidates, RepositoryCandidate{
			Owner: owner, Name: repository.Name, Description: truncateRunes(repository.Description, 300),
			Language: repository.Language, Topics: repository.Topics, PushedAt: repository.PushedAt, URL: repository.ProfileURL,
		})
	}
	return candidates
}

// MergeCandidates appends next to existing, dropping duplicates and keeping at most maxCandidates.
func MergeCandidates(existing, next []RepositoryCandidate) []RepositoryCandidate {
	seen := map[string]bool{}
	merged := make([]RepositoryCandidate, 0, len(existing)+len(next))
	for _, candidate := range append(append([]RepositoryCandidate{}, existing...), next...) {
		key := strings.ToLower(candidate.FullName())
		if seen[key] || len(merged) == maxCandidates {
			continue
		}
		seen[key] = true
		merged = append(merged, candidate)
	}
	return merged
}

// SelectedRepository is one repository chosen for research.
type SelectedRepository struct {
	Owner      string   `json:"owner"`
	Name       string   `json:"name"`
	URL        string   `json:"url,omitempty"`
	Reason     string   `json:"reason"`
	FocusAreas []string `json:"focusAreas,omitempty"`
}

func (repository SelectedRepository) FullName() string {
	return repository.Owner + "/" + repository.Name
}

type FileChange struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch,omitempty"`
}

type PullRequestEvidence struct {
	Number   int          `json:"number"`
	Title    string       `json:"title"`
	Body     string       `json:"body,omitempty"`
	URL      string       `json:"url,omitempty"`
	Author   string       `json:"author,omitempty"`
	MergedAt time.Time    `json:"mergedAt"`
	Labels   []string     `json:"labels,omitempty"`
	Files    []FileChange `json:"files,omitempty"`
}

type CommitEvidence struct {
	SHA         string    `json:"sha"`
	Message     string    `json:"message"`
	Author      string    `json:"author,omitempty"`
	URL         string    `json:"url,omitempty"`
	CommittedAt time.Time `json:"committedAt"`
}

// RepositoryResearch is everything gathered about one repository.
type RepositoryResearch struct {
	Repository   SelectedRepository    `json:"repository"`
	PullRequests []PullRequestEvidence `json:"pullRequests"`
	Commits      []CommitEvidence      `json:"commits"`
	// Notes record GitHub queries that failed, so the gap stays visible.
	Notes []string `json:"notes,omitempty"`
}

// PullRequestsFromPage converts a merged-PR page into bounded evidence.
func PullRequestsFromPage(page github.MergedPullRequestPage, limit int) []PullRequestEvidence {
	evidence := []PullRequestEvidence{}
	for _, pullRequest := range page.PullRequests {
		if len(evidence) == limit {
			break
		}
		evidence = append(evidence, PullRequestEvidence{
			Number: pullRequest.Number, Title: pullRequest.Title, Body: truncateRunes(pullRequest.Body, maxPullRequestBodyRunes),
			URL: pullRequest.URL, Author: pullRequest.AuthorLogin, MergedAt: pullRequest.MergedAt, Labels: pullRequest.Labels,
		})
	}
	return evidence
}

// FilesFromPage converts changed files into bounded evidence, largest changes first.
func FilesFromPage(page github.PullRequestFilePage) []FileChange {
	files := append([]github.PullRequestFile(nil), page.Files...)
	sort.SliceStable(files, func(left, right int) bool { return files[left].Changes > files[right].Changes })
	changes := []FileChange{}
	for _, file := range files {
		if len(changes) == maxFilesPerPullRequest {
			break
		}
		changes = append(changes, FileChange{
			Path: file.Filename, Status: file.Status, Additions: file.Additions, Deletions: file.Deletions,
			Patch: truncateRunes(file.Patch, maxPatchRunes),
		})
	}
	return changes
}

var pullRequestReference = regexp.MustCompile(`(?i)(?:merge pull request #(\d+)|\(#(\d+)\)\s*$)`)

// CommitsFromPage converts commits into evidence, skipping commits that only restate a merged pull request.
func CommitsFromPage(page github.CommitPage, pullRequests []PullRequestEvidence, limit int) []CommitEvidence {
	merged := map[string]bool{}
	for _, pullRequest := range pullRequests {
		merged[fmt.Sprint(pullRequest.Number)] = true
	}
	commits := []CommitEvidence{}
	for _, commit := range page.Commits {
		if len(commits) == limit {
			break
		}
		firstLine, _, _ := strings.Cut(commit.Message, "\n")
		if match := pullRequestReference.FindStringSubmatch(firstLine); match != nil && (merged[match[1]] || merged[match[2]]) {
			continue
		}
		author := commit.AuthorLogin
		if author == "" {
			author = commit.AuthorName
		}
		commits = append(commits, CommitEvidence{
			SHA: commit.SHA, Message: truncateRunes(commit.Message, maxCommitMessageRunes), Author: author,
			URL: commit.URL, CommittedAt: commit.CommittedAt,
		})
	}
	return commits
}

// ResearchTotals counts the evidence across repositories.
func ResearchTotals(research []RepositoryResearch) (pullRequests, commits int) {
	for _, repository := range research {
		pullRequests += len(repository.PullRequests)
		commits += len(repository.Commits)
	}
	return pullRequests, commits
}

// ResearchSummary describes the evidence in one line for Dex Web and Slack.
func ResearchSummary(research []RepositoryResearch) string {
	pullRequests, commits := ResearchTotals(research)
	names := make([]string, 0, len(research))
	for _, repository := range research {
		names = append(names, repository.Repository.FullName())
	}
	return fmt.Sprintf("%d merged pull requests and %d other commits across %s", pullRequests, commits, strings.Join(names, ", "))
}

// EvidenceURLs lists every link the model may cite.
func EvidenceURLs(research []RepositoryResearch) map[string]bool {
	urls := map[string]bool{}
	for _, repository := range research {
		if repository.Repository.URL != "" {
			urls[repository.Repository.URL] = true
		}
		for _, pullRequest := range repository.PullRequests {
			if pullRequest.URL != "" {
				urls[pullRequest.URL] = true
			}
		}
		for _, commit := range repository.Commits {
			if commit.URL != "" {
				urls[commit.URL] = true
			}
		}
	}
	return urls
}

func truncateRunes(text string, limit int) string {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	// The ellipsis counts toward limit, so a truncated field still passes the editor's same bound.
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}
