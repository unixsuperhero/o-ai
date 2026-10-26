package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type skillRow struct {
	Row
	Res Resource
}

// skillRows lists skills over home and returns them by name.
func skillRows(t *testing.T, home string, extra ...string) map[string][]skillRow {
	t.Helper()
	out, _, err := runCLI(t, append([]string{"skill", "list", "--home", home, "--project", home, "--no-probe", "--ttl", "0s"}, extra...)...)
	if err != nil {
		t.Fatal(err)
	}
	rows, data := rowsOf[Resource](t, out)
	byName := map[string][]skillRow{}
	for i, r := range rows {
		byName[data[i].Name] = append(byName[data[i].Name], skillRow{r, data[i]})
	}
	return byName
}

// The ai-tools catalog is scanned directly: a skill a harness sees through
// a link is one row (the harness's, naming the source), a skill no harness
// links is a row of its own; sources.json adds and removes roots by name.
func TestSkillSourcesAreScannedAndDeduplicated(t *testing.T) {
	home := isolatedHome(t)
	catalog := filepath.Join(home, "proj", "ai-tools", "catalog", "skills")
	put(t, filepath.Join(catalog, "linked", "SKILL.md"), "---\nname: linked\ndescription: seen through a link\n---\n")
	put(t, filepath.Join(catalog, "solo", "SKILL.md"), "---\nname: solo\ndescription: in the catalog only\n---\n")
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(catalog, "linked"), filepath.Join(home, ".claude", "skills", "linked")); err != nil {
		t.Fatal(err)
	}

	// claude and omp (Claude-user discovery) see the link; each is a row
	// naming the source, and the source has no row of its own.
	rows := skillRows(t, home)
	if got := rows["linked"]; len(got) != 2 || got[0].Label != "linked [claude]" || got[1].Label != "linked [omp]" || got[0].Res.Source != "ai-tools" || got[1].Res.Source != "ai-tools" {
		t.Fatalf("linked skill rows %+v", got)
	}
	solo := rows["solo"]
	if len(solo) != 1 || solo[0].ID != "skill/skill:ai-tools:"+filepath.Join(catalog, "solo", "SKILL.md") || solo[0].Label != "solo [ai-tools]" || solo[0].Res.Scope != "source" || len(solo[0].Res.Harnesses) != 0 {
		t.Fatalf("solo skill rows %+v", solo)
	}
	if _, ok := skillRows(t, home, "--harness", "claude")["solo"]; ok {
		t.Fatal("a source-only skill has no harness; --harness claude must drop it")
	}
	out, _, err := runCLI(t, "skill", "show", "--target", solo[0].ID, "--home", home, "--project", home, "--no-probe")
	if err != nil || !strings.Contains(out, `"source": "ai-tools"`) {
		t.Fatalf("show a source row: %v %s", err, out)
	}

	put(t, filepath.Join(home, "team", "review", "SKILL.md"), "---\nname: team-review\n---\n")
	put(t, sourcesPath(home), `{"skills": [{"name": "team", "path": "~/team"}, {"name": "ai-tools", "path": ""}]}`)
	rows = skillRows(t, home)
	if _, ok := rows["solo"]; ok {
		t.Fatalf("an empty path removes the built-in ai-tools: %+v", rows)
	}
	if got := rows["team-review"]; len(got) != 1 || got[0].Label != "team-review [team]" {
		t.Fatalf("team source rows %+v", got)
	}
	if got := rows["linked"]; len(got) != 2 || got[0].Res.Source != "" {
		t.Fatalf("with ai-tools removed the linked skill names no source: %+v", got)
	}

	put(t, sourcesPath(home), `{not json`)
	_, errOut, err := runCLI(t, "skill", "list", "--home", home, "--project", home, "--no-probe", "--ttl", "0s")
	if err != nil || !strings.Contains(errOut, "Cannot read o-ai sources") {
		t.Fatalf("a broken sources file warns and lists the built-ins: %v %q", err, errOut)
	}
}

// Editing sources.json changes the cache key: the next call collects even
// within the TTL.
func TestSourcesFileIsPartOfTheCacheKey(t *testing.T) {
	w := newCacheWorld(t)
	w.run("skill", "list")
	w.run("skill", "list")
	put(t, sourcesPath(w.home), `{"skills": [{"name": "team", "path": "~/team"}]}`)
	w.run("skill", "list")
	if len(w.needs) != 2 {
		t.Fatalf("collections %d, want 2 (one per sources file)", len(w.needs))
	}
}
