package harness

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fixture copies testdata/home into a temporary home, with .claude.json's
// project key set to the temporary project, and clears the override
// variables so nothing reaches the real home.
func fixture(t *testing.T) (home, project string) {
	t.Helper()
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_CONFIG_HOME", "PI_CODING_AGENT_DIR"} {
		t.Setenv(k, "")
	}
	home, project = t.TempDir(), t.TempDir()
	err := filepath.Walk("testdata/home", func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("testdata/home", p)
		dst := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, []byte(strings.ReplaceAll(string(b), "PROJECT", project)), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return home, project
}

func TestConfiguredReadsEveryHarness(t *testing.T) {
	home, project := fixture(t)
	var got []string
	byID := map[string]MCPEntry{}
	for _, id := range []string{"claude", "codex", "opencode"} {
		for _, e := range DiscoverMCP(id, home, Root(id, home), project, true, nil, nil) {
			got = append(got, e.Server.ID)
			byID[e.Server.ID] = e
		}
	}
	want := []string{"claude:local:proj", "claude:user:local", "claude:user:remote", "codex:user:bearer", "codex:user:off", "opencode:user:oc"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	local := byID["claude:user:local"].Server
	if !reflect.DeepEqual(local.Args, []string{"--token", "[REDACTED]"}) || !reflect.DeepEqual(local.Env, []string{"API_KEY"}) {
		t.Fatalf("local = %+v", local)
	}
	remote := byID["claude:user:remote"]
	if remote.Server.URL != "https://example.test/mcp" || remote.Server.Transport != "http" || !reflect.DeepEqual(remote.Server.Headers, []string{"Authorization"}) {
		t.Fatalf("remote = %+v", remote.Server)
	}
	if h := object(remote.Raw["headers"]); h["Authorization"] != "Bearer xyz" {
		t.Fatalf("raw headers lost: %v", remote.Raw)
	}
	if !byID["codex:user:off"].Server.Disabled || byID["codex:user:bearer"].Server.Disabled {
		t.Fatal("codex enabled = false not read")
	}
	if text(byID["codex:user:bearer"].Raw, "bearer_token_env_var") != "BEARER_TOKEN" {
		t.Fatal("raw bearer_token_env_var lost")
	}
	oc := byID["opencode:user:oc"].Server
	if oc.Command != "oc-bin" || !reflect.DeepEqual(oc.Args, []string{"serve"}) || oc.Transport != "stdio" {
		t.Fatalf("opencode = %+v", oc)
	}
}

func TestDiscoverMCPWarnsWithoutExcerpts(t *testing.T) {
	home, project := fixture(t)
	path := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers": sk-secretsecretsecret`), 0o644); err != nil {
		t.Fatal(err)
	}
	var warnings, observed []string
	DiscoverMCP("claude", home, Root("claude", home), project, true, func(p string) { observed = append(observed, p) }, func(m string) { warnings = append(warnings, m) })
	if len(warnings) == 0 || strings.Contains(strings.Join(warnings, ""), "sk-") {
		t.Fatalf("warnings = %v", warnings)
	}
	if len(observed) == 0 || observed[0] != path {
		t.Fatalf("observed = %v", observed)
	}
}

func TestSessionFilesNewestFirst(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"2026/10/27/a.jsonl", "2026/10/28/b.jsonl", "2026/10/28/c.jsonl", "2026/10/28/x.txt"} {
		dst := filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(dst), 0o755)
		os.WriteFile(dst, nil, 0o644)
	}
	got := SessionFiles(root, 31, 10)
	for i := range got {
		got[i], _ = filepath.Rel(root, got[i])
	}
	if want := []string{"2026/10/28/c.jsonl", "2026/10/28/b.jsonl", "2026/10/27/a.jsonl"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("SessionFiles = %v, want %v", got, want)
	}
}
