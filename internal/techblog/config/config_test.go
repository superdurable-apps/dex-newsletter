package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDefaultConfigurationIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("default configuration: %v", err)
	}
}

func TestExampleConfigurationMatchesDefault(t *testing.T) {
	loaded, err := Load(filepath.Join("..", "..", "..", "config", "tech-blog.example.json"))
	if err != nil {
		t.Fatalf("load example: %v", err)
	}
	if !reflect.DeepEqual(loaded, Default()) {
		t.Fatal("config/tech-blog.example.json drifted from config.Default(); regenerate it")
	}
}

func TestLoadRejectsUnknownFieldsAndTrailingData(t *testing.T) {
	directory := t.TempDir()
	for name, contents := range map[string]string{
		"unknown.json":  `{"applicationName":"x","unknownField":true}`,
		"trailing.json": `{"applicationName":"x"} {"applicationName":"y"}`,
	} {
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("%s loaded without an error", name)
		}
	}
}

func TestLoadOverlaysPartialFileOnDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "partial.json")
	if err := os.WriteFile(path, []byte(`{"blog":{"siteName":"Custom","artifactDirectory":"out"},"review":{"required":true,"reminderInterval":"24h","maxReminders":2,"maxRevisions":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Blog.SiteName != "Custom" || time.Duration(loaded.Review.ReminderInterval) != 24*time.Hour || loaded.Research.DefaultLookbackDays != 7 {
		t.Fatalf("overlay = %+v", loaded)
	}
}

// stageFields returns every language-model stage by its JSON name, so tests
// cover a stage added later without listing it twice.
func stageFields(languageModel *LanguageModelConfiguration) map[string]*StageModelConfiguration {
	return map[string]*StageModelConfiguration{
		"interpretRequest":    &languageModel.InterpretRequest,
		"summarizeRepository": &languageModel.SummarizeRepository,
		"synthesizeResearch":  &languageModel.SynthesizeResearch,
		"draftBlogPost":       &languageModel.DraftBlogPost,
		"draftNewsletter":     &languageModel.DraftNewsletter,
	}
}

func TestStageFieldsCoversEveryStage(t *testing.T) {
	var languageModel LanguageModelConfiguration
	stageType := reflect.TypeOf(StageModelConfiguration{})
	count := 0
	for index := 0; index < reflect.TypeOf(languageModel).NumField(); index++ {
		if reflect.TypeOf(languageModel).Field(index).Type == stageType {
			count++
		}
	}
	if got := len(stageFields(&languageModel)); got != count {
		t.Fatalf("stageFields lists %d stages, LanguageModelConfiguration has %d", got, count)
	}
}

func TestDefaultStagesDoNotSharePointers(t *testing.T) {
	configuration := Default()
	temperatures := map[*float64]string{}
	budgets := map[*int]string{}
	for name, stage := range stageFields(&configuration.LanguageModel) {
		if stage.Temperature != nil {
			if other, shared := temperatures[stage.Temperature]; shared {
				t.Errorf("%s and %s share one Temperature pointer", name, other)
			}
			temperatures[stage.Temperature] = name
		}
		if stage.ThinkingBudget != nil {
			if other, shared := budgets[stage.ThinkingBudget]; shared {
				t.Errorf("%s and %s share one ThinkingBudget pointer", name, other)
			}
			budgets[stage.ThinkingBudget] = name
		}
	}
	// Two Default() calls must not alias each other either.
	first, second := Default(), Default()
	secondStages := stageFields(&second.LanguageModel)
	for name, stage := range stageFields(&first.LanguageModel) {
		if stage.Temperature != nil && stage.Temperature == secondStages[name].Temperature {
			t.Errorf("Default() calls share the %s Temperature pointer", name)
		}
		if stage.ThinkingBudget != nil && stage.ThinkingBudget == secondStages[name].ThinkingBudget {
			t.Errorf("Default() calls share the %s ThinkingBudget pointer", name)
		}
	}
}

// TestDefaultModelsAreOpenToNewProjects keeps the defaults (and therefore
// config/tech-blog.example.json) on a model a new Google AI Studio key can
// call, with Gemini 3's own default temperature.
func TestDefaultModelsAreOpenToNewProjects(t *testing.T) {
	configuration := Default()
	for name, stage := range stageFields(&configuration.LanguageModel) {
		if strings.HasPrefix(stage.Model, "gemini-2.") {
			t.Errorf("%s uses %s; Gemini 2.x models are closed to new projects", name, stage.Model)
		}
		if stage.Temperature != nil {
			t.Errorf("%s sets temperature %v; leave it unset so Gemini 3 uses its default", name, *stage.Temperature)
		}
	}
}

func TestLoadOverridingOneStageLeavesOtherStagesUnchanged(t *testing.T) {
	for name := range stageFields(&LanguageModelConfiguration{}) {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "stage.json")
			contents := `{"languageModel":{"` + name + `":{"model":"gemini-2.5-flash","temperature":1.7,"maxOutputTokens":4096,"thinkingBudget":321}}}`
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			loaded, err := Load(path)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			defaults := Default()
			defaultStages := stageFields(&defaults.LanguageModel)
			for otherName, stage := range stageFields(&loaded.LanguageModel) {
				if otherName == name {
					if stage.Temperature == nil || *stage.Temperature != 1.7 {
						t.Errorf("%s temperature = %v, want 1.7", name, stage.Temperature)
					}
					if stage.ThinkingBudget == nil || *stage.ThinkingBudget != 321 {
						t.Errorf("%s thinkingBudget = %v, want 321", name, stage.ThinkingBudget)
					}
					continue
				}
				if !reflect.DeepEqual(*stage, *defaultStages[otherName]) {
					t.Errorf("overriding %s changed %s: got %s, want %s", name, otherName, describeStage(*stage), describeStage(*defaultStages[otherName]))
				}
			}
		})
	}
}

func describeStage(stage StageModelConfiguration) string {
	temperature, budget := "nil", "nil"
	if stage.Temperature != nil {
		temperature = strconv.FormatFloat(*stage.Temperature, 'g', -1, 64)
	}
	if stage.ThinkingBudget != nil {
		budget = strconv.Itoa(*stage.ThinkingBudget)
	}
	return "{model=" + stage.Model + " temperature=" + temperature + " thinkingBudget=" + budget + "}"
}

func TestValidateReportsInvalidFields(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*ProcessConfiguration)
		message string
	}{
		{"unsupported provider", func(c *ProcessConfiguration) { c.LanguageModel.Provider = "openai" }, "provider"},
		{"stage provider override", func(c *ProcessConfiguration) { c.LanguageModel.DraftBlogPost.Provider = "unknown" }, "draftBlogPost provider"},
		{"padded model", func(c *ProcessConfiguration) { c.LanguageModel.InterpretRequest.Model = " gemini-3.5-flash " }, "interpretRequest.model"},
		{"output tokens", func(c *ProcessConfiguration) { c.LanguageModel.DraftNewsletter.MaxOutputTokens = 10 }, "maxOutputTokens"},
		{"empty catalog", func(c *ProcessConfiguration) { c.Research.Repositories = nil }, "at least one repository"},
		{"bad owner", func(c *ProcessConfiguration) { c.Research.Repositories[0].Owner = "bad owner/" }, "GitHub owner"},
		{"dot repository", func(c *ProcessConfiguration) { c.Research.Repositories[0].Name = ".." }, "GitHub repository name"},
		{"duplicate repository", func(c *ProcessConfiguration) {
			c.Research.Repositories = append(c.Research.Repositories, c.Research.Repositories[0])
		}, "duplicates"},
		{"default above max", func(c *ProcessConfiguration) { c.Research.DefaultLookbackDays = 120 }, "defaultLookbackDays"},
		{"recipient cap", func(c *ProcessConfiguration) { c.Newsletter.MaxRecipients = 5000 }, "maxRecipients"},
		{"reminder interval", func(c *ProcessConfiguration) { c.Review.ReminderInterval = Duration(time.Second) }, "reminderInterval"},
		{"relative base URL", func(c *ProcessConfiguration) { c.Blog.PublicBaseURL = "/posts" }, "publicBaseUrl"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			configuration := Default()
			configuration.Research.Repositories = append([]RepositoryCatalogEntry(nil), configuration.Research.Repositories...)
			testCase.mutate(&configuration)
			err := configuration.Validate()
			if err == nil || !strings.Contains(err.Error(), testCase.message) {
				t.Fatalf("Validate() = %v, want an error mentioning %q", err, testCase.message)
			}
		})
	}
}

func TestResolveStageProviderPrefersStageOverride(t *testing.T) {
	languageModel := LanguageModelConfiguration{Provider: ProviderGemini}
	if got := languageModel.ResolveStageProvider(StageModelConfiguration{}); got != ProviderGemini {
		t.Fatalf("provider = %q", got)
	}
	if got := languageModel.ResolveStageProvider(StageModelConfiguration{Provider: " other "}); got != "other" {
		t.Fatalf("override = %q", got)
	}
}

func TestFindRepositoryIsCaseInsensitive(t *testing.T) {
	entry, found := Default().Research.FindRepository("SuperDurable", "DEX")
	if !found || entry.Name != "dex" {
		t.Fatalf("FindRepository = %+v, %v", entry, found)
	}
}

// TestDefaultStagesLeaveTheModelToTheConnection keeps every default stage on
// the model chosen on the provider's Dex Web Connection.
func TestDefaultStagesLeaveTheModelToTheConnection(t *testing.T) {
	configuration := Default()
	for name, stage := range stageFields(&configuration.LanguageModel) {
		if stage.Model != "" {
			t.Errorf("default %s model = %q, want blank so the Connection's model applies", name, stage.Model)
		}
	}
	if err := configuration.Validate(); err != nil {
		t.Fatalf("Default() is invalid: %v", err)
	}
}

func TestLoadExplainsRemovedSubscriberSheetFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.json")
	contents := `{"newsletter":{"subscriberSheet":{"spreadsheetId":"sheet","tab":"Subscribers","range":"A:B"},"emailColumnHeader":"email","maxRecipients":10}}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load accepted removed subscriber sheet fields")
	}
	for _, want := range []string{"newsletter.subscriberSheet", "newsletter.emailColumnHeader", "NewsletterSubscriberListFlow"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Load error %q does not mention %q", err, want)
		}
	}
}
