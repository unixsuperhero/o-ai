package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// The inventory cache: one collection serves every verb run within the TTL,
// so a host listing each noun (nabe, at mount and refresh) collects once.
// $XDG_CACHE_HOME/o-ai/inventory.json (else ~/.cache/o-ai) holds one entry
// per cache key:
//
//	{schema: 1, entries: {KEY: {at, need, inventory}}}
//
// KEY is the options and environment a collection depends on (home,
// project, timeout, no-probe, the harness environment variables, the skill
// sources after sources.json); the
// filters (--harness, --provider, --model, --search) apply after
// collection, so they share an entry. An entry serves a verb when it is
// younger than the TTL and its need covers the verb's; processes are never
// cached but inspected per call. Writers hold inventory.json.lock, so
// concurrent callers wait for one collection instead of each running it.

// defaultTTL is how old a cached inventory may be without --ttl. A host
// that wants a newer one asks with --fresh (nabe's explicit refresh).
const defaultTTL = 5 * time.Minute

const cacheSchema = 2 // 2: MCP servers and tools

// cacheEnv are the environment variables the collectors read.
var cacheEnv = []string{"PATH", "XDG_CONFIG_HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR", "OMP_PROFILE", "OPENCODE_CONFIG", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY"}

type cacheEntry struct {
	At        time.Time `json:"at"`
	Need      Need      `json:"need"`
	Inventory Inventory `json:"inventory"`
}

type cacheFile struct {
	Schema  int                        `json:"schema"`
	Entries map[string]json.RawMessage `json:"entries"`
}

// cacheKey is the collection's identity: what changes the inventory.
func cacheKey(o Options) string {
	sources, _ := skillSources(o) // a sources.json edit is a new collection
	env := make([]string, len(cacheEnv))
	for i, k := range cacheEnv {
		env[i] = k + "=" + os.Getenv(k)
	}
	b, _ := json.Marshal(struct {
		Home, Project string
		Timeout       time.Duration
		NoProbe       bool
		Env           []string
		Sources       []Source
	}{o.Home, o.Project, o.Timeout, o.NoProbe, env, sources})
	return string(b)
}

// cachePath is the cache file: under XDG_CACHE_HOME from the tool's
// environment, else the tool's home.
func (t *Tool) cachePath() string {
	dir := filepath.Join(t.Home, ".cache")
	for _, kv := range t.Env {
		if v, ok := strings.CutPrefix(kv, "XDG_CACHE_HOME="); ok && filepath.IsAbs(v) {
			dir = v
		}
	}
	return filepath.Join(dir, "o-ai", "inventory.json")
}

// cachedInventory is the inventory for key and need: the cache file's
// entry when it is young enough and covers need, else a collection, which
// it writes back. The cache never makes a verb fail: a missing, corrupt or
// unwritable file means collecting.
func (t *Tool) cachedInventory(o Options, key string, need Need) Inventory {
	path := t.cachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err == nil {
		if lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600); err == nil {
			defer lock.Close()
			if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX) == nil {
				defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
			}
		}
	}
	now := t.clock()
	file := readCache(path)
	if !o.Fresh && o.TTL > 0 {
		if raw, ok := file.Entries[key]; ok {
			var e cacheEntry
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber() // metadata numbers print as collected
			if dec.Decode(&e) == nil && e.Need.covers(need) && !now.Before(e.At) && now.Sub(e.At) < o.TTL {
				return e.Inventory
			}
		}
	}
	collect := t.collect
	if collect == nil {
		collect = CollectNeed
	}
	in := collect(o, need)
	raw, err := json.Marshal(cacheEntry{At: now, Need: need, Inventory: in})
	if err == nil {
		for k, old := range file.Entries {
			var e struct{ At time.Time }
			if json.Unmarshal(old, &e) != nil || now.Sub(e.At) >= max(o.TTL, defaultTTL) || now.Before(e.At) {
				delete(file.Entries, k)
			}
		}
		file.Entries[key] = raw
		err = writeCache(path, file)
	}
	if err != nil {
		fmt.Fprintln(t.Stderr, "warning: inventory cache not written:", err)
	}
	return in
}

// readCache is the cache file, empty when it is missing, corrupt or of
// another schema.
func readCache(path string) cacheFile {
	var file cacheFile
	if b, err := os.ReadFile(path); err != nil || json.Unmarshal(b, &file) != nil || file.Schema != cacheSchema {
		file = cacheFile{}
	}
	if file.Entries == nil {
		file.Entries = map[string]json.RawMessage{}
	}
	file.Schema = cacheSchema
	return file
}

// writeCache replaces the cache file atomically: a temporary file in the
// same directory, renamed over it.
func writeCache(path string, file cacheFile) error {
	b, err := json.Marshal(file)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".inventory-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(b)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func (t *Tool) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}
