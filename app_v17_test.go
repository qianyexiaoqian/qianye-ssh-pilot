package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"sshpilot/internal/agent"
	"sshpilot/internal/certs"
	"sshpilot/internal/store"
	"sshpilot/internal/testsshd"
)

// askTool 调用一次 AI 工具；期间出现的审批请求按 approve 答复，并把请求原样返回，方便检查哪些操作要用户点头。
func askTool(t *testing.T, a *App, tc *agent.ToolContext, name string, args map[string]any, approve bool) (*agent.ToolResult, error, []agent.ApprovalRequest) {
	t.Helper()
	type out struct {
		r   *agent.ToolResult
		err error
	}
	done := make(chan out, 1)
	go func() {
		for _, x := range a.opsTools() {
			if x.Name == name {
				raw, _ := json.Marshal(args)
				r, err := x.Handler(context.Background(), tc, raw)
				done <- out{r, err}
				return
			}
		}
		done <- out{nil, os.ErrNotExist}
	}()
	var asked []agent.ApprovalRequest
	seen := map[string]bool{}
	deadline := time.After(90 * time.Second)
	for {
		select {
		case o := <-done:
			return o.r, o.err, asked
		case <-deadline:
			t.Fatalf("%s did not finish", name)
		case <-time.After(5 * time.Millisecond):
			for _, p := range a.agent.PendingApprovals(tc.Session.ID) {
				if !seen[p.ID] {
					seen[p.ID] = true
					asked = append(asked, p)
					_ = a.AgentResolveApproval(p.ID, approve, false)
				}
			}
		}
	}
}

func serverOrder(a *App) string {
	var names []string
	for _, s := range a.ListServers() {
		names = append(names, s.Name)
	}
	return strings.Join(names, " ")
}

func TestReorderServers(t *testing.T) {
	srv := testsshd.Start(t)
	a := newTestApp(t)
	id := map[string]string{}
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		s, err := a.SaveServer(ServerInput{Name: name, Host: srv.Host, Port: srv.Port, Username: srv.User, AuthType: "password", Password: srv.Password})
		if err != nil {
			t.Fatal(err)
		}
		id[name] = s.ID
	}
	for _, c := range []struct {
		ids  []string
		want string
	}{
		// 整个列表重新排
		{[]string{id["c"], id["a"], id["e"], id["b"], id["d"]}, "c a e b d"},
		// 只给出筛选后看得见的几台：它们在原来占着的位置里换顺序，别的服务器不动
		{[]string{id["d"], id["a"]}, "c d e b a"},
		{[]string{id["b"], id["e"], id["c"]}, "b d e c a"},
		// 不认识的 ID 和重复的 ID 不算数
		{[]string{"nope", id["a"], id["a"], id["b"]}, "a d e c b"},
		{nil, "a d e c b"},
	} {
		if err := a.ReorderServers(c.ids); err != nil {
			t.Fatal(err)
		}
		if got := serverOrder(a); got != c.want {
			t.Errorf("reorder %v: got %q, want %q", c.ids, got, c.want)
		}
	}
	// 顺序保存在数据文件里，重新打开还在
	if got := serverOrder(reopenApp(t, a.dataDir)); got != "a d e c b" {
		t.Errorf("after reopen: %q", got)
	}
}

func TestAIImportsCertificates(t *testing.T) {
	srv := testsshd.Start(t)
	a := newTestApp(t)
	s, err := a.SaveServer(ServerInput{Name: "panel-01", Host: srv.Host, Port: srv.Port, Username: srv.User, AuthType: "password", Password: srv.Password})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	dir := "/tmp/sshpilot-ai17-" + time.Now().Format("150405.000000")
	site := dir + "/sites/shop.example.com/ssl"
	old := certs.OnePanelSiteDirs
	certs.OnePanelSiteDirs = []string{dir + "/nothing/*/ssl", dir + "/sites/*/ssl"}
	defer func() { certs.OnePanelSiteDirs = old }()
	put := func(p, text string) {
		t.Helper()
		if err := a.pool.WriteFile(ctx, s.ID, p, []byte(text), "600", ""); err != nil {
			t.Fatal(err)
		}
	}
	// 没有批准全部：不该打扰用户的操作不会弹确认，该确认的会被记录下来
	tc := &agent.ToolContext{Agent: a.agent, Session: &agent.Session{ID: "test"}}
	only := func() CertView {
		t.Helper()
		list := a.ListCerts()
		if len(list) != 1 {
			t.Fatalf("expected one certificate, got %d", len(list))
		}
		return list[0]
	}

	if r, err, _ := askTool(t, a, tc, "find_certificates", map[string]any{"server": "panel-01"}, true); err != nil || !strings.Contains(r.Content, "没有找到证书") {
		t.Fatalf("find on an empty server: %v %+v", err, r)
	}
	cert1, key1 := selfSigned(t, "shop.example.com")
	put(site+"/fullchain.pem", cert1)
	put(site+"/privkey.pem", key1)

	// 1Panel 的网站证书：宿主机上没有 nginx 命令，要从目录里找
	r, err, asked := askTool(t, a, tc, "find_certificates", map[string]any{"server": "panel-01"}, true)
	if err != nil || len(asked) != 0 {
		t.Fatalf("find: %v %v", err, asked)
	}
	for _, want := range []string{"[1Panel 网站] 证书 " + site + "/fullchain.pem", "私钥 " + site + "/privkey.pem", "域名 shop.example.com", "证书库：还没有"} {
		if !strings.Contains(r.Content, want) {
			t.Errorf("find result lacks %q: %s", want, r.Content)
		}
	}

	// 从服务器收进证书库：不需要用户确认，私钥不出现在返回给模型的文字里
	args := map[string]any{"server": "panel-01", "cert_path": site + "/fullchain.pem", "key_path": site + "/privkey.pem"}
	r, err, asked = askTool(t, a, tc, "import_certificate", args, false)
	if err != nil || r.IsError || len(asked) != 0 || !strings.Contains(r.Content, "已导入证书库：「shop.example.com」") {
		t.Fatalf("import: %v %+v %v", err, r, asked)
	}
	if strings.Contains(r.Content, "PRIVATE KEY") || strings.Contains(r.Content, strings.Split(key1, "\n")[1]) {
		t.Error("the private key must not be returned to the model")
	}
	c := only()
	fp1 := c.Fingerprint
	if c.Source == nil || c.Source.ServerID != s.ID || c.Source.CertPath != site+"/fullchain.pem" || c.Source.KeyPath != site+"/privkey.pem" || c.SourceServerName != "panel-01" {
		t.Fatalf("source not recorded: %+v", c.Source)
	}
	// 再导入一次同一张：不会多出一张
	if r, err, _ = askTool(t, a, tc, "import_certificate", args, false); err != nil || !strings.Contains(r.Content, "没有重复导入") {
		t.Fatalf("import twice: %v %+v", err, r)
	}
	only()
	if r, _, _ = askTool(t, a, tc, "find_certificates", map[string]any{"server": "panel-01"}, true); !strings.Contains(r.Content, "证书库：已有「shop.example.com」，同一个版本") {
		t.Errorf("find after import: %s", r.Content)
	}

	// 改名
	if r, err, asked = askTool(t, a, tc, "import_certificate", map[string]any{"certificate": "shop.example.com", "name": "店铺证书"}, false); err != nil || len(asked) != 0 || !strings.Contains(r.Content, "改名为「店铺证书」") {
		t.Fatalf("rename: %v %+v", err, r)
	}
	if c = only(); c.Name != "店铺证书" || c.Fingerprint != fp1 {
		t.Fatalf("after rename: %+v", c.Certificate)
	}

	// 服务器上续期了：不说清楚是不是同一张时不擅自替换，也不悄悄多存一张
	cert2, key2 := selfSigned(t, "shop.example.com")
	put(site+"/fullchain.pem", cert2)
	put(site+"/privkey.pem", key2)
	if r, _, _ = askTool(t, a, tc, "find_certificates", map[string]any{"server": "panel-01"}, true); !strings.Contains(r.Content, "证书库：已有「店铺证书」") || strings.Contains(r.Content, "同一个版本") {
		t.Errorf("find after renewal: %s", r.Content)
	}
	if r, err, _ = askTool(t, a, tc, "import_certificate", args, true); err != nil || !r.IsError || !strings.Contains(r.Content, `certificate="店铺证书"`) || !strings.Contains(r.Content, "separate=true") {
		t.Fatalf("renewal without saying which: %v %+v", err, r)
	}
	if c = only(); c.Fingerprint != fp1 {
		t.Fatal("the certificate must not change before the model says which one to update")
	}
	// 只说证书名：从记下的来源把新版本拉过来
	if r, err, asked = askTool(t, a, tc, "import_certificate", map[string]any{"certificate": "店铺证书"}, false); err != nil || len(asked) != 0 || !strings.Contains(r.Content, "已更新为新证书") {
		t.Fatalf("refresh from source: %v %+v %v", err, r, asked)
	}
	c = only()
	fp2 := c.Fingerprint
	if fp2 == fp1 || c.Name != "店铺证书" || len(a.CertHistory(c.ID)) != 1 {
		t.Fatalf("after refresh: %+v", c.Certificate)
	}
	if r, _, _ = askTool(t, a, tc, "import_certificate", map[string]any{"certificate": "店铺证书"}, false); !strings.Contains(r.Content, "没有变化") {
		t.Errorf("refresh without change: %s", r.Content)
	}

	// 用别处的文件替换证书库里的证书：要用户确认，拒绝了就不动
	cert3, key3 := selfSigned(t, "shop.example.com")
	put(dir+"/new/cert.pem", cert3+key3)
	replace := map[string]any{"certificate": c.ID, "server": "panel-01", "cert_path": dir + "/new/cert.pem"}
	r, err, asked = askTool(t, a, tc, "import_certificate", replace, false)
	if err != nil || len(asked) != 1 || asked[0].Risk != agent.RiskChange || !strings.Contains(asked[0].Title, "更新证书库里的证书「店铺证书」") || !strings.Contains(asked[0].Detail, "shop.example.com") {
		t.Fatalf("replace must ask first: %v %+v", err, asked)
	}
	if strings.Contains(asked[0].Detail, "PRIVATE KEY") {
		t.Error("the approval card must not show the private key")
	}
	if c = only(); c.Fingerprint != fp2 || !strings.Contains(r.Content, "拒绝") {
		t.Fatalf("rejected replace changed the certificate: %s", r.Content)
	}
	if r, err, _ = askTool(t, a, tc, "import_certificate", replace, true); err != nil || !strings.Contains(r.Content, "更新为新版本") {
		t.Fatalf("approved replace: %v %+v", err, r)
	}
	if c = only(); c.Fingerprint == fp2 || c.Source.CertPath != dir+"/new/cert.pem" || len(a.CertHistory(c.ID)) != 2 {
		t.Fatalf("after replace: %+v %+v", c.Certificate, c.Source)
	}

	// 明确说要另存一张
	cert4, key4 := selfSigned(t, "shop.example.com")
	put(dir+"/other/fullchain.pem", cert4)
	put(dir+"/other/privkey.pem", key4)
	other := map[string]any{"server": "panel-01", "cert_path": dir + "/other/fullchain.pem", "key_path": dir + "/other/privkey.pem", "separate": true, "name": "店铺证书（备用）"}
	if r, err, _ = askTool(t, a, tc, "import_certificate", other, false); err != nil || !strings.Contains(r.Content, "已导入证书库：「店铺证书（备用）」") {
		t.Fatalf("separate: %v %+v", err, r)
	}
	if n := len(a.ListCerts()); n != 2 {
		t.Fatalf("expected two certificates, got %d", n)
	}

	// 说不清楚的情况：什么都不保存，并告诉模型缺什么
	cert6, _ := selfSigned(t, "fresh.example.org")
	put(dir+"/fresh/fullchain.pem", cert6)
	for _, x := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{}, "请说明证书从哪里来"},
		{map[string]any{"certificate": "nope.example.com"}, "没有找到证书"},
		{map[string]any{"server": "panel-01", "cert_path": dir + "/fresh/fullchain.pem"}, "缺少私钥"},
		{map[string]any{"server": "panel-01", "cert_path": dir + "/missing.pem", "key_path": dir + "/missing.key"}, "读取证书失败"},
		{map[string]any{"server": "panel-01", "cert_path": dir + "/fresh/fullchain.pem", "key_path": site + "/privkey.pem"}, "不匹配"},
		{map[string]any{"server": "nope", "cert_path": "/a.pem", "key_path": "/a.key"}, "未找到服务器"},
		{map[string]any{"server": "panel-01", "cert_path": "/a.pem", "cert_attachment": "a.pem"}, "只能选一种"},
		{map[string]any{"certificate": "店铺证书（备用）", "cert_attachment": "nope.pem"}, "没有附件"},
	} {
		if _, err, _ := askTool(t, a, tc, "import_certificate", x.args, true); err == nil || !strings.Contains(err.Error(), x.want) {
			t.Errorf("%v: %v", x.args, err)
		}
	}
	if n := len(a.ListCerts()); n != 2 {
		t.Fatalf("failed imports must not save anything, got %d certificates", n)
	}

	// 用户把证书和私钥文件发过来：文件留在本机，程序直接读
	local := t.TempDir()
	cert5, key5 := selfSigned(t, "api.example.net")
	certFile, keyFile := filepath.Join(local, "api.crt"), filepath.Join(local, "api.key")
	if err := os.WriteFile(certFile, []byte(cert5), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, []byte(key5), 0o600); err != nil {
		t.Fatal(err)
	}
	tc.Session.Messages = []agent.Message{{Role: "user", Content: "把这张证书收进证书中心", Attachments: []agent.Attachment{
		{ID: "att_cert", Name: "api.crt", Kind: agent.AttachFile, Text: true, Size: int64(len(cert5)), Path: certFile},
		{ID: "att_key", Name: "api.key", Kind: agent.AttachFile, Text: true, Size: int64(len(key5)), Path: keyFile},
		{ID: "att_bin", Name: "api.pfx", Kind: agent.AttachFile, Size: 2048, Path: certFile},
	}}}
	if _, err, _ = askTool(t, a, tc, "import_certificate", map[string]any{"cert_attachment": "att_bin"}, true); err == nil || !strings.Contains(err.Error(), "不是 PEM 文本文件") {
		t.Errorf("binary attachment: %v", err)
	}
	if _, err, _ = askTool(t, a, tc, "import_certificate", map[string]any{"cert_attachment": "att_cert"}, true); err == nil || !strings.Contains(err.Error(), "缺少私钥") {
		t.Errorf("attachment without key: %v", err)
	}
	r, err, asked = askTool(t, a, tc, "import_certificate", map[string]any{"cert_attachment": "att_cert", "key_attachment": "api.key", "name": "接口证书"}, false)
	if err != nil || len(asked) != 0 || !strings.Contains(r.Content, "已导入证书库：「接口证书」") || strings.Contains(r.Content, "PRIVATE KEY") {
		t.Fatalf("import attachments: %v %+v", err, r)
	}
	api, err := a.findCert("api.example.net")
	if err != nil || api.Source != nil || api.Name != "接口证书" {
		t.Fatalf("imported from attachments: %+v %v", api, err)
	}
	// 私钥附件不能被模型读走
	if _, err, _ = askTool(t, a, tc, "read_attachment", map[string]any{"attachment": "att_key"}, true); err == nil || !strings.Contains(err.Error(), "私钥") || strings.Contains(err.Error(), strings.Split(key5, "\n")[1]) {
		t.Errorf("reading a private key attachment: %v", err)
	}
	if r, err, _ = askTool(t, a, tc, "read_attachment", map[string]any{"attachment": "att_cert"}, true); err != nil || !strings.Contains(r.Content, "BEGIN CERTIFICATE") {
		t.Errorf("reading a certificate attachment: %v", err)
	}

	// 线上实际返回的证书
	pair, err := tls.X509KeyPair([]byte(cert5), []byte(key5))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
	ts.StartTLS()
	defer ts.Close()
	host, portText, _ := net.SplitHostPort(ts.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	r, err, asked = askTool(t, a, tc, "check_tls", map[string]any{"host": host, "port": port, "sni": "api.example.net"}, false)
	if err != nil || r.IsError || len(asked) != 0 {
		t.Fatalf("check_tls: %v %+v", err, r)
	}
	for _, want := range []string{"域名：api.example.net", "证书库：和「接口证书」是同一个版本", "校验：没有通过", "没有带中间证书"} {
		if !strings.Contains(r.Content, want) {
			t.Errorf("check_tls lacks %q: %s", want, r.Content)
		}
	}
	// host 可以直接填受管服务器的名称（这里那个端口上不是 TLS 服务）
	if r, err, _ = askTool(t, a, tc, "check_tls", map[string]any{"host": "panel-01", "port": srv.Port}, false); err != nil || !r.IsError || !strings.Contains(r.Content, srv.Host) {
		t.Errorf("check_tls on a non-TLS port: %v %+v", err, r)
	}
}

func TestAIManagesServers(t *testing.T) {
	srv := testsshd.Start(t)
	a := newTestApp(t)
	tc := &agent.ToolContext{Agent: a.agent, Session: &agent.Session{ID: "test"}}
	find := func(name string) *store.Server {
		for _, s := range a.ListServers() {
			if s.Name == name {
				return &s
			}
		}
		return nil
	}

	// 没有登录方式：先问用户，不猜
	if _, err, _ := askTool(t, a, tc, "save_server", map[string]any{"host": srv.Host, "port": srv.Port}, true); err == nil || !strings.Contains(err.Error(), "password") || !strings.Contains(err.Error(), "先问用户") {
		t.Fatalf("add without credentials: %v", err)
	}
	if _, err, _ := askTool(t, a, tc, "save_server", map[string]any{"name": "x", "password": "p"}, true); err == nil || !strings.Contains(err.Error(), "host") {
		t.Fatalf("add without host: %v", err)
	}
	add := map[string]any{"name": "node-a", "host": srv.Host, "port": srv.Port, "username": srv.User, "password": srv.Password, "group": "生产", "tags": []string{"nginx", "hk"}}
	// 拒绝：不保存
	r, err, asked := askTool(t, a, tc, "save_server", add, false)
	if err != nil || len(asked) != 1 || asked[0].Risk != agent.RiskChange || !strings.Contains(asked[0].Title, "添加服务器 node-a") {
		t.Fatalf("add must ask first: %v %+v", err, asked)
	}
	if strings.Contains(asked[0].Detail, srv.Password) || !strings.Contains(asked[0].Detail, "登录方式：密码（••••••••") {
		t.Errorf("the approval card must hide the password: %s", asked[0].Detail)
	}
	if len(a.ListServers()) != 0 || !strings.Contains(r.Content, "拒绝") {
		t.Fatal("a rejected add must not save the server")
	}
	// 批准：保存并测试连接
	if r, err, _ = askTool(t, a, tc, "save_server", add, true); err != nil || !strings.Contains(r.Content, "已添加服务器「node-a」") || !strings.Contains(r.Content, "连接测试：连接成功") {
		t.Fatalf("add: %v %+v", err, r)
	}
	if strings.Contains(r.Content, srv.Password) {
		t.Error("the password must not be echoed back")
	}
	s := find("node-a")
	if s == nil || s.Group != "生产" || len(s.Tags) != 2 || s.AuthType != "password" || !s.HasPassword || s.HostKeyFP == "" {
		t.Fatalf("saved server: %+v", s)
	}
	if _, err, _ = askTool(t, a, tc, "save_server", add, true); err == nil || !strings.Contains(err.Error(), "已经添加过了") {
		t.Errorf("adding the same server twice: %v", err)
	}

	// 只整理名称、分组、标签、备注：不打扰用户，密码原样保留
	r, err, asked = askTool(t, a, tc, "save_server", map[string]any{"server": "node-a", "name": "香港-入口", "group": "", "tags": []string{"入口"}, "notes": "Nginx 入口机"}, false)
	if err != nil || len(asked) != 0 || !strings.Contains(r.Content, "已修改服务器「香港-入口」") || !strings.Contains(r.Content, "分组：生产 → -") || strings.Contains(r.Content, "连接测试") {
		t.Fatalf("edit labels: %v %+v %v", err, r, asked)
	}
	s = find("香港-入口")
	if s == nil || s.Group != "" || len(s.Tags) != 1 || s.Tags[0] != "入口" || s.Notes != "Nginx 入口机" || s.HostKeyFP == "" {
		t.Fatalf("after editing labels: %+v", s)
	}
	if sec, err := a.GetServerSecrets(s.ID); err != nil || sec.Password != srv.Password {
		t.Fatalf("the saved password must survive an edit: %+v %v", sec, err)
	}
	if r, err, asked = askTool(t, a, tc, "save_server", map[string]any{"server": s.ID, "notes": "Nginx 入口机"}, false); err != nil || len(asked) != 0 || !strings.Contains(r.Content, "没有需要修改") {
		t.Errorf("no-op edit: %v %+v", err, r)
	}

	// 改连接信息：要确认，保存后测试连接，不通也说清楚
	dead := deadPort(t)
	r, err, asked = askTool(t, a, tc, "save_server", map[string]any{"server": "香港-入口", "port": dead}, true)
	if err != nil || len(asked) != 1 || asked[0].Risk != agent.RiskChange || !strings.Contains(asked[0].Detail, "地址：") || !strings.Contains(r.Content, "连接测试没有通过") {
		t.Fatalf("edit port: %v %+v %v", err, r, asked)
	}
	if r, err, _ = askTool(t, a, tc, "save_server", map[string]any{"server": "香港-入口", "port": srv.Port}, true); err != nil || !strings.Contains(r.Content, "连接测试：连接成功") {
		t.Fatalf("fix port: %v %+v", err, r)
	}

	// 密钥库
	if r, _, _ = askTool(t, a, tc, "list_keys", map[string]any{}, true); !strings.Contains(r.Content, "还没有私钥") {
		t.Errorf("empty key list: %s", r.Content)
	}
	if _, err, _ = askTool(t, a, tc, "setup_key_login", map[string]any{"servers": []string{"香港-入口"}, "key": "nope"}, true); err == nil || !strings.Contains(err.Error(), "还没有私钥") {
		t.Errorf("unknown key: %v", err)
	}
	// 密码登录换成密钥登录：拒绝了不生成密钥
	r, err, asked = askTool(t, a, tc, "setup_key_login", map[string]any{"servers": []string{"香港-入口"}}, false)
	if err != nil || len(asked) != 1 || asked[0].Risk != agent.RiskChange || len(a.ListKeys()) != 0 || !strings.Contains(r.Content, "拒绝") {
		t.Fatalf("key login must ask first: %v %+v", err, asked)
	}
	if r, err, _ = askTool(t, a, tc, "setup_key_login", map[string]any{"servers": []string{"香港-入口"}}, true); err != nil || r.IsError || !strings.Contains(r.Content, "1 台成功") {
		t.Fatalf("key login: %v %+v", err, r)
	}
	keys := a.ListKeys()
	if len(keys) != 1 || keys[0].Name != "香港-入口-key" || strings.Contains(r.Content, "PRIVATE KEY") {
		t.Fatalf("generated key: %+v", keys)
	}
	if s = find("香港-入口"); s.AuthType != "key" || s.KeyID != keys[0].ID {
		t.Fatalf("server not switched to key login: %+v", s)
	}
	if res := a.TestServer(s.ID); !res.OK {
		t.Fatalf("key login does not work: %+v", res)
	}
	if r, _, _ = askTool(t, a, tc, "list_keys", map[string]any{}, true); !strings.Contains(r.Content, "香港-入口-key") || !strings.Contains(r.Content, "用于登录：香港-入口") || strings.Contains(r.Content, "PRIVATE KEY") {
		t.Errorf("key list: %s", r.Content)
	}
	// 连不上的服务器：失败，新生成的密钥不留在密钥库里
	bad, err := a.SaveServer(ServerInput{Name: "down", Host: srv.Host, Port: dead, Username: srv.User, AuthType: "password", Password: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if r, err, _ = askTool(t, a, tc, "setup_key_login", map[string]any{"servers": []string{bad.ID}}, true); err != nil || !r.IsError || len(a.ListKeys()) != 1 {
		t.Fatalf("key login on an unreachable server: %v %+v keys=%d", err, r, len(a.ListKeys()))
	}
	// 新增时用密钥库里的私钥登录；改回密码登录
	if r, err, _ = askTool(t, a, tc, "save_server", map[string]any{"server": "down", "port": srv.Port, "key": "香港-入口-key"}, true); err != nil || !strings.Contains(r.Content, "连接测试：连接成功") {
		t.Fatalf("switch to a saved key: %v %+v", err, r)
	}
	if s = find("down"); s.AuthType != "key" || s.KeyID != keys[0].ID {
		t.Fatalf("after switching to a key: %+v", s)
	}
	if _, err, _ = askTool(t, a, tc, "save_server", map[string]any{"server": "down", "key": "香港-入口-key", "password": "p"}, true); err == nil || !strings.Contains(err.Error(), "只能选一种") {
		t.Errorf("both key and password: %v", err)
	}
	// 经跳板机
	if r, err, _ = askTool(t, a, tc, "save_server", map[string]any{"server": "down", "jump_server": "香港-入口"}, true); err != nil || !strings.Contains(r.Content, "线路：直连 → 经跳板机 香港-入口") {
		t.Fatalf("jump server: %v %+v", err, r)
	}
	if s = find("down"); s.JumpServerID != find("香港-入口").ID {
		t.Fatalf("jump server not saved: %+v", s)
	}
}

func TestAIDownloadsFiles(t *testing.T) {
	srv := testsshd.Start(t)
	a := newTestApp(t)
	s, err := a.SaveServer(ServerInput{Name: "web-01", Host: srv.Host, Port: srv.Port, Username: srv.User, AuthType: "password", Password: srv.Password})
	if err != nil {
		t.Fatal(err)
	}
	local := t.TempDir()
	t.Setenv("SSHPILOT_DOWNLOAD_DIR", local)
	// SFTP 看到的路径：假服务器的主目录在本机的一个临时目录里
	home, err := a.FilesList(s.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	dir := home.Path + "/dl"
	for p, text := range map[string]string{"app.conf": "listen 443;\n", "logs/a.log": "aaaa\n", "logs/old/b.log": "bb\n"} {
		p = filepath.Join(srv.Home, "dl", filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tc := &agent.ToolContext{Agent: a.agent, Session: &agent.Session{ID: "test"}}
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("downloaded file missing: %v", err)
		}
		return string(b)
	}

	// 小文件直接下载，不用确认；保存的位置是固定的，不由模型决定
	r, err, asked := askTool(t, a, tc, "download_file", map[string]any{"server": "web-01", "paths": []string{dir + "/app.conf"}}, false)
	if err != nil || r.IsError || len(asked) != 0 || !strings.Contains(r.Content, filepath.Join(local, "app.conf")) {
		t.Fatalf("download: %v %+v %v", err, r, asked)
	}
	if read(filepath.Join(local, "app.conf")) != "listen 443;\n" {
		t.Error("downloaded content differs")
	}
	// 同名文件不覆盖
	if r, err, _ = askTool(t, a, tc, "download_file", map[string]any{"server": "web-01", "path": dir + "/app.conf"}, false); err != nil || !strings.Contains(r.Content, filepath.Join(local, "app (1).conf")) {
		t.Fatalf("download again: %v %+v", err, r)
	}
	// 整个文件夹
	if r, err, _ = askTool(t, a, tc, "download_file", map[string]any{"server": "web-01", "paths": []string{dir + "/logs"}}, false); err != nil || !strings.Contains(r.Content, "文件夹，2 个文件") {
		t.Fatalf("download a folder: %v %+v", err, r)
	}
	if read(filepath.Join(local, "logs", "a.log")) != "aaaa\n" || read(filepath.Join(local, "logs", "old", "b.log")) != "bb\n" {
		t.Error("downloaded folder differs")
	}
	// 大文件先问
	oldLimit := bigDownload
	bigDownload = 3
	defer func() { bigDownload = oldLimit }()
	r, err, asked = askTool(t, a, tc, "download_file", map[string]any{"server": "web-01", "paths": []string{dir + "/logs/a.log"}}, false)
	if err != nil || len(asked) != 1 || asked[0].Risk != agent.RiskChange || !strings.Contains(asked[0].Detail, local) || !strings.Contains(r.Content, "拒绝") {
		t.Fatalf("big download must ask first: %v %+v", err, asked)
	}
	if _, err := os.Stat(filepath.Join(local, "a.log")); err == nil {
		t.Error("a rejected download must not write the file")
	}
	for _, x := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"server": "web-01", "paths": []string{"relative.txt"}}, "绝对路径"},
		{map[string]any{"server": "web-01"}, "请给出要下载的文件路径"},
		{map[string]any{"server": "web-01", "paths": []string{dir + "/missing.txt"}}, "missing.txt"},
		{map[string]any{"server": "nope", "paths": []string{dir + "/app.conf"}}, "未找到服务器"},
	} {
		if _, err, _ := askTool(t, a, tc, "download_file", x.args, true); err == nil || !strings.Contains(err.Error(), x.want) {
			t.Errorf("%v: %v", x.args, err)
		}
	}
}

func TestAISavesQuickCommands(t *testing.T) {
	a := newTestApp(t)
	tc := &agent.ToolContext{Agent: a.agent, Session: &agent.Session{ID: "test"}}
	args := map[string]any{"name": "重启 Nginx", "command": "systemctl restart nginx", "group": "Nginx", "auto_enter": true}
	base := len(a.ListQuickCommands()) // 程序自带的几条
	r, err, asked := askTool(t, a, tc, "save_quick_command", args, false)
	if err != nil || len(asked) != 1 || asked[0].Risk != agent.RiskChange || asked[0].Detail != "systemctl restart nginx" || len(a.ListQuickCommands()) != base || !strings.Contains(r.Content, "拒绝") {
		t.Fatalf("saving a quick command must ask first: %v %+v", err, asked)
	}
	if r, err, _ = askTool(t, a, tc, "save_quick_command", args, true); err != nil || !strings.Contains(r.Content, "已保存快捷命令「重启 Nginx」") {
		t.Fatalf("save: %v %+v", err, r)
	}
	// 会改动系统又设成了直接执行：点击时还要再确认一次
	if q := a.ListQuickCommands()[base:]; len(q) != 1 || q[0].Group != "Nginx" || !q[0].AutoEnter || !q[0].Confirm {
		t.Fatalf("saved quick command: %+v", q)
	}
	if _, err, _ = askTool(t, a, tc, "save_quick_command", map[string]any{"name": "看负载", "command": "uptime"}, true); err != nil {
		t.Fatal(err)
	}
	if q := a.ListQuickCommands()[base:]; len(q) != 2 || q[1].AutoEnter || q[1].Confirm {
		t.Fatalf("read-only quick command: %+v", q)
	}
	if _, err, _ = askTool(t, a, tc, "save_quick_command", map[string]any{"name": "空", "command": "  "}, true); err == nil {
		t.Error("an empty command must be refused")
	}
}

// 系统提示词要让模型知道这些事可以自己做，而不是让用户去页面上点。
func TestPromptListsWhatTheAssistantCanDo(t *testing.T) {
	a := newTestApp(t)
	prompt := agent.BuildSystemPrompt("", "", "", time.Now())
	have := map[string]bool{}
	for _, tool := range a.opsTools() {
		if have[tool.Name] {
			t.Errorf("duplicate tool %s", tool.Name)
		}
		have[tool.Name] = true
	}
	for _, name := range []string{"save_server", "setup_key_login", "list_keys", "find_certificates", "import_certificate", "check_tls", "download_file", "save_quick_command"} {
		if !have[name] {
			t.Errorf("tool %s is not registered", name)
		}
		if !strings.Contains(prompt, name) {
			t.Errorf("the system prompt does not mention %s", name)
		}
	}
	if strings.Contains(prompt, "%!") {
		t.Errorf("broken format verb in the system prompt: %s", prompt)
	}
}
