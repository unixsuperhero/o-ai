package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// quotaNow is the clock every quota test reads: before the fixtures' resets.
var quotaNow = time.Date(2026, 10, 25, 16, 0, 0, 0, time.UTC)

// claudeFixture is the usage endpoint's answer, trimmed, as it was on
// 2026-10-25.
const claudeFixture = `{"five_hour":{"utilization":7.0,"resets_at":"2026-10-25T20:50:00.153097+00:00"},
 "seven_day":{"utilization":47.0,"resets_at":"2026-10-28T08:00:00.153117+00:00"},
 "limits":[
   {"kind":"session","group":"session","percent":7,"severity":"normal","resets_at":"2026-10-25T20:50:00.153097+00:00","scope":null,"is_active":false},
   {"kind":"weekly_all","group":"weekly","percent":47,"severity":"normal","resets_at":"2026-10-28T08:00:00.153117+00:00","scope":null,"is_active":false},
   {"kind":"weekly_scoped","group":"weekly","percent":77,"severity":"warning","resets_at":"2026-10-28T08:00:00.153283+00:00","scope":{"model":{"id":null,"display_name":"Fable"},"surface":null},"is_active":true}],
 "extra_usage":{"is_enabled":false},"spend":{}}`

// quotaWorld is a home with CLAUDE_CONFIG_DIR and CODEX_HOME under it and
// usage endpoints served by handlers.
type quotaWorld struct {
	t            *testing.T
	home         string
	claude       http.HandlerFunc
	codex        http.HandlerFunc
	keychain     []byte
	claudeHeader http.Header
	codexHeader  http.Header
}

func newQuotaWorld(t *testing.T) *quotaWorld {
	home := isolatedHome(t)
	w := &quotaWorld{t: t, home: home}
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	w.claude = func(rw http.ResponseWriter, r *http.Request) { rw.Write([]byte(claudeFixture)) }
	w.codex = func(rw http.ResponseWriter, r *http.Request) { http.Error(rw, "no", http.StatusUnauthorized) }
	return w
}

func (w *quotaWorld) run(args ...string) (string, error) {
	w.t.Helper()
	claude := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.claudeHeader = r.Header.Clone()
		w.claude(rw, r)
	}))
	defer claude.Close()
	codex := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.codexHeader = r.Header.Clone()
		w.codex(rw, r)
	}))
	defer codex.Close()
	var out, errOut bytes.Buffer
	tool := &Tool{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut, Env: os.Environ(), Home: w.home, Cwd: w.home, Prog: "o-ai",
		now: func() time.Time { return quotaNow },
		quota: quotaDeps{transport: http.DefaultTransport, claudeURL: claude.URL, codexURL: codex.URL,
			keychain: func(context.Context, string) ([]byte, error) {
				if w.keychain == nil {
					return nil, os.ErrNotExist
				}
				return w.keychain, nil
			}}}
	err := tool.Main(context.Background(), args)
	return out.String(), err
}

func (w *quotaWorld) claudeLogin(expiresAt time.Time) {
	put(w.t, filepath.Join(w.home, ".claude", ".credentials.json"), `{"claudeAiOauth":{"accessToken":"sk-ant-oat-test","refreshToken":"r","expiresAt":`+
		jsonNumber(expiresAt.UnixMilli())+`,"scopes":["user:inference"],"subscriptionType":"max","rateLimitTier":"default_claude_max_20x"}}`)
}

func jsonNumber(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestParseClaudeUsage(t *testing.T) {
	windows, err := parseClaudeUsage([]byte(claudeFixture), quotaNow)
	if err != nil {
		t.Fatal(err)
	}
	rows := quotaRows([]Quota{{Harness: "claude", Windows: windows}}, quotaNow)
	want := []struct {
		id, window string
		used, left float64
		severity   string
	}{
		{"quota/claude:session", "session", 7, 93, "normal"},
		{"quota/claude:weekly_all", "weekly_all", 47, 53, "normal"},
		{"quota/claude:weekly_scoped:fable", "weekly_scoped (Fable)", 77, 23, "warning"},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows %+v", rows)
	}
	for i, w := range want {
		got := rows[i].Data.(QuotaWindow)
		if rows[i].ID != w.id || got.Window != w.window || got.UsedPercent != w.used || got.RemainingPercent != w.left || got.Severity != w.severity || got.Source != "api" {
			t.Errorf("row %d = %s %+v, want %+v", i, rows[i].ID, got, w)
		}
	}
	weekly := rows[1].Data.(QuotaWindow)
	reset := time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC)
	if weekly.ResetsAt != "2026-10-28T08:00:00Z" || weekly.ResetsIn != reset.Sub(quotaNow).String() {
		t.Errorf("weekly reset %s in %s", weekly.ResetsAt, weekly.ResetsIn)
	}
	if label := "claude weekly_all 47% used, 53% left, resets " + reset.Local().Format("2006-01-02 15:04"); rows[1].Label != label {
		t.Errorf("label %q, want %q", rows[1].Label, label)
	}
	if rows[2].Data.(QuotaWindow).Scope != "Fable" {
		t.Errorf("scope %+v", rows[2].Data)
	}

	// Without limits, five_hour and seven_day answer.
	windows, err = parseClaudeUsage([]byte(`{"five_hour":{"utilization":12.5,"resets_at":"2026-10-25T20:50:00+00:00"},"seven_day":{"utilization":40,"resets_at":null}}`), quotaNow)
	if err != nil || len(windows) != 2 || windows[0].Window != "five_hour" || windows[0].RemainingPercent != 87.5 || windows[1].ResetsAt != "" {
		t.Fatalf("fallback windows %+v %v", windows, err)
	}
	if _, err := parseClaudeUsage([]byte(`{}`), quotaNow); err == nil {
		t.Fatal("an answer without windows must be an error")
	}
}

func TestClaudeQuotaListAndShow(t *testing.T) {
	w := newQuotaWorld(t)
	w.claudeLogin(quotaNow.Add(time.Hour))
	out, err := w.run("quota", "list", "--harness", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if got := w.claudeHeader.Get("Authorization"); got != "Bearer sk-ant-oat-test" || w.claudeHeader.Get("anthropic-beta") != "oauth-2025-04-20" || w.claudeHeader.Get("User-Agent") != "o-ai" {
		t.Fatalf("claude request headers %v", w.claudeHeader)
	}
	rows, data := rowsOf[QuotaWindow](t, out)
	if len(rows) != 3 || rows[1].ID != "quota/claude:weekly_all" || data[1].UsedPercent != 47 || data[1].RemainingPercent != 53 || data[1].ObservedAt != "2026-10-25T16:00:00Z" {
		t.Fatalf("claude rows %s", out)
	}
	out, err = w.run("quota", "show", "--target", rows[2].ID)
	if err != nil {
		t.Fatal(err)
	}
	var q Quota
	if err := json.Unmarshal([]byte(out), &q); err != nil {
		t.Fatal(err)
	}
	if q.Harness != "claude" || q.Plan != "max" || q.Tier != "default_claude_max_20x" || q.Source != "api" || len(q.Windows) != 3 || q.Error != "" {
		t.Fatalf("show %s", out)
	}

	// The keychain's login wins over the file's.
	w.keychain = []byte(`{"claudeAiOauth":{"accessToken":"from-keychain","expiresAt":` + jsonNumber(quotaNow.Add(time.Hour).UnixMilli()) + `,"subscriptionType":"pro"}}`)
	if _, err := w.run("quota", "show", "claude"); err != nil || w.claudeHeader.Get("Authorization") != "Bearer from-keychain" {
		t.Fatalf("keychain login not used: %v %v", err, w.claudeHeader)
	}
}

func TestClaudeQuotaExpiredToken(t *testing.T) {
	w := newQuotaWorld(t)
	w.claudeLogin(quotaNow.Add(-time.Minute))
	out, err := w.run("quota", "list", "claude")
	if err == nil || !strings.Contains(err.Error(), "run claude to refresh the login") {
		t.Fatalf("a list where no harness answers must fail: %v", err)
	}
	rows, _ := rowsOf[Quota](t, out)
	if len(rows) != 1 || rows[0].ID != "quota/claude:error" || !strings.Contains(rows[0].Label, "expired") || !strings.Contains(rows[0].Label, "run claude to refresh the login") {
		t.Fatalf("expired rows %s", out)
	}
	if w.claudeHeader != nil {
		t.Fatal("an expired token must not be sent")
	}
	out, err = w.run("quota", "show", "claude")
	if err != nil || !strings.Contains(out, `"error": "the Claude Code login token expired`) {
		t.Fatalf("show %v %s", err, out)
	}
}

func TestCodexQuotaAPIKeyMode(t *testing.T) {
	w := newQuotaWorld(t)
	put(t, filepath.Join(w.home, ".codex", "auth.json"), `{"auth_mode":"apikey","OPENAI_API_KEY":"sk-test"}`)
	out, err := w.run("quota", "show", "codex")
	if err != nil {
		t.Fatal(err)
	}
	var q Quota
	if err := json.Unmarshal([]byte(out), &q); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.Error, "API key") || !strings.Contains(q.Error, "no plan quota") || w.codexHeader != nil {
		t.Fatalf("api key mode: %s", out)
	}
}

// codexSession writes a session file whose last token_count carries the
// rate limits, with an older one before it.
func codexSession(t *testing.T, home string, mtime time.Time) {
	path := filepath.Join(home, ".codex", "sessions", "2026", "10", "25", "rollout-2026-10-25T12-00-00-x.jsonl")
	put(t, path, strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"x"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"primary":{"used_percent":1,"window_minutes":300,"resets_at":1793000000}}}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{},"rate_limits":{"limit_id":"codex","primary":{"used_percent":12.5,"window_minutes":300,"resets_at":` + jsonNumber(quotaNow.Add(2*time.Hour).Unix()) + `},"secondary":{"used_percent":40,"window_minutes":10080,"resets_at":` + jsonNumber(quotaNow.Add(72*time.Hour).Unix()) + `},"plan_type":"plus"}}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{},"rate_limits":null}}`,
		`{"type":"response_item","payload":{"type":"message"}}`,
	}, "\n")+"\n")
	put(t, filepath.Join(home, ".codex", "sessions", "2026", "10", "24", "rollout-2026-10-24T12-00-00-y.jsonl"),
		`{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":99,"window_minutes":300,"resets_at":1}}}}`+"\n")
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestCodexQuotaSessionFallback(t *testing.T) {
	w := newQuotaWorld(t)
	observed := quotaNow.Add(-10 * time.Minute)
	codexSession(t, w.home, observed)
	out, err := w.run("quota", "list", "--harness", "codex")
	if err != nil {
		t.Fatal(err)
	}
	rows, data := rowsOf[QuotaWindow](t, out)
	if len(rows) != 2 || rows[0].ID != "quota/codex:primary" || rows[1].ID != "quota/codex:secondary" {
		t.Fatalf("codex rows %s", out)
	}
	if data[0].Window != "primary (5h)" || data[0].UsedPercent != 12.5 || data[0].RemainingPercent != 87.5 || data[0].ResetsIn != "2h0m0s" || data[0].Source != "session" || data[0].ObservedAt != observed.Format(time.RFC3339) {
		t.Fatalf("primary %+v", data[0])
	}
	if data[1].Window != "secondary (7d)" || data[1].UsedPercent != 40 || !strings.HasPrefix(rows[1].Label, "codex secondary (7d) 40% used, 60% left, resets ") {
		t.Fatalf("secondary %+v %q", data[1], rows[1].Label)
	}
	out, err = w.run("quota", "show", "codex")
	if err != nil || !strings.Contains(out, `"plan": "plus"`) || !strings.Contains(out, `"source": "session"`) || !strings.Contains(out, "no Codex login") {
		t.Fatalf("show %v %s", err, out)
	}
}

func TestCodexQuotaEndpoint(t *testing.T) {
	w := newQuotaWorld(t)
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acct-from-jwt"}}`))
	put(t, filepath.Join(w.home, ".codex", "auth.json"), `{"auth_mode":"chatgpt","tokens":{"id_token":"i","access_token":"h.`+payload+`.s","refresh_token":"r"}}`)
	w.codex = func(rw http.ResponseWriter, r *http.Request) { rw.Write([]byte(codexFixture)) }
	codexSession(t, w.home, quotaNow) // the endpoint answers, so the session file is not read
	out, err := w.run("quota", "show", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if w.codexHeader.Get("ChatGPT-Account-Id") != "acct-from-jwt" || w.codexHeader.Get("Authorization") != "Bearer h."+payload+".s" {
		t.Fatalf("codex request headers %v", w.codexHeader)
	}
	var q Quota
	if err := json.Unmarshal([]byte(out), &q); err != nil {
		t.Fatal(err)
	}
	if q.Source != "api" || q.Plan != codexFixturePlan || len(q.Windows) != 2 || q.Windows[0].Window != "primary (5h)" || q.Windows[1].Window != "secondary (7d)" {
		t.Fatalf("codex endpoint record %s", out)
	}
	if q.Windows[0].UsedPercent != codexFixturePrimaryUsed || q.Windows[0].ResetsAt == "" {
		t.Fatalf("primary %+v", q.Windows[0])
	}

	// An endpoint without windows falls back to the session file.
	w.codex = func(rw http.ResponseWriter, r *http.Request) {
		rw.Write([]byte(`{"plan_type":"plus","rate_limit":null}`))
	}
	out, err = w.run("quota", "show", "codex")
	if err != nil || !strings.Contains(out, `"source": "session"`) || !strings.Contains(out, "no rate-limit windows") {
		t.Fatalf("fallback %v %s", err, out)
	}
}

func TestQuotaListOneHarnessAnswering(t *testing.T) {
	w := newQuotaWorld(t)
	w.claudeLogin(quotaNow.Add(time.Hour))
	out, err := w.run("quotas")
	if err != nil {
		t.Fatalf("one harness answered, the list must succeed: %v", err)
	}
	rows, _ := rowsOf[map[string]any](t, out)
	last := rows[len(rows)-1]
	if len(rows) != 4 || rows[0].ID != "quota/claude:session" || last.ID != "quota/codex:error" || !strings.Contains(last.Label, "no Codex login") {
		t.Fatalf("rows %s", out)
	}
	out, err = w.run("quota", "show")
	var both []Quota
	if err != nil || json.Unmarshal([]byte(out), &both) != nil || len(both) != 2 || both[0].Harness != "claude" || both[1].Error == "" {
		t.Fatalf("show both %v %s", err, out)
	}
}

func TestStateLeavesOutQuota(t *testing.T) {
	home := isolatedHome(t)
	out, _, err := runCLI(t, "status", "state", "--home", home, "--project", home, "--no-probe")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `"noun":"quota"`) {
		t.Fatalf("the state must not fetch quota: %s", out)
	}
}

func TestJWTAccountID(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"a-1"}}`))
	if got := jwtAccountID("x." + payload + ".y"); got != "a-1" {
		t.Fatalf("got %q", got)
	}
	if got := jwtAccountID("not-a-jwt"); got != "" {
		t.Fatalf("got %q", got)
	}
}

// codexFixture is the Codex usage endpoint's answer.
const codexFixture = `{"plan_type":"plus","rate_limit":{"allowed":true,"limit_reached":false,
 "primary_window":{"used_percent":12,"limit_window_seconds":18000,"reset_after_seconds":3600,"reset_at":1793034000},
 "secondary_window":{"used_percent":40,"limit_window_seconds":604800,"reset_after_seconds":259200,"reset_at":1793293200}}}`

const (
	codexFixturePlan        = "plus"
	codexFixturePrimaryUsed = 12
)
