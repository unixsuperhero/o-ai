package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/unixsuperhero/ochazuke"
)

// Tables: a noun with columns declares them on the runtime noun (the
// catalog's columns), and every row of it, in a list, a --format jsonl
// line or the state, carries its cells in that order (ochazuke.ListItem.Cells,
// cli.Row.Cells), so a host such as nabe draws the list as a table. The
// columns and the cells are each noun's specs (shell.go); a noun without
// columns (thinking, settings) stays a plain list of labels.

// table is a noun's columns and the cells of one of its records.
type table struct {
	columns []ochazuke.Column
	cells   func(any) []string
}

var resourceTable = table{
	columns: []ochazuke.Column{{Title: "name", Min: 12}, {Title: "source", Priority: 2}, {Title: "scope", Priority: 3}, {Title: "description", Priority: 1}},
	cells: func(v any) []string {
		r, _ := v.(Resource)
		source := strings.Join(r.Harnesses, ",")
		if source == "" {
			source = r.Source
		}
		return []string{r.Name, source, r.Scope, cut(r.Description)}
	},
}

var tables = map[string]table{
	"harness": {
		columns: []ochazuke.Column{{Title: "harness", Min: 8}, {Title: "state", Priority: 1}, {Title: "procs", Align: ochazuke.AlignRight, Priority: 2}, {Title: "catalog", Priority: 3}, {Title: "executable", Priority: 4}},
		cells: func(v any) []string {
			h, _ := v.(Harness)
			state := "missing"
			switch {
			case h.Installed:
				state = "installed"
			case h.Configured:
				state = "configured"
			}
			return []string{h.ID, state, strconv.Itoa(len(h.PIDs)), h.CatalogStatus, h.Executable}
		},
	},
	"provider": {
		columns: []ochazuke.Column{{Title: "provider", Min: 10}, {Title: "harness", Priority: 1}, {Title: "models", Align: ochazuke.AlignRight, Priority: 2}, {Title: "access", Priority: 3}},
		cells: func(v any) []string {
			p, _ := v.(Provider)
			return []string{p.Slug, p.Harness, strconv.Itoa(len(p.Models)), p.Access}
		},
	},
	"model": {
		columns: []ochazuke.Column{{Title: "model", Min: 16}, {Title: "harness", Priority: 1}, {Title: "provider", Priority: 2}, {Title: "context", Align: ochazuke.AlignRight, Priority: 3}, {Title: "thinking", Priority: 4}},
		cells: func(v any) []string {
			m, _ := v.(Model)
			levels := make([]string, len(m.Thinking))
			for i, t := range m.Thinking {
				levels[i] = t.Slug
			}
			return []string{oneLine(m.Name), m.Harness, m.Provider, tokens(contextWindow(m.Metadata)), strings.Join(levels, ",")}
		},
	},
	"skill": resourceTable,
	"agent": resourceTable,
	"mcp": {
		columns: []ochazuke.Column{{Title: "server", Min: 10}, {Title: "harness", Priority: 1}, {Title: "scope", Priority: 3}, {Title: "transport", Priority: 4}, {Title: "command", Priority: 2}},
		cells: func(v any) []string {
			s, _ := v.(MCPServer)
			command := strings.Join(append([]string{s.Command}, s.Args...), " ")
			if s.Command == "" {
				command = s.URL
			}
			if s.Disabled {
				command = "(disabled) " + command
			}
			return []string{s.Name, s.Harness, s.Scope, s.Transport, cut(command)}
		},
	},
	"mcp-tool": {
		columns: []ochazuke.Column{{Title: "tool", Min: 12}, {Title: "server", Priority: 1}, {Title: "description", Priority: 2}},
		cells: func(v any) []string {
			t, _ := v.(MCPTool)
			return []string{t.Name, t.Server, cut(t.Description)}
		},
	},
	"status": {
		columns: []ochazuke.Column{{Title: "kind", Min: 10}, {Title: "count", Align: ochazuke.AlignRight}},
		cells: func(v any) []string {
			c, _ := v.(StatusCount)
			return []string{c.Kind, strconv.Itoa(c.Count)}
		},
	},
	"quota": {
		columns: []ochazuke.Column{{Title: "harness", Min: 7}, {Title: "window", Min: 10}, {Title: "used", Align: ochazuke.AlignRight}, {Title: "left", Align: ochazuke.AlignRight, Priority: 1}, {Title: "resets", Priority: 2}},
		cells: func(v any) []string {
			switch q := v.(type) {
			case QuotaWindow:
				resets := ""
				if at, err := time.Parse(time.RFC3339, q.ResetsAt); err == nil {
					resets = at.Local().Format("2006-01-02 15:04")
				}
				if q.Stale {
					resets += " (stale)"
				}
				return []string{q.Harness, q.Window, percent(q.UsedPercent) + "%", percent(q.RemainingPercent) + "%", resets}
			case Quota:
				return []string{q.Harness, "unavailable", "", "", cut(q.Error)}
			}
			return nil
		},
	},
}

// cellsOf is a record's cells when its noun has columns, else nil.
func cellsOf(noun string, v any) []string {
	if t, ok := tables[noun]; ok {
		return t.cells(v)
	}
	return nil
}

// cut is s on one line, at most briefText characters, ending in "…" when
// cut.
func cut(s string) string {
	s = oneLine(s)
	if r := []rune(s); len(r) > briefText {
		return string(r[:briefText-1]) + "…"
	}
	return s
}

// contextWindow is a model's context size from its catalog metadata, in
// each harness's spelling (omp and pi contextWindow, codex context_window,
// opencode limit.context); 0 when the catalog does not say.
func contextWindow(meta map[string]any) int64 {
	for _, v := range []any{meta["contextWindow"], meta["context_window"], object(meta["limit"])["context"]} {
		switch n := v.(type) {
		case float64:
			return int64(n)
		case int64:
			return n
		case int:
			return int64(n)
		case interface{ Int64() (int64, error) }: // json.Number from the cache
			if i, err := n.Int64(); err == nil {
				return i
			}
		}
	}
	return 0
}

// tokens is a token count as 200k or 1M; "" for none.
func tokens(n int64) string {
	switch {
	case n <= 0:
		return ""
	case n >= 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e6), ".0") + "M"
	case n >= 1_000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	}
	return strconv.FormatInt(n, 10)
}
