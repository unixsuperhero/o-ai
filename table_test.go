package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"ofam.dev/ochazuke"
)

// The catalog declares each table noun's columns, in order.
func TestCatalogDeclaresColumns(t *testing.T) {
	out, _, err := runCLI(t, "catalog")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Nouns []struct {
			Name    string            `json:"name"`
			Columns []ochazuke.Column `json:"columns"`
		} `json:"nouns"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	titles := map[string][]string{}
	for _, n := range doc.Nouns {
		for _, c := range n.Columns {
			titles[n.Name] = append(titles[n.Name], c.Title)
		}
	}
	want := map[string][]string{
		"skill":    {"name", "source", "scope", "description"},
		"agent":    {"name", "source", "scope", "description"},
		"model":    {"model", "harness", "provider", "context", "thinking"},
		"harness":  {"harness", "state", "procs", "catalog", "executable"},
		"provider": {"provider", "harness", "models", "access"},
		"mcp":      {"server", "harness", "scope", "transport", "command"},
		"mcp-tool": {"tool", "server", "description"},
		"quota":    {"harness", "window", "used", "left", "resets"},
		"status":   {"kind", "count"},
	}
	for noun, w := range want {
		if !slices.Equal(titles[noun], w) {
			t.Errorf("%s columns %v, want %v", noun, titles[noun], w)
		}
	}
	for _, noun := range []string{"thinking", "settings"} {
		if titles[noun] != nil {
			t.Errorf("%s has columns %v", noun, titles[noun])
		}
	}
}

// Every row of a table noun carries one cell per column, in a list, a
// jsonl line and the state; the other nouns' rows carry none.
func TestRowsCarryCellsPerColumn(t *testing.T) {
	in := Inventory{
		Harnesses:  []Harness{{ID: "claude", Installed: true, CatalogStatus: "live", Executable: "/bin/claude", Settings: []Setting{{Path: "/s.json", Scope: "user"}}}},
		Providers:  []Provider{{ID: "claude:anthropic", Harness: "claude", Slug: "anthropic", Models: []string{"m"}, Access: "listed"}},
		Models:     []Model{{ID: "omp:p/m", Harness: "omp", Provider: "p", Slug: "m", Name: "M", Thinking: []Thinking{{Slug: "low"}, {Slug: "high"}}, Metadata: map[string]any{"contextWindow": 200000.0}}},
		Skills:     []Resource{{ID: "skill:ai-tools:/s", Kind: "skill", Name: "s", Description: "does\nthings", Path: "/s", Harnesses: []string{}, Scope: "source", Source: "ai-tools"}},
		Agents:     []Resource{{ID: "agent:claude:/a", Kind: "agent", Name: "a", Path: "/a", Harnesses: []string{"claude", "omp"}, Scope: "user"}},
		MCPServers: []MCPServer{{ID: "claude:user:tuios", Name: "tuios", Harness: "claude", Scope: "user", Transport: "stdio", Command: "tuios", Args: []string{"mcp"}}},
		MCPTools:   []MCPTool{{ID: "omp:tuios/send", Server: "tuios", Name: "send", Description: "Type", Harness: "omp"}},
	}
	var out, errOut bytes.Buffer
	tool := &Tool{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut, Env: append(os.Environ(), "XDG_CACHE_HOME="+t.TempDir()), Home: isolatedHome(t), Prog: "o-ai",
		collect: func(Options, Need) Inventory { return in }}
	tool.Cwd = tool.Home
	if err := tool.Main(context.Background(), []string{"status", "state", "--no-probe"}); err != nil {
		t.Fatal(err, errOut.String())
	}
	cells := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var row struct {
			Noun  string   `json:"noun"`
			ID    string   `json:"id"`
			Cells []string `json:"cells"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if tb, ok := tables[row.Noun]; ok != (row.Cells != nil) || len(row.Cells) != len(tb.columns) {
			t.Errorf("%s cells %v for columns %v", row.ID, row.Cells, tb.columns)
		}
		if _, seen := cells[row.Noun]; !seen {
			cells[row.Noun] = row.Cells
		}
	}
	procs := "?" // processes are inspected live (ps), so the count is this machine's
	if h := cells["harness"]; len(h) == 5 {
		if _, err := strconv.Atoi(h[2]); err == nil {
			procs = h[2]
		}
	}
	for noun, want := range map[string][]string{
		"harness":  {"claude", "installed", procs, "live", "/bin/claude"},
		"provider": {"anthropic", "claude", "1", "listed"},
		"model":    {"M", "omp", "p", "200k", "low,high"},
		"skill":    {"s", "ai-tools", "source", "does things"},
		"agent":    {"a", "claude,omp", "user", ""},
		"mcp":      {"tuios", "claude", "user", "stdio", "tuios mcp"},
		"mcp-tool": {"send", "tuios", "Type"},
		"status":   {"harnesses", "1"},
	} {
		if !slices.Equal(cells[noun], want) {
			t.Errorf("%s cells %q, want %q", noun, cells[noun], want)
		}
	}
	if _, ok := cells["thinking"]; !ok || cells["thinking"] != nil || cells["settings"] != nil {
		t.Errorf("thinking and settings rows carry no cells: %v %v", cells["thinking"], cells["settings"])
	}

	out.Reset()
	if err := tool.Main(context.Background(), []string{"skill", "list", "--no-probe"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"cells":["s","ai-tools","source","does things"]`) {
		t.Fatalf("skill list rows carry cells: %s", out.String())
	}
}

func TestQuotaRowsCarryCells(t *testing.T) {
	reset := time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC)
	rows := quotaRows([]Quota{
		{Harness: "claude", Windows: []QuotaWindow{{Harness: "claude", Window: "weekly_all", UsedPercent: 47, RemainingPercent: 53, ResetsAt: reset.Format(time.RFC3339), key: "weekly_all"}}},
		{Harness: "codex", Error: "no login"},
	}, reset.Add(-time.Hour))
	if want := []string{"claude", "weekly_all", "47%", "53%", reset.Local().Format("2006-01-02 15:04")}; !slices.Equal(rows[0].Cells, want) {
		t.Errorf("window cells %q, want %q", rows[0].Cells, want)
	}
	if want := []string{"codex", "unavailable", "", "", "no login"}; !slices.Equal(rows[1].Cells, want) {
		t.Errorf("error cells %q, want %q", rows[1].Cells, want)
	}
}

func TestContextWindowAndTokens(t *testing.T) {
	for _, c := range []struct {
		meta map[string]any
		want string
	}{
		{map[string]any{"contextWindow": 200000.0}, "200k"},
		{map[string]any{"context_window": int64(272000)}, "272k"},
		{map[string]any{"limit": map[string]any{"context": json.Number("1048576")}}, "1M"},
		{map[string]any{"contextWindow": 1500000}, "1.5M"},
		{map[string]any{"contextWindow": 512}, "512"},
		{map[string]any{}, ""},
	} {
		if got := tokens(contextWindow(c.meta)); got != c.want {
			t.Errorf("tokens(%v) = %q, want %q", c.meta, got, c.want)
		}
	}
}
