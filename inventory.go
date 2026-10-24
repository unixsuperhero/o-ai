package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Options struct {
	Home     string
	Project  string
	Timeout  time.Duration
	NoProbe  bool
	Harness  string
	Provider string
	Search   string
	JSON     bool
}

type Setting struct {
	Path   string         `json:"path"`
	Scope  string         `json:"scope"`
	Values map[string]any `json:"values"`
	Error  string         `json:"error,omitempty"`
}

type Harness struct {
	ID            string    `json:"id"`
	Executable    string    `json:"executable,omitempty"`
	Installed     bool      `json:"installed"`
	Configured    bool      `json:"configured"`
	PIDs          []int     `json:"pids"`
	ProcessStatus string    `json:"processStatus"`
	Settings      []Setting `json:"settings"`
	CatalogStatus string    `json:"catalogStatus"`
	Warnings      []string  `json:"warnings,omitempty"`
}

type Thinking struct {
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Args        []string `json:"args"`
}

type Model struct {
	ID             string         `json:"id"`
	Harness        string         `json:"harness"`
	Provider       string         `json:"provider"`
	Slug           string         `json:"slug"`
	Name           string         `json:"name"`
	CLISlug        string         `json:"cliSlug"`
	Args           []string       `json:"args"`
	Thinking       []Thinking     `json:"thinking"`
	ThinkingStatus string         `json:"thinkingStatus"`
	Access         string         `json:"access"`
	Source         string         `json:"source"`
	Metadata       map[string]any `json:"metadata"`
}

type Provider struct {
	ID       string         `json:"id"`
	Harness  string         `json:"harness"`
	Slug     string         `json:"slug"`
	Models   []string       `json:"models"`
	Access   string         `json:"access"`
	Settings map[string]any `json:"settings,omitempty"`
}

type Resource struct {
	ID           string         `json:"id"`
	Kind         string         `json:"kind"`
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	Path         string         `json:"path"`
	ResolvedPath string         `json:"resolvedPath,omitempty"`
	Harnesses    []string       `json:"harnesses"`
	Scope        string         `json:"scope"`
	Access       string         `json:"access"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	Content      string         `json:"content,omitempty"`
}

type Inventory struct {
	CapturedAt time.Time  `json:"capturedAt"`
	Project    string     `json:"project"`
	Harnesses  []Harness  `json:"harnesses"`
	Providers  []Provider `json:"providers"`
	Models     []Model    `json:"models"`
	Skills     []Resource `json:"skills"`
	Agents     []Resource `json:"agents"`
	Warnings   []string   `json:"warnings,omitempty"`
}

// Entry is the shared projection used by the CLI and the TUI.
type Entry struct {
	ID       string
	Name     string
	Harness  string
	Provider string
	Summary  string
	Value    any
}

var categories = []string{"harness", "provider", "model", "thinking", "skill", "agent", "settings"}

func (in Inventory) Entries(kind string, o Options) []Entry {
	var rows []Entry
	add := func(e Entry) {
		if o.Harness != "" && !contains(strings.Split(e.Harness, ","), o.Harness) {
			return
		}
		if o.Provider != "" && e.Provider != o.Provider {
			return
		}
		if o.Search != "" && !strings.Contains(strings.ToLower(e.ID+" "+e.Name+" "+e.Summary), strings.ToLower(o.Search)) {
			return
		}
		rows = append(rows, e)
	}
	switch kind {
	case "harness", "settings":
		for _, h := range in.Harnesses {
			if kind == "settings" {
				for _, s := range h.Settings {
					add(Entry{ID: h.ID + ":" + s.Path, Name: s.Path, Harness: h.ID, Summary: s.Scope, Value: s})
				}
			} else {
				summary := "not installed"
				if h.Installed {
					summary = "installed"
				}
				summary += "; " + h.ProcessStatus + "; catalog: " + h.CatalogStatus
				add(Entry{ID: h.ID, Name: h.ID, Harness: h.ID, Summary: summary, Value: h})
			}
		}
	case "model":
		for _, m := range in.Models {
			add(Entry{ID: m.ID, Name: m.Name, Harness: m.Harness, Provider: m.Provider, Summary: m.CLISlug + " · " + m.Access, Value: m})
		}
	case "provider":
		for _, p := range in.Providers {
			add(Entry{ID: p.ID, Name: p.Slug, Harness: p.Harness, Provider: p.Slug, Summary: fmt.Sprintf("%d models · %s", len(p.Models), p.Access), Value: p})
		}
	case "thinking":
		for _, m := range in.Models {
			for _, t := range m.Thinking {
				value := struct {
					Thinking
					Model      string   `json:"model"`
					Harness    string   `json:"harness"`
					Provider   string   `json:"provider"`
					CLISlug    string   `json:"cliSlug"`
					LaunchArgs []string `json:"launchArgs"`
				}{t, m.ID, m.Harness, m.Provider, m.CLISlug, append(append([]string{}, m.Args...), t.Args...)}
				add(Entry{ID: m.ID + ":" + t.Slug, Name: t.Name, Harness: m.Harness, Provider: m.Provider, Summary: m.Name + " · " + t.Slug, Value: value})
			}
		}
	case "skill", "agent":
		resources := in.Skills
		if kind == "agent" {
			resources = in.Agents
		}
		for _, r := range resources {
			add(Entry{ID: r.ID, Name: r.Name, Harness: strings.Join(r.Harnesses, ","), Summary: r.Description, Value: r})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

func detail(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err.Error()
	}
	return string(b)
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func text(m map[string]any, key string) string { s, _ := m[key].(string); return s }
func object(v any) map[string]any              { m, _ := v.(map[string]any); return m }
func array(v any) []any                        { a, _ := v.([]any); return a }
func stringList(v any) []string {
	var out []string
	for _, x := range array(v) {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
