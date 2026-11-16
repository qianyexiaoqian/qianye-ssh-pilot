package testsshd

import (
	"bufio"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ProxyOptions 配置测试用的代理。
type ProxyOptions struct {
	// User 不为空时要求用户名密码认证。
	User, Pass string
	// Routes 把客户端请求的目标换成另一个地址，键可以是 "主机:端口" 或 "主机"：
	// 用来模拟“只有经过代理才能到达”的服务器。
	Routes map[string]string
	// Coalesce（仅 HTTP）：把目标发来的第一段数据和 200 应答放在同一次写入里。
	Coalesce bool
	// Lie（仅 HTTP）：不连接目标也应答成功。
	Lie bool
	// Listener 不为空时在它上面提供服务（需要固定端口时由调用方自己监听），否则监听一个随机端口。
	Listener net.Listener
}

// Proxy 是一个正在运行的测试代理。
type Proxy struct {
	Port int
	// Hits 是成功转发的连接数。
	Hits atomic.Int32
}

func (o ProxyOptions) target(host string, port int) string {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	if to, ok := o.Routes[addr]; ok {
		return to
	}
	if to, ok := o.Routes[host]; ok {
		return net.JoinHostPort(to, strconv.Itoa(port))
	}
	return addr
}

func relay(a, b net.Conn) {
	go func() { _, _ = io.Copy(a, b); a.Close() }()
	_, _ = io.Copy(b, a)
	b.Close()
}

func listenLocal(t testing.TB) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln
}

// StartSOCKS5 启动一个最小的 SOCKS5 代理（只支持 CONNECT）。
func StartSOCKS5(t testing.TB, opt ProxyOptions) *Proxy {
	ln := opt.Listener
	if ln == nil {
		ln = listenLocal(t)
	}
	p := &Proxy{Port: ln.Addr().(*net.TCPAddr).Port}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go p.socks5(c, opt)
		}
	}()
	return p
}

func (p *Proxy) socks5(c net.Conn, opt ProxyOptions) {
	defer c.Close()
	br := bufio.NewReader(c)
	head := make([]byte, 2)
	if _, err := io.ReadFull(br, head); err != nil || head[0] != 5 {
		return
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(br, methods); err != nil {
		return
	}
	if opt.User != "" {
		if !strings.Contains(string(methods), "\x02") {
			_, _ = c.Write([]byte{5, 0xff})
			return
		}
		_, _ = c.Write([]byte{5, 2})
		b := make([]byte, 2)
		_, _ = io.ReadFull(br, b)
		user := make([]byte, b[1])
		_, _ = io.ReadFull(br, user)
		_, _ = io.ReadFull(br, b[:1])
		pass := make([]byte, b[0])
		_, _ = io.ReadFull(br, pass)
		if string(user) != opt.User || string(pass) != opt.Pass {
			_, _ = c.Write([]byte{1, 1})
			return
		}
		_, _ = c.Write([]byte{1, 0})
	} else {
		_, _ = c.Write([]byte{5, 0})
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(br, req); err != nil {
		return
	}
	var host string
	switch req[3] {
	case 1:
		ip := make([]byte, 4)
		_, _ = io.ReadFull(br, ip)
		host = net.IP(ip).String()
	case 3:
		n, _ := br.ReadByte()
		name := make([]byte, n)
		_, _ = io.ReadFull(br, name)
		host = string(name)
	default:
		_, _ = c.Write([]byte{5, 8, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	pb := make([]byte, 2)
	_, _ = io.ReadFull(br, pb)
	up, err := net.DialTimeout("tcp", opt.target(host, int(pb[0])<<8|int(pb[1])), 3*time.Second)
	if err != nil {
		_, _ = c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	p.Hits.Add(1)
	// 应答里的绑定地址用域名形式，客户端要能正确跳过
	_, _ = c.Write(append([]byte{5, 0, 0, 3, 4}, []byte("bind\x00\x00")...))
	relay(c, up)
}

// StartHTTPProxy 启动一个只支持 CONNECT 的 HTTP 代理。
func StartHTTPProxy(t testing.TB, opt ProxyOptions) *Proxy {
	ln := opt.Listener
	if ln == nil {
		ln = listenLocal(t)
	}
	p := &Proxy{Port: ln.Addr().(*net.TCPAddr).Port}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go p.httpConnect(c, opt)
		}
	}()
	return p
}

func (p *Proxy) httpConnect(c net.Conn, opt ProxyOptions) {
	defer c.Close()
	req, err := http.ReadRequest(bufio.NewReader(c))
	if err != nil {
		_, _ = io.WriteString(c, "HTTP/1.1 400 Bad Request\r\n\r\n")
		return
	}
	if req.Method != http.MethodConnect {
		_, _ = io.WriteString(c, "HTTP/1.1 405 Method Not Allowed\r\nContent-Length: 0\r\n\r\n")
		return
	}
	if opt.User != "" && req.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(opt.User+":"+opt.Pass)) {
		_, _ = io.WriteString(c, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic\r\nContent-Length: 0\r\n\r\n")
		return
	}
	ok := "HTTP/1.1 200 Connection established\r\n\r\n"
	if opt.Lie {
		_, _ = io.WriteString(c, ok)
		time.Sleep(3 * time.Second)
		return
	}
	host, port, _ := net.SplitHostPort(req.Host)
	n, _ := strconv.Atoi(port)
	up, err := net.DialTimeout("tcp", opt.target(host, n), 3*time.Second)
	if err != nil {
		_, _ = io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
		return
	}
	p.Hits.Add(1)
	if opt.Coalesce {
		first := make([]byte, 512)
		n, _ := up.Read(first)
		_, _ = c.Write(append([]byte(ok), first[:n]...))
	} else {
		_, _ = io.WriteString(c, ok)
	}
	relay(c, up)
}
