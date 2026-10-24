package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogDeclaresRowsForEveryNoun(t *testing.T) {
	out, _, err := runCLI(t, "catalog")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Schema int    `json:"schema"`
		Name   string `json:"name"`
		Nouns  []struct {
			Name  string `json:"name"`
			Verbs []struct {
				Name string `json:"name"`
				Kind string `json:"kind"`
				Rows string `json:"rows"`
			} `json:"verbs"`
		} `json:"nouns"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Schema != 1 || doc.Name != "o-ai" {
		t.Fatalf("schema %d name %q", doc.Schema, doc.Name)
	}
	rows := map[string]string{}
	for _, n := range doc.Nouns {
		for _, v := range n.Verbs {
			if v.Name == "list" {
				rows[n.Name] = v.Rows
			}
		}
	}
	for _, noun := range nouns {
		if rows[noun] != noun {
			t.Errorf("%s list declares rows %q", noun, rows[noun])
		}
	}
}

func TestSkillRowsShowByTargetAndName(t *testing.T) {
	home := isolatedHome(t)
	put(t, filepath.Join(home, ".claude", "skills", "review", "SKILL.md"), "---\nname: review\ndescription: Claude's review.\n---\nclaude body\n")
	put(t, filepath.Join(home, ".omp", "agent", "skills", "review", "SKILL.md"), "---\nname: review\ndescription: OMP's review.\n---\nomp body\n")
	put(t, filepath.Join(home, ".claude", "skills", "broken", "SKILL.md"), "---\nname: [unclosed\n---\nbody\n")
	flags := []string{"--home", home, "--project", home, "--no-probe"}
	out, _, err := runCLI(t, append([]string{"skills"}, flags...)...)
	if err != nil {
		t.Fatal(err)
	}
	rows, data := rowsOf[Resource](t, out)
	var reviews []Row
	broken := false
	for i, r := range rows {
		if !strings.HasPrefix(r.ID, "skill/skill:") {
			t.Errorf("row id %q", r.ID)
		}
		if data[i].Name == "review" {
			reviews = append(reviews, r)
		}
		if strings.Contains(data[i].Path, "broken") {
			broken = true
		}
	}
	if len(reviews) < 2 || !broken {
		t.Fatalf("want both review copies and the broken skill listed: %s", out)
	}
	out, _, err = runCLI(t, append([]string{"skill", "show", "--target", reviews[1].ID}, flags...)...)
	if err != nil {
		t.Fatal(err)
	}
	var shown Resource
	if err := json.Unmarshal([]byte(out), &shown); err != nil {
		t.Fatal(err)
	}
	if "skill/"+shown.ID != reviews[1].ID || shown.Content == "" {
		t.Fatalf("show --target %s = %s", reviews[1].ID, out)
	}
	if _, _, err := runCLI(t, append([]string{"skill", "show", "review"}, flags...)...); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("a name with two copies must be ambiguous, got %v", err)
	}
	out, _, err = runCLI(t, append([]string{"sk", "sh", "review", "--harness", "claude"}, flags...)...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "claude body") {
		t.Fatalf("show by name: %s", out)
	}
}

func TestStatusRows(t *testing.T) {
	home := isolatedHome(t)
	put(t, filepath.Join(home, ".claude", "skills", "one", "SKILL.md"), "---\nname: one\ndescription: d\n---\n")
	out, _, err := runCLI(t, "status", "list", "--home", home, "--project", home, "--no-probe")
	if err != nil {
		t.Fatal(err)
	}
	rows, counts := rowsOf[StatusCount](t, out)
	if len(rows) != 6 || rows[3].ID != "status/skills" || counts[3].Count < 1 {
		t.Fatalf("status rows: %s", out)
	}
}
