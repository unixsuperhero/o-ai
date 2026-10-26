package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
)

// Every verb o-ai defines declares its history kind (Operation.HistoryKind)
// rather than leaving it to the default, and the catalog's history is that
// kind. A new verb without one fails here.
func TestEveryVerbDeclaresItsHistory(t *testing.T) {
	out, _, err := runCLI(t, "catalog")
	if err != nil {
		t.Fatal(err)
	}
	var doc ocli.CatalogDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	// The framework's verbs, less settings.*: shell drops those, and the
	// settings noun is the inventory kind's.
	framework := ochazuke.NewRuntime().Operations
	for key, op := range framework {
		if op.Noun == "settings" {
			delete(framework, key)
		}
	}
	tool := &Tool{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard, Env: os.Environ(), Prog: "o-ai"}
	ops := tool.shell().Runtime.Operations
	n := 0
	for _, noun := range doc.Nouns {
		for _, v := range noun.Verbs {
			key := noun.Name + "." + v.Name
			if _, ok := framework[key]; ok {
				continue
			}
			n++
			if v.History == "" || ops[key].HistoryKind == "" || v.History != ops[key].HistoryKind {
				t.Errorf("catalog verb %s: history %q, declared %q", key, v.History, ops[key].HistoryKind)
			}
		}
	}
	if n < len(categories)*2+5 {
		t.Fatalf("the catalog has %d o-ai verbs, want every noun's list and show and more", n)
	}
}

// A spot check of the kinds: every read is a fetch; model select writes the
// selection file, a change.
func TestVerbHistoryKinds(t *testing.T) {
	tool := &Tool{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard, Env: os.Environ(), Prog: "o-ai"}
	ops := tool.shell().Runtime.Operations
	for key, want := range map[string]string{
		"skill.list": ochazuke.HistoryFetch, "model.show": ochazuke.HistoryFetch, "status.state": ochazuke.HistoryFetch,
		"status.show": ochazuke.HistoryFetch, "settings.list": ochazuke.HistoryFetch, "quota.show": ochazuke.HistoryFetch, "mcp.tools": ochazuke.HistoryFetch,
		"model.select": ochazuke.HistoryChange,
	} {
		if got := ops[key].HistoryKind; got != want {
			t.Errorf("%s: %q, want %q", key, got, want)
		}
	}
}
