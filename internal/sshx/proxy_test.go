package sshx_test

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sshpilot/internal/sshx"
	"sshpilot/internal/testsshd"
)

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln
}

func portOf(ln net.Listener) int { return ln.Addr().(*net.TCPAddr).Port }

func startSOCKS5(t *testing.T, user, pass string) (port int, hits *atomic.Int32) {
	p := testsshd.StartSOCKS5(t, testsshd.ProxyOptions{User: user, Pass: pass})
	return p.Port, &p.Hits
}

func startHTTPProxy(t *testing.T, user, pass string, coalesce, lie bool) (port int, hits *atomic.Int32) {
	p := testsshd.StartHTTPProxy(t, testsshd.ProxyOptions{User: user, Pass: pass, Coalesce: coalesce, Lie: lie})
	return p.Port, &p.Hits
}

func viaProxy(t *testing.T, srv *testsshd.Server, p *sshx.Proxy) (string, error) {
	t.Helper()
	ep := &sshx.Endpoint{ServerID: "s1", Name: "test", Host: srv.Host, Port: srv.Port, User: srv.User, Password: srv.Password, Proxy: p}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cl, err := sshx.Dial(ctx, ep, nil, 8*time.Second)
	if err != nil {
		return "", err
	}
	defer cl.Close()
	s, err := cl.NewSession()
	if err != nil {
		return "", err
	}
	defer s.Close()
	out, err := s.Output("echo via-proxy")
	return strings.TrimSpace(string(out)), err
}

func TestDialThroughSOCKS5(t *testing.T) {
	srv := testsshd.Start(t)
	port, hits := startSOCKS5(t, "", "")
	out, err := viaProxy(t, srv, &sshx.Proxy{Type: sshx.ProxySOCKS5, Host: "127.0.0.1", Port: port})
	if err != nil || out != "via-proxy" || hits.Load() != 1 {
		t.Fatalf("socks5: out=%q err=%v hits=%d", out, err, hits.Load())
	}

	// 用户名密码认证
	port, hits = startSOCKS5(t, "alice", "s3cret")
	out, err = viaProxy(t, srv, &sshx.Proxy{Type: sshx.ProxySOCKS5, Host: "127.0.0.1", Port: port, User: "alice", Password: "s3cret"})
	if err != nil || out != "via-proxy" || hits.Load() != 1 {
		t.Fatalf("socks5 auth: out=%q err=%v", out, err)
	}
	_, err = viaProxy(t, srv, &sshx.Proxy{Type: sshx.ProxySOCKS5, Host: "127.0.0.1", Port: port, User: "alice", Password: "wrong"})
	var pe *sshx.ProxyError
	if !errors.As(err, &pe) || pe.Stage != "auth" || !sshx.IsAuthError(err) {
		t.Fatalf("wrong password should be an auth error, got %v", err)
	}
	// 代理要求认证但没有填写账号
	_, err = viaProxy(t, srv, &sshx.Proxy{Type: sshx.ProxySOCKS5, Host: "127.0.0.1", Port: port})
	if !errors.As(err, &pe) || pe.Stage != "auth" {
		t.Fatalf("missing credentials: %v", err)
	}
}

func TestDialThroughHTTPProxy(t *testing.T) {
	srv := testsshd.Start(t)
	for _, coalesce := range []bool{false, true} {
		port, hits := startHTTPProxy(t, "", "", coalesce, false)
		out, err := viaProxy(t, srv, &sshx.Proxy{Type: sshx.ProxyHTTP, Host: "127.0.0.1", Port: port})
		if err != nil || out != "via-proxy" || hits.Load() != 1 {
			t.Fatalf("http proxy (coalesce=%v): out=%q err=%v", coalesce, out, err)
		}
	}
	port, _ := startHTTPProxy(t, "bob", "pw", false, false)
	if out, err := viaProxy(t, srv, &sshx.Proxy{Type: sshx.ProxyHTTP, Host: "127.0.0.1", Port: port, User: "bob", Password: "pw"}); err != nil || out != "via-proxy" {
		t.Fatalf("http proxy auth: out=%q err=%v", out, err)
	}
	_, err := viaProxy(t, srv, &sshx.Proxy{Type: sshx.ProxyHTTP, Host: "127.0.0.1", Port: port})
	var pe *sshx.ProxyError
	if !errors.As(err, &pe) || pe.Stage != "auth" {
		t.Fatalf("407 should be an auth error, got %v", err)
	}
}

func TestProxyAutoDetectsProtocol(t *testing.T) {
	srv := testsshd.Start(t)
	socks, _ := startSOCKS5(t, "", "")
	if out, err := viaProxy(t, srv, &sshx.Proxy{Type: sshx.ProxyAuto, Host: "127.0.0.1", Port: socks}); err != nil || out != "via-proxy" {
		t.Fatalf("auto on a socks5 port: out=%q err=%v", out, err)
	}
	// 纯 HTTP 代理收到 SOCKS 握手后不会应答：等一小会儿后改用 HTTP，并记住结果
	httpPort, _ := startHTTPProxy(t, "", "", false, false)
	p := &sshx.Proxy{Type: sshx.ProxyAuto, Host: "127.0.0.1", Port: httpPort}
	if out, err := viaProxy(t, srv, p); err != nil || out != "via-proxy" {
		t.Fatalf("auto on an http port: out=%q err=%v", out, err)
	}
	start := time.Now()
	conn, err := sshx.DialProxy(context.Background(), p, srv.Host, srv.Port, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if d := time.Since(start); d > time.Second {
		t.Fatalf("second connection should reuse the detected protocol, took %s", d)
	}
}

func TestProxyFailures(t *testing.T) {
	srv := testsshd.Start(t)
	// 代理没有在监听
	ln := listen(t)
	dead := portOf(ln)
	ln.Close()
	_, err := viaProxy(t, srv, &sshx.Proxy{Type: sshx.ProxySOCKS5, Host: "127.0.0.1", Port: dead})
	var pe *sshx.ProxyError
	if !errors.As(err, &pe) || pe.Stage != "connect" || !strings.Contains(err.Error(), "连不上代理") {
		t.Fatalf("dead proxy: %v", err)
	}
	// 代理正常，但目标端口没有服务
	socks, _ := startSOCKS5(t, "", "")
	ep := &sshx.Endpoint{Name: "x", Host: "127.0.0.1", Port: dead, User: "u", Password: "p", Proxy: &sshx.Proxy{Type: sshx.ProxySOCKS5, Host: "127.0.0.1", Port: socks}}
	_, err = sshx.Dial(context.Background(), ep, nil, 5*time.Second)
	if !errors.As(err, &pe) || pe.Stage != "target" || !strings.Contains(err.Error(), "连接被拒绝") || sshx.IsAuthError(err) {
		t.Fatalf("dead target via socks5: %v", err)
	}
	httpPort, _ := startHTTPProxy(t, "", "", false, false)
	ep.Proxy = &sshx.Proxy{Type: sshx.ProxyHTTP, Host: "127.0.0.1", Port: httpPort}
	_, err = sshx.Dial(context.Background(), ep, nil, 5*time.Second)
	if !errors.As(err, &pe) || pe.Stage != "target" || !strings.Contains(err.Error(), "502") {
		t.Fatalf("dead target via http proxy: %v", err)
	}
	// 没有填写端口
	if _, err := sshx.DialProxy(context.Background(), &sshx.Proxy{Type: sshx.ProxyHTTP, Host: "127.0.0.1"}, "h", 22, time.Second); err == nil {
		t.Fatal("a proxy without a port should be rejected")
	}
}

func TestProbeViaAndSniff(t *testing.T) {
	srv := testsshd.Start(t)
	socks, _ := startSOCKS5(t, "", "")
	httpPort, _ := startHTTPProxy(t, "", "", false, false)
	for _, p := range []*sshx.Proxy{{Type: sshx.ProxySOCKS5, Host: "127.0.0.1", Port: socks}, {Type: sshx.ProxyHTTP, Host: "127.0.0.1", Port: httpPort}} {
		if _, err := sshx.ProbeVia(context.Background(), srv.Host, srv.Port, p, 5*time.Second); err != nil {
			t.Fatalf("probe via %s: %v", p.Type, err)
		}
	}
	if _, err := sshx.ProbeVia(context.Background(), srv.Host, srv.Port, nil, 5*time.Second); err != nil {
		t.Fatalf("direct probe: %v", err)
	}
	// 有的代理不管目标通不通都先应答成功：没有等到 SSH 版本号不算在线
	liar, _ := startHTTPProxy(t, "", "", false, true)
	if _, err := sshx.ProbeVia(context.Background(), srv.Host, srv.Port, &sshx.Proxy{Type: sshx.ProxyHTTP, Host: "127.0.0.1", Port: liar}, time.Second); err == nil {
		t.Fatal("a proxy that answers 200 without reaching the server must not count as online")
	}

	if got := sshx.SniffProxy(net.JoinHostPort("127.0.0.1", strconv.Itoa(socks)), time.Second); got != sshx.ProxySOCKS5 {
		t.Fatalf("sniff socks5 port: %q", got)
	}
	if got := sshx.SniffProxy(net.JoinHostPort("127.0.0.1", strconv.Itoa(httpPort)), time.Second); got != sshx.ProxyHTTP {
		t.Fatalf("sniff http port: %q", got)
	}
	if got := sshx.SniffProxy(net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port)), time.Second); got != "" {
		t.Fatalf("an ssh port is not a proxy, got %q", got)
	}
}

func TestDetectOS(t *testing.T) {
	if got := sshx.ParseOSProbe("Welcome to my server!\r\nLast login: today\r\n@os=Ubuntu 24.04.1 LTS\r\n"); got != "Ubuntu 24.04.1 LTS" {
		t.Fatalf("parse: %q", got)
	}
	if got := sshx.ParseOSProbe("@os=\n"); got != "" {
		t.Fatalf("empty name: %q", got)
	}
	if got := sshx.ParseOSProbe("sh: command not found\n"); got != "" {
		t.Fatalf("no marker: %q", got)
	}
	srv := testsshd.Start(t)
	ep := &sshx.Endpoint{Name: "test", Host: srv.Host, Port: srv.Port, User: srv.User, Password: srv.Password}
	cl, err := sshx.Dial(context.Background(), ep, nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	if name := sshx.DetectOS(cl, 10*time.Second); name == "" || strings.ContainsAny(name, "\r\n") {
		t.Fatalf("DetectOS returned %q", name)
	}
}

func TestPoolOnConnect(t *testing.T) {
	srv := testsshd.Start(t)
	ep := &sshx.Endpoint{ServerID: "s1", Name: "test", Host: srv.Host, Port: srv.Port, User: srv.User, Password: srv.Password}
	pool := newPool(t, map[string]*sshx.Endpoint{"s1": ep})
	var calls atomic.Int32
	pool.OnConnect = func(id string, c *sshx.Client) {
		if id == "s1" && c != nil {
			calls.Add(1)
		}
	}
	ctx := context.Background()
	for range 2 {
		if _, err := pool.Exec(ctx, "s1", "true", sshx.ExecOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("pooled connection should report once, got %d", calls.Load())
	}
	c, err := pool.DialFresh(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if calls.Load() != 2 {
		t.Fatalf("a fresh connection should report too, got %d", calls.Load())
	}
}
