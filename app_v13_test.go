package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sshpilot/internal/agent"
	"sshpilot/internal/store"
	"sshpilot/internal/testsshd"
)

// 默认的 AI 服务是 DeepSeek；从旧版本升级时，只有从未配置过的设置才会被切换过去。
func TestAIDefaultIsDeepSeek(t *testing.T) {
	a := newTestApp(t)
	ai := a.settings().AI
	if ai.Provider != "openai" || ai.Preset != "deepseek" || ai.BaseURL != store.DeepSeekBaseURL || ai.Model != store.DeepSeekModel {
		t.Fatalf("fresh install default: %+v", ai)
	}
	if a.GetDashboard().AIConfigured {
		t.Fatal("the default service without an API key must not count as configured")
	}

	write := func(dir string, ai map[string]any) {
		raw, _ := json.Marshal(map[string]any{"version": 1, "settings": map[string]any{"ai": ai}})
		if err := os.WriteFile(filepath.Join(dir, "store.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(dir string) store.AISettings {
		st, err := store.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		var out store.AISettings
		st.Read(func(d *store.Data) { out = d.Settings.AI })
		return out
	}
	// 旧版本的默认值、没有填过 Key：切换到 DeepSeek
	untouched := t.TempDir()
	write(untouched, map[string]any{"provider": "anthropic", "preset": "anthropic", "model": "claude-opus-5-5", "approvalMode": "always"})
	if got := read(untouched); got.Preset != "deepseek" || got.Provider != "openai" || got.Model != store.DeepSeekModel || got.ApprovalMode != "always" {
		t.Fatalf("untouched settings should move to DeepSeek and keep other options: %+v", got)
	}
	// 用户已经在用 Claude：保持不变
	claude := t.TempDir()
	write(claude, map[string]any{"provider": "anthropic", "preset": "anthropic", "model": "claude-sonnet-5-5", "apiKey": "sk-ant-x"})
	if got := read(claude); got.Provider != "anthropic" || got.Model != "claude-sonnet-5-5" || got.APIKey != "sk-ant-x" {
		t.Fatalf("configured Claude settings must be kept: %+v", got)
	}
	// 用户选了其他兼容接口：保持不变
	other := t.TempDir()
	write(other, map[string]any{"provider": "openai", "preset": "ollama", "baseUrl": "http://localhost:11434/v1", "model": "qwen2.5:14b"})
	if got := read(other); got.Preset != "ollama" || got.Model != "qwen2.5:14b" {
		t.Fatalf("other providers must be kept: %+v", got)
	}
	// 迁移只做一次：之后用户改回 Claude（还没填 Key）不会再被换掉
	st, _ := store.Open(untouched)
	_ = st.Update(func(d *store.Data) error {
		d.Settings.AI.Provider, d.Settings.AI.Preset, d.Settings.AI.BaseURL, d.Settings.AI.Model = "anthropic", "anthropic", "", "claude-opus-5-5"
		return nil
	})
	if got := read(untouched); got.Provider != "anthropic" {
		t.Fatalf("migration must run only once: %+v", got)
	}
}

func TestHotkeysAndSettings(t *testing.T) {
	for in, want := range map[string]string{
		"ctrl+shift+c":     "Ctrl+Shift+C",
		" Alt + f ":        "Alt+F",
		"Shift+Insert":     "Shift+Insert",
		"F3":               "F3",
		"Ctrl+Alt+Shift+k": "Ctrl+Alt+Shift+K",
		"c":                "DEF", // 没有修饰键的普通按键会占用正常输入
		"Shift+C":          "DEF",
		"Ctrl+":            "DEF",
		"Ctrl+A+B":         "DEF",
		"":                 "DEF",
	} {
		if got := normalizeHotkey(in, "DEF"); got != want {
			t.Errorf("normalizeHotkey(%q) = %q, want %q", in, got, want)
		}
	}
	a := newTestApp(t)
	s := a.settings()
	if s.Terminal.KeyCopy != "Ctrl+Shift+C" || s.Terminal.KeyPaste != "Ctrl+Shift+V" || s.Terminal.KeyFind != "Ctrl+Shift+F" {
		t.Fatalf("default hotkeys: %+v", s.Terminal)
	}
	s.Terminal.KeyCopy, s.Terminal.KeyFind = "alt+c", ""
	saved, err := a.SaveSettings(s, "", false)
	if err != nil || saved.Terminal.KeyCopy != "Alt+C" || saved.Terminal.KeyFind != "Ctrl+Shift+F" {
		t.Fatalf("save hotkeys: %+v %v", saved.Terminal, err)
	}
	s.Terminal.KeyPaste = "Alt+C"
	if _, err := a.SaveSettings(s, "", false); err == nil {
		t.Fatal("two actions sharing one hotkey should be rejected")
	}
}

// 终端里的「AI 运维」：对话绑定到一台服务器，工具默认操作它，并能读取终端里最近的输出。
func TestServerSessionAndReadTerminal(t *testing.T) {
	srv := testsshd.Start(t)
	a := newTestApp(t)
	s, err := a.SaveServer(ServerInput{Name: "web-01", Host: srv.Host, Port: srv.Port, Username: srv.User, AuthType: "password", Password: srv.Password})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.AgentNewSession("missing"); err == nil {
		t.Fatal("a session for an unknown server should fail")
	}
	sum, err := a.AgentNewSession(s.ID)
	if err != nil || sum.ServerID != s.ID {
		t.Fatalf("new session: %+v %v", sum, err)
	}
	sess, err := a.agent.Sessions.Load(sum.ID)
	if err != nil || sess.ServerID != s.ID || !strings.Contains(sess.SystemPrompt, "「web-01」") || !strings.Contains(sess.SystemPrompt, "read_terminal") {
		t.Fatalf("session must carry the server focus: %v %q", err, sess.SystemPrompt[max(0, len(sess.SystemPrompt)-300):])
	}
	if v, _ := a.AgentGetSession(sum.ID); v.ServerID != s.ID {
		t.Fatalf("session view: %+v", v.ServerID)
	}
	listed := false
	for _, x := range a.AgentListSessions() {
		listed = listed || (x.ID == sum.ID && x.ServerID == s.ID)
	}
	if !listed {
		t.Fatal("session list should expose the bound server")
	}
	general, _ := a.AgentNewSession("")
	if g, _ := a.agent.Sessions.Load(general.ID); g.ServerID != "" || strings.Contains(g.SystemPrompt, "当前终端") {
		t.Fatal("a general session must not be bound to a server")
	}

	// 工具没有填服务器时落到会话绑定的服务器；普通会话仍然要求指定
	tc := &agent.ToolContext{Agent: a.agent, Session: sess}
	out := callTool(t, a, tc, "run_command", map[string]any{"command": "echo bound-ok"})
	if !strings.Contains(out.Content, "bound-ok") || !strings.Contains(out.Content, "web-01") {
		t.Fatalf("run_command should default to the bound server: %s", out.Content)
	}
	if _, err := a.targets(&agent.ToolContext{Agent: a.agent, Session: &agent.Session{ID: "g"}}, nil); err == nil {
		t.Fatal("a general session without servers should be rejected")
	}

	// 没有打开终端时给出明确的说明
	if r := callTool(t, a, tc, "read_terminal", map[string]any{}); !r.IsError || !strings.Contains(r.Content, "没有打开的终端") {
		t.Fatalf("read_terminal without a terminal: %+v", r)
	}
	id, err := a.terms.Open(context.Background(), s.ID, 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	a.terms.Ready(id)
	if got := a.terms.OpenServers(); len(got) != 1 || got[0] != s.ID {
		t.Fatalf("open servers: %v", got)
	}
	_ = a.terms.Write(id, "echo marker-$((40+2))\n")
	waitFor(t, "terminal output", func() bool {
		text, ok := a.terms.Recent(s.ID, 50)
		return ok && strings.Contains(text, "marker-42")
	})
	r := callTool(t, a, tc, "read_terminal", map[string]any{"lines": 30})
	if r.IsError || !strings.Contains(r.Content, "marker-42") || strings.Contains(r.Content, "\x1b") {
		t.Fatalf("read_terminal: %+v", r)
	}
	a.terms.Close(id)
	waitFor(t, "terminal closed", func() bool { return len(a.terms.OpenServers()) == 0 })
}

// 文件管理独立窗口以只读方式打开主窗口的数据：自己的修改不落盘，主窗口的修改能跟上。
func TestReadOnlyStoreFollowsMain(t *testing.T) {
	dir := t.TempDir()
	main, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = main.Update(func(d *store.Data) error {
		d.Servers = append(d.Servers, &store.Server{ID: "s1", Name: "web-01", Host: "10.0.0.1", Port: 22, Password: "secret-1"})
		return nil
	})
	win, err := store.OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	var pw string
	win.Read(func(d *store.Data) { pw = d.Server("s1").Password })
	if pw != "secret-1" {
		t.Fatalf("read-only store should decrypt secrets: %q", pw)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "store.json"))
	// 窗口里的修改（例如记录主机密钥）只留在内存里
	_ = win.Update(func(d *store.Data) error {
		d.Server("s1").HostKey = "ssh-ed25519 AAAA"
		return nil
	})
	win.AddRun(&store.RunRecord{ID: "r1", Status: "running"})
	after, _ := os.ReadFile(filepath.Join(dir, "store.json"))
	if string(before) != string(after) {
		t.Fatal("a read-only store must never write the data file")
	}
	if _, err := os.Stat(filepath.Join(dir, "runs.json")); err == nil {
		t.Fatal("a read-only store must not write run history")
	}
	if win.ReloadIfChanged() {
		t.Fatal("nothing changed on disk yet")
	}
	// 主窗口改了服务器配置：窗口重新读取
	time.Sleep(20 * time.Millisecond)
	_ = main.Update(func(d *store.Data) error {
		d.Server("s1").Host = "10.0.0.2"
		d.Servers = append(d.Servers, &store.Server{ID: "s2", Name: "db-01"})
		return nil
	})
	waitFor(t, "reload", win.ReloadIfChanged)
	var host string
	var n int
	win.Read(func(d *store.Data) { host, n = d.Server("s1").Host, len(d.Servers) })
	if host != "10.0.0.2" || n != 2 {
		t.Fatalf("reloaded data: %s %d", host, n)
	}
	if main.ReloadIfChanged() {
		t.Fatal("the main store never reloads from disk")
	}
}

func TestWindowState(t *testing.T) {
	dir := t.TempDir()
	if ws := loadWindowState(dir, "main"); ws.Width != 1400 || ws.Height != 880 || ws.Maximised {
		t.Fatalf("default main window: %+v", ws)
	}
	saveWindowState(dir, "main", windowState{Width: 900, Height: 600, Maximised: true})
	saveWindowState(dir, "files", windowState{Width: 700, Height: 500})
	if ws := loadWindowState(dir, "main"); ws.Width != 900 || ws.Height != 600 || !ws.Maximised {
		t.Fatalf("main window: %+v", ws)
	}
	if ws := loadWindowState(dir, "files"); ws.Width != 700 || ws.Height != 500 {
		t.Fatalf("files window: %+v", ws)
	}
	// 损坏或不合理的记录回到默认值
	saveWindowState(dir, "files", windowState{Width: 10, Height: 10})
	if ws := loadWindowState(dir, "files"); ws.Width != 1080 {
		t.Fatalf("too small a size should be ignored: %+v", ws)
	}
	_ = os.WriteFile(windowStatePath(dir), []byte("{broken"), 0o600)
	if ws := loadWindowState(dir, "main"); ws.Width != 1400 {
		t.Fatalf("broken file: %+v", ws)
	}
	// 默认数据目录沿用旧版本的 WebView2 目录，其余情况放在数据目录里
	if base, err := os.UserConfigDir(); err == nil {
		if got := webviewDataPath(filepath.Join(base, "SSHPilot")); got != filepath.Join(base, "SSHPilot.exe") {
			t.Fatalf("default webview path: %s", got)
		}
	}
	if got := webviewDataPath(dir); got != filepath.Join(dir, "webview") {
		t.Fatalf("custom webview path: %s", got)
	}
}
