package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"sshpilot/internal/agent"
	"sshpilot/internal/store"
	"sshpilot/internal/testsshd"
)

// newTestApp 构造一个不依赖 Wails 运行时的 App（事件推送为空操作）。
func newTestApp(t *testing.T) *App {
	t.Helper()
	a := NewApp()
	a.dataDir = t.TempDir()
	st, err := store.Open(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	a.wire(st)
	t.Cleanup(func() { a.shutdown(context.Background()) })
	return a
}

func selfSigned(t *testing.T, domain string) (string, string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: domain}, DNSNames: []string{domain},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(20 * 24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kd, _ := x509.MarshalPKCS8PrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}))
}

func TestAppEndToEnd(t *testing.T) {
	srv := testsshd.Start(t)
	a := newTestApp(t)

	// 添加服务器（密码登录），列表中不返回密码
	saved, err := a.SaveServer(ServerInput{Name: "web-01", Host: srv.Host, Port: srv.Port, Username: srv.User, AuthType: "password", Password: srv.Password, Group: "测试", Tags: []string{"nginx", "nginx", " "}})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Password != "" || !saved.HasPassword || len(saved.Tags) != 1 {
		t.Fatalf("sanitized server: %+v", saved)
	}
	if r := a.TestServer(saved.ID); !r.OK || r.HostKeyFP == "" {
		t.Fatalf("test server: %+v", r)
	}
	if _, err := a.SaveServer(ServerInput{Host: "1.2.3.4", AuthType: "key"}); err == nil {
		t.Fatal("key auth without key should fail")
	}
	raw, _ := os.ReadFile(a.dataDir + "/store.json")
	if strings.Contains(string(raw), srv.Password) {
		t.Fatal("password stored in plaintext")
	}

	// 生成密钥 → 部署公钥 → 对账 → 切换为密钥登录
	k, err := a.GenerateKey(GenerateKeyRequest{Name: "ops", Kind: "ed25519"})
	if err != nil {
		t.Fatal(err)
	}
	res := a.BindPublicKey(k.PublicKeyID, []string{saved.ID})
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("bind: %+v", res)
	}
	keys, err := a.ScanAuthorizedKeys(saved.ID)
	if err != nil || len(keys) != 1 || keys[0].PublicKeyID != k.PublicKeyID {
		t.Fatalf("scan: %+v %v", keys, err)
	}
	if pubs := a.ListPublicKeys(); len(pubs) != 1 || len(pubs[0].Bindings) != 1 {
		t.Fatalf("bindings: %+v", pubs)
	}
	rot := a.RotateKey(k.ID, []string{saved.ID}, false)
	if !rot[0].OK {
		t.Fatalf("rotate: %+v", rot)
	}
	if s := a.ListServers()[0]; s.AuthType != "key" || s.KeyID != k.ID {
		t.Fatalf("server not switched to key auth: %+v", s)
	}
	if r := a.TestServer(saved.ID); !r.OK {
		t.Fatalf("key login after rotation: %+v", r)
	}
	if err := a.DeleteKey(k.ID); err == nil {
		t.Fatal("deleting a key in use should fail")
	}
	// 当前登录密钥不能被解绑
	if r := a.UnbindPublicKey(k.PublicKeyID, []string{saved.ID}); r[0].OK {
		t.Fatal("unbinding the login key must be refused")
	}

	// 脚本：内置只读脚本 + 带参数的临时命令
	runID, err := a.RunScript(RunRequest{Content: "echo \"hello $NAME\"", ServerIDs: []string{saved.ID}, Params: map[string]string{"NAME": "pilot"}})
	if err != nil {
		t.Fatal(err)
	}
	rec := waitRun(t, a, runID)
	if rec.Status != "success" || !strings.Contains(rec.Results[0].Output, "hello pilot") {
		t.Fatalf("run: %+v", rec)
	}
	if _, err := a.RunScript(RunRequest{ScriptID: "builtin-disk-usage", ServerIDs: []string{saved.ID}, Params: map[string]string{"DIR": ""}}); err != nil {
		t.Fatalf("default param should be used: %v", err)
	}
	custom, err := a.SaveScript(store.Script{Name: "需要参数", Content: "echo $X", Params: []store.ScriptParam{{Name: "X", Required: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.RunScript(RunRequest{ScriptID: custom.ID, ServerIDs: []string{saved.ID}}); err == nil {
		t.Fatal("missing required param should fail")
	}
	if _, err := a.SaveScript(store.Script{ID: "builtin-sysinfo", Name: "x", Content: "x"}); err == nil {
		t.Fatal("builtin scripts must be read-only")
	}

	// 证书：导入 → 添加部署目标 → 同步 → 状态为已同步
	certPEM, keyPEM := selfSigned(t, "app.test")
	cv, err := a.ImportCert(ImportCertRequest{CertPEM: certPEM, KeyPEM: keyPEM})
	if err != nil || cv.Name != "app.test" || cv.Days < 18 {
		t.Fatalf("import: %+v %v", cv, err)
	}
	dir := "/tmp/sshpilot-app-" + time.Now().Format("150405.000000")
	target, err := a.SaveTarget(store.DeployTarget{CertID: cv.ID, ServerID: saved.ID, Preset: "custom", CertPath: dir + "/app.crt", KeyPath: dir + "/app.key", Backup: true, TestCommand: "true", ServiceAction: "none"})
	if err != nil {
		t.Fatal(err)
	}
	results, err := a.deployTargets(context.Background(), cv.ID, nil, nil, nil)
	if err != nil || len(results) != 1 || !results[0].Success {
		t.Fatalf("deploy: %+v %v", results, err)
	}
	if c := a.ListCerts()[0]; !c.Targets[0].Synced {
		t.Fatalf("target should be synced: %+v", c.Targets[0])
	}
	// 续期：用同一私钥替换证书后，目标变为待同步
	if _, err := a.ImportCert(ImportCertRequest{ID: cv.ID, CertPEM: certPEM, KeyPEM: keyPEM}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RefreshCert(cv.ID); err == nil {
		t.Fatal("refresh without source should fail")
	}
	pulled, err := a.PullCert(PullCertRequest{CertID: cv.ID, ServerID: saved.ID, CertPath: target.CertPath, KeyPath: target.KeyPath})
	if err != nil || pulled.Source == nil {
		t.Fatalf("pull: %+v %v", pulled, err)
	}
	if r, err := a.RefreshCert(cv.ID); err != nil || r.Changed {
		t.Fatalf("refresh same cert: %+v %v", r, err)
	}

	// AI 工具：只读命令在 safe 模式下自动执行
	tc := &agent.ToolContext{Agent: a.agent, Session: &agent.Session{ID: "test"}}
	out := callTool(t, a, tc, "run_command", map[string]any{"servers": []string{"web-01"}, "command": "echo from-ai"})
	if !strings.Contains(out.Content, "from-ai") || !strings.Contains(out.Content, "退出码 0") {
		t.Fatalf("run_command: %s", out.Content)
	}
	if out := callTool(t, a, tc, "list_servers", map[string]any{}); !strings.Contains(out.Content, "web-01") {
		t.Fatalf("list_servers: %s", out.Content)
	}
	if out := callTool(t, a, tc, "list_certificates", map[string]any{}); !strings.Contains(out.Content, "app.test") {
		t.Fatalf("list_certificates: %s", out.Content)
	}
	if out := callTool(t, a, tc, "save_script", map[string]any{"name": "AI 脚本", "content": "uptime", "read_only": true}); !strings.Contains(out.Content, "已保存") {
		t.Fatalf("save_script: %s", out.Content)
	}
	if _, err := a.resolveServerRefs([]string{"nope"}); err == nil || !strings.Contains(err.Error(), "web-01") {
		t.Fatalf("unknown server error should list available servers: %v", err)
	}

	// 巡检
	rep, err := a.RunInspection(nil)
	if err != nil || len(rep.Servers) != 1 {
		t.Fatalf("inspection: %+v %v", rep, err)
	}
	if !strings.Contains(a.envSnapshot(), "web-01") {
		t.Fatal("env snapshot should list servers")
	}

	// 删除服务器时清理绑定与部署目标
	if err := a.DeleteServer(saved.ID); err != nil {
		t.Fatal(err)
	}
	if c := a.ListCerts()[0]; len(c.Targets) != 0 {
		t.Fatal("targets should be removed with the server")
	}
	if pubs := a.ListPublicKeys(); len(pubs[0].Bindings) != 0 {
		t.Fatal("bindings should be removed with the server")
	}
}

func waitRun(t *testing.T, a *App, id string) store.RunRecord {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if r, ok := a.store.GetRun(id); ok && r.Status != "running" {
			return r
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("run %s did not finish", id)
	return store.RunRecord{}
}

func callTool(t *testing.T, a *App, tc *agent.ToolContext, name string, args map[string]any) *agent.ToolResult {
	t.Helper()
	for _, tool := range a.opsTools() {
		if tool.Name == name {
			raw, _ := json.Marshal(args)
			r, err := tool.Handler(context.Background(), tc, raw)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			return r
		}
	}
	t.Fatalf("tool %s not found", name)
	return nil
}

func TestStoreSecretsAndBackup(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Update(func(d *store.Data) error {
		d.Servers = append(d.Servers, &store.Server{ID: "s", Name: "x", Password: "p@ss-123"})
		d.Settings.AI.APIKey = "sk-test-key"
		return nil
	})
	st2, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var pw, key string
	st2.Read(func(d *store.Data) { pw, key = d.Servers[0].Password, d.Settings.AI.APIKey })
	if pw != "p@ss-123" || key != "sk-test-key" {
		t.Fatalf("decrypt: %q %q", pw, key)
	}
	snap, _ := st2.Snapshot()
	blob, err := store.EncryptBackup(&store.Backup{Version: 1, Data: snap, Files: map[string]string{"memory/MEMORY.md": "x"}}, "backup-pass")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DecryptBackup(blob, "wrong-pass"); err == nil {
		t.Fatal("wrong password should fail")
	}
	b, err := store.DecryptBackup(blob, "backup-pass")
	if err != nil || b.Data.Servers[0].Password != "p@ss-123" || b.Files["memory/MEMORY.md"] != "x" {
		t.Fatalf("backup roundtrip: %+v %v", b, err)
	}
}
