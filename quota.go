package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/o-ai/harness"
)

// The quota noun: how much of a plan's rate-limit windows Claude Code and
// Codex have used, read from each vendor's usage endpoint with the
// harness's own login (a read-only HTTPS GET, never an inference request).
// Codex falls back to the rate limits its newest session file recorded.
// Quota is live: it is fetched on every call, never cached in the
// inventory file, and needs no collection.

// quotaHarnesses are the harnesses quota reads, in row order.
var quotaHarnesses = []string{"claude", "codex"}

// The usage endpoints, the keychain reader and the HTTP transport a Tool
// uses when its quota field leaves them unset. TestMain replaces the
// keychain and transport so tests never reach the user's login or the
// network.
var (
	claudeUsageURL                    = "https://api.anthropic.com/api/oauth/usage"
	codexUsageURL                     = "https://chatgpt.com/backend-api/codex/usage"
	defaultKeychain                   = macKeychain
	quotaTransport  http.RoundTripper = http.DefaultTransport
)

// quotaDeps are a Tool's quota sources; a zero field is the package
// default.
type quotaDeps struct {
	transport   http.RoundTripper
	keychain    func(ctx context.Context, service string) ([]byte, error)
	claudeURL   string
	codexURL    string
	useKeychain bool // read the keychain even though --home is not the tool's home (tests)
}

// Quota is one harness's quota record: what show prints.
type Quota struct {
	Harness    string        `json:"harness"`
	Plan       string        `json:"plan,omitempty"`
	Tier       string        `json:"tier,omitempty"`
	Source     string        `json:"source,omitempty"` // "api" or "session"
	ObservedAt string        `json:"observedAt,omitempty"`
	Windows    []QuotaWindow `json:"windows"`
	Warning    string        `json:"warning,omitempty"` // why the fallback answered
	Error      string        `json:"error,omitempty"`
}

// QuotaWindow is one rate-limit window: a list row's data.
type QuotaWindow struct {
	Harness          string  `json:"harness"`
	Window           string  `json:"window"`
	UsedPercent      float64 `json:"usedPercent"`
	RemainingPercent float64 `json:"remainingPercent"`
	ResetsAt         string  `json:"resetsAt,omitempty"`
	ResetsIn         string  `json:"resetsIn,omitempty"`
	Severity         string  `json:"severity,omitempty"`
	Scope            string  `json:"scope,omitempty"`
	Stale            bool    `json:"stale,omitempty"` // the window reset after it was observed
	Source           string  `json:"source"`
	ObservedAt       string  `json:"observedAt"`

	key string // the row id's window part
}

func (t *Tool) defineQuota(r *ochazuke.Runtime, list *ochazuke.List) {
	params := map[string]ochazuke.Parameter{
		"harness": {Type: "string", Enum: quotaHarnesses, Description: "only this harness: claude or codex"},
		"home":    collectParams["home"],
		"timeout": {Type: "string", Description: "the usage request's deadline, a Go duration (default 15s)"},
	}
	r.Define("quota").
		Verb("list", func(c ochazuke.Ctx) ochazuke.Result { return t.quotaList(c.Args, list) },
			"one row per rate-limit window: {id: quota/HARNESS:WINDOW, label, data: {usedPercent, remainingPercent, resetsAt, ...}}; a harness that cannot answer is one quota/HARNESS:error row").Rows("quota").Fetch().
		Verb("show", func(c ochazuke.Ctx) ochazuke.Result { return t.quotaShow(c) },
			"a harness's quota record (claude or codex; none prints both): plan, source, observedAt, windows, error").Fetch()
	listOp := withParams(r.Operations["quota.list"], merged(params, formatParam))
	listOp.Positional = []string{"harness"} // quota list claude
	r.Operations["quota.list"] = listOp
	show := withParams(r.Operations["quota.show"], merged(params, map[string]ochazuke.Parameter{
		"entry": {Type: "string", Enum: quotaHarnesses, Description: "claude or codex"},
	}))
	show.Positional = []string{"entry"}
	r.Operations["quota.show"] = show
}

func (t *Tool) quotaList(args ochazuke.Arguments, list *ochazuke.List) ochazuke.Result {
	o, _, err := t.options(args)
	if err != nil {
		return ochazuke.Failed(err)
	}
	harnesses := quotaHarnesses
	if o.Harness != "" {
		if !contains(quotaHarnesses, o.Harness) {
			return ochazuke.Failed(fmt.Errorf("quota reads %s, not %q", strings.Join(quotaHarnesses, " and "), o.Harness))
		}
		harnesses = []string{o.Harness}
	}
	quotas := t.quotas(o, harnesses)
	rows := quotaRows(quotas, t.clock())
	items := make([]ochazuke.ListItem, len(rows))
	for i, row := range rows {
		items[i] = ochazuke.ListItem{ID: row.ID, Label: row.Label, Noun: "quota", Data: strings.TrimPrefix(row.ID, "quota/"), Cells: row.Cells}
	}
	if err := list.SetItems(items); err != nil {
		return ochazuke.Failed(err)
	}
	var value any = rows
	switch format := ochazuke.StringArg(args, "format", "json"); format {
	case "jsonl":
		value = lines("quota", rows)
	case "json":
	default:
		return ochazuke.Failed(fmt.Errorf("unknown --format %q: json or jsonl", format))
	}
	var failures []error
	for _, q := range quotas {
		if q.Error != "" {
			failures = append(failures, fmt.Errorf("%s: %s", q.Harness, q.Error))
		}
	}
	if len(failures) == len(quotas) {
		// No harness answered: the error rows still print, and the
		// command fails.
		if err := writeJSON(t.Stdout, value); err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Failed(fmt.Errorf("no quota answered: %w", errors.Join(failures...)))
	}
	return ochazuke.Done(value)
}

func writeJSON(w io.Writer, v any) error {
	if l, ok := v.(ochazuke.Lines); ok {
		for _, line := range l {
			b, err := json.Marshal(line)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "%s\n", b); err != nil {
				return err
			}
		}
		return nil
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", b)
	return err
}

// quotaShow prints a harness's record: --target's harness, else ENTRY,
// else both.
func (t *Tool) quotaShow(c ochazuke.Ctx) ochazuke.Result {
	o, _, err := t.options(c.Args)
	if err != nil {
		return ochazuke.Failed(err)
	}
	harness := ochazuke.StringArg(c.Args, "entry", "")
	if c.Instance != nil && c.Instance.Noun == "quota" {
		if id, _ := c.Instance.Data.(string); id != "" {
			harness, _, _ = strings.Cut(id, ":")
		}
	}
	if harness == "" {
		harness = o.Harness
	}
	if harness == "" {
		return ochazuke.Done(t.quotas(o, quotaHarnesses))
	}
	if !contains(quotaHarnesses, harness) {
		return ochazuke.Failed(fmt.Errorf("quota show takes claude or codex, not %q", harness))
	}
	return ochazuke.Done(t.quotas(o, []string{harness})[0])
}

// quotas reads each harness's quota concurrently, in harnesses' order.
func (t *Tool) quotas(o Options, harnesses []string) []Quota {
	out := make([]Quota, len(harnesses))
	var wg sync.WaitGroup
	for i, h := range harnesses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), o.Timeout)
			defer cancel()
			switch h {
			case "claude":
				out[i] = t.claudeQuota(ctx, o)
			case "codex":
				out[i] = t.codexQuota(ctx, o)
			}
			if out[i].Windows == nil {
				out[i].Windows = []QuotaWindow{}
			}
		}()
	}
	wg.Wait()
	return out
}

// quotaRows are the list's rows: each record's windows, else its error.
func quotaRows(quotas []Quota, now time.Time) []Row {
	rows := []Row{}
	for _, q := range quotas {
		if q.Error != "" {
			rows = append(rows, Row{ID: "quota/" + q.Harness + ":error", Label: q.Harness + " quota unavailable: " + q.Error, Data: q, Cells: cellsOf("quota", q)})
			continue
		}
		for _, w := range q.Windows {
			rows = append(rows, Row{ID: "quota/" + q.Harness + ":" + w.key, Label: windowLabel(w), Data: w, Cells: cellsOf("quota", w)})
		}
	}
	return rows
}

// windowLabel is "claude weekly_all 47% used, 53% left, resets 2026-10-28
// 08:00", the reset in local time.
func windowLabel(w QuotaWindow) string {
	s := fmt.Sprintf("%s %s %s%% used, %s%% left", w.Harness, w.Window, percent(w.UsedPercent), percent(w.RemainingPercent))
	if w.ResetsAt != "" {
		if at, err := time.Parse(time.RFC3339, w.ResetsAt); err == nil {
			s += ", resets " + at.Local().Format("2006-01-02 15:04")
		}
	}
	if w.Stale {
		s += " (reset since observed)"
	}
	return s
}

func percent(v float64) string { return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64) }

// window is a QuotaWindow from a used percentage and a reset time (zero
// when unknown).
func window(harness, name, key string, used float64, reset time.Time, source string, observed, now time.Time) QuotaWindow {
	w := QuotaWindow{Harness: harness, Window: name, key: key, UsedPercent: used, RemainingPercent: math.Max(0, 100-used), Source: source, ObservedAt: observed.UTC().Format(time.RFC3339)}
	if !reset.IsZero() {
		w.ResetsAt = reset.Round(time.Second).UTC().Format(time.RFC3339)
		in := reset.Sub(now).Truncate(time.Minute)
		if in < 0 {
			in = 0
			w.Stale = source == "session"
		}
		w.ResetsIn = in.String()
	}
	return w
}

func (t *Tool) httpGet(ctx context.Context, url string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "o-ai")
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	transport := t.quota.transport
	if transport == nil {
		transport = quotaTransport
	}
	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s: %s", req.URL.Host, resp.Status, oneLine(truncate(string(body), 200)))
	}
	return body, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// --- Claude Code ---

type claudeCredentials struct {
	OAuth *struct {
		AccessToken      string `json:"accessToken"`
		ExpiresAt        int64  `json:"expiresAt"` // ms since the epoch
		SubscriptionType string `json:"subscriptionType"`
		RateLimitTier    string `json:"rateLimitTier"`
	} `json:"claudeAiOauth"`
}

type claudeUsage struct {
	FiveHour *claudeWindow `json:"five_hour"`
	SevenDay *claudeWindow `json:"seven_day"`
	Limits   []struct {
		Kind     string          `json:"kind"`
		Group    string          `json:"group"`
		Percent  float64         `json:"percent"`
		Severity string          `json:"severity"`
		ResetsAt string          `json:"resets_at"`
		Scope    json.RawMessage `json:"scope"`
	} `json:"limits"`
}

type claudeWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

// claudeConfigDir is CLAUDE_CONFIG_DIR, else --home's .claude.
func claudeConfigDir(o Options) string {
	return envDefault("CLAUDE_CONFIG_DIR", filepath.Join(o.Home, ".claude"))
}

// claudeKeychainService is the keychain item Claude Code keeps its login
// in: a CLAUDE_CONFIG_DIR adds the first 8 hex digits of its sha256.
func claudeKeychainService() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		sum := sha256.Sum256([]byte(dir))
		return "Claude Code-credentials-" + hex.EncodeToString(sum[:])[:8]
	}
	return "Claude Code-credentials"
}

func macKeychain(ctx context.Context, service string) ([]byte, error) {
	if runtime.GOOS != "darwin" {
		return nil, errors.New("no keychain")
	}
	return exec.CommandContext(ctx, "security", "find-generic-password", "-s", service, "-w").Output()
}

// claudeLogin is the OAuth login: the macOS keychain (when --home is the
// user's own), else $CLAUDE_CONFIG_DIR/.credentials.json.
func (t *Tool) claudeLogin(ctx context.Context, o Options) (claudeCredentials, error) {
	var creds claudeCredentials
	if o.Home == t.Home || t.quota.useKeychain {
		read := t.quota.keychain
		if read == nil {
			read = defaultKeychain
		}
		if out, err := read(ctx, claudeKeychainService()); err == nil && json.Unmarshal(bytes.TrimSpace(out), &creds) == nil && creds.OAuth != nil && creds.OAuth.AccessToken != "" {
			return creds, nil
		}
	}
	path := filepath.Join(claudeConfigDir(o), ".credentials.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return creds, errors.New("no Claude Code login (keychain or " + path + "); run claude and log in with a Claude plan")
	}
	if err := json.Unmarshal(data, &creds); err != nil || creds.OAuth == nil || creds.OAuth.AccessToken == "" {
		return creds, errors.New(path + " holds no Claude plan login (claudeAiOauth); run claude and log in with a Claude plan")
	}
	return creds, nil
}

func (t *Tool) claudeQuota(ctx context.Context, o Options) Quota {
	q := Quota{Harness: "claude"}
	creds, err := t.claudeLogin(ctx, o)
	if err != nil {
		q.Error = err.Error()
		return q
	}
	q.Plan, q.Tier = creds.OAuth.SubscriptionType, creds.OAuth.RateLimitTier
	now := t.clock()
	if exp := creds.OAuth.ExpiresAt; exp > 0 && now.After(time.UnixMilli(exp)) {
		q.Error = "the Claude Code login token expired at " + time.UnixMilli(exp).Local().Format("2006-01-02 15:04") + "; run claude to refresh the login"
		return q
	}
	url := t.quota.claudeURL
	if url == "" {
		url = claudeUsageURL
	}
	body, err := t.httpGet(ctx, url, map[string]string{"Authorization": "Bearer " + creds.OAuth.AccessToken, "anthropic-beta": "oauth-2025-04-20"})
	if err != nil {
		q.Error = err.Error()
		if strings.Contains(err.Error(), "401") {
			q.Error += "; run claude to refresh the login"
		}
		return q
	}
	windows, err := parseClaudeUsage(body, now)
	if err != nil {
		q.Error = err.Error()
		return q
	}
	q.Source, q.ObservedAt, q.Windows = "api", now.UTC().Format(time.RFC3339), windows
	return q
}

// parseClaudeUsage reads the usage answer's limits, one window each (a
// scoped limit named with its model), else five_hour and seven_day.
func parseClaudeUsage(body []byte, now time.Time) ([]QuotaWindow, error) {
	var u claudeUsage
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("unreadable Claude usage answer: %v", err)
	}
	var out []QuotaWindow
	for _, l := range u.Limits {
		if l.Kind == "" {
			continue
		}
		name, key := l.Kind, l.Kind
		scope := claudeScope(l.Scope)
		if scope != "" {
			name += " (" + scope + ")"
			key += ":" + strings.ToLower(strings.Join(strings.Fields(scope), "-"))
		}
		w := window("claude", name, key, l.Percent, parseTime(l.ResetsAt), "api", now, now)
		w.Severity, w.Scope = l.Severity, scope
		out = append(out, w)
	}
	if len(out) == 0 {
		for _, f := range []struct {
			name string
			w    *claudeWindow
		}{{"five_hour", u.FiveHour}, {"seven_day", u.SevenDay}} {
			if f.w != nil && f.w.Utilization != nil {
				out = append(out, window("claude", f.name, f.name, *f.w.Utilization, parseTime(f.w.ResetsAt), "api", now, now))
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("the Claude usage answer has no limits")
	}
	return out, nil
}

// claudeScope names a scoped limit: its model's display name, then its
// surface's.
func claudeScope(raw json.RawMessage) string {
	var scope map[string]any
	if json.Unmarshal(raw, &scope) != nil || scope == nil {
		return ""
	}
	var parts []string
	for _, k := range []string{"model", "surface"} {
		switch v := scope[k].(type) {
		case string:
			parts = append(parts, v)
		case map[string]any:
			if s := text(v, "display_name"); s != "" {
				parts = append(parts, s)
			} else if s := text(v, "id"); s != "" {
				parts = append(parts, s)
			}
		}
	}
	return strings.Join(parts, ", ")
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return at
}

// --- Codex ---

type codexAuth struct {
	Mode   string `json:"auth_mode"`
	APIKey string `json:"OPENAI_API_KEY"`
	Tokens *struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

func codexHome(o Options) string { return envDefault("CODEX_HOME", filepath.Join(o.Home, ".codex")) }

func (t *Tool) codexQuota(ctx context.Context, o Options) Quota {
	q := Quota{Harness: "codex"}
	now := t.clock()
	path := filepath.Join(codexHome(o), "auth.json")
	var apiErr error
	data, err := os.ReadFile(path)
	var auth codexAuth
	switch {
	case err != nil:
		apiErr = errors.New("no Codex login (" + path + "); run codex login with a ChatGPT plan")
	case json.Unmarshal(data, &auth) != nil:
		apiErr = errors.New(path + " is not readable JSON")
	case strings.EqualFold(auth.Mode, "apikey") || (auth.Tokens == nil && auth.APIKey != ""):
		q.Error = "Codex is logged in with an API key (pay as you go): no plan quota windows; run codex login with a ChatGPT plan for them"
		return q
	case auth.Tokens == nil || auth.Tokens.AccessToken == "":
		apiErr = errors.New(path + " holds no ChatGPT tokens; run codex login")
	default:
		account := auth.Tokens.AccountID
		if account == "" {
			account = jwtAccountID(auth.Tokens.AccessToken)
		}
		url := t.quota.codexURL
		if url == "" {
			url = codexUsageURL
		}
		headers := map[string]string{"Authorization": "Bearer " + auth.Tokens.AccessToken}
		if account != "" {
			headers["ChatGPT-Account-Id"] = account
		}
		body, err := t.httpGet(ctx, url, headers)
		if err == nil {
			var plan string
			var windows []QuotaWindow
			if plan, windows, err = parseCodexUsage(body, now); err == nil {
				q.Plan, q.Source, q.ObservedAt, q.Windows = plan, "api", now.UTC().Format(time.RFC3339), windows
				return q
			}
		}
		apiErr = err
	}
	plan, windows, observed, err := codexSessionQuota(codexHome(o), now)
	if err != nil {
		q.Error = apiErr.Error() + "; " + err.Error()
		return q
	}
	q.Plan, q.Source, q.ObservedAt, q.Windows = plan, "session", observed.UTC().Format(time.RFC3339), windows
	q.Warning = "usage endpoint: " + apiErr.Error()
	return q
}

// jwtAccountID is the ChatGPT account id an access token's payload claims
// (https://api.openai.com/auth chatgpt_account_id); no signature check.
func jwtAccountID(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	auth, _ := claims["https://api.openai.com/auth"].(map[string]any)
	return text(auth, "chatgpt_account_id")
}

// parseCodexUsage reads the usage endpoint's answer: rate_limit's
// primary_window and secondary_window (or the session file's primary and
// secondary), and plan_type.
func parseCodexUsage(body []byte, now time.Time) (string, []QuotaWindow, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return "", nil, fmt.Errorf("unreadable Codex usage answer: %v", err)
	}
	limits, _ := m["rate_limit"].(map[string]any)
	if limits == nil {
		limits, _ = m["rate_limits"].(map[string]any)
	}
	plan := text(m, "plan_type")
	if plan == "" {
		plan = text(limits, "plan_type")
	}
	windows := codexWindows(limits, "api", now, now)
	if len(windows) == 0 {
		return plan, nil, errors.New("the Codex usage answer has no rate-limit windows")
	}
	return plan, windows, nil
}

// codexWindows are a rate-limit object's primary and secondary windows,
// named by their length: "primary (5h)".
func codexWindows(limits map[string]any, source string, observed, now time.Time) []QuotaWindow {
	var out []QuotaWindow
	for _, key := range []string{"primary", "secondary"} {
		w, _ := limits[key+"_window"].(map[string]any)
		if w == nil {
			w, _ = limits[key].(map[string]any)
		}
		used, ok := number(w, "used_percent")
		if !ok {
			continue
		}
		var minutes float64
		if s, ok := number(w, "limit_window_seconds"); ok {
			minutes = s / 60
		} else if m, ok := number(w, "window_minutes"); ok {
			minutes = m
		}
		name := key
		if minutes > 0 {
			name += " (" + windowLength(int(minutes)) + ")"
		}
		var reset time.Time
		for _, k := range []string{"reset_at", "resets_at"} {
			if s, ok := number(w, k); ok {
				reset = time.Unix(int64(s), 0)
			} else if s := text(w, k); s != "" {
				reset = parseTime(s)
			}
			if !reset.IsZero() {
				break
			}
		}
		if reset.IsZero() {
			for _, k := range []string{"reset_after_seconds", "resets_in_seconds"} {
				if s, ok := number(w, k); ok {
					reset = observed.Add(time.Duration(s) * time.Second)
					break
				}
			}
		}
		out = append(out, window("codex", name, key, used, reset, source, observed, now))
	}
	return out
}

func number(m map[string]any, key string) (float64, bool) {
	f, ok := m[key].(float64)
	return f, ok
}

// windowLength is minutes as 5h, 7d or 90m.
func windowLength(minutes int) string {
	switch {
	case minutes%1440 == 0:
		return strconv.Itoa(minutes/1440) + "d"
	case minutes%60 == 0:
		return strconv.Itoa(minutes/60) + "h"
	}
	return strconv.Itoa(minutes) + "m"
}

// codexSessionQuota is the rate limits the newest Codex session file
// recorded: the last token_count event with rate_limits, newest file
// first, observed at the file's modification time.
func codexSessionQuota(home string, now time.Time) (string, []QuotaWindow, time.Time, error) {
	files := harness.SessionFiles(filepath.Join(home, "sessions"), 31, 200)
	for _, path := range files {
		limits := lastRateLimits(path)
		if limits == nil {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		windows := codexWindows(limits, "session", info.ModTime(), now)
		if len(windows) == 0 {
			continue
		}
		return text(limits, "plan_type"), windows, info.ModTime(), nil
	}
	return "", nil, time.Time{}, errors.New("no Codex session file records rate limits")
}

// lastRateLimits is the payload.rate_limits of a session file's last
// token_count event that has them, else nil.
func lastRateLimits(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	for len(data) > 0 {
		i := bytes.LastIndexByte(bytes.TrimRight(data, "\n"), '\n')
		line := data[i+1:]
		data = data[:max(i, 0)]
		if !bytes.Contains(line, []byte(`"token_count"`)) || !bytes.Contains(line, []byte(`"rate_limits"`)) {
			continue
		}
		var ev struct {
			Payload struct {
				Type       string         `json:"type"`
				RateLimits map[string]any `json:"rate_limits"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &ev) == nil && ev.Payload.Type == "token_count" && ev.Payload.RateLimits != nil {
			return ev.Payload.RateLimits
		}
	}
	return nil
}
