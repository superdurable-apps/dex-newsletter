package templatecontract_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
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
	DexSkill                            string            `json:"dexSkill"`
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
	if contract.SchemaVersion != 1 || contract.BuildProfile != "go-react-v1" || contract.TemplateVersion != "1.4.1" || contract.MinimumSandboxImageContractRevision != 2 {
		t.Fatalf("unexpected template identity: %+v", contract)
	}
	if baseline := strings.TrimSpace(readFile(t, filepath.Join(root, "DEX_SERVER_BASELINE"))); baseline != "server/v0.13.2" {
		t.Fatalf("unexpected Dex Server baseline: %q", baseline)
	}
	if baseline := strings.TrimSpace(readFile(t, filepath.Join(root, "DEX_CLI_BASELINE"))); baseline != "cli-v0.13.8" {
		t.Fatalf("unexpected Dex CLI baseline: %q", baseline)
	}
	goModule := readFile(t, filepath.Join(root, "go.mod"))
	if !regexp.MustCompile(`(?m)^\s*(require\s+)?github\.com/superdurable/dex/sdk-go v0\.12\.1(\s|$)`).MatchString(goModule) {
		t.Fatal("go.mod must pin Dex Go SDK v0.12.1")
	}
	for _, path := range []string{contract.OpenAPISpec, contract.AgentInstructions, contract.DexSkill} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Errorf("manifest path %q: %v", path, err)
		}
	}
	makefile := readFile(t, filepath.Join(root, "Makefile"))
	agents := readFile(t, filepath.Join(root, contract.AgentInstructions))
	wantCommands := map[string]string{
		"bootstrap":       "make bootstrap",
		"generate":        "make generate",
		"checkGenerated":  "make check-generated",
		"checkFdgV2":      "make check-fdg-v2",
		"testUnit":        "make test-unit",
		"testIntegration": "make test-integration",
		"testE2E":         "make test-e2e",
		"build":           "make build",
		"dev":             "make dev",
		"check":           "make check",
	}
	if !reflect.DeepEqual(contract.Commands, wantCommands) {
		t.Errorf("manifest commands = %v, want %v", contract.Commands, wantCommands)
	}
	for _, command := range contract.Commands {
		target := strings.TrimPrefix(command, "make ")
		if !strings.Contains(makefile, "\n"+target+":") && !strings.HasPrefix(makefile, target+":") {
			t.Errorf("Makefile does not define %q", target)
		}
		if !strings.Contains(agents, command) {
			t.Errorf("AGENTS.md does not mention %q", command)
		}
	}
	// make dev-dex and make dev-app split make dev so the application can
	// restart without restarting Dex. They are local helpers, not manifest
	// commands, but must stay defined and documented.
	readme := readFile(t, filepath.Join(root, "README.md"))
	for _, target := range []string{"dev-dex", "dev-app"} {
		if !strings.Contains(makefile, "\n"+target+":") {
			t.Errorf("Makefile does not define %q", target)
		}
		if !strings.Contains(agents, "make "+target) {
			t.Errorf("AGENTS.md does not mention %q", "make "+target)
		}
		if !strings.Contains(readme, "make "+target) {
			t.Errorf("README.md does not mention %q", "make "+target)
		}
	}
	if gitignore := readFile(t, filepath.Join(root, ".gitignore")); !regexp.MustCompile(`(?m)^/?\.dex-dev/$`).MatchString(gitignore) {
		t.Error(".gitignore must ignore the persistent local Dex state directory .dex-dev/")
	}
	gitmodules := readFile(t, filepath.Join(root, ".gitmodules"))
	if !strings.Contains(gitmodules, "path = .agents/skills/dex-app-builder/upstream") ||
		!strings.Contains(gitmodules, "url = https://github.com/superdurable/dex-skills.git") {
		t.Fatal("Dex skill submodule path or public HTTPS URL is not allowlisted")
	}
	if _, err := os.Stat(filepath.Join(root, ".agents/skills/dex-sdk/SKILL.md")); err != nil {
		t.Fatalf("Dex SDK wrapper: %v", err)
	}
	command := exec.Command("git", "ls-files", "--stage", ".agents/skills/dex-app-builder/upstream")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		t.Fatalf("read Dex skill submodule pin: %v", err)
	}
	fields := strings.Fields(string(output))
	if len(fields) < 2 || fields[0] != "160000" || fields[1] != "45410a3ceea6f5a726f28573cbdef0432d1d9405" {
		t.Fatalf("Dex skill is not pinned as a gitlink: %q", output)
	}
}

// TestNoCustomUIShell keeps the product in the dex-app-builder "No custom UI"
// mode: Dex Web v2 is the only process-management surface.
func TestNoCustomUIShell(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	for _, removed := range []string{
		"cmd/mock-server",
		"internal/mockserver",
		"internal/process",
		"scripts/with-mock.sh",
		"scripts/run-mock-e2e.sh",
		"docs/local-mock.md",
		"web/src/MockControls.tsx",
		"web/e2e/mock-basic-process.spec.ts",
	} {
		if _, err := os.Stat(filepath.Join(root, removed)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("custom process-management surface %q must not exist (stat error: %v)", removed, err)
		}
	}
	spec := readFile(t, filepath.Join(root, "openapi", "openapi.yaml"))
	if count := strings.Count(spec, "operationId:"); count != 1 || !strings.Contains(spec, "operationId: getApplicationInfo") {
		t.Errorf("OpenAPI must define only getApplicationInfo; found %d operations", count)
	}
	for _, path := range []string{"Makefile", "AGENTS.md", "README.md", ".superverse/template.json"} {
		if contents := readFile(t, filepath.Join(root, path)); strings.Contains(contents, "make mock") || strings.Contains(contents, "test-mock-e2e") {
			t.Errorf("%s still references the removed mock workflow", path)
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
