// Package config loads and validates the non-secret process configuration for
// the tech blog newsletter Process. Credentials never appear here: provider
// secrets live in the Dex connector connection store, and the Slack channel,
// subscriber sheet, and other per-connection choices live in Dex Web
// connection and operation configuration.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// ProviderGemini is the initial language-model provider.
const ProviderGemini = "gemini"

// SupportedLanguageModelProviders lists providers with a registered Connector
// Step in LanguageModelGenerationFlow. Add a provider here only together with
// its Connector Step.
var SupportedLanguageModelProviders = []string{ProviderGemini}

var (
	githubOwnerPattern      = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	githubRepositoryPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

// Duration is a time.Duration encoded as a Go duration string such as "72h".
type Duration time.Duration

// UnmarshalJSON decodes a Go duration string.
func (duration *Duration) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("duration must be a string such as \"72h\": %w", err)
	}
	parsed, err := time.ParseDuration(text)
	if err != nil {
		return err
	}
	*duration = Duration(parsed)
	return nil
}

// MarshalJSON encodes the duration as a Go duration string.
func (duration Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(duration).String())
}

// ProcessConfiguration is the complete non-secret process configuration.
type ProcessConfiguration struct {
	ApplicationName string                     `json:"applicationName"`
	DexWebURL       string                     `json:"dexWebUrl"`
	LanguageModel   LanguageModelConfiguration `json:"languageModel"`
	Research        ResearchConfiguration      `json:"research"`
	Blog            BlogConfiguration          `json:"blog"`
	Newsletter      NewsletterConfiguration    `json:"newsletter"`
	Review          ReviewConfiguration        `json:"review"`
}

// StageModelConfiguration selects the model and generation settings for one
// language-model stage. Provider overrides LanguageModelConfiguration.Provider
// for this stage only. A blank Model uses the model chosen on the provider's
// Dex Web Connection, so a model can be switched there without editing this
// file; a Model set here overrides the Connection for this stage only.
type StageModelConfiguration struct {
	Provider        string   `json:"provider,omitempty"`
	Model           string   `json:"model,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	MaxOutputTokens int      `json:"maxOutputTokens"`
	ThinkingBudget  *int     `json:"thinkingBudget,omitempty"`
}

// LanguageModelConfiguration configures every language-model stage.
type LanguageModelConfiguration struct {
	Provider            string                  `json:"provider"`
	InterpretRequest    StageModelConfiguration `json:"interpretRequest"`
	SummarizeRepository StageModelConfiguration `json:"summarizeRepository"`
	SynthesizeResearch  StageModelConfiguration `json:"synthesizeResearch"`
	DraftBlogPost       StageModelConfiguration `json:"draftBlogPost"`
	DraftNewsletter     StageModelConfiguration `json:"draftNewsletter"`
}

// ResolveStageProvider returns the provider that serves one stage.
func (configuration LanguageModelConfiguration) ResolveStageProvider(stage StageModelConfiguration) string {
	if strings.TrimSpace(stage.Provider) != "" {
		return strings.TrimSpace(stage.Provider)
	}
	return strings.TrimSpace(configuration.Provider)
}

// RepositoryCatalogEntry is one GitHub repository the Process may research.
type RepositoryCatalogEntry struct {
	Owner       string   `json:"owner"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Topics      []string `json:"topics"`
	PathHints   []string `json:"pathHints"`
}

// ResearchConfiguration bounds repository research.
type ResearchConfiguration struct {
	DefaultLookbackDays                int                      `json:"defaultLookbackDays"`
	MaxLookbackDays                    int                      `json:"maxLookbackDays"`
	MaxRepositoriesPerRequest          int                      `json:"maxRepositoriesPerRequest"`
	MaxMergedPullRequestsPerRepository int                      `json:"maxMergedPullRequestsPerRepository"`
	MaxPullRequestsWithFileDetails     int                      `json:"maxPullRequestsWithFileDetails"`
	MaxFilesPerPullRequest             int                      `json:"maxFilesPerPullRequest"`
	MaxCommitsPerRepository            int                      `json:"maxCommitsPerRepository"`
	MaxPatchCharactersPerFile          int                      `json:"maxPatchCharactersPerFile"`
	MaxEvidenceCharactersPerRepository int                      `json:"maxEvidenceCharactersPerRepository"`
	Repositories                       []RepositoryCatalogEntry `json:"repositories"`
}

// BlogConfiguration configures blog voice, presentation, and the HTML
// artifact location.
type BlogConfiguration struct {
	SiteName          string `json:"siteName"`
	Author            string `json:"author"`
	StyleGuide        string `json:"styleGuide"`
	PublicBaseURL     string `json:"publicBaseUrl"`
	ArtifactDirectory string `json:"artifactDirectory"`
}

// NewsletterConfiguration bounds the subscriber list and delivery.
// MaxRecipients caps both the subscriber list, which rejects new
// subscriptions once full, and each newsletter's recipients.
type NewsletterConfiguration struct {
	MaxRecipients int    `json:"maxRecipients"`
	Footer        string `json:"footer"`
}

// ReviewConfiguration configures the editorial approval gate.
type ReviewConfiguration struct {
	Required         bool     `json:"required"`
	ReminderInterval Duration `json:"reminderInterval"`
	MaxReminders     int      `json:"maxReminders"`
	MaxRevisions     int      `json:"maxRevisions"`
}

// Default returns a complete configuration for the superdurable public
// repositories with Gemini as the language-model provider.
//
// The default stages leave Temperature unset so Gemini 3 uses its own
// default temperature, which Google recommends keeping for Gemini 3 models.
// A stage that sets Temperature (or ThinkingBudget) must own its pointer:
// json.Decoder writes through a non-nil pointer in place, so a shared
// pointer would let a file that overrides one stage silently change the
// others.
func Default() ProcessConfiguration {
	return ProcessConfiguration{
		ApplicationName: "Dex Tech Blog",
		DexWebURL:       "http://127.0.0.1:8802",
		LanguageModel: LanguageModelConfiguration{
			Provider:            ProviderGemini,
			InterpretRequest:    StageModelConfiguration{MaxOutputTokens: 8192},
			SummarizeRepository: StageModelConfiguration{MaxOutputTokens: 16384},
			SynthesizeResearch:  StageModelConfiguration{MaxOutputTokens: 16384},
			DraftBlogPost:       StageModelConfiguration{MaxOutputTokens: 32768},
			DraftNewsletter:     StageModelConfiguration{MaxOutputTokens: 16384},
		},
		Research: ResearchConfiguration{
			DefaultLookbackDays:                7,
			MaxLookbackDays:                    90,
			MaxRepositoriesPerRequest:          5,
			MaxMergedPullRequestsPerRepository: 30,
			MaxPullRequestsWithFileDetails:     10,
			MaxFilesPerPullRequest:             40,
			MaxCommitsPerRepository:            60,
			MaxPatchCharactersPerFile:          1500,
			MaxEvidenceCharactersPerRepository: 60000,
			Repositories: []RepositoryCatalogEntry{
				{
					Owner: "superdurable", Name: "dex",
					Description: "Dex durable execution: server, SDKs (Go, Python, Java, TypeScript, Rust), dexcli, Dex Web, examples, and docs.",
					Topics:      []string{"durable execution", "flows", "sdk", "dex web", "cli", "server"},
					PathHints:   []string{"server/", "sdk-go/", "sdk-python/", "sdk-typescript/", "sdk-java/", "sdk-rust/", "cli/", "web/", "docs/"},
				},
				{
					Owner: "superdurable", Name: "dex-connectors-library",
					Description: "Official Dex connectors (Slack, Gmail, Google Sheets, GitHub, and language-model providers such as Gemini, OpenAI, and Claude), the connector SDK, manifests, and code generation.",
					Topics:      []string{"connectors", "integrations", "triggers", "connector sdk"},
					PathHints:   []string{"connectors/", "sdkgo/", "sdk/", "cmd/connectorctl/", "docs/"},
				},
				{
					Owner: "superdurable", Name: "dex-skills",
					Description: "Official Dex coding-agent skills: dex-sdk, dex-app-builder, and dex-connector-contributor.",
					Topics:      []string{"agent skills", "ai coding", "developer experience"},
					PathHints:   []string{"dex-sdk/", "dex-app-builder/", "dex-connector-contributor/"},
				},
				{
					Owner: "superdurable", Name: "dex-template-basic-process",
					Description: "Go and React template for Dex process applications with Dex Web v2.",
					Topics:      []string{"templates", "process applications", "dex web"},
					PathHints:   []string{"internal/", "web/", "openapi/"},
				},
			},
		},
		Blog: BlogConfiguration{
			SiteName:          "Dex Engineering Blog",
			Author:            "The Dex team",
			StyleGuide:        "Write for experienced engineers. Be concrete and technically precise, explain how things work, show short code where it clarifies, and avoid marketing language.",
			ArtifactDirectory: "artifacts",
		},
		Newsletter: NewsletterConfiguration{
			MaxRecipients: 500,
			Footer:        "You are receiving this because you subscribed to Dex engineering updates.",
		},
		Review: ReviewConfiguration{
			Required:         true,
			ReminderInterval: Duration(72 * time.Hour),
			MaxReminders:     1,
			MaxRevisions:     3,
		},
	}
}

// Load strictly decodes a configuration file over Default and validates it.
// An empty path returns the validated default configuration.
func Load(path string) (ProcessConfiguration, error) {
	configuration := Default()
	if strings.TrimSpace(path) == "" {
		return configuration, configuration.Validate()
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return ProcessConfiguration{}, fmt.Errorf("read process configuration: %w", err)
	}
	if err := rejectRemovedFields(contents); err != nil {
		return ProcessConfiguration{}, fmt.Errorf("process configuration %s: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&configuration); err != nil {
		return ProcessConfiguration{}, fmt.Errorf("decode process configuration %s: %w", path, err)
	}
	if decoder.More() {
		return ProcessConfiguration{}, fmt.Errorf("decode process configuration %s: trailing data", path)
	}
	if err := configuration.Validate(); err != nil {
		return ProcessConfiguration{}, fmt.Errorf("process configuration %s: %w", path, err)
	}
	return configuration, nil
}

// removedNewsletterFields are the Google Sheets subscriber settings that the
// Dex subscriber list replaced.
var removedNewsletterFields = []string{"subscriberSheet", "emailColumnHeader", "statusColumnHeader", "unsubscribedStatusValues"}

// rejectRemovedFields explains a configuration file written for the Google
// Sheets subscriber list instead of failing with a bare unknown-field error.
// Malformed JSON is left for the strict decoder to report.
func rejectRemovedFields(contents []byte) error {
	var document struct {
		Newsletter map[string]json.RawMessage `json:"newsletter"`
	}
	if json.Unmarshal(contents, &document) != nil {
		return nil
	}
	var present []string
	for _, field := range removedNewsletterFields {
		if _, found := document.Newsletter[field]; found {
			present = append(present, "newsletter."+field)
		}
	}
	if len(present) == 0 {
		return nil
	}
	return fmt.Errorf("%s no longer exist: newsletter subscribers now live in Dex (the NewsletterSubscriberListFlow), not a Google Sheet; delete them from the file",
		strings.Join(present, ", "))
}

// Validate reports every invalid field.
func (configuration ProcessConfiguration) Validate() error {
	var problems []error
	add := func(format string, arguments ...any) { problems = append(problems, fmt.Errorf(format, arguments...)) }

	if strings.TrimSpace(configuration.ApplicationName) == "" {
		add("applicationName is required")
	}
	if configuration.DexWebURL != "" && !isAbsoluteHTTPURL(configuration.DexWebURL) {
		add("dexWebUrl must be an absolute http(s) URL")
	}

	languageModel := configuration.LanguageModel
	stages := []struct {
		name  string
		stage StageModelConfiguration
	}{
		{"interpretRequest", languageModel.InterpretRequest},
		{"summarizeRepository", languageModel.SummarizeRepository},
		{"synthesizeResearch", languageModel.SynthesizeResearch},
		{"draftBlogPost", languageModel.DraftBlogPost},
		{"draftNewsletter", languageModel.DraftNewsletter},
	}
	for _, entry := range stages {
		provider := languageModel.ResolveStageProvider(entry.stage)
		if !isSupportedProvider(provider) {
			add("languageModel.%s provider %q is not one of %v", entry.name, provider, SupportedLanguageModelProviders)
		}
		if entry.stage.Model != strings.TrimSpace(entry.stage.Model) {
			add("languageModel.%s.model must not have leading or trailing spaces", entry.name)
		}
		if entry.stage.MaxOutputTokens < 256 || entry.stage.MaxOutputTokens > 65536 {
			add("languageModel.%s.maxOutputTokens must be between 256 and 65536", entry.name)
		}
		if entry.stage.Temperature != nil && (*entry.stage.Temperature < 0 || *entry.stage.Temperature > 2) {
			add("languageModel.%s.temperature must be between 0 and 2", entry.name)
		}
		if entry.stage.ThinkingBudget != nil && *entry.stage.ThinkingBudget < -1 {
			add("languageModel.%s.thinkingBudget must be -1 (dynamic) or non-negative", entry.name)
		}
	}

	research := configuration.Research
	positive := []struct {
		name  string
		value int
		limit int
	}{
		{"research.defaultLookbackDays", research.DefaultLookbackDays, 366},
		{"research.maxLookbackDays", research.MaxLookbackDays, 366},
		{"research.maxRepositoriesPerRequest", research.MaxRepositoriesPerRequest, 10},
		{"research.maxMergedPullRequestsPerRepository", research.MaxMergedPullRequestsPerRepository, 200},
		{"research.maxPullRequestsWithFileDetails", research.MaxPullRequestsWithFileDetails, 50},
		{"research.maxFilesPerPullRequest", research.MaxFilesPerPullRequest, 300},
		{"research.maxCommitsPerRepository", research.MaxCommitsPerRepository, 500},
		{"research.maxPatchCharactersPerFile", research.MaxPatchCharactersPerFile, 20000},
		{"research.maxEvidenceCharactersPerRepository", research.MaxEvidenceCharactersPerRepository, 400000},
	}
	for _, entry := range positive {
		if entry.value < 1 || entry.value > entry.limit {
			add("%s must be between 1 and %d", entry.name, entry.limit)
		}
	}
	if research.DefaultLookbackDays > research.MaxLookbackDays {
		add("research.defaultLookbackDays must not exceed research.maxLookbackDays")
	}
	if research.MaxPullRequestsWithFileDetails > research.MaxMergedPullRequestsPerRepository {
		add("research.maxPullRequestsWithFileDetails must not exceed research.maxMergedPullRequestsPerRepository")
	}
	if len(research.Repositories) == 0 {
		add("research.repositories must list at least one repository")
	}
	seenRepositories := map[string]bool{}
	for index, repository := range research.Repositories {
		if !githubOwnerPattern.MatchString(repository.Owner) {
			add("research.repositories[%d].owner %q is not a GitHub owner", index, repository.Owner)
		}
		if !githubRepositoryPattern.MatchString(repository.Name) || repository.Name == "." || repository.Name == ".." {
			add("research.repositories[%d].name %q is not a GitHub repository name", index, repository.Name)
		}
		key := strings.ToLower(repository.Owner + "/" + repository.Name)
		if seenRepositories[key] {
			add("research.repositories[%d] duplicates %s", index, key)
		}
		seenRepositories[key] = true
		if strings.TrimSpace(repository.Description) == "" {
			add("research.repositories[%d].description is required so repositories can be selected by topic", index)
		}
	}

	blog := configuration.Blog
	if strings.TrimSpace(blog.SiteName) == "" {
		add("blog.siteName is required")
	}
	if strings.TrimSpace(blog.ArtifactDirectory) == "" {
		add("blog.artifactDirectory is required")
	}
	if blog.PublicBaseURL != "" && !isAbsoluteHTTPURL(blog.PublicBaseURL) {
		add("blog.publicBaseUrl must be an absolute http(s) URL")
	}

	newsletter := configuration.Newsletter
	if newsletter.MaxRecipients < 1 || newsletter.MaxRecipients > 2000 {
		add("newsletter.maxRecipients must be between 1 and 2000 (Gmail daily sending limits)")
	}

	review := configuration.Review
	if time.Duration(review.ReminderInterval) < time.Minute {
		add("review.reminderInterval must be at least 1m")
	}
	if review.MaxReminders < 0 || review.MaxReminders > 10 {
		add("review.maxReminders must be between 0 and 10")
	}
	if review.MaxRevisions < 0 || review.MaxRevisions > 10 {
		add("review.maxRevisions must be between 0 and 10")
	}
	return errors.Join(problems...)
}

// FindRepository returns the catalog entry for owner/name, case-insensitively.
func (research ResearchConfiguration) FindRepository(owner string, name string) (RepositoryCatalogEntry, bool) {
	for _, repository := range research.Repositories {
		if strings.EqualFold(repository.Owner, owner) && strings.EqualFold(repository.Name, name) {
			return repository, true
		}
	}
	return RepositoryCatalogEntry{}, false
}

func isSupportedProvider(provider string) bool {
	for _, supported := range SupportedLanguageModelProviders {
		if provider == supported {
			return true
		}
	}
	return false
}

func isAbsoluteHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != ""
}
