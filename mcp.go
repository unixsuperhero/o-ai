package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MCP servers are read from the harnesses' own configuration files, never
// started. A server's tools are listed only where a harness has cached
// them: omp keeps each server's tools/list answer in agent.db (table
// cache, key mcp_tools:SERVER), read here with the sqlite3 command in
// read-only mode. Listing the tools of any other server would mean
// starting it (initialize, tools/list), which o-ai does not do.

// MCPServer is one configured server of one harness. Env and Headers are
// the variable and header names only; args pass the secret-value redaction
// and a value following or attached to a secret-looking flag is redacted.
type MCPServer struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Harness   string   `json:"harness"`
	Scope     string   `json:"scope"`
	Config    string   `json:"config"`
	Transport string   `json:"transport"`
	Command   string   `json:"command,omitempty"`
	Args      []string `json:"args,omitempty"`
	URL       string   `json:"url,omitempty"`
	Env       []string `json:"env,omitempty"`
	Headers   []string `json:"headers,omitempty"`
	Disabled  bool     `json:"disabled,omitempty"`
}

// MCPTool is one tool a harness cached for a server.
type MCPTool struct {
	ID          string         `json:"id"`
	Server      string         `json:"server"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Harness     string         `json:"harness"`
	Source      string         `json:"source"`
	ExpiresAt   *time.Time     `json:"expiresAt,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

// mcpFile is a configuration file that may hold servers: under key, a map
// of name to server (project, for Claude's ~/.claude.json, the project's
// entry under projects).
type mcpFile struct{ path, key, scope string }

func (c *collector) discoverMCP() {
	h, o := c.harness.ID, c.options
	var files []mcpFile
	switch h {
	case "claude", "clawed":
		state := filepath.Join(o.Home, "."+h+".json")
		if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" && h == "claude" {
			state = filepath.Join(dir, ".claude.json")
		}
		files = append(files, mcpFile{state, "mcpServers", "user"}, mcpFile{state, "projects", "local"}, mcpFile{filepath.Join(o.Project, ".mcp.json"), "mcpServers", "project"})
	case "codex":
		files = append(files, mcpFile{filepath.Join(c.root, "config.toml"), "mcp_servers", "user"}, mcpFile{filepath.Join(o.Project, ".codex", "config.toml"), "mcp_servers", "project (trust-dependent)"})
	case "omp":
		files = append(files, mcpFile{filepath.Join(c.root, "mcp.json"), "mcpServers", "user"}, mcpFile{filepath.Join(o.Project, ".omp", "mcp.json"), "mcpServers", "project"})
	case "gemini":
		files = append(files, mcpFile{filepath.Join(c.root, "settings.json"), "mcpServers", "user"}, mcpFile{filepath.Join(o.Project, ".gemini", "settings.json"), "mcpServers", "project"})
	case "opencode":
		for _, name := range []string{"opencode.json", "opencode.jsonc"} {
			files = append(files, mcpFile{filepath.Join(c.root, name), "mcp", "user"}, mcpFile{filepath.Join(o.Project, name), "mcp", "project"})
		}
	}
	present := c.harness.Installed || c.harness.Configured
	seen := map[string]bool{}
	for _, f := range files {
		// A project's file names a harness's servers only when the harness
		// is here; a file read once (the project is the home) is one scope.
		if (!present && f.scope != "user" && f.scope != "local") || seen[f.path+"#"+f.key] {
			continue
		}
		seen[f.path+"#"+f.key] = true
		cfg, err := readConfig(f.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			c.warn("Cannot read MCP servers in " + f.path + ": " + safeParseError(err))
			continue
		}
		servers := object(cfg[f.key])
		if f.key == "projects" {
			servers = object(object(servers[o.Project])["mcpServers"])
		}
		disabled := stringList(cfg["disabledServers"]) // omp's denylist
		for name, v := range servers {
			s := mcpServer(object(v))
			s.ID, s.Name, s.Harness, s.Scope, s.Config = h+":"+f.scope+":"+name, name, h, f.scope, f.path
			s.Disabled = s.Disabled || contains(disabled, name)
			c.inventory.MCPServers = append(c.inventory.MCPServers, s)
		}
	}
	sort.Slice(c.inventory.MCPServers, func(i, j int) bool { return c.inventory.MCPServers[i].ID < c.inventory.MCPServers[j].ID })
	if h == "omp" {
		c.ompMCPTools()
	}
}

// mcpServer is a server entry in any harness's spelling: Claude, Gemini,
// omp and Codex (command, args, env, url or httpUrl, headers or
// http_headers, type), OpenCode (type local or remote, command as an
// array, environment).
func mcpServer(m map[string]any) MCPServer {
	var s MCPServer
	args := stringList(m["args"])
	if cmd, ok := m["command"].(string); ok {
		s.Command = cmd
	} else if parts := stringList(m["command"]); len(parts) > 0 {
		s.Command, args = parts[0], append(parts[1:], args...)
	}
	s.Command, _ = redact(s.Command, "").(string)
	s.Args = redactArgs(args)
	for _, k := range []string{"url", "httpUrl", "serverUrl"} {
		if u := text(m, k); u != "" {
			s.URL, _ = redact(u, "").(string)
			break
		}
	}
	for _, k := range []string{"env", "environment"} {
		s.Env = append(s.Env, keys(object(m[k]))...)
	}
	for _, k := range []string{"headers", "http_headers"} {
		s.Headers = append(s.Headers, keys(object(m[k]))...)
	}
	switch t := text(m, "type"); {
	case t == "local":
		s.Transport = "stdio"
	case t == "remote", t == "streamable-http", t == "streamableHttp":
		s.Transport = "http"
	case t != "":
		s.Transport = t
	case s.URL != "" && s.Command == "":
		s.Transport = "http"
	default:
		s.Transport = "stdio"
	}
	s.Disabled = m["enabled"] == false || m["disabled"] == true
	return s
}

// redactArgs passes each argument through the value redaction and hides a
// value given to a secret-looking flag (--api-key VALUE, --token=VALUE).
func redactArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, len(args))
	secretFlag := func(flag string) bool {
		return strings.HasPrefix(flag, "-") && sensitiveKey.MatchString(strings.TrimLeft(flag, "-"))
	}
	for i, a := range args {
		out[i], _ = redact(a, "").(string)
		if flag, _, ok := strings.Cut(a, "="); ok && secretFlag(flag) {
			out[i] = flag + "=[REDACTED]"
		} else if i > 0 && secretFlag(args[i-1]) && !strings.Contains(args[i-1], "=") && !strings.HasPrefix(a, "-") {
			out[i] = "[REDACTED]"
		}
	}
	return out
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

// sqlite3 is the command that reads omp's agent.db; a test replaces it.
var sqlite3 = "sqlite3"

// ompMCPTools reads the tools omp cached per server from agent.db, read
// only, the cache table's mcp_tools rows alone (the database also holds
// logins, which are never selected). An expired row is still listed with
// its expiry: omp would ask the server again.
func (c *collector) ompMCPTools() {
	db := filepath.Join(c.root, "agent.db")
	if _, err := os.Stat(db); err != nil {
		return
	}
	exe, err := exec.LookPath(sqlite3)
	if err != nil {
		c.warn("MCP tools in omp's agent.db not read: no sqlite3 command")
		return
	}
	o := c.options
	o.Timeout = min(o.Timeout, 5*time.Second)
	b, err := runCommand(o, exe, []string{"-readonly", "-json", db, "SELECT key, value, expires_at FROM cache WHERE key LIKE 'mcp\\_tools:%' ESCAPE '\\'"}, "")
	if err != nil {
		c.warn("Cannot read MCP tools in omp's agent.db")
		return
	}
	var rows []struct {
		Key       string `json:"key"`
		Value     string `json:"value"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if len(strings.TrimSpace(string(b))) > 0 && json.Unmarshal(b, &rows) != nil {
		c.warn("omp's agent.db returned unreadable MCP tools")
		return
	}
	for _, row := range rows {
		server := strings.TrimPrefix(row.Key, "mcp_tools:")
		var payload struct {
			Tools []map[string]any `json:"tools"`
		}
		if json.Unmarshal([]byte(row.Value), &payload) != nil {
			c.warn("Unreadable cached MCP tools for " + server)
			continue
		}
		var expires *time.Time
		if row.ExpiresAt > 0 {
			t := time.Unix(row.ExpiresAt, 0).UTC()
			expires = &t
		}
		for _, raw := range payload.Tools {
			name := text(raw, "name")
			if name == "" {
				continue
			}
			c.inventory.MCPTools = append(c.inventory.MCPTools, MCPTool{
				ID: c.harness.ID + ":" + server + "/" + name, Server: server, Name: name, Description: redact(text(raw, "description"), "").(string),
				Harness: c.harness.ID, Source: "omp tool cache " + db, ExpiresAt: expires, InputSchema: object(raw["inputSchema"]),
			})
		}
	}
	sort.Slice(c.inventory.MCPTools, func(i, j int) bool { return c.inventory.MCPTools[i].ID < c.inventory.MCPTools[j].ID })
}
