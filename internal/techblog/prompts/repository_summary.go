package prompts

import (
	"fmt"
	"strings"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/config"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

// Repository digest bounds.
const (
	maximumDigestHighlights   = 12
	maximumDigestSummaryRunes = 3000
	shortCommitSHARunes       = 7
)

// evidenceURLDescription tells the model which URLs a digest may cite.
const evidenceURLDescription = "A pull request or commit URL copied exactly from the evidence section."

// repositoryChangeDigestOutput is the strict wire shape of the model answer.
type repositoryChangeDigestOutput struct {
	Relevant   *bool                   `json:"relevant"`
	Summary    string                  `json:"summary"`
	Highlights []model.ChangeHighlight `json:"highlights"`
}

// BuildRepositorySummaryRequest builds the summarize-repository generation
// request for one repository. The evidence is encoded as JSON (pull requests
// and commits newest first) and bounded to
// configuration.Research.MaxEvidenceCharactersPerRepository characters by
// removing the lowest-priority content first: patches, then file lists of
// older pull requests, then commit messages beyond the newest twenty, then
// pull request descriptions (truncated), with further fallbacks if needed.
// Whatever was removed is described in the prompt outside the evidence
// section, and when a very small budget cannot be met even by removing
// everything, the prompt says the evidence is incomplete instead of claiming
// that it fits.
func BuildRepositorySummaryRequest(configuration config.ProcessConfiguration, research model.RepositoryResearchRequest, evidence model.RepositoryChangeEvidence) model.GenerationRequest {
	evidence.Repository = digestRepository(research, evidence)
	bounded := boundRepositoryEvidence(evidence, configuration.Research.MaxEvidenceCharactersPerRepository)
	focus := researchFocusDocument{
		Repository:      formatRepositoryReference(evidence.Repository),
		Topic:           research.Topic,
		Instructions:    research.Instructions,
		PathHints:       copyStrings(research.Selection.PathHints),
		SelectionReason: research.Selection.Reason,
	}

	var prompt strings.Builder
	writeParagraph(&prompt, "Task: summarize what changed in one GitHub repository during the change window, as research for a "+
		"technical blog post about the topic in the research-request section. The repository is named in that section.")
	writeParagraph(&prompt, "Change window: merged or committed "+describeChangeWindow(research.Window)+".")
	evidenceDescription := "The evidence section holds the repository's merged pull requests (newest first, with descriptions, labels, " +
		"and changed files with patches where available), its commits (newest first), and notes from the collection step."
	switch {
	case !bounded.withinBudget:
		evidenceDescription += fmt.Sprintf(" The evidence could not be reduced to fit the budget of %d characters, so it is incomplete",
			bounded.maximumCharacters)
		if len(bounded.reductionNotes) > 0 {
			evidenceDescription += ": " + strings.Join(bounded.reductionNotes, "; ")
		}
		evidenceDescription += ". Treat omitted details as unknown, not as absent, and say in the summary that the research was incomplete."
	case len(bounded.reductionNotes) > 0:
		evidenceDescription += fmt.Sprintf(" The evidence was reduced to fit a budget of %d characters: %s. Treat omitted details as "+
			"unknown, not as absent.", bounded.maximumCharacters, strings.Join(bounded.reductionNotes, "; "))
	}
	writeParagraph(&prompt, evidenceDescription)
	writeBulletList(&prompt, "Instructions:", []string{
		"Focus on the topic and on changes under the path hints in the research-request section. Ignore unrelated changes, routine " +
			"dependency bumps, formatting, CI, and release chores unless the topic is about them.",
		"The instructions field of the research-request section describes how the final post should be written; use it only to " +
			"decide what to emphasize.",
		"Report only meaningful new capabilities, features, and technical improvements. Merge pull requests and commits that belong " +
			"to the same change into one highlight.",
		"For each highlight give a short title, whatChanged, howItWorks (the mechanism, grounded in patches, file names, and " +
			"descriptions), whyItMatters (the benefit to users or developers), and references to the pull requests or commits it is based on.",
		"Cite only pull request and commit URLs that appear in the evidence section, copied exactly. References with any other URL are discarded.",
		"Do not copy other URLs, such as links in pull request descriptions, into the summary or highlight text: any link or URL there " +
			"that is not one of those pull request or commit URLs is removed. A web address without https://, such as www.example.com or " +
			"example.com/path, counts as a URL, inside `code` spans too.",
		"Base every statement on the evidence. When the evidence does not explain how something works, say what is known instead of guessing.",
		"relevant: false when nothing in the evidence relates to the topic; then return an empty highlights list.",
		"summary: two to four sentences on the repository's topic-related changes in the window, or on why nothing relevant was found.",
		fmt.Sprintf("Return at most %d highlights, most significant first.", maximumDigestHighlights),
	})
	writeDelimitedSection(&prompt, sectionResearchRequest, focus)
	writeEncodedSection(&prompt, sectionEvidence, bounded.encodedJSON)

	systemInstruction := composeSystemInstruction(
		"You are a senior software engineer who reads merged pull requests and commits and explains, accurately and concretely, "+
			"what was built and how it works.",
		"Never invent features, mechanisms, numbers, people, or links that the evidence does not support.",
	)
	return newStageGenerationRequest(configuration, PurposeSummarizeRepository, configuration.LanguageModel.SummarizeRepository,
		systemInstruction, finishPrompt(&prompt), repositoryChangeDigestSchema())
}

// repositoryChangeDigestSchema describes the digest answer.
func repositoryChangeDigestSchema() map[string]any {
	return objectSchema("Topic-focused summary of one repository's changes.", map[string]any{
		"relevant": booleanSchema("Whether the evidence contains changes related to the topic."),
		"summary":  stringSchema("Two to four sentences on the topic-related changes, or why none were found.", maximumDigestSummaryRunes),
		"highlights": arraySchema("Meaningful topic-related changes, most significant first.",
			changeHighlightSchema(evidenceURLDescription), 0, maximumDigestHighlights),
	}, "relevant", "summary", "highlights")
}

// ParseRepositoryChangeDigest strictly decodes the summarize-repository
// answer into a digest with Status model.RepositoryResearched. Repository
// comes from the evidence (or the research selection when the evidence names
// none); PullRequestCount, CommitCount, and a copy of Notes come from the
// evidence. References are kept only when their URL is a pull request or
// commit URL present in the evidence (matched ignoring case and trailing
// slashes, and rewritten to the evidence spelling), so invented links never
// survive; the same rule applies to [label](url) links and bare URLs inside
// the summary and every highlight text field, where any other link is reduced
// to its label and any other URL is removed, so URLs from pull request
// descriptions never reach later stages. Highlights are bounded and capped at
// 12. An answer with relevant false keeps no highlights, and evidence without
// pull requests or commits yields none, so Relevant is true exactly when at
// least one highlight remains.
func ParseRepositoryChangeDigest(result model.GenerationResult, research model.RepositoryResearchRequest, evidence model.RepositoryChangeEvidence) (model.RepositoryChangeDigest, error) {
	var output repositoryChangeDigestOutput
	if err := decodeGenerationResult(result, PurposeSummarizeRepository, &output); err != nil {
		return model.RepositoryChangeDigest{}, err
	}
	if output.Relevant == nil {
		return model.RepositoryChangeDigest{}, invalidModelOutput(PurposeSummarizeRepository, "relevant is missing")
	}
	repository := digestRepository(research, evidence)
	catalog := evidenceCitableSources(repository, evidence)
	highlights := []model.ChangeHighlight{}
	if *output.Relevant && (len(evidence.PullRequests) > 0 || len(evidence.Commits) > 0) {
		highlights = sanitizeChangeHighlights(output.Highlights, catalog, maximumDigestHighlights)
	}
	return model.RepositoryChangeDigest{
		Repository:       repository,
		Status:           model.RepositoryResearched,
		Relevant:         len(highlights) > 0,
		Summary:          sanitizeInlineMarkupLine(output.Summary, strictInlineMarkupRules(catalog), maximumDigestSummaryRunes),
		Highlights:       highlights,
		PullRequestCount: len(evidence.PullRequests),
		CommitCount:      len(evidence.Commits),
		Notes:            copyStrings(evidence.Notes),
	}, nil
}

// digestRepository is the evidence repository, or the selected repository
// when the evidence names none.
func digestRepository(research model.RepositoryResearchRequest, evidence model.RepositoryChangeEvidence) model.RepositoryReference {
	if formatRepositoryReference(evidence.Repository) == "" {
		return research.Selection.Repository
	}
	return evidence.Repository
}

// evidenceCitableSources registers every pull request and commit URL in the
// evidence, in input order, with labels such as owner/name#12 and
// owner/name@abc1234.
func evidenceCitableSources(repository model.RepositoryReference, evidence model.RepositoryChangeEvidence) citableSourceCatalog {
	catalog := newCitableSourceCatalog()
	repositoryName := formatRepositoryReference(repository)
	for _, pullRequest := range evidence.PullRequests {
		label := "Pull request"
		if pullRequest.Number > 0 {
			label = fmt.Sprintf("%s#%d", repositoryName, pullRequest.Number)
		}
		catalog.addSource(pullRequest.URL, label)
	}
	for _, commit := range evidence.Commits {
		label := "Commit"
		if shortSHA := []rune(sanitizeSingleLine(commit.SHA, 0)); len(shortSHA) > 0 {
			label = repositoryName + "@" + string(shortSHA[:min(len(shortSHA), shortCommitSHARunes)])
		}
		catalog.addSource(commit.URL, label)
	}
	return catalog
}
