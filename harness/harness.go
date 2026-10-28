// Package harness reads the AI harnesses' own files: their configuration
// (JSON, JSONC, TOML, YAML), the MCP servers each one configures, and the
// Codex session files. It is o-ai's reader, importable by other family
// tools (o-mcp); like o-ai it only reads, and it never starts a server.
//
// A Server is the redacted, public view of a configured server (names of
// environment variables and headers, never their values). An Entry adds
// the raw configuration object, for a caller that must connect to the
// server; that caller must never store it.
package harness

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/pelletier/go-toml/v2"
	"github.com/tailscale/hujson"
	"gopkg.in/yaml.v3"
)

// MaxInput bounds every file read.
const MaxInput = 32 << 20

// IDs are the harnesses o-ai knows, in its collection order.
var IDs = []string{"omp", "claude", "codex", "pi", "opencode", "gemini", "clawed"}

// Root is a harness's configuration directory under home, following the
// harness's own override variable (CLAUDE_CONFIG_DIR, CODEX_HOME,
// XDG_CONFIG_HOME for opencode, PI_CODING_AGENT_DIR for omp and pi).
func Root(id, home string) string {
	root := filepath.Join(home, "."+id)
	switch id {
	case "omp", "pi":
		root = envDefault("PI_CODING_AGENT_DIR", filepath.Join(root, "agent"))
	case "opencode":
		root = filepath.Join(envDefault("XDG_CONFIG_HOME", filepath.Join(home, ".config")), "opencode")
	case "claude":
		root = envDefault("CLAUDE_CONFIG_DIR", root)
	case "codex":
		root = envDefault("CODEX_HOME", root)
	}
	return root
}

func envDefault(key, fallback string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}
	return fallback
}

// SafeParseError describes a read failure without parser excerpts:
// malformed configuration may contain credentials.
func SafeParseError(err error) string {
	if os.IsPermission(err) {
		return "permission denied"
	}
	return "invalid or unreadable configuration"
}

// ReadConfig is a configuration file as an object: TOML, YAML, else JSON
// with comments and trailing commas (JSONC).
func ReadConfig(path string) (map[string]any, error) {
	b, err := ReadBounded(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	switch strings.ToLower(filepath.Ext(path)) {
	case ".toml":
		err = toml.Unmarshal(b, &m)
	case ".yaml", ".yml":
		err = yaml.Unmarshal(b, &m)
	default:
		b, err = hujson.Standardize(b)
		if err == nil {
			err = json.Unmarshal(b, &m)
		}
	}
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("expected object")
	}
	return m, nil
}

// ReadBounded reads a file of at most MaxInput bytes.
func ReadBounded(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxInput+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxInput {
		return nil, fmt.Errorf("file exceeds %d bytes", MaxInput)
	}
	return b, nil
}

// SensitiveKey matches a key whose value is never shown.
var SensitiveKey = regexp.MustCompile(`(?i)(api.?key|access.?key|private.?key|secret|password|passwd|credential|authorization|cookie|headers|environment|token$|(^|[_-])env($|[_-])|^(key|auth|hooks|command|commands|args|arguments|notify|apikeyhelper)$)`)

// SecretValue matches a value that looks like a credential.
var SecretValue = regexp.MustCompile(`(?i)(sk-[a-z0-9_-]{8,}|gh[pousr]_[a-z0-9_]{8,}|Bearer\s+[^\s"']+|eyJ[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+)`)

// Redact is v with sensitive keys' values, secret-looking strings, URL
// credentials, queries and fragments, and control characters removed.
func Redact(v any, key string) any {
	if SensitiveKey.MatchString(key) {
		return "[REDACTED]"
	}
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, value := range x {
			out[k] = Redact(value, k)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, value := range x {
			out[i] = Redact(value, "")
		}
		return out
	case string:
		s := SecretValue.ReplaceAllString(x, "[REDACTED]")
		if u, err := url.Parse(s); err == nil && u.Scheme != "" && u.Host != "" {
			u.User = nil
			u.RawQuery = ""
			u.Fragment = ""
			s = u.String()
		}
		return strings.Map(func(r rune) rune {
			if unicode.IsControl(r) && r != '\n' && r != '\t' {
				return -1
			}
			return r
		}, s)
	default:
		return v
	}
}

// RedactArgs passes each argument through the value redaction and hides a
// value given to a secret-looking flag (--api-key VALUE, --token=VALUE).
func RedactArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, len(args))
	secretFlag := func(flag string) bool {
		return strings.HasPrefix(flag, "-") && SensitiveKey.MatchString(strings.TrimLeft(flag, "-"))
	}
	for i, a := range args {
		out[i], _ = Redact(a, "").(string)
		if flag, _, ok := strings.Cut(a, "="); ok && secretFlag(flag) {
			out[i] = flag + "=[REDACTED]"
		} else if i > 0 && secretFlag(args[i-1]) && !strings.Contains(args[i-1], "=") && !strings.HasPrefix(a, "-") {
			out[i] = "[REDACTED]"
		}
	}
	return out
}

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

// MCPEntry is a configured server and its raw configuration object, read
// from Server.Config. Raw holds credentials: connect with it, never store
// or print it.
type MCPEntry struct {
	Server MCPServer
	Raw    map[string]any
}

// MCPFile is a configuration file that may hold servers: under Key, a map
// of name to server (projects, for Claude's ~/.claude.json, the project's
// entry under projects).
type MCPFile struct{ Path, Key, Scope string }

// MCPFiles are the files a harness reads its MCP servers from, user scope
// first.
func MCPFiles(id, home, root, project string) []MCPFile {
	var files []MCPFile
	switch id {
	case "claude", "clawed":
		state := filepath.Join(home, "."+id+".json")
		if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" && id == "claude" {
			state = filepath.Join(dir, ".claude.json")
		}
		files = append(files, MCPFile{state, "mcpServers", "user"}, MCPFile{state, "projects", "local"}, MCPFile{filepath.Join(project, ".mcp.json"), "mcpServers", "project"})
	case "codex":
		files = append(files, MCPFile{filepath.Join(root, "config.toml"), "mcp_servers", "user"}, MCPFile{filepath.Join(project, ".codex", "config.toml"), "mcp_servers", "project (trust-dependent)"})
	case "omp":
		files = append(files, MCPFile{filepath.Join(root, "mcp.json"), "mcpServers", "user"}, MCPFile{filepath.Join(project, ".omp", "mcp.json"), "mcpServers", "project"})
	case "gemini":
		files = append(files, MCPFile{filepath.Join(root, "settings.json"), "mcpServers", "user"}, MCPFile{filepath.Join(project, ".gemini", "settings.json"), "mcpServers", "project"})
	case "opencode":
		for _, name := range []string{"opencode.json", "opencode.jsonc"} {
			files = append(files, MCPFile{filepath.Join(root, name), "mcp", "user"}, MCPFile{filepath.Join(project, name), "mcp", "project"})
		}
	}
	return files
}

// DiscoverMCP is every server a harness configures, sorted by ID. present
// says the harness is installed or configured: a project's file names its
// servers only then. observe is told each path read or looked for, warn
// each unreadable file (either may be nil).
func DiscoverMCP(id, home, root, project string, present bool, observe, warn func(string)) []MCPEntry {
	var out []MCPEntry
	seen := map[string]bool{}
	for _, f := range MCPFiles(id, home, root, project) {
		// A file read once (the project is the home) is one scope.
		if (!present && f.Scope != "user" && f.Scope != "local") || seen[f.Path+"#"+f.Key] {
			continue
		}
		seen[f.Path+"#"+f.Key] = true
		if observe != nil {
			observe(f.Path)
		}
		cfg, err := ReadConfig(f.Path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			if warn != nil {
				warn("Cannot read MCP servers in " + f.Path + ": " + SafeParseError(err))
			}
			continue
		}
		servers := object(cfg[f.Key])
		if f.Key == "projects" {
			servers = object(object(servers[project])["mcpServers"])
		}
		disabled := stringList(cfg["disabledServers"]) // omp's denylist
		for name, v := range servers {
			raw := object(v)
			s := ParseMCPServer(raw)
			s.ID, s.Name, s.Harness, s.Scope, s.Config = id+":"+f.Scope+":"+name, name, id, f.Scope, f.Path
			s.Disabled = s.Disabled || contains(disabled, name)
			out = append(out, MCPEntry{Server: s, Raw: raw})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Server.ID < out[j].Server.ID })
	return out
}

// Configured is every harness's servers for project, as o-ai lists them:
// a harness is present when its executable is on PATH or its root exists.
// Warnings name the harness ("[codex] ...").
func Configured(home, project string) (entries []MCPEntry, warnings []string) {
	for _, id := range IDs {
		root := Root(id, home)
		_, lookErr := exec.LookPath(id)
		_, statErr := os.Stat(root)
		warn := func(m string) { warnings = append(warnings, "["+id+"] "+m) }
		entries = append(entries, DiscoverMCP(id, home, root, project, lookErr == nil || statErr == nil, nil, warn)...)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Server.ID < entries[j].Server.ID })
	return entries, warnings
}

// ParseMCPServer is a server entry in any harness's spelling: Claude,
// Gemini, omp and Codex (command, args, env, url or httpUrl, headers or
// http_headers, type), OpenCode (type local or remote, command as an
// array, environment).
func ParseMCPServer(m map[string]any) MCPServer {
	var s MCPServer
	args := stringList(m["args"])
	if cmd, ok := m["command"].(string); ok {
		s.Command = cmd
	} else if parts := stringList(m["command"]); len(parts) > 0 {
		s.Command, args = parts[0], append(parts[1:], args...)
	}
	s.Command, _ = Redact(s.Command, "").(string)
	s.Args = RedactArgs(args)
	for _, k := range []string{"url", "httpUrl", "serverUrl"} {
		if u := text(m, k); u != "" {
			s.URL, _ = Redact(u, "").(string)
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

// SessionFiles are the .jsonl files under root/YYYY/MM/DD (Codex's
// sessions directory), the newest days first (at most days of them),
// newest name first, at most n.
func SessionFiles(root string, days, n int) []string {
	desc := func(dir string) []string {
		entries, _ := os.ReadDir(dir)
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Sort(sort.Reverse(sort.StringSlice(names)))
		return names
	}
	var out []string
	for _, y := range desc(root) {
		for _, m := range desc(filepath.Join(root, y)) {
			for _, d := range desc(filepath.Join(root, y, m)) {
				if days--; days < 0 {
					return out
				}
				for _, f := range desc(filepath.Join(root, y, m, d)) {
					if strings.HasSuffix(f, ".jsonl") {
						out = append(out, filepath.Join(root, y, m, d, f))
						if len(out) >= n {
							return out
						}
					}
				}
			}
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

func text(m map[string]any, key string) string { s, _ := m[key].(string); return s }
func object(v any) map[string]any              { m, _ := v.(map[string]any); return m }

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func stringList(v any) []string {
	a, _ := v.([]any)
	var out []string
	for _, x := range a {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
