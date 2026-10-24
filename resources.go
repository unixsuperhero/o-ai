package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type resourceRoot struct{ path, kind, scope, access string }

func (c *collector) discoverResources() {
	h := c.harness.ID
	if !c.harness.Installed && !c.harness.Configured {
		return
	}
	var roots []resourceRoot
	add := func(root, scope, access string) {
		roots = append(roots, resourceRoot{filepath.Join(root, "skills"), "skill", scope, access}, resourceRoot{filepath.Join(root, "agents"), "agent", scope, access})
	}
	access := "discovered in harness search path; session loading not verified"
	add(c.root, "user", access)
	for _, dir := range projectAncestors(c.options.Project, c.options.Home) {
		add(filepath.Join(dir, "."+h), "project", access)
	}
	switch h {
	case "omp", "codex", "pi", "opencode":
		add(filepath.Join(c.options.Home, ".agents"), "shared user", access)
		for _, dir := range projectAncestors(c.options.Project, c.options.Home) {
			add(filepath.Join(dir, ".agents"), "shared project", access)
		}
	}
	if h == "omp" {
		add(filepath.Join(c.options.Home, ".agent"), "shared user", access)
		for _, dir := range projectAncestors(c.options.Project, c.options.Home) {
			add(filepath.Join(dir, ".agent"), "shared project", access)
		}
		claudeAccess := "discovered cross-harness source; requires Claude-user discovery opt-in"
		if object(c.settings["skills"])["enableClaudeUser"] == true {
			claudeAccess = access
		}
		roots = append(roots, resourceRoot{filepath.Join(c.options.Home, ".claude", "skills"), "skill", "Claude user", claudeAccess})
		for _, dir := range projectAncestors(c.options.Project, c.options.Home) {
			roots = append(roots, resourceRoot{filepath.Join(dir, ".claude", "skills"), "skill", "Claude project", access})
		}
		for _, dir := range stringList(object(c.settings["skills"])["customDirectories"]) {
			roots = append(roots, resourceRoot{expandPath(dir, c.options.Home, c.options.Project), "skill", "configured", access})
		}
		if c.harness.Installed && !c.options.NoProbe {
			c.ompBundledAgents()
		} else if executable, err := filepath.EvalSymlinks(c.harness.Executable); err == nil {
			root := filepath.Dir(filepath.Dir(executable))
			roots = append(roots, resourceRoot{filepath.Join(root, "src", "prompts", "agents"), "agent", "bundled", "bundled OMP agent definition"})
		}
	}
	if h == "pi" {
		for _, dir := range stringList(c.settings["skills"]) {
			roots = append(roots, resourceRoot{expandPath(dir, c.options.Home, c.options.Project), "skill", "configured", access})
		}
	}
	if h == "opencode" {
		roots = append(roots, resourceRoot{filepath.Join(c.root, "agent"), "agent", "user", access})
		for _, dir := range projectAncestors(c.options.Project, c.options.Home) {
			roots = append(roots, resourceRoot{filepath.Join(dir, ".opencode", "agent"), "agent", "project", access}, resourceRoot{filepath.Join(dir, ".claude", "skills"), "skill", "Claude project", access})
		}
		for _, dir := range stringList(object(c.settings["skills"])["paths"]) {
			roots = append(roots, resourceRoot{expandPath(dir, c.options.Home, c.options.Project), "skill", "configured", access})
		}
	}
	if h == "claude" {
		c.claudePluginRoots(&roots)
	}
	for _, root := range roots {
		if root.kind == "skill" && object(c.settings["skills"])["enabled"] == false {
			root.access = "disabled by skills.enabled=false"
		}
		c.scanResources(root)
	}
	if h == "codex" {
		for name, v := range object(c.settings["agents"]) {
			meta := object(v)
			if meta == nil {
				continue
			}
			path := text(meta, "config_file")
			if path != "" {
				path = expandPath(path, c.options.Home, c.root)
				if cfg, err := readConfig(path); err == nil {
					meta = map[string]any{"registration": meta, "config": cfg}
				} else {
					c.warn("Cannot read Codex agent configuration " + path)
				}
			} else {
				path = filepath.Join(c.root, "config.toml") + "#agents." + name
			}
			c.inventory.Agents = append(c.inventory.Agents, Resource{ID: "agent:codex:" + name, Kind: "agent", Name: name, Path: path, Harnesses: []string{h}, Scope: "configured", Access: "configured agent", Metadata: object(redact(meta, ""))})
		}
	}
	if h == "opencode" {
		for name, v := range object(c.settings["agent"]) {
			meta := object(v)
			c.inventory.Agents = append(c.inventory.Agents, Resource{ID: "agent:opencode:" + name, Kind: "agent", Name: name, Description: text(meta, "description"), Path: c.root + "/opencode.json#agent." + name, Harnesses: []string{h}, Scope: "configured", Access: "configured agent", Metadata: object(redact(meta, ""))})
		}
	}
}

func projectAncestors(project, home string) []string {
	var out []string
	for dir := project; dir != home && dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		out = append(out, dir)
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			break
		}
	}
	return out
}

func expandPath(path, home, base string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(base, path)
}

func (c *collector) claudePluginRoots(roots *[]resourceRoot) {
	manifest := filepath.Join(c.root, "plugins", "installed_plugins.json")
	cfg, err := readConfig(manifest)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		c.warn("Cannot read installed Claude plugins")
		return
	}
	for name, records := range object(cfg["plugins"]) {
		for _, v := range array(records) {
			record := object(v)
			root := text(record, "installPath")
			if root == "" {
				continue
			}
			scope := text(record, "scope")
			project := text(record, "projectPath")
			if project != "" && project != c.options.Project && !strings.HasPrefix(c.options.Project, project+string(filepath.Separator)) {
				continue
			}
			access := "installed plugin; enablement not verified"
			if enabled, ok := object(c.settings["enabledPlugins"])[name].(bool); ok {
				if enabled {
					access = "enabled plugin; session loading not verified"
				} else {
					access = "disabled plugin"
				}
			}
			*roots = append(*roots, resourceRoot{filepath.Join(root, "skills"), "skill", "plugin " + scope, access}, resourceRoot{filepath.Join(root, "agents"), "agent", "plugin " + scope, access})
		}
	}
}

func (c *collector) scanResources(root resourceRoot) {
	seen := map[string]bool{}
	var visit func(string, int)
	visit = func(path string, depth int) {
		if depth > 16 {
			c.warn("Resource discovery depth exceeded at " + path)
			return
		}
		resolved, err := filepath.EvalSymlinks(path)
		if os.IsNotExist(err) {
			return
		}
		if err != nil {
			c.warn("Cannot resolve resource " + path)
			return
		}
		if seen[resolved] {
			return
		}
		seen[resolved] = true
		info, err := os.Stat(resolved)
		if err != nil {
			return
		}
		if info.IsDir() {
			entries, err := os.ReadDir(path)
			if err != nil {
				c.warn("Cannot read resource directory " + path)
				return
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".") {
					continue
				}
				visit(filepath.Join(path, entry.Name()), depth+1)
			}
			return
		}
		name := filepath.Base(path)
		if root.kind == "skill" && name != "SKILL.md" {
			return
		}
		if root.kind == "agent" && filepath.Ext(path) != ".md" {
			return
		}
		b, err := readBounded(path)
		if err != nil {
			c.warn("Cannot read resource " + path)
			return
		}
		meta, body, err := frontmatter(b)
		resourceAccess := root.access
		if err != nil {
			c.warn("Invalid resource frontmatter at " + path)
			meta = map[string]any{"parseError": "invalid YAML frontmatter"}
			body = string(b)
			resourceAccess = "invalid frontmatter; source discovered, loading not verified"
		}
		resourceName := text(meta, "name")
		if root.scope == "bundled" && resourceName == "" {
			return
		}
		if resourceName == "" {
			resourceName = strings.TrimSuffix(name, ".md")
			if root.kind == "skill" {
				resourceName = filepath.Base(filepath.Dir(path))
			}
		}
		r := Resource{ID: root.kind + ":" + path, Kind: root.kind, Name: resourceName, Description: text(meta, "description"), Path: path, ResolvedPath: resolved, Harnesses: []string{c.harness.ID}, Scope: root.scope, Access: resourceAccess, Metadata: object(redact(meta, "")), Content: redact(body, "").(string)}
		if root.kind == "skill" {
			c.inventory.Skills = append(c.inventory.Skills, r)
		} else {
			c.inventory.Agents = append(c.inventory.Agents, r)
		}
	}
	visit(root.path, 0)
}

func frontmatter(b []byte) (map[string]any, string, error) {
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(b, []byte("---\n")) {
		return map[string]any{}, string(b), nil
	}
	offset := 4
	for _, line := range bytes.SplitAfter(b[4:], []byte("\n")) {
		if bytes.Equal(bytes.TrimSuffix(line, []byte("\n")), []byte("---")) {
			var meta map[string]any
			if err := yaml.Unmarshal(b[4:offset], &meta); err != nil {
				return nil, "", err
			}
			if meta == nil {
				meta = map[string]any{}
			}
			return meta, strings.TrimSpace(string(b[offset+len(line):])), nil
		}
		offset += len(line)
	}
	return nil, "", fmt.Errorf("unterminated frontmatter")
}

func mergeResources(resources []Resource) []Resource {
	out := []Resource{}
	byPath := map[string]int{}
	for _, r := range resources {
		// Different enablement states must not be flattened into a false access claim.
		key := r.Kind + ":" + r.Path + ":" + r.Access
		if i, ok := byPath[key]; ok {
			for _, h := range r.Harnesses {
				if !contains(out[i].Harnesses, h) {
					out[i].Harnesses = append(out[i].Harnesses, h)
				}
			}
			sort.Strings(out[i].Harnesses)
		} else {
			byPath[key] = len(out)
			out = append(out, r)
		}
	}
	for i := range out {
		out[i].ID = out[i].Kind + ":" + strings.Join(out[i].Harnesses, ",") + ":" + out[i].Path
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (c *collector) ompBundledAgents() {
	dir, err := os.MkdirTemp("", "aiview-omp-agents-*")
	if err != nil {
		c.warn("Cannot create temporary directory for bundled agent metadata")
		return
	}
	defer os.RemoveAll(dir)
	if _, err := runCommand(c.options, c.harness.Executable, []string{"agents", "unpack", "--dir", dir, "--json"}, ""); err != nil {
		c.warn(err.Error())
		return
	}
	start := len(c.inventory.Agents)
	c.scanResources(resourceRoot{dir, "agent", "bundled", "reported by OMP bundled agent exporter"})
	for i := start; i < len(c.inventory.Agents); i++ {
		r := &c.inventory.Agents[i]
		r.Path = c.harness.Executable + "#bundled/" + filepath.Base(r.Path)
		r.ResolvedPath = ""
	}
}
