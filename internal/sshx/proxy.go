package sshx

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 代理类型。ProxyAuto 用于本机代理软件（Clash、v2rayN 等）：它们的端口可能是 SOCKS5、HTTP 或两者混合，连接时自动识别。
const (
	ProxyHTTP   = "http"
	ProxySOCKS5 = "socks5"
	ProxyAuto   = "auto"
)

// Proxy 描述连接服务器前要经过的代理。
type Proxy struct {
	Type     string
	Host     string
	Port     int
	User     string
	Password string
}

func (p *Proxy) Addr() string { return net.JoinHostPort(p.Host, strconv.Itoa(p.Port)) }

// ProxyError 是代理这一段出的错：stage 为 connect 表示连不上代理本身，auth 表示代理拒绝了账号，
// target 表示代理连不上目标服务器。
type ProxyError struct {
	Proxy string
	Stage string
	Err   error
}

func (e *ProxyError) Error() string {
	switch e.Stage {
	case "connect":
		msg := e.Err.Error()
		switch {
		case strings.Contains(msg, "refused"):
			msg = "这个端口上没有代理在运行，请确认代理软件已经启动、端口没有填错"
		case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "failed to respond"):
			msg = "连接超时，请检查代理的地址和端口"
		case strings.Contains(msg, "no such host"):
			msg = "无法解析代理的地址"
		}
		return fmt.Sprintf("连不上代理 %s：%s", e.Proxy, msg)
	case "auth":
		return fmt.Sprintf("代理认证失败（%s）：%v", e.Proxy, e.Err)
	default:
		return fmt.Sprintf("代理 %s 连不上目标服务器：%v", e.Proxy, e.Err)
	}
}

func (e *ProxyError) Unwrap() error { return e.Err }

// errNotSOCKS 表示对端不是 SOCKS5 代理（自动识别时据此改用 HTTP）。
var errNotSOCKS = errors.New("对端不是 SOCKS5 代理")

// autoProto 记住自动识别出的协议（按代理地址），之后的连接不用再试一遍。
var autoProto sync.Map

// socksGreetWait 是自动识别时等待 SOCKS5 握手应答的时间：SOCKS5 代理会立刻应答，纯 HTTP 代理收到这几个字节后只会一直等。
const socksGreetWait = 1500 * time.Millisecond

// DialProxy 通过代理建立到 host:port 的 TCP 连接。
func DialProxy(ctx context.Context, p *Proxy, host string, port int, timeout time.Duration) (net.Conn, error) {
	if p.Host == "" || p.Port <= 0 || p.Port > 65535 {
		return nil, &ProxyError{Proxy: p.Addr(), Stage: "connect", Err: errors.New("代理地址或端口没有填写")}
	}
	switch p.Type {
	case ProxyHTTP:
		return dialHTTPProxy(ctx, p, host, port, timeout)
	case ProxySOCKS5:
		return dialSOCKS5(ctx, p, host, port, timeout, 0)
	}
	if known, ok := autoProto.Load(p.Addr()); ok && known == ProxyHTTP {
		conn, err := dialHTTPProxy(ctx, p, host, port, timeout)
		var pe *ProxyError
		if err == nil || (errors.As(err, &pe) && pe.Stage != "connect") {
			return conn, err
		}
		// 代理本身连不上或不再按 HTTP 应答（可能换了软件）：重新识别
		autoProto.Delete(p.Addr())
	}
	conn, err := dialSOCKS5(ctx, p, host, port, timeout, socksGreetWait)
	if err == nil {
		autoProto.Store(p.Addr(), ProxySOCKS5)
		return conn, nil
	}
	if !errors.Is(err, errNotSOCKS) {
		return nil, err
	}
	conn, err = dialHTTPProxy(ctx, p, host, port, timeout)
	if err == nil {
		autoProto.Store(p.Addr(), ProxyHTTP)
	}
	return conn, err
}

// proxyConn 连接代理本身，并在握手期间响应 ctx 的取消与超时；握手结束后调用 done 解除限制。
func proxyConn(ctx context.Context, p *Proxy, timeout time.Duration) (conn net.Conn, done func(), err error) {
	d := net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	conn, err = d.DialContext(ctx, "tcp", p.Addr())
	if err != nil {
		return nil, nil, &ProxyError{Proxy: p.Addr(), Stage: "connect", Err: err}
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-stop:
		}
	}()
	return conn, func() {
		close(stop)
		_ = conn.SetDeadline(time.Time{})
	}, nil
}

// bufferedConn 让握手时多读进缓冲区的数据（SSH 服务器紧跟在代理应答后面发来的版本号）不丢失。
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func dialHTTPProxy(ctx context.Context, p *Proxy, host string, port int, timeout time.Duration) (net.Conn, error) {
	conn, done, err := proxyConn(ctx, p, timeout)
	if err != nil {
		return nil, err
	}
	target := net.JoinHostPort(host, strconv.Itoa(port))
	var b strings.Builder
	fmt.Fprintf(&b, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Connection: Keep-Alive\r\n", target, target)
	if p.User != "" {
		b.WriteString("Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(p.User+":"+p.Password)) + "\r\n")
	}
	b.WriteString("\r\n")
	fail := func(stage string, err error) (net.Conn, error) {
		done()
		conn.Close()
		return nil, &ProxyError{Proxy: p.Addr(), Stage: stage, Err: err}
	}
	if _, err := io.WriteString(conn, b.String()); err != nil {
		return fail("connect", err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		return fail("connect", fmt.Errorf("代理没有按 HTTP 协议应答（%v）", err))
	}
	resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusProxyAuthRequired:
		return fail("auth", errors.New("需要正确的用户名和密码"))
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return fail("target", errors.New(resp.Status))
	}
	done()
	return &bufferedConn{Conn: conn, r: br}, nil
}

var socksReplies = map[byte]string{
	1: "代理内部错误",
	2: "代理的规则不允许这个连接",
	3: "网络不可达",
	4: "主机不可达",
	5: "连接被拒绝",
	6: "连接超时",
	7: "代理不支持这种请求",
	8: "代理不支持这种地址",
}

// dialSOCKS5 按 RFC 1928 / 1929 握手。greetWait 大于 0 时只等这么久的首个应答，等不到就认为对端不是 SOCKS5。
func dialSOCKS5(ctx context.Context, p *Proxy, host string, port int, timeout, greetWait time.Duration) (net.Conn, error) {
	conn, done, err := proxyConn(ctx, p, timeout)
	if err != nil {
		return nil, err
	}
	fail := func(stage string, err error) (net.Conn, error) {
		done()
		conn.Close()
		if errors.Is(err, errNotSOCKS) {
			return nil, err
		}
		return nil, &ProxyError{Proxy: p.Addr(), Stage: stage, Err: err}
	}
	methods := []byte{0}
	if p.User != "" {
		methods = []byte{0, 2}
	}
	if _, err := conn.Write(append([]byte{5, byte(len(methods))}, methods...)); err != nil {
		return fail("connect", err)
	}
	if greetWait > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(greetWait))
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil || reply[0] != 5 {
		if greetWait > 0 {
			return fail("connect", errNotSOCKS)
		}
		if err == nil {
			err = errors.New("对端不是 SOCKS5 代理")
		}
		return fail("connect", err)
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	switch reply[1] {
	case 0:
	case 2:
		if len(p.User) > 255 || len(p.Password) > 255 {
			return fail("auth", errors.New("用户名或密码过长"))
		}
		req := append([]byte{1, byte(len(p.User))}, p.User...)
		req = append(append(req, byte(len(p.Password))), p.Password...)
		if _, err := conn.Write(req); err != nil {
			return fail("auth", err)
		}
		if _, err := io.ReadFull(conn, reply); err != nil {
			return fail("auth", err)
		}
		if reply[1] != 0 {
			return fail("auth", errors.New("用户名或密码不正确"))
		}
	default:
		return fail("auth", errors.New("代理要求的认证方式不受支持（需要填写用户名和密码）"))
	}
	req := []byte{5, 1, 0}
	if ip := net.ParseIP(host); ip == nil {
		if len(host) > 255 {
			return fail("target", errors.New("主机名过长"))
		}
		// 域名交给代理去解析：只有代理那一侧能解析或访问的地址也能连上
		req = append(append(req, 3, byte(len(host))), host...)
	} else if v4 := ip.To4(); v4 != nil {
		req = append(append(req, 1), v4...)
	} else {
		req = append(append(req, 4), ip.To16()...)
	}
	req = append(req, byte(port>>8), byte(port))
	if _, err := conn.Write(req); err != nil {
		return fail("target", err)
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return fail("target", err)
	}
	if head[1] != 0 {
		msg := socksReplies[head[1]]
		if msg == "" {
			msg = fmt.Sprintf("代理返回错误码 %d", head[1])
		}
		return fail("target", errors.New(msg))
	}
	// 丢弃应答里的绑定地址
	skip := 0
	switch head[3] {
	case 1:
		skip = 4
	case 4:
		skip = 16
	case 3:
		n := make([]byte, 1)
		if _, err := io.ReadFull(conn, n); err != nil {
			return fail("target", err)
		}
		skip = int(n[0])
	default:
		return fail("target", errors.New("代理的应答格式不正确"))
	}
	if _, err := io.ReadFull(conn, make([]byte, skip+2)); err != nil {
		return fail("target", err)
	}
	done()
	return conn, nil
}

// ProbeVia 测试经过代理能否连到服务器的 SSH 端口并返回延迟（毫秒）；proxy 为空时与 Probe 相同。
// 有些代理软件在连上目标之前就应答成功，所以这里还要等到 SSH 服务发来版本号才算在线。
func ProbeVia(ctx context.Context, host string, port int, proxy *Proxy, timeout time.Duration) (int, error) {
	if proxy == nil {
		return Probe(ctx, host, port, timeout)
	}
	start := time.Now()
	conn, err := DialProxy(ctx, proxy, host, port, timeout)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	banner := make([]byte, 4)
	if _, err := io.ReadFull(conn, banner); err != nil || string(banner) != "SSH-" {
		return 0, &ProxyError{Proxy: proxy.Addr(), Stage: "target", Err: errors.New("没有收到 SSH 服务的应答")}
	}
	return int(time.Since(start).Milliseconds()), nil
}

// SniffProxy 探测本机某个端口上是不是代理：返回 socks5、http 或空字符串。
func SniffProxy(addr string, timeout time.Duration) string {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return ""
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	reply := make([]byte, 2)
	_, _ = conn.Write([]byte{5, 1, 0})
	_, err = io.ReadFull(conn, reply)
	conn.Close()
	if err == nil && reply[0] == 5 {
		return ProxySOCKS5
	}
	conn, err = net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return ""
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	_, _ = io.WriteString(conn, "OPTIONS * HTTP/1.1\r\nHost: localhost\r\n\r\n")
	head := make([]byte, 5)
	if _, err := io.ReadFull(conn, head); err == nil && string(head) == "HTTP/" {
		return ProxyHTTP
	}
	return ""
}
