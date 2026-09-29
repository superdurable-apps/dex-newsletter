package templatecontract_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type manifest struct {
	SchemaVersion                       int               `json:"schemaVersion"`
	BuildProfile                        string            `json:"buildProfile"`
	TemplateVersion                     string            `json:"templateVersion"`
	MinimumSandboxImageContractRevision int               `json:"minimumSandboxImageContractRevision"`
	OpenAPISpec                         string            `json:"openapiSpec"`
	AgentInstructions                   string            `json:"agentInstructions"`
	Commands                            map[string]string `json:"commands"`
}

func TestTemplateContract(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	manifestBytes, err := os.ReadFile(filepath.Join(root, ".superverse", "template.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var contract manifest
	if err := json.Unmarshal(manifestBytes, &contract); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if contract.SchemaVersion != 1 || contract.BuildProfile != "go-react-v1" || contract.TemplateVersion != "1.7.2" || contract.MinimumSandboxImageContractRevision != 3 {
		t.Fatalf("unexpected template identity: %+v", contract)
	}
	if baseline := strings.TrimSpace(readFile(t, filepath.Join(root, "DEX_SERVER_BASELINE"))); baseline != "server/v0.14.1" {
		t.Fatalf("unexpected Dex Server baseline: %q", baseline)
	}
	if baseline := strings.TrimSpace(readFile(t, filepath.Join(root, "DEX_CLI_BASELINE"))); baseline != "cli-v0.14.1" {
		t.Fatalf("unexpected Dex CLI baseline: %q", baseline)
	}
	goModule := readFile(t, filepath.Join(root, "go.mod"))
	if !strings.Contains(goModule, "github.com/superdurable/dex/sdk-go v0.13.1") {
		t.Fatal("template must pin Dex Go SDK v0.13.1")
	}
	for _, path := range []string{contract.OpenAPISpec, contract.AgentInstructions} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Errorf("manifest path %q: %v", path, err)
		}
	}
	makefile := readFile(t, filepath.Join(root, "Makefile"))
	agents := readFile(t, filepath.Join(root, contract.AgentInstructions))
	readme := readFile(t, filepath.Join(root, "README.md"))
	for name, contents := range map[string]string{"AGENTS.md": agents, "README.md": readme} {
		if strings.Contains(contents, "/opt/superverse/dex-skills") {
			t.Errorf("%s must discover Dex Skills through the coding-agent host", name)
		}
	}
	if !strings.Contains(agents, "`dex-app-builder` skill") {
		t.Error("AGENTS.md must require the installed dex-app-builder skill")
	}
	expectedCommands := map[string]string{
		"bootstrap":       "make bootstrap",
		"generate":        "make generate",
		"checkFdgV2":      "make check-fdg-v2",
		"testUnit":        "make test-unit",
		"testIntegration": "make test-integration",
		"testE2E":         "make test-e2e",
		"build":           "make build",
		"dev":             "make dev",
		"check":           "make check",
	}
	if len(contract.Commands) != len(expectedCommands) {
		t.Fatalf("unexpected template commands: %+v", contract.Commands)
	}
	for name, command := range expectedCommands {
		if contract.Commands[name] != command {
			t.Errorf("template command %q = %q, want %q", name, contract.Commands[name], command)
		}
		target := strings.TrimPrefix(command, "make ")
		if !strings.Contains(makefile, "\n"+target+":") && !strings.HasPrefix(makefile, target+":") {
			t.Errorf("Makefile does not define %q", target)
		}
		if !strings.Contains(agents, command) {
			t.Errorf("AGENTS.md does not mention %q", command)
		}
	}
	for _, removedPath := range []string{
		".gitmodules",
		".agents",
		"cmd/mock-server",
		"internal/mockserver",
		"docs/local-mock.md",
		"scripts/run-mock-e2e.sh",
		"scripts/with-mock.sh",
		"web/src/MockControls.tsx",
		".github/workflows/update-dex-dependencies.yml",
		"scripts/update-dex-dependencies.py",
		"scripts/check-template-version.py",
	} {
		if _, err := os.Stat(filepath.Join(root, removedPath)); !os.IsNotExist(err) {
			t.Errorf("removed template path %q still exists", removedPath)
		}
	}
	gitignore := readFile(t, filepath.Join(root, ".gitignore"))
	for _, generatedPath := range []string{"/internal/api/generated/", "/web/src/api/generated/"} {
		if !strings.Contains(gitignore, generatedPath) {
			t.Errorf(".gitignore must exclude %q", generatedPath)
		}
	}
	gitCheck := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	gitCheck.Dir = root
	if err := gitCheck.Run(); err == nil {
		command := exec.Command("git", "ls-files", "internal/api/generated", "web/src/api/generated")
		command.Dir = root
		trackedGenerated, err := command.Output()
		if err != nil {
			t.Fatalf("list tracked generated files: %v", err)
		}
		if strings.TrimSpace(string(trackedGenerated)) != "" {
			t.Errorf("generated OpenAPI files must not be tracked:\n%s", trackedGenerated)
		}
	}

	ciWorkflow := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	for _, required := range []string{"workflow_dispatch:", "make check"} {
		if !strings.Contains(ciWorkflow, required) {
			t.Errorf("CI does not mention %q", required)
		}
	}
	// This is an application repository, not the template: it publishes no template releases.
	for _, removed := range []string{"gh release create", "check-template-version"} {
		if strings.Contains(ciWorkflow, removed) {
			t.Errorf("CI still runs the template release step %q", removed)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(contents)
}
