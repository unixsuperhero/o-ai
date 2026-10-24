package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
)

// The command line is an ochazuke runtime behind ochazuke/cli: one noun per
// inventory kind (categories) plus status, each with list (rows, declared
// with Rows so a host such as nabe lists them) and show; model also has
// select. A row is {id: "NOUN/ENTRY_ID", label, data: the inventory record},
// the record in the JSON shape aiview always printed, and show prints that
// record alone. A --target no list shows yet is revealed as a bare row of
// its noun; show then finds the id in its own collection.

// nouns are the runtime's nouns: the inventory kinds, then status.
var nouns = append(append([]string{}, categories...), "status")

// Version is the catalog's version.
const Version = "0.1.0"

// Row is one element of a list.
type Row struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Data  any    `json:"data"`
}

// StatusCount is a status row's data: how many records of a kind.
type StatusCount struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

var collectParams = map[string]ochazuke.Parameter{
	"project":  {Type: "string", Description: "inspect this project's settings and resources (default: the working directory)"},
	"home":     {Type: "string", Description: "alternate home for file discovery and child processes"},
	"timeout":  {Type: "string", Description: "per metadata command deadline, a Go duration (default 15s)"},
	"no-probe": {Type: "boolean", Description: "files only; do not run harness metadata commands"},
}

var filterParams = map[string]ochazuke.Parameter{
	"harness":  {Type: "string", Description: "exact harness id, e.g. omp, claude, codex"},
	"provider": {Type: "string", Description: "exact provider slug"},
	"model":    {Type: "string", Description: "filter models and thinking levels by model id, slug or display name"},
	"search":   {Type: "string", Description: "case-insensitive id, name and description search"},
}

// entryParam sorts before every other parameter, so a bare word after
// show or select fills it (ochazuke.Operation.ParameterOrder is by name).
var entryParam = map[string]ochazuke.Parameter{
	"entry": {Type: "string", Description: "an id, name, slug or path from the noun's list"},
}

func merged(ms ...map[string]ochazuke.Parameter) map[string]ochazuke.Parameter {
	out := map[string]ochazuke.Parameter{}
	for _, m := range ms {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

func (t *Tool) shell() *ocli.Shell {
	r := ochazuke.NewRuntime()
	lists := map[string]*ochazuke.List{}
	for _, noun := range nouns {
		list, err := r.AddList("list/"+noun, nil)
		if err != nil {
			panic(err) // a fresh runtime takes every list
		}
		lists[noun] = list
	}
	result := func(v any, err error) ochazuke.Result {
		if err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(v)
	}
	load := func(noun string, args ochazuke.Arguments) ([]Row, error) {
		o, model, err := t.options(args)
		if err != nil {
			return nil, err
		}
		in := t.inventory(o)
		rows := []Row{}
		items := []ochazuke.ListItem{}
		add := func(id, label string, value any) {
			rows = append(rows, Row{ID: noun + "/" + id, Label: label, Data: value})
			items = append(items, ochazuke.ListItem{ID: noun + "/" + id, Label: label, Noun: noun, Data: id})
		}
		if noun == "status" {
			for _, c := range statusCounts(in) {
				add(c.Kind, fmt.Sprintf("%s: %d", c.Kind, c.Count), c)
			}
		} else {
			for _, e := range filterModel(in.Entries(noun, o), model) {
				add(e.ID, label(noun, e), e.Value)
			}
		}
		return rows, lists[noun].SetItems(items)
	}
	for _, noun := range categories {
		b := r.Define(noun).
			Verb("list", func(c ochazuke.Ctx) ochazuke.Result { return result(load(noun, c.Args)) },
				"the "+noun+" rows: {id, label, data}, data the inventory record").Rows(noun).
			Verb("show", func(c ochazuke.Ctx) ochazuke.Result { return result(t.show(noun, c, false)) },
				"one "+noun+" record: ENTRY (an id, name, slug or path) or --target ROW_ID")
		params := merged(collectParams, filterParams)
		r.Operations[noun+".list"] = withParams(r.Operations[noun+".list"], params)
		r.Operations[noun+".show"] = withParams(r.Operations[noun+".show"], merged(params, entryParam))
		if noun == "model" {
			b.Verb("select", func(c ochazuke.Ctx) ochazuke.Result { return result(t.show(noun, c, true)) },
				"save ENTRY as o-ai's model selection (~/.config/aiview/selection.json); model show with no ENTRY prints it")
			r.Operations["model.select"] = withParams(r.Operations["model.select"], merged(params, entryParam))
		}
	}
	r.Define("status").
		Verb("list", func(c ochazuke.Ctx) ochazuke.Result { return result(load("status", c.Args)) },
			"how many records of each kind: {id, label, data: {kind, count}}").Rows("status").
		Verb("show", func(c ochazuke.Ctx) ochazuke.Result {
			o, _, err := t.options(c.Args)
			if err != nil {
				return ochazuke.Failed(err)
			}
			return ochazuke.Done(t.inventory(o))
		}, "the whole inventory, warnings included")
	r.Operations["status.list"] = withParams(r.Operations["status.list"], collectParams)
	r.Operations["status.show"] = withParams(r.Operations["status.show"], collectParams)

	sh := ocli.New(r, "o-ai", t.Stdin, t.Stdout, t.Stderr)
	sh.Usage = ocli.DefaultUsage(t.Prog)
	sh.Version = Version
	// Reveal makes the target row exist without collecting: show
	// collects with its own arguments and looks the id up there.
	sh.Reveal = func(target string) error {
		noun, id, ok := strings.Cut(target, "/")
		if !ok || id == "" || lists[noun] == nil {
			return fmt.Errorf("not a row id (NOUN/ID from a list): %s", target)
		}
		return lists[noun].SetItems([]ochazuke.ListItem{{ID: target, Label: id, Noun: noun, Data: id}})
	}
	return sh
}

func withParams(op ochazuke.Operation, p map[string]ochazuke.Parameter) ochazuke.Operation {
	op.Parameters = p
	return op
}

// label is a row's label: the name, a thinking level's model and slug, and
// the harness for every kind but harness itself.
func label(noun string, e Entry) string {
	name := e.Name
	if noun == "thinking" {
		name = e.Summary
	}
	if noun == "harness" || e.Harness == "" {
		return oneLine(name)
	}
	return oneLine(name) + " [" + e.Harness + "]"
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func statusCounts(in Inventory) []StatusCount {
	return []StatusCount{
		{"harnesses", len(in.Harnesses)}, {"providers", len(in.Providers)}, {"models", len(in.Models)},
		{"skills", len(in.Skills)}, {"agents", len(in.Agents)}, {"warnings", len(in.Warnings)},
	}
}

// show is the record a show or select names: the target row, else the
// entry argument; model show with neither is the saved selection.
func (t *Tool) show(noun string, c ochazuke.Ctx, sel bool) (any, error) {
	o, model, err := t.options(c.Args)
	if err != nil {
		return nil, err
	}
	rows := filterModel(t.inventory(o).Entries(noun, o), model)
	var row Entry
	var id string
	if c.Instance != nil && c.Instance.Noun == noun {
		id, _ = c.Instance.Data.(string)
	}
	if id != "" {
		found := false
		for _, e := range rows {
			if e.ID == id {
				row, found = e, true
			}
		}
		if !found {
			return nil, fmt.Errorf("no %s %q with these filters", noun, id)
		}
	} else {
		query := ochazuke.StringArg(c.Args, "entry", "")
		if query == "" && noun == "model" && !sel {
			if query, err = loadSelection(o); err != nil {
				return nil, err
			}
		}
		if query == "" {
			return nil, fmt.Errorf("%s %s needs ENTRY or --target ROW_ID", noun, map[bool]string{true: "select", false: "show"}[sel])
		}
		if row, err = selectEntry(rows, query); err != nil {
			return nil, err
		}
	}
	if sel {
		if err := saveSelection(o, row.ID); err != nil {
			return nil, err
		}
	}
	return row.Value, nil
}

// options are the collection options and the model filter a verb's
// arguments spell, defaulting home and project to the tool's.
func (t *Tool) options(args ochazuke.Arguments) (Options, string, error) {
	o := Options{
		Home:     ochazuke.StringArg(args, "home", t.Home),
		Project:  ochazuke.StringArg(args, "project", t.Cwd),
		Timeout:  15 * time.Second,
		Harness:  ochazuke.StringArg(args, "harness", ""),
		Provider: ochazuke.StringArg(args, "provider", ""),
		Search:   ochazuke.StringArg(args, "search", ""),
	}
	o.NoProbe, _ = args["no-probe"].(bool)
	if v := ochazuke.StringArg(args, "timeout", ""); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return o, "", fmt.Errorf("invalid positive duration %q", v)
		}
		o.Timeout = d
	}
	var err error
	if o.Home, err = filepath.Abs(expandPath(o.Home, t.Home, t.Cwd)); err != nil {
		return o, "", err
	}
	if o.Project, err = filepath.Abs(expandPath(o.Project, o.Home, t.Cwd)); err != nil {
		return o, "", err
	}
	for _, path := range []string{o.Home, o.Project} {
		if !isDir(path) {
			return o, "", errors.New("not a readable directory: " + path)
		}
	}
	return o, ochazuke.StringArg(args, "model", ""), nil
}

// inventory collects once per collection option set, printing the
// warnings to Stderr the first time.
func (t *Tool) inventory(o Options) Inventory {
	key := Options{Home: o.Home, Project: o.Project, Timeout: o.Timeout, NoProbe: o.NoProbe}
	if in, ok := t.cache[key]; ok {
		return in
	}
	in := Collect(key)
	printWarnings(t.Stderr, in)
	if t.cache == nil {
		t.cache = map[Options]Inventory{}
	}
	t.cache[key] = in
	return in
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
