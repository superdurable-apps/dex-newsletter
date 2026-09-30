// Package config loads the application's non-connector settings.
//
// Provider credentials, the Slack channel, and each Step's model live in Dex Web
// Connections; this file holds only what no connector owns: which GitHub owners
// to research, research bounds, and where readers and artifacts live.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// EnvironmentVariable names the JSON configuration file.
const EnvironmentVariable = "BLOG_NEWSLETTER_CONFIG"

// DefaultPath is used when EnvironmentVariable is unset.
const DefaultPath = "config/blog-newsletter.json"

type Config struct {
	GitHub     GitHub     `json:"github"`
	Research   Research   `json:"research"`
	Blog       Blog       `json:"blog"`
	Newsletter Newsletter `json:"newsletter"`
	// DexWebURL is the Dex Web base URL linked from Slack replies, such as http://127.0.0.1:8822.
	DexWebURL string `json:"dexWebUrl"`
}

type GitHub struct {
	// Owners are the GitHub logins whose public repositories may be researched.
	Owners []string `json:"owners"`
}

type Research struct {
	DefaultWindowDays            int `json:"defaultWindowDays"`
	MaxWindowDays                int `json:"maxWindowDays"`
	MaxRepositories              int `json:"maxRepositories"`
	RepositoriesPerOwner         int `json:"repositoriesPerOwner"`
	PullRequestsPerRepository    int `json:"pullRequestsPerRepository"`
	PullRequestsWithChangedFiles int `json:"pullRequestsWithChangedFiles"`
	CommitsPerRepository         int `json:"commitsPerRepository"`
}

type Blog struct {
	// ArtifactDirectory receives one self-contained HTML file per draft.
	ArtifactDirectory string `json:"artifactDirectory"`
	// PostURLTemplate optionally links the newsletter to the published post; {slug} is replaced.
	PostURLTemplate string `json:"postUrlTemplate"`
	// PublicationName appears in the blog header and the newsletter.
	PublicationName string `json:"publicationName"`
}

type Newsletter struct {
	// PublicBaseURL is where readers reach this application, used for unsubscribe links.
	PublicBaseURL string `json:"publicBaseUrl"`
	// UnsubscribeKeyFile holds the HMAC key that signs unsubscribe links.
	UnsubscribeKeyFile string `json:"unsubscribeKeyFile"`
	// EditorKeyFile holds the separate HMAC key that signs editor links; replacing it revokes
	// every editor link without breaking unsubscribe links in emails already sent.
	EditorKeyFile string `json:"editorKeyFile"`
}

// Load reads EnvironmentVariable, or DefaultPath, and applies defaults.
func Load() (Config, error) {
	path := strings.TrimSpace(os.Getenv(EnvironmentVariable))
	if path == "" {
		path = DefaultPath
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read %s (copy config/blog-newsletter.example.json): %w", path, err)
	}
	return Parse(contents)
}

// Parse decodes contents strictly, applies defaults, and validates the result.
func Parse(contents []byte) (Config, error) {
	var configuration Config
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&configuration); err != nil {
		return Config{}, fmt.Errorf("decode configuration: %w", err)
	}
	configuration.applyDefaults()
	return configuration, configuration.Validate()
}

func (configuration *Config) applyDefaults() {
	research := &configuration.Research
	defaultInt(&research.DefaultWindowDays, 7)
	defaultInt(&research.MaxWindowDays, 90)
	defaultInt(&research.MaxRepositories, 5)
	defaultInt(&research.RepositoriesPerOwner, 100)
	defaultInt(&research.PullRequestsPerRepository, 30)
	defaultInt(&research.PullRequestsWithChangedFiles, 5)
	defaultInt(&research.CommitsPerRepository, 50)
	if configuration.Blog.ArtifactDirectory == "" {
		configuration.Blog.ArtifactDirectory = "artifacts/blog"
	}
	if configuration.Blog.PublicationName == "" {
		configuration.Blog.PublicationName = "Dex Tech Blog"
	}
	if configuration.Newsletter.UnsubscribeKeyFile == "" {
		configuration.Newsletter.UnsubscribeKeyFile = ".dex-dev/unsubscribe.key"
	}
	if configuration.Newsletter.EditorKeyFile == "" {
		configuration.Newsletter.EditorKeyFile = ".dex-dev/editor.key"
	}
	owners := make([]string, 0, len(configuration.GitHub.Owners))
	for _, owner := range configuration.GitHub.Owners {
		if owner = strings.TrimSpace(owner); owner != "" {
			owners = append(owners, owner)
		}
	}
	configuration.GitHub.Owners = owners
	configuration.DexWebURL = strings.TrimRight(strings.TrimSpace(configuration.DexWebURL), "/")
	configuration.Newsletter.PublicBaseURL = strings.TrimRight(strings.TrimSpace(configuration.Newsletter.PublicBaseURL), "/")
}

func defaultInt(value *int, fallback int) {
	if *value == 0 {
		*value = fallback
	}
}

// Validate reports every invalid field together.
func (configuration Config) Validate() error {
	var problems []error
	if len(configuration.GitHub.Owners) == 0 {
		problems = append(problems, errors.New("github.owners needs at least one GitHub login"))
	}
	if len(configuration.GitHub.Owners) > 10 {
		problems = append(problems, errors.New("github.owners allows at most 10 logins"))
	}
	research := configuration.Research
	for name, bound := range map[string][3]int{
		"research.defaultWindowDays":            {research.DefaultWindowDays, 1, 365},
		"research.maxWindowDays":                {research.MaxWindowDays, 1, 365},
		"research.maxRepositories":              {research.MaxRepositories, 1, 10},
		"research.repositoriesPerOwner":         {research.RepositoriesPerOwner, 1, 500},
		"research.pullRequestsPerRepository":    {research.PullRequestsPerRepository, 1, 100},
		"research.pullRequestsWithChangedFiles": {research.PullRequestsWithChangedFiles, 0, 20},
		"research.commitsPerRepository":         {research.CommitsPerRepository, 1, 100},
	} {
		if bound[0] < bound[1] || bound[0] > bound[2] {
			problems = append(problems, fmt.Errorf("%s must be between %d and %d", name, bound[1], bound[2]))
		}
	}
	if research.DefaultWindowDays > research.MaxWindowDays {
		problems = append(problems, errors.New("research.defaultWindowDays cannot exceed research.maxWindowDays"))
	}
	for name, raw := range map[string]string{"dexWebUrl": configuration.DexWebURL, "newsletter.publicBaseUrl": configuration.Newsletter.PublicBaseURL} {
		if err := validateHTTPURL(raw); err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", name, err))
		}
	}
	if template := configuration.Blog.PostURLTemplate; template != "" {
		if !strings.Contains(template, "{slug}") {
			problems = append(problems, errors.New("blog.postUrlTemplate must contain {slug}"))
		} else if err := validateHTTPURL(strings.ReplaceAll(template, "{slug}", "slug")); err != nil {
			problems = append(problems, fmt.Errorf("blog.postUrlTemplate: %w", err))
		}
	}
	return errors.Join(problems...)
}

func validateHTTPURL(raw string) error {
	if raw == "" {
		return errors.New("is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("must be an absolute http or https URL")
	}
	return nil
}

// IsLoopback reports whether PublicBaseURL points at this machine, which readers cannot reach.
func (newsletter Newsletter) IsLoopback() bool {
	parsed, err := url.Parse(newsletter.PublicBaseURL)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	return host == "localhost" || strings.HasPrefix(host, "127.") || host == "::1"
}
