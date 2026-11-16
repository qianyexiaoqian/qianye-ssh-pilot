package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sshpilot/internal/store"
)

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// 长期记忆接入对话流程：memory 工具的 longterm / archive、对话要点自动存档、下一次对话自动召回、recall 工具。
func TestAutoRecallAndLongTermTools(t *testing.T) {
	servers := map[string]string{"id-web": "web-01", "id-db": "db-01"}
	fp := &fakeProvider{steps: []func(ChatRequest) *ChatResponse{
		// 会话 1
		toolCall("t1", "memory", `{"target":"longterm","action":"add","content":"Nginx 把请求转发到本机 8080 的 Java 服务，502 多半是后端进程被 OOM 杀掉","servers":["web-01"]}`),
		toolCall("t2", "memory", `{"target":"memory","action":"add","content":"生产环境改配置前先备份"}`),
		toolCall("t3", "memory", `{"target":"memory","action":"archive","old_text":"改配置前先备份"}`),
		toolCall("t4", "memory", `{"target":"longterm","action":"add","content":"随便写点什么","servers":["no-such-server"]}`),
		toolCall("t5", "run_command", `{"command":"systemctl restart app\necho done"}`),
		reply("结论：web-01 的 502 是后端 Java 进程被 OOM 杀掉导致的，已经重启 app 服务，访问恢复正常。"),
		// 会话 2，第一轮
		toolCall("t6", "recall", `{"query":"502 的原因","servers":["web-01"]}`),
		toolCall("t7", "memory", `{"target":"longterm","action":"patch","old_text":"多半是后端进程被 OOM 杀掉","content":"多半是后端进程被 OOM 杀掉（JVM 堆已经调大到 4G）"}`),
		reply("上次的原因是后端 Java 进程被 OOM 杀掉，当时重启 app 服务后恢复；这次建议先看 dmesg 里有没有 OOM 记录。"),
	}}
	old := providerFactory
	providerFactory = func(store.AISettings) (Provider, error) { return fp, nil }
	t.Cleanup(func() { providerFactory = old })
	events := make(chan Event, 512)
	embed := store.EmbedSettings{RecallCount: 6}
	a := New(Config{
		DataDir: t.TempDir(),
		Settings: func() store.AISettings {
			return store.AISettings{Provider: "anthropic", APIKey: "k", Model: "m", ApprovalMode: "auto", MaxSteps: 10}
		},
		EnvSnapshot: func() string { return "服务器（共 2 台）：web-01、db-01" },
		Emit: func(name string, data ...any) {
			if e, ok := data[0].(Event); ok {
				events <- e
			}
		},
		Tools: []*Tool{{Name: "run_command", Description: "执行命令", Schema: obj(map[string]any{"command": str("命令")}),
			Handler: func(_ context.Context, _ *ToolContext, _ json.RawMessage) (*ToolResult, error) {
				return &ToolResult{Content: "done"}, nil
			}}},
		Embed:   func() store.EmbedSettings { return embed },
		Servers: func() map[string]string { return servers },
		ResolveServer: func(ref string) (string, bool) {
			for id, name := range servers {
				if ref == id || ref == name {
					return id, true
				}
			}
			return "", false
		},
	})
	t.Cleanup(a.Close)

	// ---- 会话 1：在 web-01 的终端里发起 ----
	s1, _ := a.NewSessionFor("id-web", "# 当前终端\n服务器 web-01")
	q1 := "nginx 报 502 了，帮我看看是什么原因"
	if err := a.Send(s1.ID, q1); err != nil {
		t.Fatal(err)
	}
	results := map[string]Message{}
	waitFor(t, events, "done", func(e Event) {
		if e.Type == "tool_end" {
			results[e.Message.ToolCallID] = *e.Message
		}
		if e.Type == "recall" {
			t.Fatalf("nothing to recall yet: %+v", e.Message)
		}
	})
	if got := fp.reqs[0].Messages[0].Content; got != q1 {
		t.Fatalf("first message should be sent unchanged: %q", got)
	}
	for id, want := range map[string]string{"t1": "已存入长期记忆", "t3": "已从核心记忆移到长期记忆"} {
		if r := results[id]; r.IsError || !strings.Contains(r.Content, want) {
			t.Fatalf("%s: %+v", id, r)
		}
	}
	if r := results["t4"]; !r.IsError || !strings.Contains(r.Content, "未找到服务器 no-such-server") || !strings.Contains(r.Content, "db-01、web-01") {
		t.Fatalf("an unknown server must be rejected with the list of valid names: %+v", r)
	}
	facts := a.Long.List(KindFact)
	if len(facts) != 2 {
		t.Fatalf("long-term facts: %+v", facts)
	}
	byText := func(part string) MemItem {
		t.Helper()
		for _, it := range a.Long.List("") {
			if strings.Contains(it.Text, part) {
				return it
			}
		}
		t.Fatalf("no long-term item containing %q", part)
		return MemItem{}
	}
	if it := byText("8080"); len(it.Servers) != 1 || it.Servers[0] != "id-web" || it.Source != "agent" || it.SessionID != s1.ID {
		t.Fatalf("scoped fact: %+v", it)
	}
	if it := byText("改配置前先备份"); len(it.Servers) != 0 || it.Source != "archive" {
		t.Fatalf("archived entry: %+v", it)
	}
	if v := a.Memory.View("memory"); len(v.Entries) != 0 {
		t.Fatalf("the archived entry should have left the core memory: %+v", v.Entries)
	}
	var kinds []string
	for _, l := range a.Learn.Recent(0) {
		kinds = append(kinds, l.Kind+"/"+l.Action)
	}
	if got := strings.Join(kinds, " "); got != "longterm/archive memory/add longterm/add" {
		t.Fatalf("learning log: %s", got)
	}
	// 这一轮的“问题 → 操作 → 结论”自动存档，并标注了会话绑定的服务器
	waitUntil(t, "episode", func() bool { return len(a.Long.List(KindEpisode)) == 1 })
	ep := a.Long.List(KindEpisode)[0]
	if ep.ID != episodeID(s1.ID, 1) || ep.Servers[0] != "id-web" || ep.Title == "" ||
		!strings.Contains(ep.Text, "问题："+q1) || !strings.Contains(ep.Text, "操作：systemctl restart app") || !strings.Contains(ep.Text, "结论：结论：web-01 的 502") {
		t.Fatalf("episode: %+v", ep)
	}

	// ---- 会话 2：没有绑定服务器，问题里提到了 web-01 ----
	s2, _ := a.NewSession()
	q2 := "web-01 的 nginx 又报 502 了，上次是什么原因？"
	if err := a.Send(s2.ID, q2); err != nil {
		t.Fatal(err)
	}
	var recalled *Message
	results = map[string]Message{}
	waitFor(t, events, "done", func(e Event) {
		if e.Type == "recall" {
			recalled = e.Message
		}
		if e.Type == "tool_end" {
			results[e.Message.ToolCallID] = *e.Message
		}
	})
	if recalled == nil || recalled.Display != q2 {
		t.Fatalf("recall event: %+v", recalled)
	}
	sent := fp.reqs[6].Messages[0].Content
	for _, want := range []string{q2 + "\n\n<system-reminder>", "【记忆】（web-01）Nginx 把请求转发到本机 8080", "【往期对话 ", "《nginx 报 502 了", "OOM 杀掉导致的", "</system-reminder>"} {
		if !strings.Contains(sent, want) {
			t.Fatalf("the model should receive the recalled memory (%q missing):\n%s", want, sent)
		}
	}
	if strings.Contains(sent, "改配置前先备份") {
		t.Fatalf("unrelated memory must not be attached:\n%s", sent)
	}
	items, _ := recalled.Meta.(map[string]any)["recall"].([]RecallItem)
	if len(items) < 2 || items[0].Kind != KindFact || items[0].Servers[0] != "web-01" || items[0].Score <= 0 {
		t.Fatalf("recall records for the UI: %+v", items)
	}
	// recall 工具：按意思检索，结果带服务器标注和往期对话
	if r := results["t6"]; r.IsError || !strings.Contains(r.Content, "[记忆（web-01）") || !strings.Contains(r.Content, "8080") {
		t.Fatalf("recall tool: %+v", r)
	}
	if r := results["t7"]; r.IsError || !strings.Contains(byText("8080").Text, "JVM 堆已经调大到 4G") {
		t.Fatalf("patch on long-term memory: %+v / %+v", r, byText("8080"))
	}
	waitUntil(t, "second episode", func() bool { return len(a.Long.List(KindEpisode)) == 2 })

	// 同一个会话的第二轮：已经附带过的内容不再重复
	q3 := "那 web-01 的 nginx 502 以后还会再出现吗"
	if err := a.Send(s2.ID, q3); err != nil {
		t.Fatal(err)
	}
	waitFor(t, events, "done", func(e Event) {
		if e.Type == "recall" {
			t.Fatalf("already recalled items must not be attached again: %s", e.Message.Content)
		}
	})
	if got := fp.reqs[9].Messages; got[len(got)-1].Content != q3 {
		t.Fatalf("second turn should be sent unchanged: %q", got[len(got)-1].Content)
	}
	// 历史记录、复盘与检索看到的仍然是用户的原话
	saved, _ := a.Sessions.Load(s2.ID)
	if saved.Messages[0].Display != q2 || !strings.Contains(saved.Messages[0].Content, "system-reminder") || len(saved.Recalled) < 2 {
		t.Fatalf("stored message: %+v recalled=%v", saved.Messages[0], saved.Recalled)
	}
	if tr := Transcript(saved.Messages, 0); strings.Contains(tr, "system-reminder") {
		t.Fatalf("the review transcript must not contain the recall block:\n%s", tr)
	}

	// 关闭自动召回后不再附带
	embed.RecallOff = true
	s3, _ := a.NewSession()
	if err := a.Send(s3.ID, q2); err != nil {
		t.Fatal(err)
	}
	waitFor(t, events, "done", func(e Event) {
		if e.Type == "recall" {
			t.Fatal("recall is switched off")
		}
	})
	embed.RecallOff = false

	// 历史会话的要点可以补建；删除会话时一并删除它的要点
	for _, it := range a.Long.List(KindEpisode) {
		_ = a.Long.Delete(it.ID)
	}
	if n := a.SyncEpisodes(); n != 2 || len(a.Long.List(KindEpisode)) != 2 {
		t.Fatalf("backfilled episodes: %d", n)
	}
	if n := a.SyncEpisodes(); n != 0 {
		t.Fatalf("second backfill should add nothing: %d", n)
	}
	if err := a.DeleteSession(s1.ID); err != nil {
		t.Fatal(err)
	}
	if eps := a.Long.List(KindEpisode); len(eps) != 1 || eps[0].SessionID != s2.ID {
		t.Fatalf("episodes after deleting a session: %+v", eps)
	}
	// 复盘时能看到相关的长期记忆，避免重复保存
	if rel := a.relatedMemories(t.Context(), saved, saved.Messages); !strings.Contains(rel, "（web-01）") || !strings.Contains(rel, "8080") {
		t.Fatalf("related memories for review: %s", rel)
	}
}

func TestMemoryArchiveAndTake(t *testing.T) {
	m := NewMemoryStore(t.TempDir())
	for _, e := range []string{"web-01 是生产入口", "db-01 的数据盘在 /data", "web-02 是备用入口"} {
		if _, err := m.Apply("memory", "add", e, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Take("memory", "入口"); err == nil {
		t.Fatal("an ambiguous fragment must be rejected")
	}
	got, err := m.Take("memory", "数据盘")
	if err != nil || got != "db-01 的数据盘在 /data" || len(m.View("memory").Entries) != 2 {
		t.Fatalf("take: %q %v %v", got, err, m.View("memory").Entries)
	}
	if _, err := m.Take("memory", "不存在"); err == nil {
		t.Fatal("a missing entry must be reported")
	}
	// 容量满时的提示指向长期记忆
	_, err = m.Apply("memory", "add", strings.Repeat("很长的内容", 700), "")
	if err == nil || !strings.Contains(err.Error(), "longterm") || !strings.Contains(err.Error(), "archive") {
		t.Fatalf("capacity error should point to long-term memory: %v", err)
	}
}

// 文件管理独立窗口的进程只读取主窗口的数据：不写长期记忆、不播种内置技能、不建立索引。
func TestReadOnlyAgentDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{DataDir: dir, Settings: func() store.AISettings { return store.AISettings{} }}
	main := New(cfg)
	if _, err := main.Long.AddFact(t.Context(), "Nginx 证书统一放在 /etc/nginx/ssl", nil, "user", ""); err != nil {
		t.Fatal(err)
	}
	main.Close()
	before, err := os.ReadFile(filepath.Join(dir, "memory", "longterm.json"))
	if err != nil {
		t.Fatal(err)
	}

	cfg.ReadOnly = true
	child := New(cfg)
	if hits := child.Long.Search(t.Context(), "nginx 证书在哪", SearchOptions{}); len(hits) != 1 {
		t.Fatalf("a read-only agent can still read the memory: %+v", hits)
	}
	if _, err := child.Long.AddFact(t.Context(), "只存在于内存里的一条", nil, "user", ""); err != nil {
		t.Fatal(err)
	}
	child.Close()
	after, _ := os.ReadFile(filepath.Join(dir, "memory", "longterm.json"))
	if string(before) != string(after) {
		t.Fatal("a read-only agent must not write the long-term memory file")
	}

	fresh := t.TempDir()
	New(Config{DataDir: fresh, ReadOnly: true, Settings: cfg.Settings})
	if entries, _ := os.ReadDir(filepath.Join(fresh, "skills")); len(entries) != 0 {
		t.Fatalf("a read-only agent must not seed built-in skills: %v", entries)
	}
}

// 用户的消息里带着密码时不做召回（检索需要把这段文字发给向量接口）。
func TestRecallSkipsSecrets(t *testing.T) {
	a := New(Config{DataDir: t.TempDir(), Settings: func() store.AISettings { return store.AISettings{} }})
	t.Cleanup(a.Close)
	if _, err := a.Long.AddFact(t.Context(), "Nginx 证书统一放在 /etc/nginx/ssl，由 certbot 续期", nil, "user", ""); err != nil {
		t.Fatal(err)
	}
	sess := &Session{ID: "s"}
	if block, items := a.recall(t.Context(), sess, "nginx 的证书放在哪个目录"); block == "" || len(items) != 1 {
		t.Fatalf("a normal question should recall the memory: %q", block)
	}
	sess = &Session{ID: "s2"}
	if block, _ := a.recall(t.Context(), sess, "nginx 的证书放在哪个目录？root 的密码: Abcd1234!"); block != "" {
		t.Fatalf("a message carrying a password must not be sent for retrieval: %q", block)
	}
	if block, _ := a.recall(t.Context(), &Session{ID: "s3"}, "继续"); block != "" {
		t.Fatal("very short messages are not worth a lookup")
	}
}

func TestPlainText(t *testing.T) {
	md := "## 巡检结论\n\n| 服务器 | 状态 |\n|---|:---:|\n| web-01 | **在线**，负载偏高 |\n| db-01 | 在线 |\n\n可以用下面的命令查看：\n\n```bash\nsystemctl status sshd --no-pager\n```\n\n- 第一步：`df -h`\n1. 然后清理 a|b 不是表格"
	got := plainText(md)
	want := "巡检结论 服务器、状态 web-01、在线，负载偏高 db-01、在线 可以用下面的命令查看： systemctl status sshd --no-pager 第一步：df -h 然后清理 a、b 不是表格"
	if got != want {
		t.Fatalf("plain text:\n got %q\nwant %q", got, want)
	}
	if plainText("普通的一句话，没有任何标记。") != "普通的一句话，没有任何标记。" {
		t.Fatal("plain sentences must stay as they are")
	}
}
