package main

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// MCP servers come from each harness's own files, never started: names,
// transport, command and args (secrets redacted), env and header names
// without values, disabled flags.
func TestMCPServersFromHarnessConfigs(t *testing.T) {
	home := isolatedHome(t)
	put(t, filepath.Join(home, ".claude.json"), `{"oauthAccount": {"emailAddress": "x"},
		"mcpServers": {"tuios": {"type": "stdio", "command": "tuios", "args": ["mcp", "--api-key", "sk-live-abcdefghijkl", "--token=hunter2"], "env": {"TUIOS_TOKEN": "secret-value"}}},
		"projects": {"`+home+`": {"mcpServers": {"local-one": {"command": "local"}}}, "/elsewhere": {"mcpServers": {"other": {"command": "x"}}}}}`)
	put(t, filepath.Join(home, ".claude", "settings.json"), "{}") // configured, so the project's .mcp.json is claude's
	put(t, filepath.Join(home, ".mcp.json"), `{"mcpServers": {"web": {"type": "http", "url": "https://user:pw@example.test/mcp?key=private", "headers": {"Authorization": "Bearer abc"}}}}`)
	put(t, filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.mtm]\ncommand = \"mtm\"\nargs = [\"mcp\", \"serve\"]\n\n[mcp_servers.off]\ncommand = \"off\"\nenabled = false\n")
	put(t, filepath.Join(home, ".config", "opencode", "opencode.json"), `{"mcp": {"remote-one": {"type": "remote", "url": "https://r.example/mcp"}, "local-two": {"type": "local", "command": ["npx", "-y", "pkg"], "environment": {"A": "1"}}}}`)
	put(t, filepath.Join(home, ".omp", "agent", "mcp.json"), `{"mcpServers": {"denied": {"command": "d"}}, "disabledServers": ["denied"]}`)

	out, _, err := runCLI(t, "mcp", "list", "--home", home, "--project", home, "--no-probe", "--ttl", "0s")
	if err != nil {
		t.Fatal(err)
	}
	rows, servers := rowsOf[MCPServer](t, out)
	byID := map[string]MCPServer{}
	for i, r := range rows {
		byID[strings.TrimPrefix(r.ID, "mcp/")] = servers[i]
	}
	want := []string{"claude:local:local-one", "claude:project:web", "claude:user:tuios", "codex:user:mtm", "codex:user:off", "omp:user:denied", "opencode:user:local-two", "opencode:user:remote-one"}
	got := make([]string, 0, len(byID))
	for id := range byID {
		got = append(got, id)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("server ids %v, want %v", got, want)
	}
	tuios := byID["claude:user:tuios"]
	if tuios.Command != "tuios" || tuios.Transport != "stdio" || !slices.Equal(tuios.Args, []string{"mcp", "--api-key", "[REDACTED]", "--token=[REDACTED]"}) || !slices.Equal(tuios.Env, []string{"TUIOS_TOKEN"}) {
		t.Fatalf("tuios %+v", tuios)
	}
	if web := byID["claude:project:web"]; web.Transport != "http" || web.URL != "https://example.test/mcp" || !slices.Equal(web.Headers, []string{"Authorization"}) {
		t.Fatalf("web %+v", web)
	}
	if s := byID["opencode:user:local-two"]; s.Command != "npx" || !slices.Equal(s.Args, []string{"-y", "pkg"}) || s.Transport != "stdio" || !slices.Equal(s.Env, []string{"A"}) {
		t.Fatalf("opencode local %+v", s)
	}
	if s := byID["opencode:user:remote-one"]; s.Transport != "http" || s.URL != "https://r.example/mcp" {
		t.Fatalf("opencode remote %+v", s)
	}
	if !byID["codex:user:off"].Disabled || byID["codex:user:mtm"].Disabled || !byID["omp:user:denied"].Disabled {
		t.Fatalf("disabled flags %+v", byID)
	}
	for _, secret := range []string{"sk-live", "hunter2", "secret-value", "pw@", "private", "Bearer", "emailAddress"} {
		if strings.Contains(out, secret) {
			t.Errorf("mcp list leaks %q:\n%s", secret, out)
		}
	}
	out, _, err = runCLI(t, "mcp", "list", "--server", "tuios", "--home", home, "--project", home, "--no-probe")
	if err != nil || strings.Count(out, `"id":"mcp/`) != 1 {
		t.Fatalf("--server tuios: %v %s", err, out)
	}
}

// ompAgentDB writes an omp agent.db under home with sql, skipping the test
// without the sqlite3 command.
func ompAgentDB(t *testing.T, home, sql string) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("no sqlite3 command")
	}
	put(t, filepath.Join(home, ".omp", "agent", "config.yml"), "{}\n")
	if b, err := exec.Command("sqlite3", filepath.Join(home, ".omp", "agent", "agent.db"), sql).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v %s", err, b)
	}
}

// omp caches each server's tools in agent.db; mcp-tool list and mcp tools
// read them without starting a server, and nothing else in the database.
func TestMCPToolsFromOmpCache(t *testing.T) {
	home := isolatedHome(t)
	ompAgentDB(t, home, `CREATE TABLE cache (key TEXT PRIMARY KEY, value TEXT NOT NULL, expires_at INTEGER NOT NULL);
CREATE TABLE auth_credentials (secret TEXT);
INSERT INTO auth_credentials VALUES ('do-not-read');
INSERT INTO cache VALUES ('mcp_tools:tuios', '{"version":1,"configHash":"h","tools":[{"name":"send_text","description":"Type text","inputSchema":{"type":"object","properties":{"text":{"type":"string"}}}},{"name":"list_windows"}]}', 1800000000);
INSERT INTO cache VALUES ('mcp_tools:mtm', '{"version":1,"configHash":"h","tools":[{"name":"whoami"}]}', 1800000000);
INSERT INTO cache VALUES ('mcpXtools:nope', '{"tools":[{"name":"never"}]}', 1800000000);
INSERT INTO cache VALUES ('other', 'x', 1800000000);`)
	flags := []string{"--home", home, "--project", home, "--no-probe", "--ttl", "0s"}
	out, _, err := runCLI(t, append([]string{"mcp-tool", "list"}, flags...)...)
	if err != nil {
		t.Fatal(err)
	}
	rows, tools := rowsOf[MCPTool](t, out)
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	if !slices.Equal(ids, []string{"mcp-tool/omp:mtm/whoami", "mcp-tool/omp:tuios/list_windows", "mcp-tool/omp:tuios/send_text"}) || rows[2].Label != "tuios/send_text [omp]" || tools[2].Description != "Type text" || tools[2].InputSchema != nil || tools[2].ExpiresAt == nil {
		t.Fatalf("tool rows %s", out)
	}
	if strings.Contains(out, "do-not-read") || strings.Contains(out, "never") {
		t.Fatal("read outside the cache's mcp_tools rows")
	}
	for _, args := range [][]string{{"mcp", "tools", "tuios"}, {"mcp-tool", "list", "tuios"}, {"mcp-tool", "list", "--server", "tuios"}, {"mcp", "tools", "--server", "tuios"}} {
		out, _, err := runCLI(t, append(args, flags...)...)
		if err != nil || strings.Count(out, `"id":"mcp-tool/omp:tuios/`) != 2 || strings.Contains(out, "mtm") {
			t.Fatalf("%v: %v %s", args, err, out)
		}
	}
	out, _, err = runCLI(t, append([]string{"mcp-tool", "show", "tuios/send_text"}, flags...)...)
	if err != nil || !strings.Contains(out, `"inputSchema"`) {
		t.Fatalf("show keeps the input schema: %v %s", err, out)
	}
}

// mcp tools --target MCP_ROW lists the tools of that row's server.
func TestMCPToolsOfATargetServer(t *testing.T) {
	home := isolatedHome(t)
	put(t, filepath.Join(home, ".claude.json"), `{"mcpServers": {"tuios": {"command": "tuios"}}}`)
	ompAgentDB(t, home, `CREATE TABLE cache (key TEXT PRIMARY KEY, value TEXT NOT NULL, expires_at INTEGER NOT NULL);
INSERT INTO cache VALUES ('mcp_tools:tuios', '{"tools":[{"name":"send_text"}]}', 0), ('mcp_tools:mtm', '{"tools":[{"name":"whoami"}]}', 0);`)
	out, _, err := runCLI(t, "mcp", "tools", "--target", "mcp/claude:user:tuios", "--home", home, "--project", home, "--no-probe", "--ttl", "0s")
	if err != nil || !strings.Contains(out, "mcp-tool/omp:tuios/send_text") || strings.Contains(out, "whoami") {
		t.Fatalf("mcp tools --target: %v %s", err, out)
	}
}

func TestRedactArgs(t *testing.T) {
	got := redactArgs([]string{"serve", "--api-key", "abc", "--port", "8080", "--secret=x", "-t", "Bearer zzzzzz", "--password", "--verbose"})
	want := []string{"serve", "--api-key", "[REDACTED]", "--port", "8080", "--secret=[REDACTED]", "-t", "[REDACTED]", "--password", "--verbose"}
	if !slices.Equal(got, want) {
		t.Fatalf("redactArgs = %v, want %v", got, want)
	}
}
