package main

import (
	"bytes"
	"context"
	"encoding/json"
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
	w.now = w.now.Add(29 * time.Second)
	w.run("skill", "list")
	w.now = w.now.Add(2 * time.Second)
	w.run("skill", "list")
	if len(w.needs) != 3 {
		t.Fatalf("an entry older than 30s must be recollected: %d collections", len(w.needs))
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
