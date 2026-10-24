package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (c *collector) ompModels() {
	b, err := runCommand(c.options, c.harness.Executable, []string{"models", "--json", "--no-extensions", "--kind", "all"}, "")
	if err != nil {
		c.warn(err.Error())
		return
	}
	var catalog struct {
		Models []map[string]any `json:"models"`
	}
	if json.Unmarshal(b, &catalog) != nil {
		c.warn("OMP returned an invalid model catalog")
		return
	}
	for _, raw := range catalog.Models {
		levels := stringList(raw["thinking"])
		status := "reported by OMP"
		if raw["thinking"] == nil {
			status = "not supported or not reported by OMP"
		}
		c.addModel(text(raw, "provider"), text(raw, "id"), text(raw, "name"), text(raw, "selector"), "omp models --json --no-extensions --kind all", raw, levels, status)
	}
	c.harness.CatalogStatus = "live metadata (extensions disabled)"
}

func (c *collector) claudeProvider() string {
	for env, provider := range map[string]string{"CLAUDE_CODE_USE_BEDROCK": "amazon-bedrock", "CLAUDE_CODE_USE_VERTEX": "google-vertex", "CLAUDE_CODE_USE_FOUNDRY": "anthropic-foundry"} {
		value := os.Getenv(env)
		if configured := text(object(c.settings["env"]), env); configured != "" {
			value = configured
		}
		if value == "1" || value == "true" {
			return provider
		}
	}
	return "anthropic"
}

func (c *collector) claudeModels() {
	input := `{"type":"control_request","request_id":"aiview","request":{"subtype":"initialize"}}` + "\n"
	b, err := runCommand(c.options, c.harness.Executable, []string{"--no-session-persistence", "--print", "--verbose", "--input-format", "stream-json", "--output-format", "stream-json"}, input)
	if err != nil {
		c.warn(err.Error())
		return
	}
	found := false
	for _, line := range bytes.Split(b, []byte{'\n'}) {
		var msg map[string]any
		if json.Unmarshal(line, &msg) != nil || text(msg, "type") != "control_response" {
			continue
		}
		body := object(object(msg["response"])["response"])
		for _, v := range array(body["models"]) {
			raw := object(v)
			slug := text(raw, "value")
			levels := stringList(raw["supportedEffortLevels"])
			status := "reported by Claude initialization"
			if len(levels) == 0 {
				status = "effort levels not reported by Claude"
			}
			c.addModel(c.claudeProvider(), slug, text(raw, "displayName"), slug, c.harness.ID+" initialization protocol (no prompt)", raw, levels, status)
			found = true
		}
		for _, v := range array(body["agents"]) {
			raw := object(v)
			name := text(raw, "name")
			if name == "" {
				continue
			}
			c.inventory.Agents = append(c.inventory.Agents, Resource{ID: "agent:" + c.harness.ID + ":runtime:" + name, Kind: "agent", Name: name, Description: text(raw, "description"), Path: c.harness.ID + ":initialization/agents/" + name, Harnesses: []string{c.harness.ID}, Scope: "effective runtime", Access: "reported by harness", Metadata: object(redact(raw, ""))})
		}
	}
	if found {
		c.harness.CatalogStatus = "live initialization metadata"
	} else {
		c.warn("Claude initialization did not return models")
	}
}

func (c *collector) codexModels() {
	path := filepath.Join(c.root, "models_cache.json")
	raw, err := readConfig(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		c.warn("Cannot read Codex model cache: " + safeParseError(err))
		return
	}
	// The cache is OpenAI account metadata, not a catalog for custom model providers.
	for _, v := range array(raw["models"]) {
		m := object(v)
		slug := text(m, "slug")
		levels := []string{}
		descriptions := map[string]string{}
		for _, l := range array(m["supported_reasoning_levels"]) {
			level := object(l)
			effort := text(level, "effort")
			if effort != "" {
				levels = append(levels, effort)
				descriptions[effort] = text(level, "description")
			}
		}
		m["cacheFetchedAt"] = raw["fetched_at"]
		c.addModel("openai", slug, text(m, "display_name"), slug, path, m, levels, "reported by cached Codex catalog")
		if len(c.inventory.Models) > 0 {
			last := &c.inventory.Models[len(c.inventory.Models)-1]
			for i := range last.Thinking {
				last.Thinking[i].Description = descriptions[last.Thinking[i].Slug]
			}
		}
	}
	c.harness.CatalogStatus = "cached: " + text(raw, "fetched_at")
}

func (c *collector) openCodeModels() {
	b, err := runCommand(c.options, c.harness.Executable, []string{"models", "--verbose", "--pure"}, "")
	if err != nil {
		c.warn(err.Error())
		return
	}
	count := 0
	for len(bytes.TrimSpace(b)) > 0 {
		b = bytes.TrimSpace(b)
		end := bytes.IndexByte(b, '\n')
		if end < 0 {
			break
		}
		selector := strings.TrimSpace(string(b[:end]))
		b = bytes.TrimSpace(b[end+1:])
		if len(b) == 0 || b[0] != '{' {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(b))
		var raw map[string]any
		if dec.Decode(&raw) != nil {
			c.warn("Invalid OpenCode model metadata")
			break
		}
		b = b[dec.InputOffset():]
		levels := []string{}
		for level := range object(raw["variants"]) {
			levels = append(levels, level)
		}
		sort.Strings(levels)
		c.addModel(text(raw, "providerID"), text(raw, "id"), text(raw, "name"), selector, "opencode models --verbose --pure", raw, levels, "reported model variants; not necessarily reasoning effort")
		count++
	}
	if count > 0 {
		c.harness.CatalogStatus = "live metadata (external plugins disabled)"
	} else {
		c.warn("OpenCode returned no parseable model metadata")
	}
}

// Pi's installed SDK supplies the same capability calculation as its picker.
// No credential refresh or command-backed API-key resolution is requested.
const piMetadataScript = `
import {pathToFileURL} from 'node:url';
import {readFileSync} from 'node:fs';
const [registryPath,authPath,modelsPath,authFile,capabilitiesPath]=process.argv.slice(1);
const {ModelRegistry}=await import(pathToFileURL(registryPath).href);
const {AuthStorage}=await import(pathToFileURL(authPath).href);
const {getSupportedThinkingLevels}=await import(pathToFileURL(capabilitiesPath).href);
let credentials={};
try { credentials=JSON.parse(readFileSync(authFile,'utf8')); } catch(e) { if(e.code!=='ENOENT') throw e; }
const registry=ModelRegistry.create(AuthStorage.inMemory(credentials),modelsPath);
if(registry.getError()) throw new Error('Invalid Pi models configuration');
const models=registry.getAll().map(m=>({...m,thinking:getSupportedThinkingLevels(m),authConfigured:registry.getProviderAuthStatus(m.provider).configured}));
console.log(JSON.stringify({models}));
`

func (c *collector) piModels() {
	executable, err := filepath.EvalSymlinks(c.harness.Executable)
	if err != nil {
		c.warn("Cannot resolve Pi installation")
		return
	}
	dist := filepath.Dir(executable)
	capabilities := ""
	for dir := filepath.Dir(dist); ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, "node_modules", "@mariozechner", "pi-ai", "dist", "models.js")
		if _, err := os.Stat(candidate); err == nil {
			capabilities = candidate
			break
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	if capabilities == "" {
		c.warn("Pi SDK not found beside executable; model metadata unavailable for this installation layout")
		return
	}
	args := []string{"--input-type=module", "-e", piMetadataScript, filepath.Join(dist, "core", "model-registry.js"), filepath.Join(dist, "core", "auth-storage.js"), filepath.Join(c.root, "models.json"), filepath.Join(c.root, "auth.json"), capabilities}
	b, err := runCommand(c.options, "node", args, "")
	if err != nil {
		c.warn(err.Error())
		return
	}
	var catalog struct {
		Models []map[string]any `json:"models"`
	}
	if json.Unmarshal(b, &catalog) != nil {
		c.warn("Invalid Pi SDK model catalog")
		return
	}
	for _, raw := range catalog.Models {
		provider, slug := text(raw, "provider"), text(raw, "id")
		c.addModel(provider, slug, text(raw, "name"), provider+"/"+slug, "installed Pi ModelRegistry + getSupportedThinkingLevels", raw, stringList(raw["thinking"]), "reported by installed Pi SDK")
		if len(c.inventory.Models) > 0 {
			m := &c.inventory.Models[len(c.inventory.Models)-1]
			if raw["authConfigured"] == true {
				m.Access = "credentials configured; not validated"
			} else {
				m.Access = "no configured credentials reported"
			}
		}
	}
	c.harness.CatalogStatus = "installed SDK metadata (extensions excluded)"
}
