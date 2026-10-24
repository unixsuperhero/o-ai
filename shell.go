package main

import (
	"encoding/json"
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

// nounSpec is what a noun's verbs read: the bulky fields a list row's data
// leaves out (multi-KB at the long tail; show still prints the whole
// record) and the costly parts of a collection they need.
type nounSpec struct {
	omit []string
	need Need
}

var catalogs = Need{Catalogs: true}

// specs are the nouns' needs. Resource and settings lists read files only;
// harness and status read everything, processes included.
var specs = map[string]nounSpec{
	"harness":  {omit: []string{"settings"}, need: needAll},
	"provider": {omit: []string{"models"}, need: catalogs},
	"model":    {omit: []string{"metadata"}, need: catalogs},
	"thinking": {need: catalogs},
	"skill":    {omit: []string{"content"}},
	"agent":    {omit: []string{"content"}, need: Need{RuntimeAgents: true}},
	"settings": {},
	"status":   {need: needAll},
}

// lean is a record as a list row carries it: its JSON without the noun's
// omit fields, every other key unchanged.
func lean(noun string, v any) (any, error) {
	omit := specs[noun].omit
	if len(omit) == 0 {
		return v, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	for _, k := range omit {
		delete(m, k)
	}
	return m, nil
}

// Version is the catalog's version.
const Version = "0.1.0"

// Row is one element of a list.
type Row struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Data  any    `json:"data,omitempty"`
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
	"fresh":    {Type: "boolean", Description: "collect now even if the inventory cache is young"},
	"ttl":      {Type: "string", Description: "how old a cached inventory may be, a Go duration (default 5m; 0s collects)"},
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
		rows, err := nounRows(noun, t.inventory(o, specs[noun].need), o, model, true)
		if err != nil {
			return nil, err
		}
		items := make([]ochazuke.ListItem, len(rows))
		for i, row := range rows {
			items[i] = ochazuke.ListItem{ID: row.ID, Label: row.Label, Noun: noun, Data: strings.TrimPrefix(row.ID, noun+"/")}
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
			return ochazuke.Done(t.inventory(o, specs["status"].need))
		}, "the whole inventory, warnings included")
	r.Define("status").
		Verb("state", func(c ochazuke.Ctx) ochazuke.Result { return result(t.state(c.Args)) },
			"every list's rows in one collection: {NOUN: [{id, label}]} for "+strings.Join(nouns, ", ")+" (the catalog's state)").State()
	r.Operations["status.state"] = withParams(r.Operations["status.state"], merged(collectParams, filterParams))
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

// nounRows is one noun's rows from an inventory: the status counts, else
// the noun's entries after the model filter. withData keeps each record
// (lean); without it a row is its id and label, what a host's list needs.
func nounRows(noun string, in Inventory, o Options, model string, withData bool) ([]Row, error) {
	rows := []Row{}
	var err error
	add := func(id, label string, value any) {
		row := Row{ID: noun + "/" + id, Label: label}
		if withData && err == nil {
			row.Data, err = lean(noun, value)
		}
		rows = append(rows, row)
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
	return rows, err
}

// state is the catalog's state source: one collection covering every
// noun's need, processes inspected once, and every list's rows by noun.
func (t *Tool) state(args ochazuke.Arguments) (map[string][]Row, error) {
	o, model, err := t.options(args)
	if err != nil {
		return nil, err
	}
	var need Need
	for _, noun := range nouns {
		n := specs[noun].need
		need.Processes = need.Processes || n.Processes
		need.Catalogs = need.Catalogs || n.Catalogs
		need.RuntimeAgents = need.RuntimeAgents || n.RuntimeAgents
	}
	in := t.inventory(o, need)
	out := make(map[string][]Row, len(nouns))
	for _, noun := range nouns {
		if out[noun], err = nounRows(noun, in, o, model, false); err != nil {
			return nil, err
		}
	}
	return out, nil
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
	rows := filterModel(t.inventory(o, specs[noun].need).Entries(noun, o), model)
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
	o.Fresh, _ = args["fresh"].(bool)
	if v := ochazuke.StringArg(args, "timeout", ""); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return o, "", fmt.Errorf("invalid positive duration %q", v)
		}
		o.Timeout = d
	}
	o.TTL = defaultTTL
	if v := ochazuke.StringArg(args, "ttl", ""); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return o, "", fmt.Errorf("invalid --ttl %q (a duration, 0s or more)", v)
		}
		o.TTL = d
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

// inventory is the collection need asks for: this process's, else the
// cache file's (cache.go), else a new one; warnings go to Stderr the first
// time. Processes are inspected afresh on every call that needs them.
func (t *Tool) inventory(o Options, need Need) Inventory {
	if o.NoProbe {
		need.Catalogs, need.RuntimeAgents = false, false // nothing to probe
	}
	key := cacheKey(o)
	e, ok := t.cache[key]
	if !ok || !e.Need.covers(need) {
		e = cacheEntry{Need: need, Inventory: t.cachedInventory(o, key, need)}
		printWarnings(t.Stderr, e.Inventory)
		if t.cache == nil {
			t.cache = map[string]cacheEntry{}
		}
		t.cache[key] = e
	}
	in := e.Inventory
	if need.Processes {
		in.Harnesses = append([]Harness{}, in.Harnesses...)
		in.Warnings = append([]string{}, in.Warnings...)
		for i := range in.Harnesses {
			in.Harnesses[i].PIDs = []int{}
		}
		inspectProcesses(&in, o)
	}
	return in
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
