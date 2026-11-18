package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"sshpilot/internal/agent"
	"sshpilot/internal/store"
	"sshpilot/internal/testsshd"
)

func TestAIManagesDeployTargets(t *testing.T) {
	srv := testsshd.Start(t)
	a := newTestApp(t)
	s, err := a.SaveServer(ServerInput{Name: "web-01", Host: srv.Host, Port: srv.Port, Username: srv.User, AuthType: "password", Password: srv.Password})
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM := selfSigned(t, "node.example.com")
	cv, err := a.ImportCert(ImportCertRequest{CertPEM: certPEM, KeyPEM: keyPEM})
	if err != nil {
		t.Fatal(err)
	}
	dir := "/tmp/sshpilot-ai-" + time.Now().Format("150405.000000")
	tc := &agent.ToolContext{Agent: a.agent, Session: &agent.Session{ID: "test", ApproveAll: true}}
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
	targets := func() []TargetView {
		for _, c := range a.ListCerts() {
			if c.ID == cv.ID {
				return c.Targets
			}
		}
		return nil
	}

	// 没有部署目标时，部署工具告诉模型自己去添加，而不是让用户去页面上填
	if _, err := tool("deploy_certificate", map[string]any{"certificate": "node.example.com"}); err == nil || !strings.Contains(err.Error(), "save_deploy_target") || strings.Contains(err.Error(), "请先在") {
		t.Fatalf("deploy without targets: %v", err)
	}
	if r, _ := tool("list_certificates", map[string]any{}); !strings.Contains(r.Content, "没有部署目标：需要部署时用 save_deploy_target 添加") {
		t.Errorf("list without targets: %s", r.Content)
	}

	// 参数不全或不合法：说清楚缺什么，什么都不保存
	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"certificate": "nope.example.com", "server": "web-01", "cert_path": dir + "/a.pem", "key_path": dir + "/a.key"}, "没有找到证书"},
		{map[string]any{"certificate": cv.ID, "cert_path": dir + "/a.pem", "key_path": dir + "/a.key"}, "请指定服务器"},
		{map[string]any{"certificate": cv.ID, "server": "nope", "cert_path": dir + "/a.pem", "key_path": dir + "/a.key"}, "未找到服务器"},
		{map[string]any{"certificate": cv.ID, "server": "web-01"}, "请填写 cert_path"},
		{map[string]any{"certificate": cv.ID, "server": "web-01", "cert_path": dir + "/a.pem"}, "请填写 key_path"},
		{map[string]any{"certificate": cv.ID, "server": "web-01", "cert_path": "relative/a.pem", "key_path": dir + "/a.key"}, "绝对路径"},
		{map[string]any{"certificate": cv.ID, "server": "web-01", "cert_path": dir + "/a.pem", "key_path": dir + "/a.key", "services": []string{"nginx; rm -rf /"}}, "服务名"},
		{map[string]any{"certificate": cv.ID, "server": "web-01", "cert_path": dir + "/a.pem", "key_path": dir + "/a.key", "key_mode": "rw-------"}, "八进制"},
		{map[string]any{"certificate": cv.ID, "server": "web-01", "cert_path": dir + "/a.pem", "key_path": dir + "/a.key", "owner": "root;id"}, "owner"},
		{map[string]any{"certificate": cv.ID, "server": "web-01", "cert_path": dir + "/a.pem", "key_path": dir + "/a.key", "cert_format": "pkcs12"}, "cert_format"},
		{map[string]any{"certificate": cv.ID, "server": "web-01", "cert_path": dir + "/a.pem", "key_path": dir + "/a.key", "services": []string{"nginx"}, "service_action": "kill"}, "service_action"},
		{map[string]any{"certificate": cv.ID, "target_id": "nope", "post_command": "true"}, "不要填 target_id"},
		{map[string]any{"certificate": cv.ID, "action": "remove"}, "需要填 target_id"},
	} {
		if _, err := tool("save_deploy_target", c.args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v", c.args, err)
		}
	}
	if n := len(targets()); n != 0 {
		t.Fatalf("invalid calls must not save anything, got %d targets", n)
	}

	// 添加并立即部署：一步完成，证书和私钥出现在服务器上
	r, err := tool("save_deploy_target", map[string]any{"certificate": "node.example.com", "server": "web-01",
		"cert_path": dir + "/fullchain.pem", "key_path": dir + "/privkey.pem", "test_command": "true", "deploy": true})
	if err != nil || r.IsError || !strings.Contains(r.Content, "已添加部署目标") || !strings.Contains(r.Content, "✅ web-01") {
		t.Fatalf("add and deploy: %v %+v", err, r)
	}
	list := targets()
	if len(list) != 1 || !list[0].Synced || list[0].ServerID != s.ID || list[0].Preset != "custom" || list[0].CertFormat != "fullchain" ||
		list[0].CertMode != "644" || list[0].KeyMode != "600" || !list[0].Backup || list[0].ServiceAction != "none" || list[0].VerifyPort != 0 {
		t.Fatalf("saved target: %+v", list)
	}
	id := list[0].ID
	out := callTool(t, a, tc, "run_command", map[string]any{"servers": []string{"web-01"}, "command": "head -n 1 " + dir + "/fullchain.pem " + dir + "/privkey.pem"})
	if !strings.Contains(out.Content, "BEGIN CERTIFICATE") || !strings.Contains(out.Content, "PRIVATE KEY") {
		t.Errorf("deployed files: %s", out.Content)
	}
	// 私钥的内容不会出现在工具返回给模型的文字里
	if strings.Contains(r.Content, "PRIVATE KEY") || strings.Contains(r.Content, strings.Split(keyPEM, "\n")[1]) {
		t.Error("the private key must not be returned to the model")
	}

	// 修改：只写要改的项，其余保持不变
	if r, err = tool("save_deploy_target", map[string]any{"certificate": cv.ID, "target_id": id, "post_command": "true", "verify_port": 8443}); err != nil || !strings.Contains(r.Content, "已修改部署目标") || !strings.Contains(r.Content, "还没有部署") {
		t.Fatalf("modify: %v %+v", err, r)
	}
	if got := targets()[0]; got.ID != id || got.PostCommand != "true" || got.VerifyPort != 8443 || got.CertPath != dir+"/fullchain.pem" || got.KeyPath != dir+"/privkey.pem" || got.TestCommand != "true" || !got.Synced {
		t.Errorf("after modify: %+v", got)
	}
	// 同一台服务器、同一个路径再“添加”一次：当作修改，不会多出一个目标；给了服务没说动作，默认 reload
	if _, err = tool("save_deploy_target", map[string]any{"certificate": cv.ID, "server": "web-01", "cert_path": dir + "/fullchain.pem", "services": []string{"xboard-node", " "}}); err != nil {
		t.Fatal(err)
	}
	if list = targets(); len(list) != 1 || list[0].ID != id || len(list[0].Services) != 1 || list[0].Services[0] != "xboard-node" || list[0].ServiceAction != "reload" || list[0].PostCommand != "true" {
		t.Errorf("same path again: %+v", list)
	}
	if _, err = tool("save_deploy_target", map[string]any{"certificate": cv.ID, "target_id": id, "service_action": "restart"}); err != nil {
		t.Fatal(err)
	}
	if got := targets()[0]; got.ServiceAction != "restart" {
		t.Errorf("service action: %+v", got)
	}
	// 证书列表里能看到目标的完整配置，模型据此判断要不要改
	if r, _ = tool("list_certificates", map[string]any{}); !strings.Contains(r.Content, "目标 "+id) || !strings.Contains(r.Content, "私钥文件："+dir+"/privkey.pem") || !strings.Contains(r.Content, "然后 restart 服务：xboard-node") || !strings.Contains(r.Content, "最后执行：true") {
		t.Errorf("list with target: %s", r.Content)
	}
	// 清空服务：不再重载
	if _, err = tool("save_deploy_target", map[string]any{"certificate": cv.ID, "target_id": id, "services": []string{}}); err != nil {
		t.Fatal(err)
	}
	if got := targets()[0]; len(got.Services) != 0 || got.ServiceAction != "none" {
		t.Errorf("cleared services: %+v", got)
	}
	// HAProxy 这类合并成一个文件的格式不需要私钥路径
	if r, err = tool("save_deploy_target", map[string]any{"certificate": cv.ID, "server": "web-01", "cert_path": dir + "/haproxy.pem", "cert_format": "combined", "key_path": "/ignored.key"}); err != nil {
		t.Fatalf("combined: %v", err)
	}
	if list = targets(); len(list) != 2 || list[1].CertFormat != "combined" || list[1].KeyPath != "" {
		t.Errorf("combined target: %+v", list)
	}
	if _, err = tool("save_deploy_target", map[string]any{"certificate": cv.ID, "target_id": "nope", "post_command": "x"}); err == nil || !strings.Contains(err.Error(), id) {
		t.Errorf("unknown target id should list the existing ones: %v", err)
	}
	// 预设：XBoard 节点没有 reload，部署后重启 xboard-node；用了预设仍然可以单独改其中一项
	if r, err = tool("save_deploy_target", map[string]any{"certificate": cv.ID, "server": "web-01", "preset": "xboard", "cert_path": "/root/cert/fullchain.pem", "key_path": "/root/cert/privkey.pem"}); err != nil {
		t.Fatalf("xboard preset: %v", err)
	}
	if !strings.Contains(r.Content, "已添加部署目标") {
		t.Errorf("xboard preset result: %s", r.Content)
	}
	list = targets()
	xb := list[len(list)-1]
	if xb.Preset != "xboard" || len(xb.Services) != 1 || xb.Services[0] != "xboard-node" || xb.ServiceAction != "restart" || xb.TestCommand != "" || xb.CertFormat != "fullchain" || xb.KeyPath != "/root/cert/privkey.pem" {
		t.Errorf("xboard target: %+v", xb)
	}
	if _, err = tool("save_deploy_target", map[string]any{"certificate": cv.ID, "target_id": xb.ID, "preset": "nginx", "services": []string{"openresty"}}); err != nil {
		t.Fatal(err)
	}
	for _, x := range targets() {
		if x.ID == xb.ID && (x.Preset != "nginx" || x.TestCommand != "nginx -t" || x.Services[0] != "openresty" || x.ServiceAction != "reload" || x.CertPath != "/root/cert/fullchain.pem") {
			t.Errorf("preset with an override: %+v", x)
		}
	}
	if _, err = tool("save_deploy_target", map[string]any{"certificate": cv.ID, "server": "web-01", "preset": "caddy", "cert_path": dir + "/c.pem", "key_path": dir + "/c.key"}); err == nil || !strings.Contains(err.Error(), "xboard") {
		t.Errorf("unknown preset: %v", err)
	}
	if _, err = tool("save_deploy_target", map[string]any{"certificate": cv.ID, "action": "remove", "target_id": xb.ID}); err != nil {
		t.Fatal(err)
	}
	list = targets()

	// 关键路径按高危处理，即使用户选了「本会话全部允许」也要单独确认；这里没有人确认，所以不会保存
	strict := &agent.ToolContext{Agent: a.agent, Session: &agent.Session{ID: "bg"}, Background: true}
	for _, x := range a.opsTools() {
		if x.Name == "save_deploy_target" {
			raw, _ := json.Marshal(map[string]any{"certificate": cv.ID, "server": "web-01", "cert_path": "/etc/ssh/sshd_config", "key_path": dir + "/k.pem"})
			if res, err := x.Handler(context.Background(), strict, raw); err != nil || res.Meta.(map[string]any)["kind"] != "denied" {
				t.Errorf("unapproved save: %v %+v", err, res)
			}
		}
	}
	if len(targets()) != 2 {
		t.Error("an unapproved target must not be saved")
	}

	// 删除
	if r, err = tool("save_deploy_target", map[string]any{"certificate": cv.ID, "action": "remove", "target_id": list[1].ID}); err != nil || !strings.Contains(r.Content, "已删除部署目标") {
		t.Fatalf("remove: %v %+v", err, r)
	}
	if list = targets(); len(list) != 1 || list[0].ID != id {
		t.Errorf("after remove: %+v", list)
	}
}

// 走完整的对话流程：模型发现没有部署目标 → 自己添加并部署 → 用户只批准一次。
func TestAIDeploysCertificateToNewTarget(t *testing.T) {
	srv := testsshd.Start(t)
	a := newTestApp(t)
	if _, err := a.SaveServer(ServerInput{Name: "db-01", Host: srv.Host, Port: srv.Port, Username: srv.User, AuthType: "password", Password: srv.Password}); err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM := selfSigned(t, "api.example.org")
	cv, err := a.ImportCert(ImportCertRequest{CertPEM: certPEM, KeyPEM: keyPEM})
	if err != nil {
		t.Fatal(err)
	}
	dir := "/tmp/sshpilot-ai-" + time.Now().Format("150405.000000")
	old := mockTargetArgs
	mockTargetArgs = `{"certificate":"api.example.org","server":"db-01","cert_path":"` + dir + `/fullchain.pem","key_path":"` + dir + `/privkey.pem","test_command":"true","services":[],"deploy":true}`
	t.Cleanup(func() { mockTargetArgs = old })
	mock := startMockModel(t)
	settings := a.settings()
	settings.AI.Provider, settings.AI.Preset, settings.AI.BaseURL, settings.AI.Model = "openai", "custom", mock+"/v1", "mock-ops"
	if _, err := a.SaveSettings(settings, "mock-key", false); err != nil {
		t.Fatal(err)
	}
	sess, _ := a.AgentNewSession("")
	if err := a.AgentSend(sess.ID, "把 api.example.org 的证书部署到 db-01", nil); err != nil {
		t.Fatal(err)
	}
	var asked []agent.ApprovalRequest
	waitTurn(t, a, sess.ID, func(p agent.ApprovalRequest) {
		asked = append(asked, p)
		_ = a.AgentResolveApproval(p.ID, true, false)
	})
	if len(asked) != 1 {
		t.Fatalf("expected exactly one approval, got %d: %+v", len(asked), asked)
	}
	p := asked[0]
	for _, want := range []string{"证书文件：" + dir + "/fullchain.pem", "私钥文件：" + dir + "/privkey.pem", "部署后先检测配置：true", "不重载任何服务", "保存后立即部署"} {
		if !strings.Contains(p.Detail, want) {
			t.Errorf("approval detail lacks %q:\n%s", want, p.Detail)
		}
	}
	if p.Tool != "save_deploy_target" || p.Title != "添加证书 api.example.org 的部署目标，并立即部署" || len(p.Servers) != 1 || p.Servers[0] != "db-01" || p.Risk != agent.RiskChange {
		t.Errorf("approval: %+v", p)
	}
	var target *store.DeployTarget
	a.store.Read(func(d *store.Data) {
		for _, x := range d.Targets {
			if x.CertID == cv.ID {
				cp := *x
				target = &cp
			}
		}
	})
	if target == nil || target.LastStatus != "success" || target.CertPath != dir+"/fullchain.pem" {
		t.Fatalf("target after the conversation: %+v", target)
	}
	if got := lastAssistant(t, a, sess.ID); !strings.Contains(got, "证书已经部署好了") {
		t.Errorf("final reply: %q", got)
	}
	// 对话记录里没有私钥
	view, _ := a.AgentGetSession(sess.ID)
	raw, _ := json.Marshal(view.Messages)
	if strings.Contains(string(raw), "PRIVATE KEY") {
		t.Error("the private key leaked into the conversation")
	}
}

// AI 用的预设和「证书」页上的预设是两份代码，内容必须一致。
func TestTargetPresetsMatchTheUI(t *testing.T) {
	raw, err := os.ReadFile("frontend/src/pages/Certs.tsx")
	if err != nil {
		t.Skip("界面源码不在：", err)
	}
	ui := string(raw)
	for name, p := range targetPresets {
		_, rest, ok := strings.Cut(ui, `case "`+name+`":`)
		if !ok {
			t.Errorf("preset %s is missing from the UI", name)
			continue
		}
		block, _, _ := strings.Cut(rest, "};")
		for _, want := range []string{`certFormat: "` + p.Format + `"`, `testCommand: "` + p.TestCommand + `"`, `services: ["` + strings.Join(p.Services, `", "`) + `"]`, `serviceAction: "` + p.Action + `"`} {
			if !strings.Contains(block, want) {
				t.Errorf("preset %s: the UI does not have %s", name, want)
			}
		}
	}
}
