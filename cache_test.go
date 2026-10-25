package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain keeps every test's inventory cache out of the user's.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "o-ai-cache-*")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_CACHE_HOME", dir)
	// Quota never reads the user's keychain or the network in a test.
	defaultKeychain = func(context.Context, string) ([]byte, error) { return nil, errors.New("no keychain in tests") }
	quotaTransport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("no network in tests") })
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// cacheWorld runs o-ai as separate processes would (a new Tool per call)
// over one cache directory, with a fake collection that counts its calls
// and the needs it was asked for.
type cacheWorld struct {
	t     *testing.T
	home  string
	cache string
	now   time.Time
	needs []Need
}

func newCacheWorld(t *testing.T) *cacheWorld {
	return &cacheWorld{t: t, home: isolatedHome(t), cache: t.TempDir(), now: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
}

func (w *cacheWorld) run(args ...string) string {
	w.t.Helper()
	var out, errOut bytes.Buffer
	tool := &Tool{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut, Env: append(os.Environ(), "XDG_CACHE_HOME="+w.cache), Home: w.home, Cwd: w.home, Prog: "o-ai",
		now: func() time.Time { return w.now },
		collect: func(o Options, need Need) Inventory {
			w.needs = append(w.needs, need)
			return Inventory{Project: o.Project, Skills: []Resource{{ID: "skill:claude:/s/SKILL.md", Kind: "skill", Name: "s", Path: "/s/SKILL.md", Harnesses: []string{"claude"}, Content: "body"}}}
		}}
	if err := tool.Main(context.Background(), args); err != nil {
		w.t.Fatalf("%v: %v (%s)", args, err, errOut.String())
	}
	return out.String()
}

func (w *cacheWorld) path() string { return filepath.Join(w.cache, "o-ai", "inventory.json") }

func TestCacheServesWithinTTLAndFreshRecollects(t *testing.T) {
	w := newCacheWorld(t)
	first := w.run("skill", "list")
	if second := w.run("skill", "list"); second != first || len(w.needs) != 1 {
		t.Fatalf("second call within the TTL collected (%d collections) or differs:\n%s\n%s", len(w.needs), first, second)
	}
	if out := w.run("skill", "show", "--target", "skill/skill:claude:/s/SKILL.md"); !strings.Contains(out, `"content": "body"`) || len(w.needs) != 1 {
		t.Fatalf("show from the cache: %d collections, %s", len(w.needs), out)
	}
	w.run("skill", "list", "--fresh")
	if len(w.needs) != 2 {
		t.Fatalf("--fresh: %d collections", len(w.needs))
	}
	w.now = w.now.Add(defaultTTL - time.Second)
	w.run("skill", "list")
	w.now = w.now.Add(2 * time.Second)
	w.run("skill", "list")
	if len(w.needs) != 3 {
		t.Fatalf("an entry older than the 5m TTL must be recollected: %d collections", len(w.needs))
	}
	w.now = w.now.Add(time.Minute)
	w.run("skill", "list", "--ttl", "2m")
	w.run("skill", "list", "--ttl", "0s")
	if len(w.needs) != 4 {
		t.Fatalf("--ttl 2m reuses, --ttl 0s collects: %d collections", len(w.needs))
	}
}

func TestCacheCollectsPerNounNeed(t *testing.T) {
	w := newCacheWorld(t)
	w.run("skill", "list")    // files only
	w.run("model", "list")    // needs the catalogs: a skill collection does not cover it
	w.run("skill", "list")    // a catalog collection covers files
	w.run("harness", "list")  // needs everything
	w.run("thinking", "list") // covered
	want := []Need{{}, catalogs, needAll}
	if len(w.needs) != len(want) {
		t.Fatalf("needs %+v, want %+v", w.needs, want)
	}
	for i := range want {
		if w.needs[i] != want[i] {
			t.Fatalf("needs %+v, want %+v", w.needs, want)
		}
	}
	var file cacheFile
	b, _ := os.ReadFile(w.path())
	if err := json.Unmarshal(b, &file); err != nil || len(file.Entries) != 1 || strings.Contains(string(b), `"pids":[1`) {
		t.Fatalf("one entry per key and no cached processes: %v %s", err, b)
	}
}

func TestCorruptCacheRecollects(t *testing.T) {
	w := newCacheWorld(t)
	put(t, w.path(), "{not json")
	if out := w.run("skill", "list"); !strings.Contains(out, "skill/skill:claude:/s/SKILL.md") || len(w.needs) != 1 {
		t.Fatalf("corrupt cache: %d collections, %s", len(w.needs), out)
	}
	w.run("skill", "list")
	if len(w.needs) != 1 {
		t.Fatalf("the rewritten cache is not read back: %d collections", len(w.needs))
	}
	file := readCache(w.path())
	if len(file.Entries) != 1 {
		t.Fatalf("entries: %d", len(file.Entries))
	}
	for k := range file.Entries {
		file.Entries[k] = json.RawMessage(`"x"`)
	}
	if err := writeCache(w.path(), file); err != nil {
		t.Fatal(err)
	}
	w.run("skill", "list")
	if len(w.needs) != 2 {
		t.Fatalf("a cache whose entries do not decode must be recollected: %d collections", len(w.needs))
	}
}

// The state verb answers every list in one collection: one inventory that
// covers every noun's need, rows by noun with ids and labels only.
func TestStateAnswersEveryListInOneCollection(t *testing.T) {
	w := newCacheWorld(t)
	state := map[string][]map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(w.run("status", "state")), "\n") {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("%v: %q", err, line)
		}
		noun, _ := row["noun"].(string)
		state[noun] = append(state[noun], row)
	}
	if len(w.needs) != 1 || w.needs[0] != needAll {
		t.Fatalf("collections %+v, want one of everything", w.needs)
	}
	// A noun with no records has no lines; the fake inventory has a skill
	// and the status counts.
	if len(state) != 2 || len(state["status"]) != 6 {
		t.Fatalf("state nouns %v", state)
	}
	if rows := state["skill"]; len(rows) != 1 || rows[0]["id"] != "skill/skill:claude:/s/SKILL.md" || rows[0]["label"] != "s [claude]" || rows[0]["data"] != nil {
		t.Fatalf("skill rows %+v", rows)
	}
	out := w.run("skill", "list", "--format", "jsonl") // covered by the state's collection
	if !strings.HasPrefix(out, `{"noun":"skill","id":"skill/skill:claude:/s/SKILL.md","label":"s [claude]","data":{`) || !strings.Contains(out, `"path":"/s/SKILL.md"`) || strings.Count(out, "\n") != 1 {
		t.Fatalf("list --format jsonl: %q", out)
	}
	w.run("status", "state", "--fresh")
	if len(w.needs) != 2 {
		t.Fatalf("list after state, then state --fresh: %d collections", len(w.needs))
	}
}
