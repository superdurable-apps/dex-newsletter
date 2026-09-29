package config

import (
	"strings"
	"testing"
)

func TestParseAppliesDefaults(t *testing.T) {
	configuration, err := Parse([]byte(`{"github":{"owners":[" acme ",""]},"dexWebUrl":"http://127.0.0.1:8842/","newsletter":{"publicBaseUrl":"https://news.acme.test/"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if configuration.GitHub.Owners[0] != "acme" || len(configuration.GitHub.Owners) != 1 || configuration.Research.MaxRepositories != 5 ||
		configuration.DexWebURL != "http://127.0.0.1:8842" || configuration.Newsletter.PublicBaseURL != "https://news.acme.test" {
		t.Fatalf("configuration = %+v", configuration)
	}
	if configuration.Newsletter.IsLoopback() {
		t.Fatal("a public URL reads as loopback")
	}
}

func TestParseRejectsInvalidSettings(t *testing.T) {
	_, err := Parse([]byte(`{"github":{"owners":[]},"dexWebUrl":"ftp://x","newsletter":{"publicBaseUrl":""},"research":{"maxRepositories":50},"blog":{"postUrlTemplate":"https://b/x"}}`))
	if err == nil {
		t.Fatal("invalid configuration parsed")
	}
	for _, want := range []string{"github.owners", "dexWebUrl", "newsletter.publicBaseUrl", "research.maxRepositories", "{slug}"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if _, err := Parse([]byte(`{"github":{"owners":["a"]},"unknown":1}`)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown field error = %v", err)
	}
}
