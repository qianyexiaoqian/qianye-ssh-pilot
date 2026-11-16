package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"sshpilot/internal/sshx"
	"sshpilot/internal/store"
	"sshpilot/internal/testsshd"
)

func serverOS(a *App, id string) string {
	var os string
	a.store.Read(func(d *store.Data) {
		if s := d.Server(id); s != nil {
			os = s.OS
		}
	})
	return os
}

// reopenApp 模拟重新启动程序：用同一个数据目录再开一个 App。
func reopenApp(t *testing.T, dir string) *App {
	t.Helper()
	a := NewApp()
	a.dataDir = dir
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	a.wire(st)
	t.Cleanup(func() { a.shutdown(context.Background()) })
	return a
}

// deadPort 返回一个本机上没有服务在监听的端口。
func deadPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func TestSystemIsLearnedOnAnyConnection(t *testing.T) {
	srv := testsshd.Start(t)
	a := newTestApp(t)
	add := func(name, pw string) string {
		s, err := a.SaveServer(ServerInput{Name: name, Host: srv.Host, Port: srv.Port, Username: srv.User, AuthType: "password", Password: pw})
		if err != nil {
			t.Fatal(err)
		}
		return s.ID
	}

	// 刚添加、还没有连接过：只探测端口，不会为了识别系统去登录
	idle := add("idle", srv.Password)
	a.probeAll()
	if st := a.statusOf(idle); st.Status != "online" {
		t.Fatalf("status: %+v", st)
	}

	// 执行命令（连接池）连上之后识别
	pooled := add("pooled", srv.Password)
	if _, err := a.pool.Exec(context.Background(), pooled, "true", sshx.ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "system detected on a pooled connection", func() bool { return serverOS(a, pooled) != "" })

	// 打开终端（独立连接）之后识别
	term := add("terminal", srv.Password)
	id, err := a.OpenTerminal(term, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "system detected after opening a terminal", func() bool { return serverOS(a, term) != "" })
	a.CloseTerminal(id)

	// 「测试连接」也会记下系统；地址改了之后清掉旧的记录
	if r := a.TestServer(idle); !r.OK || r.OS == "" || serverOS(a, idle) != r.OS {
		t.Fatalf("test connection: %+v, saved %q", r, serverOS(a, idle))
	}
	if _, err := a.SaveServer(ServerInput{ID: idle, Name: "idle", Host: "10.1.2.3", Port: 22, Username: srv.User, AuthType: "password"}); err != nil {
		t.Fatal(err)
	}
	if serverOS(a, idle) != "" {
		t.Fatal("system info should be cleared when the address changes")
	}
	// 保存前在表单里测试过：识别出的系统随保存一起记下
	fresh, err := a.SaveServer(ServerInput{Name: "fresh", Host: srv.Host, Port: srv.Port, Username: srv.User, AuthType: "password", Password: srv.Password, OS: "Debian GNU/Linux 12 (bookworm)"})
	if err != nil || fresh.OS != "Debian GNU/Linux 12 (bookworm)" {
		t.Fatalf("system from the form test: %+v %v", fresh, err)
	}
	// 没有连接过的服务器一直没有系统信息，也没有被登录过
	if serverOS(a, term) == "" || serverOS(a, pooled) == "" {
		t.Fatal("lost system info")
	}
}

func TestServerSecretsAreReadable(t *testing.T) {
	a := newTestApp(t)
	s, err := a.SaveServer(ServerInput{Name: "web", Host: "10.0.0.9", Username: "deploy", AuthType: "password", Password: "login-pw", UseSudo: true, SudoPassword: "sudo-pw",
		Proxy: ProxyInput{Type: "socks5", Host: "10.0.0.1", Port: 1080, Username: "u", Password: "proxy-pw"}})
	if err != nil {
		t.Fatal(err)
	}
	// 列表里仍然不带密码，单独读取时才返回
	if s.Password != "" || s.SudoPassword != "" || s.Proxy == nil || s.Proxy.Password != "" || !s.Proxy.HasPassword {
		t.Fatalf("list view leaks secrets: %+v %+v", s, s.Proxy)
	}
	sec, err := a.GetServerSecrets(s.ID)
	if err != nil || sec.Password != "login-pw" || sec.SudoPassword != "sudo-pw" || sec.ProxyPassword != "proxy-pw" {
		t.Fatalf("secrets: %+v %v", sec, err)
	}
	if _, err := a.GetServerSecrets("nope"); err == nil {
		t.Fatal("unknown server should fail")
	}
	raw, _ := os.ReadFile(filepath.Join(a.dataDir, "store.json"))
	for _, pw := range []string{"login-pw", "sudo-pw", "proxy-pw"} {
		if strings.Contains(string(raw), pw) {
			t.Fatalf("%s stored in plain text", pw)
		}
	}
}

func TestProxyAndTemporaryRoutes(t *testing.T) {
	srv := testsshd.Start(t)
	jump := testsshd.Start(t)
	a := newTestApp(t)
	// 这台服务器直连连不上（端口没有服务），只有经过代理或跳板机才能到达
	hidden := net.JoinHostPort("127.0.0.1", strconv.Itoa(deadPort(t)))
	hiddenHost, hiddenPortText, _ := net.SplitHostPort(hidden)
	hiddenPort, _ := strconv.Atoi(hiddenPortText)
	routes := map[string]string{hidden: srv.Addr}
	socks := testsshd.StartSOCKS5(t, testsshd.ProxyOptions{User: "px", Pass: "px-pass", Routes: routes})
	httpProxy := testsshd.StartHTTPProxy(t, testsshd.ProxyOptions{Routes: routes})
	jump.Route(hidden, srv.Addr)

	s, err := a.SaveServer(ServerInput{Name: "hidden", Host: hiddenHost, Port: hiddenPort, Username: srv.User, AuthType: "password", Password: srv.Password})
	if err != nil {
		t.Fatal(err)
	}
	j, err := a.SaveServer(ServerInput{Name: "jump", Host: jump.Host, Port: jump.Port, Username: jump.User, AuthType: "password", Password: jump.Password})
	if err != nil {
		t.Fatal(err)
	}
	if r := a.TestServer(s.ID); r.OK {
		t.Fatal("the hidden server must not be reachable directly")
	}
	socksIn := RouteInput{Mode: "proxy", Proxy: ProxyInput{Type: "socks5", Host: "127.0.0.1", Port: socks.Port, Username: "px", Password: "px-pass"}}

	// 先测试一条线路：不改变任何设置
	if r := a.TestRoute(s.ID, socksIn); !r.OK || r.OS == "" {
		t.Fatalf("test via socks5: %+v", r)
	}
	if r := a.TestRoute(s.ID, RouteInput{Mode: "direct"}); r.OK {
		t.Fatal("direct route should still fail")
	}
	if len(a.ListTempRoutes()) != 0 || a.TestServer(s.ID).OK {
		t.Fatal("testing a route must not apply it")
	}

	// 临时线路：经代理
	view, err := a.SetTempRoute(s.ID, socksIn)
	if err != nil || view.Mode != "proxy" || view.Proxy == nil || view.Proxy.Password != "" || !strings.Contains(view.Label, "SOCKS5") {
		t.Fatalf("temp route view: %+v %v", view, err)
	}
	if r := a.TestServer(s.ID); !r.OK {
		t.Fatalf("via temporary proxy route: %+v", r)
	}
	a.probeAll()
	if st := a.statusOf(s.ID); st.Status != "online" {
		t.Fatalf("status with a temporary route should follow the route: %+v", st)
	}
	// 没有用它打开过终端时，线路一直保留
	if len(a.ListTempRoutes()) != 1 {
		t.Fatal("route dropped too early")
	}
	// 终端断开后自动取消
	t1, err := a.OpenTerminal(s.ID, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	t2, err := a.OpenTerminal(s.ID, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	a.CloseTerminal(t1)
	if len(a.ListTempRoutes()) != 1 {
		t.Fatal("route must stay while another terminal is still open")
	}
	a.CloseTerminal(t2)
	if len(a.ListTempRoutes()) != 0 {
		t.Fatal("route should be cancelled after the last terminal closes")
	}
	if r := a.TestServer(s.ID); r.OK {
		t.Fatal("after the route is cancelled the server is direct again")
	}
	var saved store.Server
	a.store.Read(func(d *store.Data) { saved = *d.Server(s.ID) })
	if saved.JumpServerID != "" || saved.Proxy != nil {
		t.Fatalf("a temporary route must not change the saved settings: %+v", saved)
	}

	// 临时线路：经跳板机；手动取消
	if _, err := a.SetTempRoute(s.ID, RouteInput{Mode: "jump", JumpServerID: j.ID}); err != nil {
		t.Fatal(err)
	}
	if r := a.TestServer(s.ID); !r.OK {
		t.Fatalf("via temporary jump route: %+v", r)
	}
	if v := a.ListTempRoutes(); len(v) != 1 || v[0].Label != "经 jump" {
		t.Fatalf("jump route view: %+v", v)
	}
	a.ClearTempRoute(s.ID)
	if len(a.ListTempRoutes()) != 0 || a.TestServer(s.ID).OK {
		t.Fatal("manual cancel failed")
	}
	// 不合理的线路
	if _, err := a.SetTempRoute(s.ID, RouteInput{Mode: "jump", JumpServerID: s.ID}); err == nil {
		t.Fatal("a server cannot be its own jump host")
	}
	if _, err := a.SetTempRoute(j.ID, RouteInput{Mode: "jump", JumpServerID: s.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetTempRoute(s.ID, RouteInput{Mode: "jump", JumpServerID: j.ID}); err == nil {
		t.Fatal("two servers cannot jump through each other")
	}
	a.ClearTempRoute(j.ID)
	if _, err := a.SetTempRoute(s.ID, RouteInput{Mode: "proxy", Proxy: ProxyInput{Type: "http", Host: "127.0.0.1"}}); err == nil {
		t.Fatal("a proxy without a port should be rejected")
	}

	// 本地代理：协议自动识别（这里端口上是 HTTP 代理）
	if _, err := a.SetTempRoute(s.ID, RouteInput{Mode: "proxy", Proxy: ProxyInput{Type: "local", Port: httpProxy.Port}}); err != nil {
		t.Fatal(err)
	}
	if r := a.TestServer(s.ID); !r.OK {
		t.Fatalf("via local proxy: %+v", r)
	}
	// 文件管理窗口进程沿用主窗口的临时线路
	t.Setenv(envTempRoute, a.tempRouteEnv(s.ID))
	child := reopenApp(t, a.dataDir)
	child.filesWindow = s.ID
	child.adoptParentRoute()
	if ep, err := child.resolveEndpoint(s.ID); err != nil || ep.Proxy == nil || ep.Proxy.Port != httpProxy.Port || ep.Proxy.Type != sshx.ProxyAuto {
		t.Fatalf("child endpoint: %+v %v", ep, err)
	}
	a.ClearTempRoute(s.ID)

	// 记住线路：保存到服务器设置里。再次保存时没有重新填写代理密码，沿用已保存的
	sv, err := a.SaveServerRoute(s.ID, socksIn)
	if err != nil || sv.Proxy == nil || sv.Proxy.Password != "" || !sv.Proxy.HasPassword {
		t.Fatalf("saved route: %+v %v", sv, err)
	}
	if r := a.TestServer(s.ID); !r.OK {
		t.Fatalf("via saved proxy: %+v", r)
	}
	keep := socksIn.Proxy
	keep.Password, keep.KeepPassword = "", true
	if _, err := a.SaveServer(ServerInput{ID: s.ID, Name: "hidden", Host: hiddenHost, Port: hiddenPort, Username: srv.User, AuthType: "password", Proxy: keep}); err != nil {
		t.Fatal(err)
	}
	if r := a.TestServer(s.ID); !r.OK {
		t.Fatalf("proxy password should be kept: %+v", r)
	}
	if last := a.GetSettings().General.LastProxy; last == nil || last.Port != socks.Port || last.Password != "" || !last.HasPassword {
		t.Fatalf("last proxy: %+v", last)
	}
	// 代理密码错了属于认证失败
	bad := socksIn
	bad.Proxy.Password = "nope"
	if r := a.TestRoute(s.ID, bad); r.OK || !strings.Contains(r.Message, "代理认证失败") {
		t.Fatalf("wrong proxy password: %+v", r)
	}
	// 保存的线路改回直连
	if sv, err := a.SaveServerRoute(s.ID, RouteInput{Mode: "direct"}); err != nil || sv.Proxy != nil || sv.JumpServerID != "" {
		t.Fatalf("back to direct: %+v %v", sv, err)
	}
	// 删除跳板机时，经过它的临时线路一并取消
	if _, err := a.SetTempRoute(s.ID, RouteInput{Mode: "jump", JumpServerID: j.ID}); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteServer(j.ID); err != nil {
		t.Fatal(err)
	}
	if len(a.ListTempRoutes()) != 0 {
		t.Fatal("routes through a deleted jump host should be dropped")
	}
}

func TestParseSystemProxy(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1:7890": "7890",
		"http=127.0.0.1:7890;https=127.0.0.1:7890":   "7890",
		"http=127.0.0.1:10809;socks=127.0.0.1:10808": "10808,10809",
		"http://localhost:8080/":                     "8080",
		"proxy.corp.example:3128":                    "",
		"":                                           "",
		"socks=[::1]:1080":                           "1080",
		"http=10.0.0.5:8080;https=127.0.0.1:not-a-num": "",
	} {
		var got []string
		for _, p := range parseSystemProxy(in) {
			got = append(got, strconv.Itoa(p))
		}
		if strings.Join(got, ",") != want {
			t.Errorf("parseSystemProxy(%q) = %v, want %s", in, got, want)
		}
	}
}

func TestDetectLocalProxy(t *testing.T) {
	a := newTestApp(t)
	if _, ok := systemProxy(); ok {
		t.Skip("这台电脑设置了系统代理，结果取决于它")
	}
	if a.DetectLocalProxy().Found {
		t.Skip("这台电脑上已经有代理软件在运行")
	}
	// 在一个常见端口上启动 HTTP 代理（列表里靠后的端口，尽量不和别的程序冲突）
	for i := len(commonProxyPorts) - 1; i >= 0; i-- {
		port := commonProxyPorts[i]
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			continue
		}
		t.Cleanup(func() { ln.Close() })
		testsshd.StartHTTPProxy(t, testsshd.ProxyOptions{Listener: ln})
		if got := a.DetectLocalProxy(); !got.Found || got.Port != port || got.Type != sshx.ProxyHTTP {
			t.Fatalf("detect: %+v, want port %d", got, port)
		}
		return
	}
	t.Skip("常见的代理端口都被占用了")
}

func TestStartupPasswordFlow(t *testing.T) {
	srv := testsshd.Start(t)
	a := newTestApp(t)
	s, err := a.SaveServer(ServerInput{Name: "web", Host: srv.Host, Port: srv.Port, Username: srv.User, AuthType: "password", Password: srv.Password})
	if err != nil {
		t.Fatal(err)
	}
	k, err := a.GenerateKey(GenerateKeyRequest{Name: "ops", Kind: "ed25519"})
	if err != nil {
		t.Fatal(err)
	}
	keyServer, err := a.SaveServer(ServerInput{Name: "by-key", Host: "10.9.9.9", AuthType: "key", KeyID: k.ID})
	if err != nil {
		t.Fatal(err)
	}
	if st := a.GetLockState(); st.Enabled || st.Locked {
		t.Fatalf("no password yet: %+v", st)
	}
	if _, err := a.LockNow(); err == nil {
		t.Fatal("locking without a password should be refused")
	}
	if _, err := a.SetLockPassword("", "12"); err == nil {
		t.Fatal("too short")
	}
	st, err := a.SetLockPassword("", "open-sesame")
	if err != nil || !st.Enabled || st.Locked {
		t.Fatalf("set password: %+v %v", st, err)
	}
	raw, _ := os.ReadFile(filepath.Join(a.dataDir, "store.json"))
	if strings.Contains(string(raw), srv.Password) || strings.Contains(string(raw), "open-sesame") {
		t.Fatal("secrets on disk")
	}

	// 运行中锁定：只挡住界面上的敏感操作，已经连上的不受影响
	term, err := a.OpenTerminal(s.ID, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := a.LockNow(); err != nil || !st.Locked || st.Sealed {
		t.Fatalf("lock now: %+v %v", st, err)
	}
	if _, err := a.GetServerSecrets(s.ID); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("secrets while locked: %v", err)
	}
	if _, err := a.OpenTerminal(s.ID, 80, 24); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("terminal while locked: %v", err)
	}
	if err := a.TerminalWrite(term, "ls\n"); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("typing while locked: %v", err)
	}
	if _, err := a.ExportPrivateKey(k.ID); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("key export while locked: %v", err)
	}
	if _, err := a.ExportBackup("backup-password"); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("backup while locked: %v", err)
	}
	if _, err := a.SetLockPassword("open-sesame", "another-one"); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("changing the password while locked: %v", err)
	}
	if _, err := a.Unlock("wrong"); !errors.Is(err, store.ErrWrongPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	if st, err := a.Unlock("open-sesame"); err != nil || st.Locked {
		t.Fatalf("unlock: %+v %v", st, err)
	}
	if err := a.TerminalWrite(term, "true\n"); err != nil {
		t.Fatalf("the terminal should have survived the lock: %v", err)
	}
	a.CloseTerminal(term)

	// 重新启动：输入密码之前凭据是密文，不连接任何服务器
	b := reopenApp(t, a.dataDir)
	if st := b.GetLockState(); !st.Locked || !st.Sealed || !st.Enabled {
		t.Fatalf("after restart: %+v", st)
	}
	if _, err := b.resolveEndpoint(s.ID); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("endpoint while sealed: %v", err)
	}
	if r := b.TestServer(s.ID); r.OK || !strings.Contains(r.Message, "锁定") {
		t.Fatalf("test while sealed: %+v", r)
	}
	if _, err := b.GetServerSecrets(s.ID); err == nil {
		t.Fatal("secrets while sealed")
	}
	// 文件管理窗口：主窗口没有解锁时拿不到密钥，自己也是锁定的；拿到密钥后直接可用
	t.Setenv(envVaultKey, "")
	child := reopenApp(t, a.dataDir)
	child.filesWindow = s.ID
	child.unlockFromParent()
	if !child.GetLockState().Locked {
		t.Fatal("a file window without the key must stay locked")
	}
	if st, err := b.Unlock("open-sesame"); err != nil || st.Locked || st.Sealed {
		t.Fatalf("unlock after restart: %+v %v", st, err)
	}
	if r := b.TestServer(s.ID); !r.OK {
		t.Fatalf("after unlocking: %+v", r)
	}
	if sec, err := b.GetServerSecrets(s.ID); err != nil || sec.Password != srv.Password {
		t.Fatalf("secrets after unlocking: %+v %v", sec, err)
	}
	t.Setenv(envVaultKey, strings.ToUpper(hexOf(b.store.VaultKey())))
	child = reopenApp(t, a.dataDir)
	child.filesWindow = s.ID
	child.unlockFromParent()
	if child.GetLockState().Locked {
		t.Fatal("a file window started by an unlocked main window should not ask again")
	}
	if r := child.TestServer(s.ID); !r.OK {
		t.Fatalf("file window connection: %+v", r)
	}

	// 修改密码、设置自动锁定
	if _, err := b.SetLockPassword("open-sesame", "new-password"); err != nil {
		t.Fatal(err)
	}
	if st, err := b.SetAutoLock(10); err != nil || st.AutoLock != 10 {
		t.Fatalf("auto lock: %+v %v", st, err)
	}

	// 忘记密码：重置后服务器列表还在，密码和私钥被清空
	c := reopenApp(t, a.dataDir)
	if _, err := c.Unlock("open-sesame"); err == nil {
		t.Fatal("old password should no longer work")
	}
	res, err := c.ResetLock()
	if err != nil || res.State.Enabled || res.State.Locked || res.Wiped == 0 || res.Keys != 1 || res.Servers != 2 {
		t.Fatalf("reset: %+v %v", res, err)
	}
	servers := c.ListServers()
	if len(servers) != 2 || servers[0].HasPassword {
		t.Fatalf("servers after reset: %+v", servers)
	}
	if len(c.ListKeys()) != 0 {
		t.Fatal("keys without their private part should be removed")
	}
	if _, err := c.resolveEndpoint(keyServer.ID); err == nil {
		t.Fatal("a server whose key is gone cannot connect")
	}
	if _, err := c.ResetLock(); err == nil {
		t.Fatal("reset is only for a locked program")
	}
	// 重置后可以重新设置，取消时需要密码
	if _, err := c.SetLockPassword("", "third-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RemoveLockPassword("nope"); err == nil {
		t.Fatal("removing needs the password")
	}
	if st, err := c.RemoveLockPassword("third-password"); err != nil || st.Enabled {
		t.Fatalf("remove: %+v %v", st, err)
	}
	if reopenApp(t, a.dataDir).GetLockState().Locked {
		t.Fatal("after removing the password the program opens directly")
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, x := range b {
		out = append(out, digits[x>>4], digits[x&15])
	}
	return string(out)
}
