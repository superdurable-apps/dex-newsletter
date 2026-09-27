package prompts

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/config"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

// Research brief bounds.
const (
	maximumBriefHighlights    = 12
	maximumBriefHeadlineRunes = 200
	maximumBriefOverviewRunes = 4000
	maximumOpenQuestions      = 10
	maximumOpenQuestionRunes  = 400
)

// digestURLDescription tells the model which URLs the brief may cite.
const digestURLDescription = "A reference URL copied exactly from the repository-digests section."

// researchBriefOutput is the strict wire shape of the model answer.
type researchBriefOutput struct {
	HasNotableChanges *bool                   `json:"hasNotableChanges"`
	Headline          string                  `json:"headline"`
	Overview          string                  `json:"overview"`
	Highlights        []model.ChangeHighlight `json:"highlights"`
	OpenQuestions     []string                `json:"openQuestions"`
}

// BuildResearchSynthesisRequest builds the synthesize-research generation
// request that merges repository digests into one research brief. Digests
// are sorted by repository so the prompt does not depend on the order in
// which repository research completed, and digests not marked relevant are
// shown without highlights.
func BuildResearchSynthesisRequest(configuration config.ProcessConfiguration, interpretation model.RequestInterpretation, window model.ChangeWindow, digests []model.RepositoryChangeDigest) model.GenerationRequest {
	focus := researchFocusDocument{
		Topic:        interpretation.Topic,
		Instructions: interpretation.Instructions,
		Audience:     interpretation.Audience,
	}

	var prompt strings.Builder
	writeParagraph(&prompt, "Task: synthesize the per-repository research digests in the repository-digests section into one research "+
		"brief for a technical blog post about the topic in the research-request section.")
	writeParagraph(&prompt, "Change window: merged or committed "+describeChangeWindow(window)+".")
	writeParagraph(&prompt, fmt.Sprintf("Each digest has a status: %q means the repository was researched; %q, %q, and %q mean its "+
		"research is missing or partial. relevant tells whether the repository had topic-related changes.",
		model.RepositoryResearched, model.RepositoryNotFound, model.RepositoryResearchIncomplete, model.RepositoryResearchFailed))
	writeBulletList(&prompt, "Instructions:", []string{
		"Include only meaningful new capabilities, features, and technical improvements related to the topic. Leave out chores, " +
			"dependency bumps, and unrelated changes.",
		"Merge duplicate or closely related highlights across repositories into one highlight and combine their references.",
		"Use only facts stated in the digests. Do not invent features, mechanisms, numbers, or links.",
		"Cite only reference URLs that appear in the repository-digests section, copied exactly. References with any other URL are discarded.",
		"Do not write any other URL into the headline, overview, open questions, or highlight text: any link or URL there that is not " +
			"one of those reference URLs is removed. A web address without https://, such as www.example.com or example.com/path, counts " +
			"as a URL, inside `code` spans too.",
		"hasNotableChanges: false when no digest contains a relevant, meaningful change; then return an empty highlights list and " +
			"explain in the overview.",
		"headline: a specific, factual one-line headline. overview: one paragraph that connects the highlights.",
		fmt.Sprintf("openQuestions: at most %d gaps the writer should know about, such as repositories whose research was incomplete "+
			"or changes whose motivation is unclear.", maximumOpenQuestions),
		fmt.Sprintf("Return at most %d highlights, most significant first.", maximumBriefHighlights),
	})
	writeDelimitedSection(&prompt, sectionResearchRequest, focus)
	writeDelimitedSection(&prompt, sectionRepositoryDigests, synthesisPromptDigests(digests))

	systemInstruction := composeSystemInstruction(
		"You are a technical editor who turns per-repository engineering research into one accurate, well-organized brief.",
		"Never invent features, mechanisms, numbers, people, or links that the digests do not support.",
	)
	return newStageGenerationRequest(configuration, PurposeSynthesizeResearch, configuration.LanguageModel.SynthesizeResearch,
		systemInstruction, finishPrompt(&prompt), researchBriefSchema())
}

// researchBriefSchema describes model.ResearchBrief.
func researchBriefSchema() map[string]any {
	return objectSchema("Cross-repository research brief for the blog post.", map[string]any{
		"hasNotableChanges": booleanSchema("Whether any meaningful topic-related change was found."),
		"headline":          stringSchema("Specific, factual one-line headline.", maximumBriefHeadlineRunes),
		"overview":          stringSchema("One paragraph connecting the highlights.", maximumBriefOverviewRunes),
		"highlights": arraySchema("Merged, meaningful changes, most significant first.",
			changeHighlightSchema(digestURLDescription), 0, maximumBriefHighlights),
		"openQuestions": arraySchema("Gaps the writer should know about.",
			stringSchema("One open question.", maximumOpenQuestionRunes), 0, maximumOpenQuestions),
	}, "hasNotableChanges", "headline", "overview", "highlights", "openQuestions")
}

// ParseResearchBrief strictly decodes the synthesize-research answer.
// References are kept only when their URL appears as a reference of a
// highlight in some relevant digest (rewritten to that spelling); the same
// rule applies to [label](url) links and bare URLs in the headline, overview,
// open questions, and every highlight text field, where any other link is
// reduced to its label and any other URL is removed. Highlights are bounded
// and capped at 12, open questions at 10. An answer with hasNotableChanges
// false keeps no highlights, so HasNotableChanges is true exactly when at
// least one highlight remains.
func ParseResearchBrief(result model.GenerationResult, digests []model.RepositoryChangeDigest) (model.ResearchBrief, error) {
	var output researchBriefOutput
	if err := decodeGenerationResult(result, PurposeSynthesizeResearch, &output); err != nil {
		return model.ResearchBrief{}, err
	}
	if output.HasNotableChanges == nil {
		return model.ResearchBrief{}, invalidModelOutput(PurposeSynthesizeResearch, "hasNotableChanges is missing")
	}
	catalog := digestCitableSources(digests)
	highlights := []model.ChangeHighlight{}
	if *output.HasNotableChanges {
		highlights = sanitizeChangeHighlights(output.Highlights, catalog, maximumBriefHighlights)
	}
	return model.ResearchBrief{
		HasNotableChanges: len(highlights) > 0,
		Headline:          sanitizeInlineMarkupLine(output.Headline, strictInlineMarkupRules(catalog), maximumBriefHeadlineRunes),
		Overview:          sanitizeInlineMarkupLine(output.Overview, strictInlineMarkupRules(catalog), maximumBriefOverviewRunes),
		Highlights:        highlights,
		OpenQuestions:     sanitizeTextList(output.OpenQuestions, catalog, maximumOpenQuestionRunes, maximumOpenQuestions),
	}, nil
}

// synthesisPromptDigests returns the digests as the synthesis prompt shows
// them: sorted by repository, with the highlights of digests not marked
// relevant removed, because a repository without topic-related changes must
// contribute neither changes nor citations to the brief.
func synthesisPromptDigests(digests []model.RepositoryChangeDigest) []model.RepositoryChangeDigest {
	sorted := sortedRepositoryChangeDigests(digests)
	for index := range sorted {
		if !sorted[index].Relevant {
			sorted[index].Highlights = []model.ChangeHighlight{}
		}
	}
	return sorted
}

// sortedRepositoryChangeDigests returns a copy ordered by repository, case
// insensitively and then exactly.
func sortedRepositoryChangeDigests(digests []model.RepositoryChangeDigest) []model.RepositoryChangeDigest {
	sorted := slices.Clone(digests)
	if sorted == nil {
		sorted = []model.RepositoryChangeDigest{}
	}
	slices.SortStableFunc(sorted, func(first model.RepositoryChangeDigest, second model.RepositoryChangeDigest) int {
		firstName, secondName := formatRepositoryReference(first.Repository), formatRepositoryReference(second.Repository)
		if order := cmp.Compare(strings.ToLower(firstName), strings.ToLower(secondName)); order != 0 {
			return order
		}
		return cmp.Compare(firstName, secondName)
	})
	return sorted
}

// digestCitableSources registers every highlight reference of every digest
// shown to the synthesis model, which excludes digests not marked relevant.
func digestCitableSources(digests []model.RepositoryChangeDigest) citableSourceCatalog {
	catalog := newCitableSourceCatalog()
	for _, digest := range synthesisPromptDigests(digests) {
		for _, highlight := range digest.Highlights {
			for _, reference := range highlight.References {
				catalog.addSource(reference.URL, reference.Label)
			}
		}
	}
	return catalog
}
