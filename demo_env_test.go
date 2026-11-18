package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"sshpilot/internal/agent"
	"sshpilot/internal/sshx"
	"sshpilot/internal/store"
	"sshpilot/internal/testsshd"
)

// TestDemoEnvironment 为界面验收准备一套演示数据，并保持测试 SSH 服务器运行。
// 仅在设置 SSHPILOT_DEMO_DIR 时运行；在该目录创建 stop 文件即可结束。
func TestDemoEnvironment(t *testing.T) {
	dir := os.Getenv("SSHPILOT_DEMO_DIR")
	if dir == "" {
		t.Skip("SSHPILOT_DEMO_DIR 未设置")
	}
	web := testsshd.Start(t)
	db := testsshd.Start(t)
	a := NewApp()
	a.dataDir = dir
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	a.wire(st)

	w, err := a.SaveServer(ServerInput{Name: "web-01", Host: web.Host, Port: web.Port, Username: web.User, AuthType: "password", Password: web.Password, Group: "生产", Tags: []string{"nginx", "web"}, Notes: "生产 Nginx 入口"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := a.SaveServer(ServerInput{Name: "db-01", Host: db.Host, Port: db.Port, Username: db.User, AuthType: "password", Password: db.Password, Group: "生产", Tags: []string{"mysql"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveServer(ServerInput{Name: "staging-01", Host: "127.0.0.1", Port: 1, Username: "root", AuthType: "password", Password: "x", Group: "测试"}); err != nil {
		t.Fatal(err)
	}
	// intranet-01 直连连不上（端口上没有服务），只有经 web-01 中转或经代理才能到达：用于验收「换线路」。
	// SOCKS5 代理不需要认证，HTTP 代理需要用户名和密码（ops / proxy-pass），端口写在 ready 文件里。
	hiddenPort := deadPort(t)
	hidden := net.JoinHostPort("127.0.0.1", strconv.Itoa(hiddenPort))
	web.Route(hidden, db.Addr)
	socks := testsshd.StartSOCKS5(t, testsshd.ProxyOptions{Routes: map[string]string{hidden: db.Addr}})
	httpProxy := testsshd.StartHTTPProxy(t, testsshd.ProxyOptions{User: "ops", Pass: "proxy-pass", Routes: map[string]string{hidden: db.Addr}})
	if _, err := a.SaveServer(ServerInput{Name: "intranet-01", Host: "127.0.0.1", Port: hiddenPort, Username: db.User, AuthType: "password", Password: db.Password, Group: "内网"}); err != nil {
		t.Fatal(err)
	}
	// 在 web-01 的主目录里放一些文件，用于验收文件管理
	for name, body := range map[string]string{
		"nginx/nginx.conf":         "user www-data;\nworker_processes auto;\n\nhttp {\n    server {\n        listen 443 ssl;\n        server_name app.example.com;\n        ssl_certificate /etc/nginx/ssl/app.crt;\n    }\n}\n",
		"nginx/conf.d/api.conf":    "upstream api { server 127.0.0.1:8080; }\n",
		"deploy.sh":                "#!/bin/bash\nset -e\necho \"发布版本 $1\"\nsystemctl reload nginx\n",
		"docker-compose.yml":       "services:\n  web:\n    image: nginx:stable\n    ports:\n      - \"443:443\"\n",
		".env":                     "APP_ENV=production\nAPP_PORT=8080\n",
		"logs/access.log":          strings.Repeat("10.0.0.8 - - [14/Nov/2026:21:00:00 +0800] \"GET /api/health HTTP/1.1\" 200 15\n", 4000),
		"backup/site-20261114.tgz": string(make([]byte, 6<<20)),
	} {
		p := filepath.Join(web.Home, filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(body), 0o644)
	}
	k, err := a.GenerateKey(GenerateKeyRequest{Name: "ops-ed25519", Kind: "ed25519"})
	if err != nil {
		t.Fatal(err)
	}
	a.BindPublicKey(k.PublicKeyID, []string{w.ID, d.ID})
	a.RotateKey(k.ID, []string{w.ID}, false)
	_, info, _ := sshx.GenerateKey("ed25519", 0, "zhangsan@laptop", "")
	_, _ = a.ImportPublicKey("张三 · 笔记本", info.PublicKey)

	certPEM, keyPEM := selfSigned(t, "app.example.com")
	cv, err := a.ImportCert(ImportCertRequest{CertPEM: certPEM, KeyPEM: keyPEM, Notes: "certbot 续期后从 web-01 同步"})
	if err != nil {
		t.Fatal(err)
	}
	base := "/tmp/sshpilot-demo"
	for _, s := range []store.Server{{ID: w.ID}, {ID: d.ID}} {
		_, _ = a.SaveTarget(store.DeployTarget{CertID: cv.ID, ServerID: s.ID, Preset: "nginx", CertPath: base + "/" + s.ID + "/app.crt", KeyPath: base + "/" + s.ID + "/app.key",
			CertFormat: "fullchain", CertMode: "644", KeyMode: "600", Backup: true, TestCommand: "true", Services: []string{}, ServiceAction: "none"})
	}
	if _, err := a.deployTargets(context.Background(), cv.ID, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	// 把 web-01 上的证书文件记录为来源，用于验收「自动同步」
	if _, err := a.PullCert(PullCertRequest{CertID: cv.ID, ServerID: w.ID, CertPath: base + "/" + w.ID + "/app.crt", KeyPath: base + "/" + w.ID + "/app.key"}); err != nil {
		t.Fatal(err)
	}
	c2, k2 := selfSigned(t, "api.example.org")
	_, _ = a.ImportCert(ImportCertRequest{CertPEM: c2, KeyPEM: k2})

	if id, err := a.RunScript(RunRequest{ScriptID: "builtin-sysinfo", ServerIDs: []string{w.ID, d.ID}}); err == nil {
		waitRun(t, a, id)
	}
	_, _ = a.SaveScript(store.Script{Name: "Nginx 访问量 Top IP", Description: "统计 access.log 中请求最多的 IP", Category: "网络", ReadOnly: true, CreatedBy: "agent",
		Content: "awk '{print $1}' /var/log/nginx/access.log | sort | uniq -c | sort -rn | head -n 20"})
	_, _ = a.agent.Memory.Apply("memory", "add", "web-01 是生产 Nginx 入口，证书统一放在 /etc/nginx/ssl，部署后需要 reload nginx", "")
	_, _ = a.agent.Memory.Apply("memory", "add", "db-01 的数据盘挂载在 /data，日常占用 80% 左右属正常", "")
	_, _ = a.agent.Memory.Apply("user", "add", "偏好先给结论再给细节；重启生产服务前需要先确认", "")
	a.agent.RecordLearning("", agent.LearnEvent{Source: "agent", Kind: "memory", Action: "add", Target: "memory", Summary: "web-01 是生产 Nginx 入口"})
	if _, err := a.RunInspection(nil); err != nil {
		t.Fatal(err)
	}
	// 本地模拟一个 OpenAI 兼容模型，按对话进度返回预设的工具调用，用于验收 AI 助手界面
	mock := startMockModel(t)
	// 对假模型说“把 api.example.org 的证书部署到 db-01”：它会添加一个部署目标并部署
	mockTargetArgs = `{"certificate":"api.example.org","server":"db-01","cert_path":"` + base + `/ai/fullchain.pem","key_path":"` + base + `/ai/privkey.pem","test_command":"true","services":[],"deploy":true}`
	// 假模型把用户发来的文件传到 web-01 主目录下的 uploads 文件夹
	mockUploadDir = "/" + strings.TrimPrefix(filepath.ToSlash(web.Home), "/") + "/uploads/"
	settings := a.settings()
	settings.AI.Provider, settings.AI.Preset, settings.AI.BaseURL, settings.AI.Model = "openai", "custom", mock+"/v1", "mock-ops"
	// 向量模型也指向本地的模拟接口
	settings.Embed.Preset, settings.Embed.BaseURL, settings.Embed.Model = "custom", mock+"/v1", "mock-embed"
	if _, err := a.SaveSettings(settings, "mock-key", false); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveEmbedKey("mock-embed-key", false); err != nil {
		t.Fatal(err)
	}
	// 长期记忆：几条带服务器标注的事实、两段往期对话的要点
	for _, m := range []struct {
		text    string
		servers []string
	}{
		{"Nginx 把 /api 转发到本机 8080 端口的 Java 服务；出现 502 多半是后端进程被 OOM 杀掉，重启 app 服务即可恢复", []string{w.ID}},
		{"站点证书由 certbot 自动续期，证书在 /etc/nginx/ssl，续期后需要 reload nginx 并同步到 db-01", []string{w.ID, d.ID}},
		{"MySQL 每周日凌晨 3 点全量备份到 /data/backup，保留 4 周；备份期间负载偏高属于正常", []string{d.ID}},
		{"发布流程：先在 staging-01 验证，再用 deploy.sh 发布到生产；发布窗口是工作日 20:00 之后", nil},
	} {
		if _, err := a.SaveLongMemory("", m.text, m.servers); err != nil {
			t.Fatal(err)
		}
	}
	day := int64(24 * 3600 * 1000)
	a.agent.Long.AddEpisodes([]agent.MemItem{
		{ID: "ep:demo-502:1", Text: "问题：web-01 的接口突然大量 502，帮我看看\n操作：systemctl status app；dmesg | grep -i oom | tail\n结论：后端 Java 进程被 OOM 杀掉，已重启 app 服务并把 JVM 堆上限调到 4G，接口恢复正常。", Servers: []string{w.ID},
			SessionID: "demo-502", Title: "web-01 接口 502", Source: "episode", CreatedAt: store.Now() - 9*day},
		{ID: "ep:demo-disk:1", Text: "问题：db-01 磁盘告警了\n操作：df -hT；du -xh --max-depth=2 /data | sort -rh | head\n结论：/data/backup 里堆了 9 周的备份，按保留 4 周的约定清理后占用回到 62%。", Servers: []string{d.ID},
			SessionID: "demo-disk", Title: "db-01 磁盘告警", Source: "episode", CreatedAt: store.Now() - 3*day},
	})
	// 技能库：一个很久没用的 AI 沉淀技能、一个已经归档的技能，用于验收技能的整理
	_ = a.agent.Skills.Create("redis-memory-check", "## 步骤\n1. redis-cli info memory\n2. 对比 maxmemory 与 used_memory", "Redis 内存占用排查", "agent")
	old := time.Now().Add(-45 * 24 * time.Hour)
	_ = os.Chtimes(filepath.Join(dir, "skills", "redis-memory-check", "SKILL.md"), old, old)
	_ = a.agent.Skills.Create("legacy-ftp-sync", "## 步骤\n1. lftp 同步", "用 FTP 同步静态文件（已不再使用）", "agent")
	_ = a.ArchiveSkill("legacy-ftp-sync")
	a.agent.StartIndexing()
	t.Logf("demo data ready in %s", dir)
	_ = os.WriteFile(filepath.Join(dir, "ready"), fmt.Appendf(nil, `{"socks5":%d,"http":%d,"httpUser":"ops","httpPass":"proxy-pass"}`, socks.Port, httpProxy.Port), 0o600)
	deadline := time.Now().Add(55 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, "stop")); err == nil {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}
