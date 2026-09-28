package templatecontract_test

import (
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
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
	if contract.SchemaVersion != 1 || contract.BuildProfile != "go-react-v1" || contract.TemplateVersion != "1.6.1" || contract.MinimumSandboxImageContractRevision != 3 {
		t.Fatalf("unexpected template identity: %+v", contract)
	}
	if baseline := strings.TrimSpace(readFile(t, filepath.Join(root, "DEX_SERVER_BASELINE"))); baseline != "server/v0.13.2" {
		t.Fatalf("unexpected Dex Server baseline: %q", baseline)
	}
	if baseline := strings.TrimSpace(readFile(t, filepath.Join(root, "DEX_CLI_BASELINE"))); baseline != "cli-v0.13.8" {
		t.Fatalf("unexpected Dex CLI baseline: %q", baseline)
	}
	goModule := readFile(t, filepath.Join(root, "go.mod"))
	if !regexp.MustCompile(`(?m)^\s*(require\s+)?github\.com/superdurable/dex/sdk-go v0\.13\.1(\s|$)`).MatchString(goModule) {
		t.Fatal("go.mod must pin Dex Go SDK v0.13.1")
	}
	for _, path := range []string{contract.OpenAPISpec, contract.AgentInstructions} {
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
		"testMockE2E":     "make test-mock-e2e",
		"build":           "make build",
		"dev":             "make dev",
		"mock":            "make mock",
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
	// The installed Dex Skills release loaded by the coding-agent host is the
	// only skill authority; the repository never vendors a project-local copy.
	if !strings.Contains(agents, "`dex-app-builder` skill") {
		t.Error("AGENTS.md must require the installed dex-app-builder skill")
	}
	for name, contents := range map[string]string{"AGENTS.md": agents, "README.md": readme} {
		normalized := strings.Join(strings.Fields(contents), " ")
		for _, stale := range []string{"/opt/superverse/dex-skills", "submodule", "pinned upstream skill", "local `dex-app-builder`"} {
			if strings.Contains(normalized, stale) {
				t.Errorf("%s still describes a project-local Dex skill (%q)", name, stale)
			}
		}
	}
	for _, removedPath := range []string{".gitmodules", ".agents"} {
		if _, err := os.Stat(filepath.Join(root, removedPath)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("removed project-local skill path %q still exists (stat error: %v)", removedPath, err)
		}
	}
}

// TestCustomUIScope keeps the confirmed Custom UI scope: the page adds only
// the newsletter subscription form, Dex Web v2 remains the only
// process-management surface, and the mock server is a contract test double
// for exactly the two application operations.
func TestCustomUIScope(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	spec := readFile(t, filepath.Join(root, "openapi", "openapi.yaml"))
	operations := regexp.MustCompile(`(?m)^\s*operationId:\s*(\S+)\s*$`).FindAllStringSubmatch(spec, -1)
	var operationIDs []string
	for _, operation := range operations {
		operationIDs = append(operationIDs, operation[1])
	}
	if want := []string{"getApplicationInfo", "subscribeToNewsletter"}; !reflect.DeepEqual(operationIDs, want) || strings.Count(spec, "operationId:") != len(want) {
		t.Errorf("OpenAPI operations = %v, want exactly %v", operationIDs, want)
	}
	for _, required := range []string{"cmd/mock-server", "internal/mockserver", "scripts/with-mock.sh", "scripts/run-mock-e2e.sh"} {
		if _, err := os.Stat(filepath.Join(root, required)); err != nil {
			t.Errorf("mock contract test double %q must exist: %v", required, err)
		}
	}
	for _, removed := range []string{"internal/process", "docs/local-mock.md", "web/src/MockControls.tsx"} {
		if _, err := os.Stat(filepath.Join(root, removed)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("custom process-management surface %q must not exist (stat error: %v)", removed, err)
		}
	}
	makefile := readFile(t, filepath.Join(root, "Makefile"))
	for _, target := range []string{"mock", "test-mock-e2e"} {
		if !strings.Contains(makefile, "\n"+target+":") {
			t.Errorf("Makefile does not define %q", target)
		}
	}

	// The mock implements the generated contract only; it never reaches the
	// Flows, the runtime, or a provider.
	module := regexp.MustCompile(`(?m)^module\s+(\S+)`).FindStringSubmatch(readFile(t, filepath.Join(root, "go.mod")))
	if module == nil {
		t.Fatal("go.mod does not declare a module path")
	}
	allowed := map[string]bool{module[1] + "/internal/api/generated": true, module[1] + "/internal/mockserver": true}
	for _, directory := range []string{"cmd/mock-server", "internal/mockserver"} {
		files, err := filepath.Glob(filepath.Join(root, directory, "*.go"))
		if err != nil || len(files) == 0 {
			t.Errorf("no Go files in %s (error: %v)", directory, err)
			continue
		}
		for _, file := range files {
			parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
			if err != nil {
				t.Errorf("parse %s: %v", file, err)
				continue
			}
			for _, imported := range parsed.Imports {
				path, _ := strconv.Unquote(imported.Path.Value)
				if (path == module[1] || strings.HasPrefix(path, module[1]+"/")) && !allowed[path] {
					t.Errorf("%s imports %s; the mock may import only internal/api/generated from this module", file, path)
				}
			}
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
