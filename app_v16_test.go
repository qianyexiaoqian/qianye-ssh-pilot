package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sshpilot/internal/agent"
	"sshpilot/internal/testsshd"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 48, 32))
	for i := range img.Pix {
		img.Pix[i] = uint8(i * 3)
	}
	img.Set(1, 1, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// waitTurn 等一轮对话结束；期间出现需要批准的操作时交给 approve 处理。
func waitTurn(t *testing.T, a *App, sessionID string, approve func(agent.ApprovalRequest)) {
	t.Helper()
	seen := map[string]bool{}
	waitFor(t, "对话结束", func() bool {
		for _, p := range a.agent.PendingApprovals(sessionID) {
			if !seen[p.ID] {
				seen[p.ID] = true
				if approve == nil {
					t.Errorf("unexpected approval request: %s", p.Title)
					_ = a.AgentResolveApproval(p.ID, false, false)
				} else {
					approve(p)
				}
			}
		}
		return !a.agent.IsRunning(sessionID)
	})
}

func lastAssistant(t *testing.T, a *App, sessionID string) string {
	t.Helper()
	v, err := a.AgentGetSession(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(v.Messages) - 1; i >= 0; i-- {
		if m := v.Messages[i]; m.Role == "assistant" && m.Kind == "" && m.Content != "" {
			return m.Content
		}
	}
	return ""
}

func TestChatAttachments(t *testing.T) {
	srv := testsshd.Start(t)
	a := newTestApp(t)
	s, err := a.SaveServer(ServerInput{Name: "web-01", Host: srv.Host, Port: srv.Port, Username: srv.User, AuthType: "password", Password: srv.Password})
	if err != nil {
		t.Fatal(err)
	}
	home, err := a.FilesList(s.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	oldDir := mockUploadDir
	mockUploadDir = home.Path + "/uploads/"
	t.Cleanup(func() { mockUploadDir = oldDir })
	mock := startMockModel(t)
	settings := a.settings()
	settings.AI.Provider, settings.AI.Preset, settings.AI.BaseURL, settings.AI.Model = "openai", "custom", mock+"/v1", "mock-ops"
	if _, err := a.SaveSettings(settings, "mock-key", false); err != nil {
		t.Fatal(err)
	}

	local := t.TempDir()
	pkg := filepath.Join(local, "app-1.2.tar.gz")
	pkgData := append([]byte{0x1f, 0x8b, 0x08, 0, 0}, bytes.Repeat([]byte{0, 0xfe, 0x42}, 30000)...)
	_ = os.WriteFile(pkg, pkgData, 0o600)
	conf := filepath.Join(local, "nginx.conf")
	_ = os.WriteFile(conf, []byte("server {\n  listen 8080;\n}\n"), 0o600)
	logFile := filepath.Join(local, "access.log")
	var lines []string
	for i := 1; i <= 3000; i++ {
		lines = append(lines, "10.0.0.8 GET /api/health 200 #"+strings.Repeat("x", i%7))
	}
	_ = os.WriteFile(logFile, []byte(strings.Join(lines, "\r\n")), 0o600)

	// 添加附件：文件夹和不存在的路径各给一句原因，其余的照常添加
	res, err := a.AgentAttachPaths([]string{pkg, local, conf, filepath.Join(local, "missing.txt"), logFile})
	if err != nil || len(res.Added) != 3 || len(res.Errors) != 2 || !strings.Contains(res.Errors[0], "文件夹") {
		t.Fatalf("attach paths: %+v %v", res, err)
	}
	pkgAtt, confAtt, logAtt := res.Added[0], res.Added[1], res.Added[2]
	if pkgAtt.Text || !confAtt.Text || !logAtt.Text || pkgAtt.Path != pkg || pkgAtt.Size != int64(len(pkgData)) {
		t.Fatalf("attachments: %+v", res.Added)
	}
	shot, err := a.AgentAttachData("image.png", base64.StdEncoding.EncodeToString(tinyPNG(t)))
	if err != nil || shot.Kind != agent.AttachImage || shot.Width != 48 || shot.Height != 32 {
		t.Fatalf("pasted image: %+v %v", shot, err)
	}
	if _, err := a.AgentAttachData("x.png", "不是 base64"); err == nil {
		t.Error("invalid base64 should fail")
	}

	// 界面通过 /attachment/... 显示图片；文件不提供下载，别的路径一律 404
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		a.assetHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	if rec := get("/attachment/draft/" + shot.ID); rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" || !bytes.Equal(rec.Body.Bytes(), tinyPNG(t)) {
		t.Errorf("draft image: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	for _, p := range []string{"/attachment/draft/" + confAtt.ID, "/attachment/draft/ffffffffffffffff", "/attachment/..%2f..%2fstore.json/x", "/other"} {
		if rec := get(p); rec.Code != 404 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}

	// 让 AI 把安装包传到服务器：先征求同意，批准后文件出现在服务器上
	sess, _ := a.AgentNewSession("")
	if err := a.AgentSend(sess.ID, "把这个安装包上传到 web-01", []string{pkgAtt.ID}); err != nil {
		t.Fatal(err)
	}
	asked := 0
	waitTurn(t, a, sess.ID, func(p agent.ApprovalRequest) {
		asked++
		if p.Tool != "upload_attachment" || p.Risk != agent.RiskChange || !strings.Contains(p.Detail, pkg) || !strings.Contains(p.Detail, mockUploadDir) || len(p.Servers) != 1 || p.Servers[0] != "web-01" {
			t.Errorf("approval: %+v", p)
		}
		if err := a.AgentResolveApproval(p.ID, true, false); err != nil {
			t.Error(err)
		}
	})
	if asked != 1 {
		t.Fatalf("expected one approval, got %d", asked)
	}
	uploaded := filepath.Join(srv.Home, "uploads", "app-1.2.tar.gz")
	if b, err := os.ReadFile(uploaded); err != nil || !bytes.Equal(b, pkgData) {
		t.Fatalf("uploaded file: %v (%d bytes)", err, len(b))
	}
	if got := lastAssistant(t, a, sess.ID); !strings.Contains(got, "已经传上去了") {
		t.Errorf("final reply: %q", got)
	}
	view, _ := a.AgentGetSession(sess.ID)
	if m := view.Messages[0]; m.Display != "把这个安装包上传到 web-01" || len(m.Attachments) != 1 || m.Attachments[0].Name != "app-1.2.tar.gz" || strings.Contains(m.Display, "<attachments>") {
		t.Errorf("user message for the UI: %+v", m)
	}

	// 拒绝：文件不上传，AI 得知被拒绝
	_ = os.Remove(uploaded)
	res, _ = a.AgentAttachPaths([]string{pkg})
	sess2, _ := a.AgentNewSession("")
	_ = a.AgentSend(sess2.ID, "部署一下", []string{res.Added[0].ID})
	waitTurn(t, a, sess2.ID, func(p agent.ApprovalRequest) { _ = a.AgentResolveApproval(p.ID, false, false) })
	if _, err := os.Stat(uploaded); err == nil {
		t.Error("a rejected upload must not reach the server")
	}

	// 较长的文本文件：内容不直接附在消息里，AI 用 read_attachment 分段读
	sess3, _ := a.AgentNewSession("")
	_ = a.AgentSend(sess3.ID, "看看这个日志", []string{logAtt.ID})
	waitTurn(t, a, sess3.ID, nil)
	view, _ = a.AgentGetSession(sess3.ID)
	var read string
	for _, m := range view.Messages {
		if m.Role == "tool" && m.ToolName == "read_attachment" {
			read = m.Content
		}
	}
	if !strings.Contains(read, "access.log（第 1–20 行，共 3000 行；后面还有，接着读请把 start_line 设为 21）") || strings.Contains(read, "\r") || strings.Count(read, "GET /api/health") != 20 {
		t.Errorf("read_attachment result: %.300q", read)
	}

	// 图片：随消息发给模型，界面能按会话取到；发出后不再是草稿
	sess4, _ := a.AgentNewSession("")
	_ = a.AgentSend(sess4.ID, "", []string{shot.ID, confAtt.ID})
	waitTurn(t, a, sess4.ID, nil)
	if got := lastAssistant(t, a, sess4.ID); !strings.Contains(got, "1 张图片") {
		t.Errorf("reply about the image: %q", got)
	}
	if rec := get("/attachment/" + sess4.ID + "/" + shot.ID); rec.Code != 200 || rec.Body.Len() == 0 {
		t.Errorf("sent image: %d", rec.Code)
	}
	if rec := get("/attachment/draft/" + shot.ID); rec.Code != 404 {
		t.Errorf("a sent attachment should no longer be a draft: %d", rec.Code)
	}
	if err := a.AgentSend(sess4.ID, "再来", []string{shot.ID}); err == nil {
		t.Error("an attachment cannot be sent twice")
	}
	if a.agent.IsRunning(sess4.ID) {
		t.Error("a failed send must not leave the session running")
	}

	// 不支持图片的模型：照常回复，并在对话里说明图片没有发出去
	settings.AI.Model = "mock-novision"
	if _, err := a.SaveSettings(settings, "", true); err != nil {
		t.Fatal(err)
	}
	shot2, _ := a.AgentAttachData("image.png", base64.StdEncoding.EncodeToString(tinyPNG(t)))
	_ = a.AgentSend(sess4.ID, "这张图里是什么", []string{shot2.ID})
	waitTurn(t, a, sess4.ID, nil)
	view, _ = a.AgentGetSession(sess4.ID)
	notice := ""
	for _, m := range view.Messages {
		if m.Kind == "notice" {
			notice = m.Content
		}
	}
	if !strings.Contains(notice, "mock-novision") || !strings.Contains(notice, "没有接受图片") {
		t.Errorf("notice = %q", notice)
	}
	if got := lastAssistant(t, a, sess4.ID); !strings.Contains(got, "没有收到内容") {
		t.Errorf("reply from a model without vision: %q", got)
	}

	// 直接调用工具：各种不能读、不能传的情况
	saved, _ := a.agent.Sessions.Load(sess.ID)
	saved.Messages = append(saved.Messages, agent.Message{Role: "user", Attachments: []agent.Attachment{confAtt, shot}})
	saved.ApproveAll = true
	tc := &agent.ToolContext{Agent: a.agent, Session: saved}
	tool := func(name string, args map[string]any) (*agent.ToolResult, error) {
		for _, x := range a.opsTools() {
			if x.Name == name {
				raw, _ := json.Marshal(args)
				return x.Handler(context.Background(), tc, raw)
			}
		}
		t.Fatalf("tool %s not found", name)
		return nil, nil
	}
	if r, err := tool("read_attachment", map[string]any{"attachment": "nginx.conf"}); err != nil || !strings.Contains(r.Content, "listen 8080;") || !strings.Contains(r.Content, "第 1–3 行，共 3 行）") {
		t.Errorf("read by name: %v %+v", err, r)
	}
	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"attachment": pkgAtt.ID}, "二进制文件"},
		{map[string]any{"attachment": shot.ID}, "是图片"},
		{map[string]any{"attachment": "nope.txt"}, "没有找到附件"},
		{map[string]any{"attachment": confAtt.ID, "start_line": 99}, "一共只有 3 行"},
	} {
		if _, err := tool("read_attachment", c.args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("read_attachment %v: %v", c.args, err)
		}
	}
	// 指定完整的文件路径和权限；目标是已有的目录时放进目录里
	r, err := tool("upload_attachment", map[string]any{"attachment": confAtt.ID, "servers": []string{"web-01"}, "path": home.Path + "/etc/site.conf", "mode": "640"})
	if err != nil || r.IsError || !strings.Contains(r.Content, "已上传到 "+home.Path+"/etc/site.conf") {
		t.Fatalf("upload to a file path: %v %+v", err, r)
	}
	if b, _ := os.ReadFile(filepath.Join(srv.Home, "etc", "site.conf")); !strings.Contains(string(b), "listen 8080;") {
		t.Errorf("uploaded conf = %q", b)
	}
	if r, err = tool("upload_attachment", map[string]any{"attachment": confAtt.ID, "servers": []string{"web-01"}, "path": home.Path + "/etc"}); err != nil || !strings.Contains(r.Content, "/etc/nginx.conf") {
		t.Errorf("upload into an existing directory: %v %+v", err, r)
	}
	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"attachment": confAtt.ID, "servers": []string{"web-01"}, "path": "relative/x"}, "绝对路径"},
		{map[string]any{"attachment": confAtt.ID, "servers": []string{"nope"}, "path": "/tmp/x"}, "未找到服务器"},
		{map[string]any{"attachment": confAtt.ID, "servers": []string{"web-01"}, "path": "/tmp/x", "mode": "abc"}, "权限"},
	} {
		if _, err := tool("upload_attachment", c.args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("upload_attachment %v: %v", c.args, err)
		}
	}
	// 原文件被用户删掉以后：说清楚文件不在了，而不是报一个看不懂的错误
	_ = os.Remove(pkg)
	if _, err := tool("upload_attachment", map[string]any{"attachment": pkgAtt.ID, "servers": []string{"web-01"}, "path": home.Path + "/"}); err == nil || !strings.Contains(err.Error(), "不在原来的位置") {
		t.Errorf("upload of a removed file: %v", err)
	}

	// 删除对话时，保存的图片一起删除
	dir := filepath.Join(a.dataDir, "attachments", sess4.ID)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("session attachments dir: %v", err)
	}
	if err := a.AgentDeleteSession(sess4.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("attachments should be removed with the session")
	}
	// 没有发出去的附件可以丢弃
	extra, _ := a.AgentAttachData("image.png", base64.StdEncoding.EncodeToString(tinyPNG(t)))
	a.AgentDiscardAttachment(extra.ID)
	if rec := get("/attachment/draft/" + extra.ID); rec.Code != 404 {
		t.Errorf("discarded draft: %d", rec.Code)
	}
}

func TestResizableClient(t *testing.T) {
	win := winRect{Left: 100, Top: 100, Right: 1500, Bottom: 980}
	// 150% 缩放时系统边框是 12 像素，标题栏另算
	def := winRect{Left: 112, Top: 146, Right: 1488, Bottom: 968}
	got, ok := resizableClient(win, def)
	if want := (winRect{Left: 112, Top: 100, Right: 1488, Bottom: 969}); !ok || got != want {
		t.Errorf("client = %+v, ok=%v", got, ok)
	}
	// 系统没有给出边框（或者给出的结果不合理）时保持原样
	for _, bad := range []winRect{win, {Left: 112, Top: 146, Right: 100, Bottom: 968}, {Left: 112, Top: 146, Right: 1488, Bottom: 90}} {
		if got, ok := resizableClient(win, bad); ok || got != win {
			t.Errorf("def %+v: client = %+v, ok=%v", bad, got, ok)
		}
	}
}
