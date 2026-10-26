package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
)

// A collection's inputs: every path the collectors read or looked for
// (settings and MCP files, model caches, the plugin manifest, each skill or
// agent file and every directory walked to reach it, the harness
// executables, the PATH directories, sources.json and each source root),
// recorded while collecting and kept beside the inventory cache in
// inputs.json, one entry per cache key:
//
//	{schema: 1, entries: {KEY: {at, need, stat, inputs, harnesses}}}
//
// stat is cli.StatFingerprint over inputs when the collection was written.
// A directory's mtime changes when an entry is added, removed or renamed in
// it, and every directory on the way to a skill is an input, so a new skill
// deep in a tree changes the stat of the directory that holds it.
//
// The inputs serve two things. An inventory cache entry whose inputs'
// stat no longer matches is collected again (a settings or skill edit shows
// on the next call instead of after the TTL). And the state's
// StateCache.Fingerprint (stateFingerprint) answers an unchanged poll
// without reading the inventory or running the state verb.
//
// Not covered by the stat: what a probe prints (omp, claude, opencode and
// pi report their model catalogs and runtime agents by running them; only
// their executables and the files o-ai itself reads are stat'd, so a
// catalog that changes behind an unchanged executable shows after the TTL,
// as before), and a file rewritten with the same size and mtime.

const inputsSchema = 1

type inputsEntry struct {
	At        time.Time     `json:"at"`
	Need      Need          `json:"need"`
	Stat      string        `json:"stat"`
	Inputs    []string      `json:"inputs"`
	Harnesses []harnessExec `json:"harnesses"` // what inspectProcesses matches
}

type harnessExec struct {
	ID         string `json:"id"`
	Executable string `json:"executable"`
}

type inputsFile struct {
	Schema  int                    `json:"schema"`
	Entries map[string]inputsEntry `json:"entries"`
}

// inputSet collects the paths of one collection; the harnesses collect at
// once, so it locks. A nil set records nothing.
type inputSet struct {
	mu    sync.Mutex
	paths map[string]bool
}

func (s *inputSet) add(paths ...string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.paths == nil {
		s.paths = map[string]bool{}
	}
	for _, p := range paths {
		if p != "" {
			s.paths[filepath.Clean(p)] = true
		}
	}
}

func (s *inputSet) list() []string {
	if s == nil {
		return []string{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.paths))
	for p := range s.paths {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func inputsPath(cachePath string) string {
	return filepath.Join(filepath.Dir(cachePath), "inputs.json")
}

// readInputs is the inputs file, empty when it is missing, corrupt or of
// another schema.
func readInputs(path string) inputsFile {
	var file inputsFile
	if b, err := os.ReadFile(path); err != nil || json.Unmarshal(b, &file) != nil || file.Schema != inputsSchema {
		file = inputsFile{}
	}
	if file.Entries == nil {
		file.Entries = map[string]inputsEntry{}
	}
	file.Schema = inputsSchema
	return file
}

// fresh reports whether e still describes the inventory collected at at:
// the same collection and its inputs unchanged.
func (e inputsEntry) fresh(at time.Time) bool {
	return e.At.Equal(at) && e.Stat == ocli.StatFingerprint(e.Inputs...)
}

// stateNeed is the collection the state reads: every state noun's need.
func stateNeed() Need {
	var need Need
	for _, noun := range stateNouns() {
		n := specs[noun].need
		need.Processes = need.Processes || n.Processes
		need.Catalogs = need.Catalogs || n.Catalogs
		need.RuntimeAgents = need.RuntimeAgents || n.RuntimeAgents
	}
	return need
}

var errNoFingerprint = errors.New("no recorded inputs for this collection")

// stateFingerprint is the state verb's StateCache.Fingerprint: the inputs
// of the inventory the state would read, the state verb's identity, and the
// processes. It refuses (so the verb runs) when no collection is recorded,
// when the recorded one does not cover the state's need, and when it is as
// old as the TTL (the verb would collect again, so a probe's catalog still
// refreshes every TTL). Processes are never cached: the harnesses' process
// statuses are inspected here as the state would (one ps), so a harness
// starting or exiting changes the fingerprint.
func (t *Tool) stateFingerprint(args ochazuke.Arguments) (string, error) {
	o, _, err := t.options(args)
	if err != nil {
		return "", err
	}
	need := stateNeed()
	if o.NoProbe {
		need.Catalogs, need.RuntimeAgents = false, false
	}
	key := cacheKey(o)
	e, ok := readInputs(inputsPath(t.cachePath())).Entries[key]
	now := t.clock()
	switch {
	case !ok, !e.Need.covers(need):
		return "", errNoFingerprint
	case o.TTL <= 0 || now.Before(e.At) || now.Sub(e.At) >= o.TTL:
		return "", errors.New("recorded collection is as old as the TTL")
	}
	in := Inventory{Harnesses: make([]Harness, len(e.Harnesses))}
	for i, h := range e.Harnesses {
		in.Harnesses[i] = Harness{ID: h.ID, Executable: h.Executable, PIDs: []int{}}
	}
	inspectProcesses(&in, o)
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00", key, e.At.Format(time.RFC3339Nano), ocli.StatFingerprint(e.Inputs...), selfStat())
	for _, hr := range in.Harnesses {
		fmt.Fprintf(h, "%s\x00%s\x00", hr.ID, hr.ProcessStatus)
	}
	for _, w := range in.Warnings {
		fmt.Fprintf(h, "%s\x00", w)
	}
	return hex.EncodeToString(h.Sum(nil)[:16]), nil
}

// selfStat is the stat of the running o-ai, so a rebuild (new rows, a new
// row shape) never answers unchanged from an older binary's answer.
func selfStat() string {
	exe, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	return ocli.StatFingerprint(exe)
}
