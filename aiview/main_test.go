package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestUniqueCommandPrefixes(t *testing.T) {
	for _, tt := range []struct {
		args         []string
		kind, action string
	}{
		{[]string{"mo", "li"}, "model", "list"},
		{[]string{"list", "providers"}, "provider", "list"},
		{[]string{"sh", "mo", "opus"}, "model", "show"},
		{[]string{"mo", "se", "opus"}, "model", "select"},
		{[]string{"th", "li"}, "thinking", "list"},
	} {
		r, err := parseArgs(tt.args)
		if err != nil || r.kind != tt.kind || r.action != tt.action {
			t.Fatalf("%v => %s %s, %v", tt.args, r.kind, r.action, err)
		}
	}
	for _, args := range [][]string{{"s"}, {"t"}, {"mo", "s"}, {"mo", "li", "extra"}, {"--timeout", "0s"}, {"mo", "--unknown"}} {
		if _, err := parseArgs(args); err == nil {
			t.Errorf("accepted invalid/ambiguous command %v", args)
		}
	}
}

func TestSettingsRedactSecretsButRetainCapabilities(t *testing.T) {
	input := map[string]any{
		"maxTokens": 128000, "model": "example-model", "apiKey": "private-value",
		"env":      map[string]any{"CUSTOM_AUTH": "sensitive-env"},
		"provider": map[string]any{"local": map[string]any{"baseURL": "https://user:pass@example.test/api?token=private", "headers": map[string]any{"X-Custom": "sensitive-header"}, "thinking": []any{"low", "high"}}},
	}
	got := object(redact(input, ""))
	want := map[string]any{
		"maxTokens": 128000, "model": "example-model", "apiKey": "[REDACTED]", "env": "[REDACTED]",
		"provider": map[string]any{"local": map[string]any{"baseURL": "https://example.test/api", "headers": "[REDACTED]", "thinking": []any{"low", "high"}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("redacted settings: %s", detail(got))
	}
	if input["apiKey"] != "private-value" {
		t.Fatal("redaction mutated collector settings")
	}
}

func isolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, key := range []string{"PI_CODING_AGENT_DIR", "OMP_PROFILE", "CODEX_HOME", "CLAUDE_CONFIG_DIR", "XDG_CONFIG_HOME", "OPENCODE_CONFIG"} {
		t.Setenv(key, "")
	}
	return home
}
func put(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCLIReadsModelCapabilitiesAndSavedSelection(t *testing.T) {
	home := isolatedHome(t)
	put(t, filepath.Join(home, ".codex", "models_cache.json"), `{"fetched_at":"2026-10-01T00:00:00Z","models":[{"slug":"test-model","display_name":"Test Model","supported_reasoning_levels":[{"effort":"high","description":"Deep reasoning"}],"context_window":250000}]}`)
	base := []string{"--home", home, "--project", home, "--no-probe", "--json", "--harness", "codex"}
	var out, errOut bytes.Buffer
	if err := run(append(append([]string{}, base...), "mo", "se", "test-model"), &out, &errOut); err != nil {
		t.Fatal(err)
	}
	var selected Model
	if err := json.Unmarshal(out.Bytes(), &selected); err != nil {
		t.Fatal(err)
	}
	if selected.ID != "codex:openai/test-model" || selected.Name != "Test Model" || !reflect.DeepEqual(selected.Thinking, []Thinking{{Slug: "high", Name: "High", Description: "Deep reasoning", Args: []string{"-c", `model_reasoning_effort="high"`}}}) {
		t.Fatalf("wrong selected model: %+v", selected)
	}
	info, err := os.Stat(selectionPath(Options{Home: home}))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("selection permissions %o", info.Mode().Perm())
	}
	out.Reset()
	if err := run(append(append([]string{}, base...), "mo", "sh"), &out, &errOut); err != nil {
		t.Fatal(err)
	}
	var shown Model
	if err := json.Unmarshal(out.Bytes(), &shown); err != nil {
		t.Fatal(err)
	}
	if shown.ID != "codex:openai/test-model" {
		t.Fatalf("saved selection not used: %s", shown.ID)
	}
	out.Reset()
	if err := run(append(append([]string{}, base...), "th", "li", "--model", "test-model"), &out, &errOut); err != nil {
		t.Fatal(err)
	}
	var levels []struct {
		Slug       string   `json:"slug"`
		LaunchArgs []string `json:"launchArgs"`
	}
	if err := json.Unmarshal(out.Bytes(), &levels); err != nil {
		t.Fatal(err)
	}
	if len(levels) != 1 || levels[0].Slug != "high" || !reflect.DeepEqual(levels[0].LaunchArgs, []string{"codex", "--model", "test-model", "-c", `model_provider="openai"`, "-c", `model_reasoning_effort="high"`}) {
		t.Fatalf("wrong thinking launch: %s", out.String())
	}
}

func TestAmbiguousModelSelectionRequiresHarness(t *testing.T) {
	rows := []Entry{
		{ID: "omp:p/shared", Name: "Shared", Value: Model{Slug: "shared", CLISlug: "p/shared"}},
		{ID: "pi:p/shared", Name: "Shared", Value: Model{Slug: "shared", CLISlug: "p/shared"}},
	}
	if _, err := selectEntry(rows, "shared"); err == nil {
		t.Fatal("ambiguous model was silently selected")
	}
	row, err := selectEntry(rows, "omp:p/shared")
	if err != nil || row.ID != "omp:p/shared" {
		t.Fatalf("exact selection = %+v, %v", row, err)
	}
}

func TestSkillDiscoveryFollowsLinksWithoutCycles(t *testing.T) {
	home := isolatedHome(t)
	source := filepath.Join(home, "library", "example")
	put(t, filepath.Join(source, "SKILL.md"), "---\nname: example\ndescription: |\n  Review code safely.\n---\nOnly inspect the current project.\n")
	root := filepath.Join(home, ".omp", "agent", "skills")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, filepath.Join(root, "example")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(source, "loop")); err != nil {
		t.Fatal(err)
	}
	c := collector{options: Options{Home: home, Project: home}, harness: Harness{ID: "omp"}}
	c.scanResources(resourceRoot{root, "skill", "user", "discovered"})
	if len(c.inventory.Skills) != 1 {
		t.Fatalf("wanted exactly one skill despite cycle, got %d", len(c.inventory.Skills))
	}
	skill := c.inventory.Skills[0]
	if skill.Name != "example" || skill.Description != "Review code safely.\n" || skill.ResolvedPath != filepath.Join(source, "SKILL.md") || !strings.Contains(skill.Content, "Only inspect the current project.") {
		t.Fatalf("wrong discovered skill: %+v", skill)
	}
}

func TestJSONCAndTOMLSettings(t *testing.T) {
	home := isolatedHome(t)
	put(t, filepath.Join(home, ".claude", "settings.json"), "{ // user's selected model\n\"model\": \"opus\", \"env\": {\"API_KEY\": \"secret-fixture\"}, }")
	put(t, filepath.Join(home, ".codex", "config.toml"), "model = 'custom'\n[model_providers.local]\nbase_url = 'http://localhost:8080/v1'\napi_key = 'local-private'\n")
	var out, errOut bytes.Buffer
	err := run([]string{"settings", "list", "--home", home, "--project", home, "--no-probe", "--json"}, &out, &errOut)
	if err != nil {
		t.Fatal(err)
	}
	var settings []Setting
	if err := json.Unmarshal(out.Bytes(), &settings); err != nil {
		t.Fatal(err)
	}
	if len(settings) != 2 {
		t.Fatalf("wanted two settings files: %s", out.String())
	}
	if strings.Contains(out.String(), "secret-fixture") || strings.Contains(out.String(), "local-private") {
		t.Fatal("CLI leaked a credential")
	}
	if settings[0].Values["model"] != "opus" || settings[1].Values["model"] != "custom" {
		t.Fatalf("incorrect settings: %s", out.String())
	}
}

func TestInvalidConfigurationRemainsInspectable(t *testing.T) {
	home := isolatedHome(t)
	path := filepath.Join(home, ".claude", "settings.json")
	put(t, path, `{"model":"opus" "apiKey":"do-not-expose"}`)
	var out, warnings bytes.Buffer
	if err := run([]string{"settings", "list", "--home", home, "--project", home, "--no-probe", "--json"}, &out, &warnings); err != nil {
		t.Fatal(err)
	}
	var settings []Setting
	if err := json.Unmarshal(out.Bytes(), &settings); err != nil {
		t.Fatal(err)
	}
	if len(settings) != 1 || settings[0].Path != path || settings[0].Error == "" {
		t.Fatalf("invalid source disappeared: %s", out.String())
	}
	if strings.Contains(out.String()+warnings.String(), "do-not-expose") {
		t.Fatal("parser error leaked the credential")
	}
}

func TestNonChatModelsDoNotSuggestChatLaunch(t *testing.T) {
	c := collector{harness: Harness{ID: "omp"}}
	c.addModel("local", "speech", "Speech", "local/speech", "catalog", map[string]any{"kind": "tts"}, nil, "")
	c.addModel("provider", "chat", "Chat", "provider/chat", "catalog", map[string]any{"kind": "chat"}, []string{"high"}, "")
	if len(c.inventory.Models[0].Args) != 0 || !reflect.DeepEqual(c.inventory.Models[1].Args, []string{"omp", "--model", "provider/chat"}) {
		t.Fatalf("incorrect launch suggestions: %+v", c.inventory.Models)
	}
}

func TestScriptHarnessProcessIdentity(t *testing.T) {
	paths := []string{"omp", "/opt/bin/omp", "/opt/agent/dist/cli.js"}
	for _, command := range []string{"omp --model example", "/usr/bin/bun /opt/agent/dist/cli.js --resume", "/opt/bin/omp", "/opt/releases/older/bin/omp --daemon"} {
		if !processMatches(command, paths) {
			t.Errorf("missed running harness: %s", command)
		}
	}
	for _, command := range []string{"echo omp", "node other.js /opt/agent/dist/cli.js", "omp-helper", "sh -c 'omp --help'"} {
		if processMatches(command, paths) {
			t.Errorf("misidentified process: %s", command)
		}
	}
}

func TestCustomProviderPreservesNamespacedModelSlug(t *testing.T) {
	c := collector{
		harness:  Harness{ID: "codex"},
		settings: map[string]any{"model_provider": "gateway", "model": "vendor/model"},
	}
	c.configuredModels()
	model := c.inventory.Models[0]
	if model.Provider != "gateway" || model.Slug != "vendor/model" || !reflect.DeepEqual(model.Args, []string{"codex", "--model", "vendor/model", "-c", `model_provider="gateway"`}) {
		t.Fatalf("namespaced model changed provider: %+v", model)
	}
}
