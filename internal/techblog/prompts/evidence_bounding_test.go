package prompts

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

// largeEvidence has 30 pull requests (the newest 10 with patched files, the
// next 5 with unpatched files), 60 commits with long messages, multi-byte
// text, and characters that JSON escapes. Pull requests are supplied oldest
// first so the tests also exercise sorting.
func largeEvidence() model.RepositoryChangeEvidence {
	evidence := model.RepositoryChangeEvidence{
		Repository: model.RepositoryReference{Owner: "superdurable", Name: "dex"},
		Notes:      []string{"Commit listing was capped.", "Two pull requests had no files."},
	}
	for index := 29; index >= 0; index-- {
		pullRequest := model.PullRequestEvidence{
			Number:      1000 + index,
			Title:       fmt.Sprintf("Change %d with café ✓", index),
			Body:        strings.Repeat(fmt.Sprintf("Body of change %d, é and <T>. ", index), 60),
			URL:         fmt.Sprintf("https://github.com/superdurable/dex/pull/%d", 1000+index),
			AuthorLogin: "author",
			MergedAt:    fixtureUntil.Add(-time.Duration(index) * time.Hour),
			Labels:      []string{"b-label", "a-label"},
		}
		switch {
		case index < 10:
			for file := 0; file < 5; file++ {
				pullRequest.Files = append(pullRequest.Files, model.FileChangeEvidence{
					Filename: fmt.Sprintf("server/file%d.go", file), Status: "modified", Additions: 10, Deletions: 2,
					Patch: strings.Repeat("+func Map[T any](<-chan T) & more\n", 40), PatchTruncated: file == 0,
				})
			}
		case index < 15:
			pullRequest.Files = []model.FileChangeEvidence{{Filename: "docs/readme.md", Status: "modified", Additions: 1}}
		}
		evidence.PullRequests = append(evidence.PullRequests, pullRequest)
	}
	for index := 0; index < 60; index++ {
		evidence.Commits = append(evidence.Commits, model.CommitEvidence{
			SHA:         fmt.Sprintf("%040x", index),
			Message:     fmt.Sprintf("Commit %d subject\n\n%s", index, strings.Repeat("détail ", 40)),
			AuthorLogin: "author",
			URL:         fmt.Sprintf("https://github.com/superdurable/dex/commit/%040x", index),
			CommittedAt: fixtureUntil.Add(-time.Duration(index) * 30 * time.Minute),
		})
	}
	return evidence
}

// applyToPullRequests applies reduce unconditionally from index fromIndex
// to the end, mirroring what bounding does when every step shrinks.
func applyToPullRequests(document evidenceDocument, fromIndex int, reduce func(pullRequestDocument) (pullRequestDocument, bool)) evidenceDocument {
	document.PullRequests = slices.Clone(document.PullRequests)
	for index := fromIndex; index < len(document.PullRequests); index++ {
		if reduced, changed := reduce(document.PullRequests[index]); changed {
			document.PullRequests[index] = reduced
		}
	}
	return document
}

func applyToCommits(document evidenceDocument, fromIndex int, reduce func(commitDocument) (commitDocument, bool)) evidenceDocument {
	document.Commits = slices.Clone(document.Commits)
	for index := fromIndex; index < len(document.Commits); index++ {
		if reduced, changed := reduce(document.Commits[index]); changed {
			document.Commits[index] = reduced
		}
	}
	return document
}

func decodeBoundedEvidence(t *testing.T, bounded boundedEvidence) evidenceDocument {
	t.Helper()
	var document evidenceDocument
	if err := json.Unmarshal([]byte(bounded.encodedJSON), &document); err != nil {
		t.Fatalf("bounded evidence is not valid JSON: %v", err)
	}
	return document
}

func TestBoundRepositoryEvidenceStaysWithinEveryBudget(t *testing.T) {
	evidence := largeEvidence()
	full := boundRepositoryEvidence(evidence, 1<<30)
	if len(full.reductionNotes) != 0 || !full.withinBudget {
		t.Fatalf("a generous budget must not reduce anything: %v", full.reductionNotes)
	}
	skeleton := encodedRuneCount(evidenceDocument{Repository: "superdurable/dex", PullRequests: []pullRequestDocument{}, Commits: []commitDocument{}, Notes: []string{}})
	limits := []int{full.characters, full.characters - 1, 60000, 20000, 5000, 2000, 500, 200, skeleton, skeleton - 1, 1}
	for _, limit := range limits {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			bounded := boundRepositoryEvidence(evidence, limit)
			if got := utf8.RuneCountInString(bounded.encodedJSON); got != bounded.characters {
				t.Fatalf("characters = %d, but the encoding has %d", bounded.characters, got)
			}
			if strings.ContainsAny(bounded.encodedJSON, "<>\n") {
				t.Errorf("encoded evidence contains a raw angle bracket or line break")
			}
			document := decodeBoundedEvidence(t, bounded)
			if limit >= skeleton {
				if !bounded.withinBudget || bounded.characters > limit {
					t.Errorf("evidence has %d characters, budget %d", bounded.characters, limit)
				}
			} else {
				if bounded.withinBudget {
					t.Errorf("a budget below the skeleton cannot be met, yet withinBudget is true")
				}
				if len(document.PullRequests)+len(document.Commits)+len(document.Notes) != 0 {
					t.Errorf("an impossible budget must leave only the skeleton")
				}
			}
			if limit < full.characters && len(bounded.reductionNotes) == 0 {
				t.Errorf("reduced evidence carries no reduction notes")
			}
		})
	}
}

func TestBoundRepositoryEvidenceRemovesLowestPriorityContentFirst(t *testing.T) {
	evidence := largeEvidence()
	original := newEvidenceDocument(evidence)
	withoutPatches := applyToPullRequests(original, 0, pullRequestWithoutPatches)
	withoutOlderFileLists := applyToPullRequests(withoutPatches, retainedFileListPullRequests, pullRequestWithoutFileList)
	withoutOlderCommitBodies := applyToCommits(withoutOlderFileLists, retainedFullCommitMessages, commitWithSubjectOnly)

	t.Run("patches go first", func(t *testing.T) {
		bounded := boundRepositoryEvidence(evidence, encodedRuneCount(withoutPatches))
		document := decodeBoundedEvidence(t, bounded)
		for index, pullRequest := range document.PullRequests {
			for _, file := range pullRequest.Files {
				if file.Patch != "" {
					t.Fatalf("pull request %d still has a patch", index)
				}
			}
			if (pullRequest.Files == nil) != (original.PullRequests[index].Files == nil) {
				t.Errorf("pull request %d lost its file list before it was necessary", index)
			}
			if pullRequest.Body != original.PullRequests[index].Body {
				t.Errorf("pull request %d body changed before it was necessary", index)
			}
		}
		if !reflect.DeepEqual(document.Commits, original.Commits) {
			t.Errorf("commits changed before it was necessary")
		}
		if want := []string{"file patches were omitted for the 10 oldest pull requests that had them"}; !reflect.DeepEqual(bounded.reductionNotes, want) {
			t.Errorf("notes = %v, want %v", bounded.reductionNotes, want)
		}
	})

	t.Run("then file lists of older pull requests", func(t *testing.T) {
		bounded := boundRepositoryEvidence(evidence, encodedRuneCount(withoutPatches)-1)
		document := decodeBoundedEvidence(t, bounded)
		// The oldest pull request with a file list is index 14; dropping it is enough.
		if document.PullRequests[14].Files != nil {
			t.Errorf("the oldest file list was kept")
		}
		for index := 0; index < 14; index++ {
			if document.PullRequests[index].Files == nil {
				t.Errorf("pull request %d lost its file list although dropping the oldest one sufficed", index)
			}
		}
		if !reflect.DeepEqual(document.Commits, original.Commits) {
			t.Errorf("commits changed before it was necessary")
		}
		if len(bounded.reductionNotes) != 2 || !strings.Contains(bounded.reductionNotes[1], "changed-file lists were omitted for the 1 oldest") {
			t.Errorf("notes = %v", bounded.reductionNotes)
		}
	})

	t.Run("the newest file lists survive the older-file-list step", func(t *testing.T) {
		bounded := boundRepositoryEvidence(evidence, encodedRuneCount(withoutOlderFileLists))
		document := decodeBoundedEvidence(t, bounded)
		for index := 0; index < retainedFileListPullRequests; index++ {
			if document.PullRequests[index].Files == nil {
				t.Errorf("newest pull request %d lost its file list", index)
			}
		}
		for index := retainedFileListPullRequests; index < len(document.PullRequests); index++ {
			if document.PullRequests[index].Files != nil {
				t.Errorf("older pull request %d kept its file list", index)
			}
		}
	})

	t.Run("then commit messages beyond the newest twenty", func(t *testing.T) {
		bounded := boundRepositoryEvidence(evidence, encodedRuneCount(withoutOlderFileLists)-1)
		document := decodeBoundedEvidence(t, bounded)
		if got := document.Commits[59].Message; got != "Commit 59 subject" {
			t.Errorf("oldest commit message = %q, want its subject line", got)
		}
		for index := 0; index < 59; index++ {
			if document.Commits[index].Message != original.Commits[index].Message {
				t.Errorf("commit %d message changed although shortening the oldest sufficed", index)
			}
		}
		for index, pullRequest := range document.PullRequests {
			if pullRequest.Body != original.PullRequests[index].Body {
				t.Errorf("pull request %d body changed before it was necessary", index)
			}
		}
	})

	t.Run("then pull request descriptions are truncated", func(t *testing.T) {
		bounded := boundRepositoryEvidence(evidence, encodedRuneCount(withoutOlderCommitBodies)-1)
		document := decodeBoundedEvidence(t, bounded)
		oldest := document.PullRequests[len(document.PullRequests)-1]
		if !oldest.BodyTruncated || utf8.RuneCountInString(oldest.Body) != truncatedPullRequestBodyRunes {
			t.Errorf("oldest body has %d characters (truncated %v), want %d", utf8.RuneCountInString(oldest.Body), oldest.BodyTruncated, truncatedPullRequestBodyRunes)
		}
		for index := 0; index < retainedFullCommitMessages; index++ {
			if document.Commits[index].Message != original.Commits[index].Message {
				t.Errorf("newest commit %d message changed", index)
			}
		}
		if want := 4; len(bounded.reductionNotes) != want {
			t.Errorf("notes = %v, want %d steps", bounded.reductionNotes, want)
		}
	})
}

func TestEvidenceSizerTotalMatchesEncoding(t *testing.T) {
	sizer := newEvidenceSizer(newEvidenceDocument(largeEvidence()))
	assertExact := func(step string) {
		t.Helper()
		if got, want := sizer.total(), encodedRuneCount(sizer.document); got != want {
			t.Fatalf("after %s: tracked size %d, encoded size %d", step, got, want)
		}
	}
	assertExact("construction")
	steps := []struct {
		name  string
		apply func()
	}{
		{"patches", func() { sizer.reducePullRequests(0, 0, pullRequestWithoutPatches) }},
		{"older file lists", func() { sizer.reducePullRequests(0, retainedFileListPullRequests, pullRequestWithoutFileList) }},
		{"older commit messages", func() { sizer.reduceCommits(0, retainedFullCommitMessages, commitWithSubjectOnly) }},
		{"truncated bodies", func() { sizer.reducePullRequests(0, 0, pullRequestWithTruncatedBody) }},
		{"bodies", func() { sizer.reducePullRequests(0, 0, pullRequestWithoutBody) }},
		{"all file lists", func() { sizer.reducePullRequests(0, 0, pullRequestWithoutFileList) }},
		{"half the commits", func() { sizer.dropOldestCommits(sizer.total() - sizer.commitSizeSum/2) }},
		{"all commits", func() { sizer.dropOldestCommits(0) }},
		{"all pull requests", func() { sizer.dropOldestPullRequests(0) }},
		{"notes", func() { sizer.omitNotes() }},
	}
	for _, step := range steps {
		step.apply()
		assertExact(step.name)
	}
	if len(sizer.document.PullRequests)+len(sizer.document.Commits)+len(sizer.document.Notes) != 0 {
		t.Errorf("forced reduction did not empty the document")
	}
}

func TestBoundRepositoryEvidenceIsDeterministicAndOrderIndependent(t *testing.T) {
	evidence := largeEvidence()
	reversed := largeEvidence()
	slices.Reverse(reversed.PullRequests)
	slices.Reverse(reversed.Commits)
	for _, limit := range []int{1 << 30, 20000, 3000} {
		first := boundRepositoryEvidence(evidence, limit)
		again := boundRepositoryEvidence(largeEvidence(), limit)
		permuted := boundRepositoryEvidence(reversed, limit)
		if !reflect.DeepEqual(first, again) {
			t.Errorf("limit %d: identical input produced different evidence", limit)
		}
		if first.encodedJSON != permuted.encodedJSON {
			t.Errorf("limit %d: input order changed the evidence", limit)
		}
	}
	document := decodeBoundedEvidence(t, boundRepositoryEvidence(evidence, 1<<30))
	for index := 1; index < len(document.PullRequests); index++ {
		if document.PullRequests[index-1].MergedAt < document.PullRequests[index].MergedAt {
			t.Fatalf("pull requests are not newest first")
		}
	}
	if !reflect.DeepEqual(document.PullRequests[0].Labels, []string{"a-label", "b-label"}) {
		t.Errorf("labels are not sorted: %v", document.PullRequests[0].Labels)
	}
}

func TestBoundRepositoryEvidenceDoesNotModifyInput(t *testing.T) {
	evidence := largeEvidence()
	boundRepositoryEvidence(evidence, 500)
	if !reflect.DeepEqual(evidence, largeEvidence()) {
		t.Errorf("bounding modified the caller's evidence")
	}
}

func TestBoundRepositoryEvidenceDefaultsTheBudget(t *testing.T) {
	for _, limit := range []int{0, -10} {
		if got := boundRepositoryEvidence(largeEvidence(), limit).maximumCharacters; got != defaultMaximumEvidenceCharacters {
			t.Errorf("budget %d resolved to %d, want %d", limit, got, defaultMaximumEvidenceCharacters)
		}
	}
}

func TestBuildRepositorySummaryRequestKeepsEvidenceUnderTheLimit(t *testing.T) {
	for _, limit := range []int{60000, 20000, 5000, 1500} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			configuration := testConfiguration()
			configuration.Research.MaxEvidenceCharactersPerRepository = limit
			request := BuildRepositorySummaryRequest(configuration, fixtureResearchRequest(), largeEvidence())
			body := sectionBody(t, request.Prompt, sectionEvidence)
			if got := utf8.RuneCountInString(body); got > limit {
				t.Errorf("evidence section has %d characters, limit %d", got, limit)
			}
			if want := fmt.Sprintf("reduced to fit a budget of %d characters: file patches were omitted", limit); !strings.Contains(request.Prompt, want) {
				t.Errorf("prompt does not describe the reduction (%q)", want)
			}
			assertSectionIntegrity(t, request.Prompt, []string{sectionResearchRequest, sectionEvidence})
		})
	}
}
