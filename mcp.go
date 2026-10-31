package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ofam.dev/o-ai/harness"
)

// MCP servers are read from the harnesses' own configuration files, never
// started. A server's tools are listed only where a harness has cached
// them: omp keeps each server's tools/list answer in agent.db (table
// cache, key mcp_tools:SERVER), read here with the sqlite3 command in
// read-only mode. Listing the tools of any other server would mean
// starting it (initialize, tools/list), which o-ai does not do.

// MCPServer is one configured server of one harness (package harness).
type MCPServer = harness.MCPServer

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

func (c *collector) discoverMCP() {
	h, o := c.harness.ID, c.options
	present := c.harness.Installed || c.harness.Configured
	for _, e := range harness.DiscoverMCP(h, o.Home, c.root, o.Project, present, func(p string) { c.input(p) }, c.warn) {
		c.inventory.MCPServers = append(c.inventory.MCPServers, e.Server)
	}
	sort.Slice(c.inventory.MCPServers, func(i, j int) bool { return c.inventory.MCPServers[i].ID < c.inventory.MCPServers[j].ID })
	if h == "omp" {
		c.ompMCPTools()
	}
}

func redactArgs(args []string) []string { return harness.RedactArgs(args) }

// sqlite3 is the command that reads omp's agent.db; a test replaces it.
var sqlite3 = "sqlite3"

// ompMCPTools reads the tools omp cached per server from agent.db, read
// only, the cache table's mcp_tools rows alone (the database also holds
// logins, which are never selected). An expired row is still listed with
// its expiry: omp would ask the server again.
func (c *collector) ompMCPTools() {
	db := filepath.Join(c.root, "agent.db")
	c.input(db, db+"-wal") // sqlite writes the WAL first
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
