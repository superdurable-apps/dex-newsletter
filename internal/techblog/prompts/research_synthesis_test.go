package prompts

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

func TestBuildResearchSynthesisRequestContents(t *testing.T) {
	request := BuildResearchSynthesisRequest(testConfiguration(), fixtureInterpretation(), fixtureWindow(), fixtureDigests())
	for _, want := range []string{
		`"researched" means the repository was researched`,
		`"repository-not-found", "research-incomplete", and "research-failed"`,
		"Merge duplicate or closely related highlights across repositories",
		"Use only facts stated in the digests",
		"until 2026-09-26T15:04:05Z (exclusive)",
	} {
		if !strings.Contains(request.Prompt, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	var focus researchFocusDocument
	decodeSection(t, request.Prompt, sectionResearchRequest, &focus)
	if want := (researchFocusDocument{Topic: "connectors", Instructions: "Keep it short.", Audience: "platform engineers"}); !reflect.DeepEqual(focus, want) {
		t.Errorf("research-request section = %+v, want %+v", focus, want)
	}
	var digests []model.RepositoryChangeDigest
	decodeSection(t, request.Prompt, sectionRepositoryDigests, &digests)
	if len(digests) != 2 || digests[0].Repository.Name != "dex" || digests[1].Repository.Name != "dex-connectors-library" {
		t.Errorf("digests are not sorted by repository: %+v", digests)
	}
	for _, name := range []string{"hasNotableChanges", "headline", "overview", "highlights", "openQuestions"} {
		schemaProperty(t, decodedResponseSchema(t, request), name)
	}
}

func TestBuildResearchSynthesisRequestHidesHighlightsOfIrrelevantDigests(t *testing.T) {
	digests := fixtureDigests()
	digests[0].Relevant = false
	request := BuildResearchSynthesisRequest(testConfiguration(), fixtureInterpretation(), fixtureWindow(), digests)
	var shown []model.RepositoryChangeDigest
	decodeSection(t, request.Prompt, sectionRepositoryDigests, &shown)
	for _, digest := range shown {
		if digest.Repository.Name == "dex-connectors-library" && (digest.Relevant || len(digest.Highlights) != 0) {
			t.Errorf("irrelevant digest shown with highlights: %+v", digest)
		}
		if digest.Repository.Name == "dex" && len(digest.Highlights) != 1 {
			t.Errorf("relevant digest lost its highlights: %+v", digest)
		}
	}
	if len(digests[0].Highlights) != 1 {
		t.Errorf("building the request modified the caller's digests")
	}
}

func TestBuildResearchSynthesisRequestIgnoresCompletionOrder(t *testing.T) {
	digests := fixtureDigests()
	reversed := fixtureDigests()
	slices.Reverse(reversed)
	first := BuildResearchSynthesisRequest(testConfiguration(), fixtureInterpretation(), fixtureWindow(), digests)
	second := BuildResearchSynthesisRequest(testConfiguration(), fixtureInterpretation(), fixtureWindow(), reversed)
	if first.Prompt != second.Prompt {
		t.Errorf("digest order changed the prompt")
	}
	if !reflect.DeepEqual(digests, fixtureDigests()) {
		t.Errorf("building the request reordered the caller's digests")
	}
	empty := BuildResearchSynthesisRequest(testConfiguration(), fixtureInterpretation(), fixtureWindow(), nil)
	if body := sectionBody(t, empty.Prompt, sectionRepositoryDigests); body != "[]" {
		t.Errorf("no digests encoded as %q, want []", body)
	}
}

func TestParseResearchBrief(t *testing.T) {
	manyQuestions := []string{" ", "Same?", "Same?"}
	for index := 0; index < 15; index++ {
		manyQuestions = append(manyQuestions, fmt.Sprintf("Question %d?", index))
	}
	cases := []struct {
		name        string
		answer      map[string]any
		digests     []model.RepositoryChangeDigest
		wantInvalid bool
		check       func(t *testing.T, brief model.ResearchBrief)
	}{
		{
			name: "only digest references survive",
			answer: map[string]any{"hasNotableChanges": true, "headline": " Connectors\nimproved ", "overview": "O", "openQuestions": []string{},
				"highlights": []any{highlightAnswer("Retries",
					referenceAnswer("PR", connectorsPullRequest101URL),
					referenceAnswer("Invented", inventedURL),
					referenceAnswer("Commit", strings.ToUpper(dexCommitURL)),
					referenceAnswer("Not cited by any digest", connectorsPullRequest102URL),
				)}},
			check: func(t *testing.T, brief model.ResearchBrief) {
				want := []model.SourceReference{{Label: "PR", URL: connectorsPullRequest101URL}, {Label: "Commit", URL: dexCommitURL}}
				if !reflect.DeepEqual(brief.Highlights[0].References, want) {
					t.Errorf("references = %+v, want %+v", brief.Highlights[0].References, want)
				}
				if !brief.HasNotableChanges || brief.Headline != "Connectors improved" {
					t.Errorf("brief = %+v", brief)
				}
			},
		},
		{
			name:   "notable changes without highlights are not notable",
			answer: map[string]any{"hasNotableChanges": true, "headline": "H", "overview": "O", "highlights": []any{}, "openQuestions": []string{}},
			check: func(t *testing.T, brief model.ResearchBrief) {
				if brief.HasNotableChanges || brief.Highlights == nil {
					t.Errorf("brief = %+v", brief)
				}
			},
		},
		{
			name: "notable changes whose highlights are all untitled are not notable",
			answer: map[string]any{"hasNotableChanges": true, "headline": "H", "overview": "O", "openQuestions": []string{},
				"highlights": []any{highlightAnswer(""), highlightAnswer(string(rune(0x200B)))}},
			check: func(t *testing.T, brief model.ResearchBrief) {
				if brief.HasNotableChanges || len(brief.Highlights) != 0 {
					t.Errorf("brief = %+v", brief)
				}
			},
		},
		{
			name: "no notable changes keeps no highlights or citations",
			answer: map[string]any{"hasNotableChanges": false, "headline": "", "overview": "Nothing new.", "openQuestions": []string{},
				"highlights": []any{highlightAnswer("Retries", referenceAnswer("PR", connectorsPullRequest101URL))}},
			check: func(t *testing.T, brief model.ResearchBrief) {
				if brief.HasNotableChanges || brief.Highlights == nil || len(brief.Highlights) != 0 {
					t.Errorf("brief = %+v", brief)
				}
			},
		},
		{
			name: "references of digests not marked relevant are not citable",
			answer: map[string]any{"hasNotableChanges": true, "headline": "H", "overview": "O", "openQuestions": []string{},
				"highlights": []any{highlightAnswer("Retries", referenceAnswer("PR", connectorsPullRequest101URL), referenceAnswer("Commit", dexCommitURL))}},
			digests: func() []model.RepositoryChangeDigest {
				digests := fixtureDigests()
				digests[0].Relevant = false
				return digests
			}(),
			check: func(t *testing.T, brief model.ResearchBrief) {
				if want := []model.SourceReference{{Label: "Commit", URL: dexCommitURL}}; !reflect.DeepEqual(brief.Highlights[0].References, want) {
					t.Errorf("references = %+v, want %+v", brief.Highlights[0].References, want)
				}
			},
		},
		{
			name: "links and URLs outside the digests are removed from text fields",
			answer: map[string]any{
				"hasNotableChanges": true,
				"headline":          "Retries [land](https://attacker.example/headline)",
				"overview":          "See https://attacker.example/overview and [PR 101](" + connectorsPullRequest101URL + ").",
				"openQuestions":     []string{"Is https://attacker.example/q documented?"},
				"highlights": []any{map[string]any{
					"title": "Retries", "whatChanged": "What [changed](https://attacker.example/what).", "howItWorks": "How.",
					"whyItMatters": "Why " + dexCommitURL + ".", "references": []any{referenceAnswer("PR", connectorsPullRequest101URL)},
				}},
			},
			check: func(t *testing.T, brief model.ResearchBrief) {
				if brief.Headline != "Retries land" || brief.Overview != "See and [PR 101]("+connectorsPullRequest101URL+")." {
					t.Errorf("headline %q overview %q", brief.Headline, brief.Overview)
				}
				if want := []string{"Is documented?"}; !reflect.DeepEqual(brief.OpenQuestions, want) {
					t.Errorf("open questions = %q, want %q", brief.OpenQuestions, want)
				}
				if got := brief.Highlights[0]; got.WhatChanged != "What changed." || got.WhyItMatters != "Why "+dexCommitURL+"." {
					t.Errorf("highlight = %+v", got)
				}
			},
		},
		{
			name:   "no notable changes stays so",
			answer: map[string]any{"hasNotableChanges": false, "headline": "", "overview": "Nothing new.", "highlights": []any{}, "openQuestions": nil},
			check: func(t *testing.T, brief model.ResearchBrief) {
				want := model.ResearchBrief{Overview: "Nothing new.", Highlights: []model.ChangeHighlight{}, OpenQuestions: []string{}}
				if !reflect.DeepEqual(brief, want) {
					t.Errorf("got %+v, want %+v", brief, want)
				}
			},
		},
		{
			name:   "open questions are cleaned, deduplicated, and capped",
			answer: map[string]any{"hasNotableChanges": false, "headline": "", "overview": "", "highlights": []any{}, "openQuestions": manyQuestions},
			check: func(t *testing.T, brief model.ResearchBrief) {
				if len(brief.OpenQuestions) != maximumOpenQuestions || brief.OpenQuestions[0] != "Same?" || brief.OpenQuestions[1] != "Question 0?" {
					t.Errorf("open questions = %v", brief.OpenQuestions)
				}
			},
		},
		{
			name: "with no digests nothing is citable",
			answer: map[string]any{"hasNotableChanges": true, "headline": "H", "overview": "O", "openQuestions": []string{},
				"highlights": []any{highlightAnswer("Retries", referenceAnswer("PR", connectorsPullRequest101URL))}},
			digests: []model.RepositoryChangeDigest{},
			check: func(t *testing.T, brief model.ResearchBrief) {
				if len(brief.Highlights[0].References) != 0 {
					t.Errorf("references = %+v", brief.Highlights[0].References)
				}
			},
		},
		{
			name:        "missing hasNotableChanges is invalid",
			answer:      map[string]any{"headline": "H", "overview": "O", "highlights": []any{}, "openQuestions": []string{}},
			wantInvalid: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			digests := testCase.digests
			if digests == nil {
				digests = fixtureDigests()
			}
			brief, err := ParseResearchBrief(succeededResult(PurposeSynthesizeResearch, jsonText(t, testCase.answer)), digests)
			if testCase.wantInvalid {
				if !errors.Is(err, ErrInvalidModelOutput) {
					t.Fatalf("err = %v, want ErrInvalidModelOutput", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			testCase.check(t, brief)
		})
	}
}
