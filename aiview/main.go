package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

const helpText = `aiview — inspect local AI harnesses without making model requests

Usage:
  aiview status
  aiview <harness|provider|model|thinking|skill|agent|settings> [list]
  aiview <resource> show <id-or-name>
  aiview list <resource>
  aiview show <resource> <id-or-name>
  aiview model select <id-or-name>
  aiview model show                     show your saved selection
  aiview tui

Commands accept unique prefixes: "mo li", "prov sh", "th li".
Ambiguous prefixes are rejected, not guessed. Plural nouns also work.

Options (anywhere):
  --harness NAME        exact harness ID, e.g. omp, claude, codex
  --provider NAME       exact provider slug
  --model NAME          filter models/thinking by ID, slug, or display name
  --search TEXT         case-insensitive inventory search
  --project DIRECTORY  inspect this project's settings/resources (default: cwd)
  --home DIRECTORY     alternate home for file discovery and child processes
  --timeout DURATION   per-command deadline (default: 15s)
  --no-probe           files only; do not invoke harness metadata commands
  --json               machine-readable output
  --help               show this help

Examples:
  aiview mo li --harness omp
  aiview mo sh openai-codex/gpt-6-sol --harness omp
  aiview th li --model openai-codex/gpt-6-sol --harness omp
  aiview mo se opus --harness claude
  aiview mo sh
  aiview sk li --search review
  aiview status --json

TUI: tab/shift-tab categories · arrows/j/k select · enter details · / search
     esc back · r refresh · s save model · o choose project · q quit

Catalog membership does not prove authorization. Native metadata commands may
update their own caches and load trusted harness configuration. Aiview sends
no user prompts or inference requests.
`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "aiview:", err)
		os.Exit(1)
	}
}

type request struct {
	kind, action, query, model string
	options                    Options
}

func parseArgs(args []string) (request, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return request{}, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return request{}, err
	}
	r := request{options: Options{Home: home, Project: cwd, Timeout: 15 * time.Second}}
	var words []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			words = append(words, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") {
			words = append(words, arg)
			continue
		}
		key, value, hasValue := strings.Cut(arg, "=")
		switch key {
		case "--help", "-h":
			r.kind = "help"
			return r, nil
		case "--json":
			if hasValue {
				return r, fmt.Errorf("%s does not take a value", key)
			}
			r.options.JSON = true
			continue
		case "--no-probe":
			if hasValue {
				return r, fmt.Errorf("%s does not take a value", key)
			}
			r.options.NoProbe = true
			continue
		}
		if !contains([]string{"--harness", "--provider", "--model", "--search", "--project", "--home", "--timeout"}, key) {
			return r, fmt.Errorf("unknown option %q", key)
		}
		if !hasValue {
			i++
			if i >= len(args) {
				return r, fmt.Errorf("%s needs a value", key)
			}
			value = args[i]
		}
		if value == "" {
			return r, fmt.Errorf("%s needs a non-empty value", key)
		}
		switch key {
		case "--harness":
			r.options.Harness = value
		case "--provider":
			r.options.Provider = value
		case "--model":
			r.model = value
		case "--search":
			r.options.Search = value
		case "--project":
			r.options.Project = value
		case "--home":
			r.options.Home = value
		case "--timeout":
			duration, err := time.ParseDuration(value)
			if err != nil || duration <= 0 {
				return r, fmt.Errorf("invalid positive duration %q", value)
			}
			r.options.Timeout = duration
		}
	}
	r.options.Home, err = filepath.Abs(r.options.Home)
	if err != nil {
		return r, err
	}
	r.options.Project = expandPath(r.options.Project, r.options.Home, cwd)
	r.options.Project, err = filepath.Abs(r.options.Project)
	if err != nil {
		return r, err
	}
	for _, path := range []string{r.options.Home, r.options.Project} {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			return r, fmt.Errorf("not a readable directory: %s", path)
		}
	}
	if len(words) == 0 {
		r.kind = "status"
		return r, nil
	}
	top := append(append([]string{}, categories...), "status", "list", "show", "tui", "help")
	command, err := resolveCommand(words[0], top)
	if err != nil {
		return r, err
	}
	words = words[1:]
	if command == "list" || command == "show" {
		r.action = command
		if len(words) == 0 {
			return r, fmt.Errorf("%s needs a resource name", command)
		}
		r.kind, err = resolveCommand(words[0], categories)
		if err != nil {
			return r, err
		}
		words = words[1:]
	} else {
		r.kind = command
		r.action = "list"
		if contains(categories, command) && len(words) > 0 {
			actions := []string{"list", "show"}
			if command == "model" {
				actions = append(actions, "select")
			}
			r.action, err = resolveCommand(words[0], actions)
			if err != nil {
				return r, err
			}
			words = words[1:]
		}
	}
	if len(words) > 1 {
		return r, fmt.Errorf("too many arguments; quote names containing spaces")
	}
	if len(words) == 1 {
		r.query = words[0]
	}
	if r.action == "list" && r.query != "" {
		return r, fmt.Errorf("list takes no ID; use --search or show")
	}
	if (r.kind == "tui" || r.kind == "status" || r.kind == "help") && r.query != "" {
		return r, fmt.Errorf("%s takes no arguments", r.kind)
	}
	return r, nil
}

func resolveCommand(word string, choices []string) (string, error) {
	word = strings.ToLower(word)
	for _, choice := range choices {
		if word == choice || word == choice+"s" {
			return choice, nil
		}
	}
	var matches []string
	for _, choice := range choices {
		if strings.HasPrefix(choice, word) {
			matches = append(matches, choice)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("unknown command %q (choose %s)", word, strings.Join(choices, ", "))
	}
	sort.Strings(matches)
	return "", fmt.Errorf("ambiguous command %q: %s", word, strings.Join(matches, ", "))
}

func run(args []string, out, errOut io.Writer) error {
	r, err := parseArgs(args)
	if err != nil {
		return err
	}
	if r.kind == "help" {
		_, err = fmt.Fprint(out, helpText)
		return err
	}
	if r.kind == "tui" {
		if r.options.JSON {
			return errors.New("tui cannot be combined with --json")
		}
		return runTUI(r.options)
	}
	in := Collect(r.options)
	printWarnings(errOut, in)
	if r.kind == "status" {
		if r.options.JSON {
			return writeJSON(out, in)
		}
		printEntries(out, in.Entries("harness", r.options))
		fmt.Fprintf(out, "\n%d models · %d providers · %d skills · %d agents\nProject: %s\n", len(in.Models), len(in.Providers), len(in.Skills), len(in.Agents), in.Project)
		return nil
	}
	rows := filterModel(in.Entries(r.kind, r.options), r.model)
	if r.action == "show" || r.action == "select" {
		if r.query == "" && r.kind == "model" && r.action == "show" {
			r.query, err = loadSelection(r.options)
			if err != nil {
				return err
			}
		}
		if r.query == "" {
			return errors.New("show/select needs an ID or name")
		}
		row, err := selectEntry(rows, r.query)
		if err != nil {
			return err
		}
		if r.action == "select" {
			if err := saveSelection(r.options, row.ID); err != nil {
				return err
			}
		}
		if r.options.JSON {
			return writeJSON(out, row.Value)
		}
		fmt.Fprintln(out, detail(row.Value))
		return nil
	}
	if r.options.JSON {
		values := make([]any, 0, len(rows))
		for _, row := range rows {
			values = append(values, row.Value)
		}
		return writeJSON(out, values)
	}
	printEntries(out, rows)
	return nil
}

func filterModel(rows []Entry, query string) []Entry {
	if query == "" {
		return rows
	}
	var out []Entry
	for _, row := range rows {
		if m, ok := row.Value.(Model); ok {
			if m.ID == query || m.Slug == query || m.CLISlug == query || strings.EqualFold(m.Name, query) {
				out = append(out, row)
			}
		} else if strings.HasPrefix(row.ID, query+":") || strings.Contains(row.ID, "/"+query+":") || strings.Contains(row.ID, ":"+query+":") || strings.HasPrefix(row.Summary, query+" · ") {
			out = append(out, row)
		}
	}
	return out
}

func selectEntry(rows []Entry, query string) (Entry, error) {
	var exact, partial []Entry
	for _, row := range rows {
		match := row.ID == query || strings.EqualFold(row.Name, query)
		switch v := row.Value.(type) {
		case Model:
			match = match || v.Slug == query || v.CLISlug == query
		case Provider:
			match = match || v.Slug == query
		case Resource:
			match = match || v.Path == query
		}
		if match {
			exact = append(exact, row)
		} else if strings.Contains(strings.ToLower(row.ID+" "+row.Name), strings.ToLower(query)) {
			partial = append(partial, row)
		}
	}
	matches := exact
	if len(matches) == 0 {
		matches = partial
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return Entry{}, fmt.Errorf("no match for %q", query)
	}
	ids := make([]string, 0, len(matches))
	for _, m := range matches {
		ids = append(ids, m.ID)
	}
	return Entry{}, fmt.Errorf("ambiguous selection %q; use an exact ID or --harness/--provider:\n  %s", query, strings.Join(ids, "\n  "))
}

func printEntries(out io.Writer, rows []Entry) {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tHARNESS\tDETAIL")
	for _, row := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", oneLine(row.ID), oneLine(row.Name), row.Harness, shorten(oneLine(row.Summary), 100))
	}
	w.Flush()
	fmt.Fprintf(out, "\n%d entries\n", len(rows))
}
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
func writeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
func printWarnings(out io.Writer, in Inventory) {
	for _, w := range in.Warnings {
		fmt.Fprintln(out, "warning:", w)
	}
	for _, h := range in.Harnesses {
		for _, w := range h.Warnings {
			fmt.Fprintf(out, "warning [%s]: %s\n", h.ID, w)
		}
	}
}

func selectionPath(o Options) string {
	return filepath.Join(o.Home, ".config", "aiview", "selection.json")
}
func saveSelection(o Options, id string) error {
	path := selectionPath(o)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".selection-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(map[string]string{"model": id})
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
func loadSelection(o Options) (string, error) {
	m, err := readConfig(selectionPath(o))
	if err != nil {
		return "", errors.New("no saved model selection; use 'aiview model select ID' first")
	}
	id := text(m, "model")
	if id == "" {
		return "", errors.New("saved model selection is empty")
	}
	return id, nil
}
