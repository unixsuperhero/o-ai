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

// nouns are the runtime's nouns: the inventory kinds, status, then quota.
var nouns = append(append([]string{}, categories...), "status", "quota")

// nounSpec is what a noun's verbs read: the bulky fields a list row's data
// leaves out (multi-KB at the long tail; show still prints the whole
// record), the fields a state row's data keeps (what a host draws a row's
// details from without a show), and the costly parts of a collection they
// need.
type nounSpec struct {
	omit  []string
	brief []string // a state row's data: these keys of the record; none keeps the lean record
	need  Need
	live  bool // fetched on every call, outside the inventory (quota.go); not in the state
}

var catalogs = Need{Catalogs: true}

// specs are the nouns' needs. Resource and settings lists read files only;
// harness and status read everything, processes included.
var specs = map[string]nounSpec{
	"harness":  {omit: []string{"settings"}, brief: []string{"id", "installed", "configured", "executable", "processStatus", "catalogStatus"}, need: needAll},
	"provider": {omit: []string{"models"}, brief: []string{"harness", "slug", "access"}, need: catalogs},
	"model":    {omit: []string{"metadata"}, brief: []string{"harness", "provider", "name", "cliSlug"}, need: catalogs},
	"thinking": {brief: []string{"slug", "harness", "provider", "cliSlug"}, need: catalogs},
	"skill":    {omit: []string{"content"}, brief: resourceBrief},
	"agent":    {omit: []string{"content"}, brief: resourceBrief, need: Need{RuntimeAgents: true}},
	"settings": {brief: []string{"path", "scope", "error"}},
	"mcp":      {brief: []string{"name", "harness", "scope", "transport", "command", "url", "disabled"}},
	"mcp-tool": {omit: []string{"inputSchema"}, brief: []string{"server", "name", "description", "harness"}},
	"status":   {need: needAll},
	"quota":    {live: true},
}

var resourceBrief = []string{"name", "description", "path", "harnesses", "scope", "source"}

// briefText is how many characters of a state row's description stay; a
// longer one is cut and ends in "…" (show prints it whole).
const briefText = 160

// brief is a record as a state row carries it: the noun's brief keys
// alone, a description cut to briefText; a noun with no brief keys gives
// its lean record.
func brief(noun string, v any) (any, error) {
	keep := specs[noun].brief
	if len(keep) == 0 {
		return lean(noun, v)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	out := make(map[string]json.RawMessage, len(keep))
	for _, k := range keep {
		if raw, ok := m[k]; ok {
			out[k] = raw
		}
	}
	if raw, ok := out["description"]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			if s = oneLine(s); len([]rune(s)) > briefText {
				s = string([]rune(s)[:briefText-1]) + "…"
			}
			out["description"], _ = json.Marshal(s)
		}
	}
	return out, nil
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

var formatParam = map[string]ochazuke.Parameter{
	"format": {Type: "string", Enum: []string{"json", "jsonl"}, Description: "json (an array, the default) or jsonl (one {noun, id, label, data} per line, the state's shape)"},
}

// nounParams are the parameters only some nouns' list and show take.
var nounParams = map[string]map[string]ochazuke.Parameter{
	"mcp":      {"server": {Type: "string", Description: "exact MCP server name"}},
	"mcp-tool": {"server": {Type: "string", Description: "exact MCP server name (its tools)"}},
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
		rows, err := nounRows(noun, t.inventory(o, specs[noun].need), o, model, lean)
		if err != nil {
			return nil, err
		}
		items := make([]ochazuke.ListItem, len(rows))
		for i, row := range rows {
			items[i] = ochazuke.ListItem{ID: row.ID, Label: row.Label, Noun: noun, Data: strings.TrimPrefix(row.ID, noun+"/")}
		}
		return rows, lists[noun].SetItems(items)
	}
	// listed is a list verb's answer: a JSON array, or JSON Lines tagged
	// with the noun under --format jsonl (the state's line shape).
	listed := func(noun string, args ochazuke.Arguments) ochazuke.Result {
		rows, err := load(noun, args)
		switch format := ochazuke.StringArg(args, "format", "json"); {
		case err != nil:
			return ochazuke.Failed(err)
		case format == "jsonl":
			return ochazuke.Done(lines(noun, rows))
		case format != "json":
			return ochazuke.Failed(fmt.Errorf("unknown --format %q: json or jsonl", format))
		}
		return ochazuke.Done(rows)
	}
	for _, noun := range categories {
		b := r.Define(noun).
			Verb("list", func(c ochazuke.Ctx) ochazuke.Result { return listed(noun, c.Args) },
				"the "+noun+" rows: {id, label, data}, data the inventory record (--format jsonl: one tagged row per line)").Rows(noun).
			Verb("show", func(c ochazuke.Ctx) ochazuke.Result { return result(t.show(noun, c, false)) },
				"one "+noun+" record: ENTRY (an id, name, slug or path) or --target ROW_ID")
		params := merged(collectParams, filterParams, nounParams[noun])
		r.Operations[noun+".list"] = withParams(r.Operations[noun+".list"], merged(params, formatParam))
		r.Operations[noun+".show"] = withParams(r.Operations[noun+".show"], merged(params, entryParam))
		if _, ok := nounParams[noun]["server"]; ok { // mcp tools tuios: a bare word after list is the server
			op := r.Operations[noun+".list"]
			op.Positional = []string{"server"}
			r.Operations[noun+".list"] = op
		}
		if noun == "model" {
			b.Verb("select", func(c ochazuke.Ctx) ochazuke.Result { return result(t.show(noun, c, true)) },
				"save ENTRY as o-ai's model selection (~/.config/aiview/selection.json); model show with no ENTRY prints it")
			r.Operations["model.select"] = withParams(r.Operations["model.select"], merged(params, entryParam))
		}
	}
	// mcp tools [SERVER] is mcp-tool list: the tools of the server a
	// bare word, --server or a --target mcp row names (all with none).
	r.Define("mcp").
		Verb("tools", func(c ochazuke.Ctx) ochazuke.Result {
			args := ochazuke.Arguments{}
			for k, v := range c.Args {
				args[k] = v
			}
			if c.Instance != nil && c.Instance.Noun == "mcp" && ochazuke.StringArg(args, "server", "") == "" {
				if id, ok := c.Instance.Data.(string); ok {
					args["server"] = id[strings.LastIndex(id, ":")+1:] // HARNESS:SCOPE:NAME
				}
			}
			return listed("mcp-tool", args)
		}, "the tools a server exposes, as a harness cached them (omp): mcp-tool rows of SERVER, --server or the --target mcp row's server").Rows("mcp-tool")
	tools := r.Operations["mcp.tools"]
	tools.Parameters, tools.Positional = r.Operations["mcp-tool.list"].Parameters, r.Operations["mcp-tool.list"].Positional
	r.Operations["mcp.tools"] = tools
	r.Define("status").
		Verb("list", func(c ochazuke.Ctx) ochazuke.Result { return listed("status", c.Args) },
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
			"every list's rows in one collection, JSON Lines {noun, id, label, data: the brief record} for "+strings.Join(stateNouns(), ", ")+" (the catalog's state)").State()
	r.Operations["status.state"] = withParams(r.Operations["status.state"], merged(collectParams, filterParams))
	r.Operations["status.list"] = withParams(r.Operations["status.list"], merged(collectParams, formatParam))
	r.Operations["status.show"] = withParams(r.Operations["status.show"], collectParams)
	t.defineQuota(r, lists["quota"])

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
// the noun's entries after the model filter, each record as shape gives it
// (lean for a list, brief for the state).
func nounRows(noun string, in Inventory, o Options, model string, shape func(string, any) (any, error)) ([]Row, error) {
	rows := []Row{}
	var err error
	add := func(id, label string, value any) {
		row := Row{ID: noun + "/" + id, Label: label}
		if err == nil {
			row.Data, err = shape(noun, value)
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
// noun's need, processes inspected once, and every list's rows as JSON
// Lines tagged by noun (ochazuke/cli Row). A row's data is its brief
// record (specs' brief keys): enough for a host to draw a row's details
// without a show, which prints the whole record.
func (t *Tool) state(args ochazuke.Arguments) (ocli.Lines, error) {
	o, model, err := t.options(args)
	if err != nil {
		return nil, err
	}
	var need Need
	for _, noun := range stateNouns() {
		n := specs[noun].need
		need.Processes = need.Processes || n.Processes
		need.Catalogs = need.Catalogs || n.Catalogs
		need.RuntimeAgents = need.RuntimeAgents || n.RuntimeAgents
	}
	in := t.inventory(o, need)
	var out ocli.Lines
	for _, noun := range stateNouns() {
		rows, err := nounRows(noun, in, o, model, brief)
		if err != nil {
			return nil, err
		}
		out = append(out, lines(noun, rows)...)
	}
	return out, nil
}

// stateNouns are the nouns the state answers: every noun but the live ones.
func stateNouns() []string {
	var out []string
	for _, noun := range nouns {
		if !specs[noun].live {
			out = append(out, noun)
		}
	}
	return out
}

// lines is rows as JSON Lines tagged with their noun.
func lines(noun string, rows []Row) ocli.Lines {
	out := make(ocli.Lines, len(rows))
	for i, r := range rows {
		out[i] = ocli.Row{Noun: noun, ID: r.ID, Label: r.Label, Data: r.Data}
	}
	return out
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
	if r, ok := e.Value.(Resource); ok && e.Harness == "" && r.Source != "" {
		return oneLine(name) + " [" + r.Source + "]"
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
		{"skills", len(in.Skills)}, {"agents", len(in.Agents)}, {"mcps", len(in.MCPServers)}, {"mcp-tools", len(in.MCPTools)},
		{"warnings", len(in.Warnings)},
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
		Server:   ochazuke.StringArg(args, "server", ""),
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
