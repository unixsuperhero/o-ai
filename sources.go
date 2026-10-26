package main

import (
	"os"
	"path/filepath"
	"sort"
)

// Skill sources are skill roots outside every harness's search path, such
// as the ai-tools catalog whose skills a harness sees only through links
// (~/.claude/skills/NAME -> ~/proj/ai-tools/catalog/skills/NAME). The
// built-ins are merged with ~/.config/o-ai/sources.json by name:
//
//	{"skills": [{"name": "team", "path": "~/team/skills"}, {"name": "ai-tools", "path": ""}]}
//
// adds the root team and removes the built-in ai-tools (an empty path).
// A source's skill that a harness also sees (the same resolved file) is
// one row: the harness's, with source naming the root. The rest are rows of
// their own, with no harness.

// Source is a named skill root.
type Source struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// builtinSkillSources are the roots scanned with no sources file.
var builtinSkillSources = []Source{{Name: "ai-tools", Path: "~/proj/ai-tools/catalog/skills"}}

func sourcesPath(home string) string { return filepath.Join(home, ".config", "o-ai", "sources.json") }

// skillSources are the built-ins merged with the sources file, by name,
// paths expanded against the home; the warning is set when the file exists
// and cannot be read.
func skillSources(o Options) ([]Source, string) {
	byName := map[string]string{}
	for _, s := range builtinSkillSources {
		byName[s.Name] = s.Path
	}
	warning := ""
	path := sourcesPath(o.Home)
	if cfg, err := readConfig(path); err == nil {
		for _, v := range array(cfg["skills"]) {
			s := object(v)
			if name := text(s, "name"); name != "" {
				byName[name] = text(s, "path")
			}
		}
	} else if !os.IsNotExist(err) {
		warning = "Cannot read o-ai sources " + path + ": " + safeParseError(err)
	}
	out := []Source{}
	for name, p := range byName {
		if p != "" {
			out = append(out, Source{Name: name, Path: expandPath(p, o.Home, o.Home)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, warning
}

// collectSources is every source's skills, each with no harness; a
// missing root is no rows.
func collectSources(o Options, need Need) ([]Resource, []string) {
	sources, warning := skillSources(o)
	o.inputs.add(sourcesPath(o.Home))
	var warnings []string
	if warning != "" {
		warnings = append(warnings, warning)
	}
	var out []Resource
	for _, s := range sources {
		c := collector{options: o, need: need, harness: Harness{}, settings: map[string]any{}}
		c.scanResources(resourceRoot{s.Path, "skill", "source", "o-ai skill source " + s.Name + "; in no harness search path"})
		for _, r := range c.inventory.Skills {
			r.Harnesses, r.Source = []string{}, s.Name
			out = append(out, r)
		}
		for _, w := range c.harness.Warnings {
			warnings = append(warnings, "source "+s.Name+": "+w)
		}
	}
	return out, warnings
}

// dedupeSources folds a source's skill into the harness rows that see the
// same file: they gain its source name and the source's own row goes.
func dedupeSources(skills []Resource) []Resource {
	source := map[string]string{} // resolved path -> source name
	for _, r := range skills {
		if r.Source != "" && len(r.Harnesses) == 0 && r.ResolvedPath != "" {
			source[r.ResolvedPath] = r.Source
		}
	}
	covered := map[string]bool{}
	out := make([]Resource, 0, len(skills))
	for _, r := range skills {
		if len(r.Harnesses) > 0 {
			if name, ok := source[r.ResolvedPath]; ok {
				r.Source, covered[r.ResolvedPath] = name, true
			}
		}
		out = append(out, r)
	}
	kept := out[:0]
	for _, r := range out {
		if len(r.Harnesses) == 0 && covered[r.ResolvedPath] {
			continue
		}
		kept = append(kept, r)
	}
	return kept
}
