package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ofam.dev/ochazuke"
)

// stateWorld runs o-ai's real collection (no probes) over a temporary home
// and cache, a new Tool per call as separate processes would.
type stateWorld struct {
	t           *testing.T
	home, cache string
	now         time.Time
	collections int
}

func newStateWorld(t *testing.T) *stateWorld {
	w := &stateWorld{t: t, home: isolatedHome(t), cache: t.TempDir(), now: time.Now()}
	writeSkill(t, filepath.Join(w.home, ".claude", "skills", "first", "SKILL.md"), "first")
	return w
}

func writeSkill(t *testing.T, path, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: "+name+"\ndescription: d\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (w *stateWorld) tool(out *bytes.Buffer) *Tool {
	return &Tool{Stdin: strings.NewReader(""), Stdout: out, Stderr: &bytes.Buffer{}, Env: append(os.Environ(), "XDG_CACHE_HOME="+w.cache), Home: w.home, Cwd: w.home, Prog: "o-ai",
		now:     func() time.Time { return w.now },
		collect: func(o Options, need Need) Inventory { w.collections++; return CollectNeed(o, need) }}
}

func (w *stateWorld) fingerprint() (string, error) {
	return w.tool(&bytes.Buffer{}).stateFingerprint(ochazuke.Arguments{"no-probe": true})
}

// state runs the state verb with since (nil: the first call) and returns
// the cache id, each list's answer and hash, and the skill row ids.
func (w *stateWorld) state(cache string, since map[string]string) (string, map[string]string, map[string]string, []string) {
	w.t.Helper()
	args := []string{"status", "state", "--json", `{"no-probe": true}`}
	if since == nil {
		args = append(args, "--since=")
	} else {
		var parts []string
		for k, v := range since {
			parts = append(parts, k+"="+v)
		}
		args = append(args, "--since-cache", cache, "--since", strings.Join(parts, ","))
	}
	var out bytes.Buffer
	if err := w.tool(&out).Main(context.Background(), args); err != nil {
		w.t.Fatal(err)
	}
	answers, hashes := map[string]string{}, map[string]string{}
	var skills []string
	sc := bufio.NewScanner(&out)
	sc.Buffer(nil, 1<<24)
	for sc.Scan() {
		var line struct {
			Cache, List, Answer, Hash, Noun, ID string
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			w.t.Fatalf("%v: %s", err, sc.Text())
		}
		switch {
		case line.Cache != "":
			cache = line.Cache
		case line.List != "":
			answers[line.List], hashes[line.List] = line.Answer, line.Hash
		case line.Noun == "skill":
			skills = append(skills, line.ID)
		}
	}
	return cache, answers, hashes, skills
}

func allUnchanged(answers map[string]string) bool {
	for _, a := range answers {
		if a != "unchanged" {
			return false
		}
	}
	return len(answers) > 0
}

// TestStateFingerprintFollowsInputs pins the fingerprint's inputs: a new
// skill deep in a tree, an environment variable a collector reads and the
// TTL each change it or refuse it, and an unchanged poll answers every list
// unchanged.
func TestStateFingerprintFollowsInputs(t *testing.T) {
	w := newStateWorld(t)
	if _, err := w.fingerprint(); err == nil {
		t.Fatal("a fingerprint with no recorded collection")
	}
	cache, _, hashes, skills := w.state("", nil)
	if len(skills) == 0 || w.collections != 1 {
		t.Fatalf("first state: skills %v, %d collections", skills, w.collections)
	}
	fp, err := w.fingerprint()
	if err != nil || fp == "" {
		t.Fatalf("fingerprint after a collection: %q %v", fp, err)
	}
	if again, _ := w.fingerprint(); again != fp {
		t.Fatalf("fingerprint of unchanged inputs moved: %s, %s", fp, again)
	}
	// The second poll records the fingerprint (the first had none); the
	// third answers from it.
	for i := 0; i < 2; i++ {
		var answers map[string]string
		cache, answers, _, _ = w.state(cache, hashes)
		if !allUnchanged(answers) {
			t.Fatalf("unchanged poll %d: %v", i, answers)
		}
	}
	if w.collections != 1 {
		t.Fatalf("unchanged polls collected: %d", w.collections)
	}

	// A skill added two directories below a skills root.
	writeSkill(t, filepath.Join(w.home, ".claude", "skills", "group", "second", "SKILL.md"), "second")
	if moved, _ := w.fingerprint(); moved == fp {
		t.Fatal("a new skill deep in a tree left the fingerprint")
	}
	cache, answers, hashes, _ := w.state(cache, hashes)
	if answers["skill"] != "delta" || w.collections != 2 {
		t.Fatalf("after a new skill: %v, %d collections", answers, w.collections)
	}
	if _, _, _, after := w.state("", nil); len(after) <= len(skills) {
		t.Fatalf("skills after the add: %v", after)
	}
	fp, _ = w.fingerprint()

	// An environment variable in cacheEnv is another collection.
	t.Setenv("CODEX_HOME", filepath.Join(w.home, "elsewhere"))
	if other, err := w.fingerprint(); err == nil && other == fp {
		t.Fatal("CODEX_HOME changed and the fingerprint did not")
	}
	t.Setenv("CODEX_HOME", "")

	// A collection as old as the TTL refuses the shortcut: the verb
	// collects again.
	w.now = w.now.Add(defaultTTL)
	if _, err := w.fingerprint(); err == nil {
		t.Fatal("a fingerprint past the TTL")
	}
	_, answers, _, _ = w.state(cache, hashes)
	if w.collections != 3 || !allUnchanged(answers) {
		t.Fatalf("past the TTL: %d collections, %v", w.collections, answers)
	}
}

// TestInventoryCacheRecollectsWhenInputsChange: a settings edit within the
// TTL is a new collection, not the cached inventory.
func TestInventoryCacheRecollectsWhenInputsChange(t *testing.T) {
	w := newStateWorld(t)
	w.state("", nil)
	w.state("", nil)
	if w.collections != 1 {
		t.Fatalf("unchanged inputs within the TTL: %d collections", w.collections)
	}
	settings := filepath.Join(w.home, ".claude", "settings.json")
	if err := os.WriteFile(settings, []byte(`{"model": "x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	w.state("", nil)
	if w.collections != 2 {
		t.Fatalf("a settings file appeared: %d collections", w.collections)
	}
}
