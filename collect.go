package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/pelletier/go-toml/v2"
	"github.com/tailscale/hujson"
	"gopkg.in/yaml.v3"
)

const maxInput = 32 << 20

type collector struct {
	options   Options
	inventory Inventory
	harness   Harness
	root      string
	settings  map[string]any
}

func Collect(o Options) Inventory {
	in := Inventory{CapturedAt: time.Now().UTC(), Project: o.Project}
	ids := []string{"omp", "claude", "codex", "pi", "opencode", "gemini", "clawed"}
	results := make([]Inventory, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id string) { defer wg.Done(); results[i] = collectHarness(o, id) }(i, id)
	}
	wg.Wait()
	for _, result := range results {
		in.Harnesses = append(in.Harnesses, result.Harnesses...)
		in.Models = append(in.Models, result.Models...)
		in.Providers = append(in.Providers, result.Providers...)
		in.Skills = append(in.Skills, result.Skills...)
		in.Agents = append(in.Agents, result.Agents...)
		in.Warnings = append(in.Warnings, result.Warnings...)
	}
	sort.Slice(in.Models, func(i, j int) bool { return in.Models[i].ID < in.Models[j].ID })
	in.Skills = mergeResources(in.Skills)
	in.Agents = mergeResources(in.Agents)
	inspectProcesses(&in, o)
	return in
}

func collectHarness(o Options, id string) Inventory {
	c := collector{options: o, harness: Harness{ID: id, PIDs: []int{}, Settings: []Setting{}, CatalogStatus: "unavailable", ProcessStatus: "unknown"}, settings: map[string]any{}}
	exe, err := exec.LookPath(id)
	if err == nil {
		c.harness.Installed = true
		c.harness.Executable = exe
	}
	c.root = filepath.Join(o.Home, "."+id)
	switch id {
	case "omp":
		c.root = filepath.Join(c.root, "agent")
	case "pi":
		c.root = filepath.Join(c.root, "agent")
	case "opencode":
		c.root = filepath.Join(envDefault("XDG_CONFIG_HOME", filepath.Join(o.Home, ".config")), "opencode")
	case "claude":
		c.root = envDefault("CLAUDE_CONFIG_DIR", c.root)
	case "codex":
		c.root = envDefault("CODEX_HOME", c.root)
	}
	if id == "omp" || id == "pi" {
		c.root = envDefault("PI_CODING_AGENT_DIR", c.root)
	}
	_, err = os.Stat(c.root)
	c.harness.Configured = err == nil
	paths := []struct{ path, scope string }{}
	seenPaths := map[string]bool{}
	add := func(path, scope string) {
		path = filepath.Clean(path)
		if !seenPaths[path] {
			seenPaths[path] = true
			paths = append(paths, struct{ path, scope string }{path, scope})
		}
	}
	switch id {
	case "omp":
		add(filepath.Join(c.root, "config.yml"), "user")
		add(filepath.Join(o.Project, ".omp", "config.yml"), "project")
		if os.Getenv("OMP_PROFILE") != "" {
			c.warn("OMP_PROFILE is set: native catalog follows it; settings paths may require PI_CODING_AGENT_DIR")
		}
	case "pi":
		add(filepath.Join(c.root, "settings.json"), "user")
		add(filepath.Join(c.root, "models.json"), "user")
		add(filepath.Join(o.Project, ".pi", "settings.json"), "project")
	case "codex":
		add(filepath.Join(c.root, "config.toml"), "user")
		add(filepath.Join(o.Project, ".codex", "config.toml"), "project (trust-dependent)")
	case "claude", "clawed":
		add(filepath.Join(c.root, "settings.json"), "user")
		add(filepath.Join(o.Project, "."+id, "settings.json"), "project")
		add(filepath.Join(o.Project, "."+id, "settings.local.json"), "project-local")
	case "opencode":
		for _, name := range []string{"opencode.json", "opencode.jsonc"} {
			add(filepath.Join(c.root, name), "user")
			add(filepath.Join(o.Project, name), "project")
		}
		if p := os.Getenv("OPENCODE_CONFIG"); p != "" {
			add(p, "environment override")
		}
	case "gemini":
		add(filepath.Join(c.root, "settings.json"), "user")
		add(filepath.Join(o.Project, ".gemini", "settings.json"), "project")
	}
	for _, p := range paths {
		settings, err := readConfig(p.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			c.warn("Cannot parse settings " + p.path + ": " + safeParseError(err))
			c.harness.Settings = append(c.harness.Settings, Setting{Path: p.path, Scope: p.scope, Error: safeParseError(err)})
			continue
		}
		c.harness.Configured = true
		mergeMap(c.settings, settings)
		c.harness.Settings = append(c.harness.Settings, Setting{Path: p.path, Scope: p.scope, Values: object(redact(settings, ""))})
	}
	if c.harness.Installed && !o.NoProbe {
		switch id {
		case "omp":
			c.ompModels()
		case "claude", "clawed":
			c.claudeModels()
		case "pi":
			c.piModels()
		case "opencode":
			c.openCodeModels()
		}
	}
	if id == "codex" {
		c.codexModels()
	}
	c.configuredModels()
	c.discoverResources()
	c.deriveProviders()
	c.inventory.Harnesses = []Harness{c.harness}
	return c.inventory
}

func envDefault(key, fallback string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}
	return fallback
}
func (c *collector) warn(message string) { c.harness.Warnings = append(c.harness.Warnings, message) }

// Never include parser excerpts: malformed configuration may contain credentials.
func safeParseError(err error) string {
	if os.IsPermission(err) {
		return "permission denied"
	}
	return "invalid or unreadable configuration"
}

func readConfig(path string) (map[string]any, error) {
	b, err := readBounded(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	switch strings.ToLower(filepath.Ext(path)) {
	case ".toml":
		err = toml.Unmarshal(b, &m)
	case ".yaml", ".yml":
		err = yaml.Unmarshal(b, &m)
	default:
		b, err = hujson.Standardize(b)
		if err == nil {
			err = json.Unmarshal(b, &m)
		}
	}
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("expected object")
	}
	return m, nil
}

func readBounded(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxInput+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxInput {
		return nil, fmt.Errorf("file exceeds %d bytes", maxInput)
	}
	return b, nil
}

func mergeMap(dst, src map[string]any) {
	for k, v := range src {
		if dm, sm := object(dst[k]), object(v); dm != nil && sm != nil {
			mergeMap(dm, sm)
		} else {
			dst[k] = v
		}
	}
}

var sensitiveKey = regexp.MustCompile(`(?i)(api.?key|access.?key|private.?key|secret|password|passwd|credential|authorization|cookie|headers|environment|token$|(^|[_-])env($|[_-])|^(key|auth|hooks|command|commands|args|arguments|notify|apikeyhelper)$)`)
var secretValue = regexp.MustCompile(`(?i)(sk-[a-z0-9_-]{8,}|gh[pousr]_[a-z0-9_]{8,}|Bearer\s+[^\s"']+|eyJ[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+)`)

func redact(v any, key string) any {
	if sensitiveKey.MatchString(key) {
		return "[REDACTED]"
	}
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, value := range x {
			out[k] = redact(value, k)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, value := range x {
			out[i] = redact(value, "")
		}
		return out
	case string:
		s := secretValue.ReplaceAllString(x, "[REDACTED]")
		if u, err := url.Parse(s); err == nil && u.Scheme != "" && u.Host != "" {
			u.User = nil
			u.RawQuery = ""
			u.Fragment = ""
			s = u.String()
		}
		return strings.Map(func(r rune) rune {
			if unicode.IsControl(r) && r != '\n' && r != '\t' {
				return -1
			}
			return r
		}, s)
	default:
		return v
	}
}

type cappedBuffer struct{ bytes.Buffer }

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > maxInput {
		return 0, fmt.Errorf("command output exceeds limit")
	}
	return b.Buffer.Write(p)
}

func runCommand(o Options, name string, args []string, input string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), o.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = o.Project
	cmd.WaitDelay = time.Second
	cmd.Stdin = strings.NewReader(input)
	var out cappedBuffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	cmd.Env = append(os.Environ(), "HOME="+o.Home, "NO_COLOR=1")
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("metadata command timed out after %s", o.Timeout)
	}
	if err != nil {
		return nil, fmt.Errorf("metadata command failed (%s)", filepath.Base(name))
	}
	return out.Bytes(), nil
}

func inspectProcesses(in *Inventory, o Options) {
	b, err := runCommand(o, "ps", []string{"-axo", "pid=,args="}, "")
	if err != nil {
		in.Warnings = append(in.Warnings, "Process status unavailable")
		return
	}
	for i := range in.Harnesses {
		h := &in.Harnesses[i]
		h.ProcessStatus = "no matching process observed"
		paths := []string{h.ID, h.Executable}
		if resolved, err := filepath.EvalSymlinks(h.Executable); err == nil {
			paths = append(paths, resolved)
		}
		for _, line := range strings.Split(string(b), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			command := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), fields[0]))
			if !processMatches(command, paths) {
				continue
			}
			pid, err := strconv.Atoi(fields[0])
			if err == nil && pid != os.Getpid() {
				h.PIDs = append(h.PIDs, pid)
			}
		}
		if len(h.PIDs) > 0 {
			h.ProcessStatus = fmt.Sprintf("%d running processes (including helpers)", len(h.PIDs))
		}
	}
}

func processMatches(command string, paths []string) bool {
	matches := func(value string) bool {
		for _, path := range paths {
			if path != "" && (value == path || strings.HasPrefix(value, path+" ")) {
				return true
			}
		}
		return false
	}
	if matches(command) {
		return true
	}
	executable, rest, ok := strings.Cut(command, " ")
	if len(paths) > 0 && filepath.Base(executable) == paths[0] {
		return true
	}
	if !ok {
		return false
	}
	switch filepath.Base(executable) {
	case "node", "bun", "deno":
		return matches(strings.TrimSpace(rest))
	}
	return false
}

func (c *collector) addModel(provider, slug, name, cli, source string, raw map[string]any, levels []string, status string) {
	if slug == "" || provider == "" {
		return
	}
	h := c.harness.ID
	id := h + ":" + provider + "/" + slug
	for _, m := range c.inventory.Models {
		if m.ID == id {
			return
		}
	}
	args := []string{h, "--model", cli}
	if h == "codex" {
		args = append(args, "-c", `model_provider="`+provider+`"`)
	}
	if kind := text(raw, "kind"); h == "omp" && kind != "" && kind != "chat" {
		args = []string{}
		status = "not a chat model; selector is a catalog reference, not an omp --model argument"
	}
	thinking := make([]Thinking, 0, len(levels))
	for _, level := range levels {
		if level == "" {
			c.warn("Catalog contains an empty thinking level for " + slug)
			continue
		}
		flag := []string{"--thinking", level}
		switch h {
		case "claude", "clawed":
			flag = []string{"--effort", level}
		case "codex":
			flag = []string{"-c", `model_reasoning_effort="` + level + `"`}
		case "opencode":
			flag = []string{"--variant", level}
		}
		label := strings.ToUpper(level[:1]) + level[1:]
		switch level {
		case "xhigh":
			label = "Extra high"
		case "max":
			label = "Maximum"
		case "auto":
			label = "Automatic"
		}
		thinking = append(thinking, Thinking{Slug: level, Name: label, Args: flag})
	}
	if name == "" {
		name = slug
	}
	c.inventory.Models = append(c.inventory.Models, Model{ID: id, Harness: h, Provider: provider, Slug: slug, Name: name, CLISlug: cli, Args: args, Thinking: thinking, ThinkingStatus: status, Access: "catalog-listed; authentication not verified", Source: source, Metadata: object(redact(raw, ""))})
}

func (c *collector) configuredModels() {
	id := c.harness.ID
	provider := text(c.settings, "model_provider")
	if provider == "" {
		provider = text(c.settings, "defaultProvider")
	}
	model := text(c.settings, "model")
	if model == "" {
		model = text(c.settings, "defaultModel")
	}
	if id == "gemini" {
		model = text(object(c.settings["model"]), "name")
	}
	if id == "omp" {
		model = text(object(c.settings["modelRoles"]), "default")
	}
	if model == "" {
		return
	}
	cli := model
	if parts := strings.SplitN(model, "/", 2); len(parts) == 2 && contains([]string{"omp", "pi", "opencode"}, id) {
		provider, model = parts[0], parts[1]
	}
	if provider == "" {
		switch id {
		case "codex":
			provider = "openai"
		case "claude", "clawed":
			provider = c.claudeProvider()
		case "gemini":
			provider = "google"
		default:
			provider = "unresolved"
		}
	}
	c.addModel(provider, model, model, cli, "configured selection (not a capability catalog)", map[string]any{"configured": true}, nil, "unknown: configured selection only")
}

func (c *collector) deriveProviders() {
	providers := map[string]*Provider{}
	for _, m := range c.inventory.Models {
		p := providers[m.Provider]
		if p == nil {
			p = &Provider{ID: c.harness.ID + ":" + m.Provider, Harness: c.harness.ID, Slug: m.Provider, Models: []string{}, Access: "catalog-listed; authentication not verified"}
			providers[m.Provider] = p
		}
		p.Models = append(p.Models, m.ID)
	}
	for _, key := range []string{"providers", "provider", "model_providers"} {
		for name, settings := range object(c.settings[key]) {
			p := providers[name]
			if p == nil {
				p = &Provider{ID: c.harness.ID + ":" + name, Harness: c.harness.ID, Slug: name, Models: []string{}, Access: "configured; authentication not verified"}
				providers[name] = p
			}
			p.Settings = object(redact(settings, ""))
		}
	}
	disabled := stringList(c.settings["disabledProviders"])
	disabled = append(disabled, stringList(c.settings["disabled_providers"])...)
	for _, p := range providers {
		if contains(disabled, p.Slug) {
			p.Access = "disabled in settings"
		}
		sort.Strings(p.Models)
		c.inventory.Providers = append(c.inventory.Providers, *p)
	}
	sort.Slice(c.inventory.Providers, func(i, j int) bool { return c.inventory.Providers[i].ID < c.inventory.Providers[j].ID })
}
