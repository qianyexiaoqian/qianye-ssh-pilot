package sshx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
)

// Endpoint 是一次连接所需的全部信息（凭据已解密）。
type Endpoint struct {
	ServerID     string
	Name         string
	Host         string
	Port         int
	User         string
	Password     string
	PrivateKey   string
	Passphrase   string
	HostKey      string // 已知主机公钥（authorized_keys 格式），为空表示首次连接
	UseSudo      bool
	SudoPassword string
	Jump         *Endpoint
	// Proxy 不为空时先连代理、再由代理连到服务器；有跳板机时由跳板机负责连接，这里的代理不生效。
	Proxy *Proxy
}

func (e *Endpoint) Addr() string { return net.JoinHostPort(e.Host, strconv.Itoa(e.Port)) }

// IsRoot 表示命令将直接以 root 身份执行。
func (e *Endpoint) IsRoot() bool { return e.User == "root" }

// HostKeyLearner 在首次连接（TOFU）时被调用以保存主机公钥。
type HostKeyLearner func(serverID, key, fingerprint string)

type HostKeyMismatchError struct {
	Server   string
	Expected string
	Got      string
}

func (e *HostKeyMismatchError) Error() string {
	return fmt.Sprintf("主机密钥与记录不一致（%s）：记录 %s，实际 %s。若服务器重装过系统，可在服务器详情中“信任新主机密钥”；否则可能存在中间人攻击。", e.Server, e.Expected, e.Got)
}

// Client 包装 ssh.Client，额外持有跳板机连接并做保活。
type Client struct {
	*ssh.Client
	jump      *Client
	ep        *Endpoint
	closed    atomic.Bool
	closeOnce sync.Once
	done      chan struct{}
}

func (c *Client) Endpoint() *Endpoint { return c.ep }
func (c *Client) Closed() bool        { return c.closed.Load() }

func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		close(c.done)
		err = c.Client.Close()
		if c.jump != nil {
			c.jump.Close()
		}
	})
	return err
}

func (c *Client) keepalive() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			if _, _, err := c.SendRequest("keepalive@openssh.com", true, nil); err != nil {
				c.Close()
				return
			}
		}
	}
}

func hostKeyAlgorithms(keyType string) []string {
	switch keyType {
	case ssh.KeyAlgoRSA:
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	case "":
		return nil
	default:
		return []string{keyType}
	}
}

func clientConfig(ep *Endpoint, learn HostKeyLearner) (*ssh.ClientConfig, error) {
	var auths []ssh.AuthMethod
	if ep.PrivateKey != "" {
		signer, err := ParseSigner(ep.PrivateKey, ep.Passphrase)
		if err != nil {
			return nil, err
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}
	if ep.Password != "" {
		pw := ep.Password
		auths = append(auths,
			ssh.Password(pw),
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range questions {
					answers[i] = pw
				}
				return answers, nil
			}),
		)
	}
	if len(auths) == 0 {
		return nil, errors.New("未配置登录凭据（私钥或密码）")
	}
	var known ssh.PublicKey
	if ep.HostKey != "" {
		if k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(ep.HostKey)); err == nil {
			known = k
		}
	}
	cfg := &ssh.ClientConfig{
		User:          ep.User,
		Auth:          auths,
		ClientVersion: "SSH-2.0-SSHPilot_1.0",
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			fp := ssh.FingerprintSHA256(key)
			if known == nil {
				if learn != nil {
					learn(ep.ServerID, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))), fp)
				}
				return nil
			}
			if bytes.Equal(known.Marshal(), key.Marshal()) {
				return nil
			}
			return &HostKeyMismatchError{Server: ep.Name, Expected: ssh.FingerprintSHA256(known), Got: fp}
		},
	}
	if known != nil {
		cfg.HostKeyAlgorithms = hostKeyAlgorithms(known.Type())
	}
	return cfg, nil
}

// Dial 建立 SSH 连接（支持一级或多级跳板机）。
func Dial(ctx context.Context, ep *Endpoint, learn HostKeyLearner, timeout time.Duration) (*Client, error) {
	cfg, err := clientConfig(ep, learn)
	if err != nil {
		return nil, err
	}
	var (
		conn net.Conn
		jump *Client
	)
	if ep.Jump != nil {
		jump, err = Dial(ctx, ep.Jump, learn, timeout)
		if err != nil {
			return nil, fmt.Errorf("连接跳板机 %s 失败: %w", ep.Jump.Name, err)
		}
		dctx, cancel := context.WithTimeout(ctx, timeout)
		conn, err = jump.DialContext(dctx, "tcp", ep.Addr())
		cancel()
	} else if ep.Proxy != nil {
		conn, err = DialProxy(ctx, ep.Proxy, ep.Host, ep.Port, timeout)
	} else {
		d := net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
		conn, err = d.DialContext(ctx, "tcp", ep.Addr())
	}
	if err != nil {
		if jump != nil {
			jump.Close()
		}
		return nil, friendlyError(err)
	}

	// 握手超时：通过跳板机得到的连接不支持 deadline，用定时器关闭连接来中断握手。
	handshakeDone := make(chan struct{})
	go func() {
		select {
		case <-handshakeDone:
		case <-ctx.Done():
			conn.Close()
		case <-time.After(timeout):
			conn.Close()
		}
	}()
	c, chans, reqs, err := ssh.NewClientConn(conn, ep.Addr(), cfg)
	close(handshakeDone)
	if err != nil {
		conn.Close()
		if jump != nil {
			jump.Close()
		}
		return nil, friendlyError(err)
	}
	cl := &Client{Client: ssh.NewClient(c, chans, reqs), jump: jump, ep: ep, done: make(chan struct{})}
	go func() {
		_ = cl.Wait()
		cl.Close()
	}()
	go cl.keepalive()
	return cl, nil
}

func friendlyError(err error) error {
	var mismatch *HostKeyMismatchError
	if errors.As(err, &mismatch) {
		return mismatch
	}
	// 代理这一段的错误已经写明是哪里出的问题
	var viaProxy *ProxyError
	if errors.As(err, &viaProxy) {
		return err
	}
	msg := err.Error()
	var hint string
	switch {
	case strings.Contains(msg, "unable to authenticate"):
		hint = "认证失败：用户名、密码或私钥不正确，或服务器未授权该公钥"
	case strings.Contains(msg, "connection refused") || strings.Contains(msg, "actively refused"):
		hint = "连接被拒绝：端口未开放或 SSH 服务未运行"
	case strings.Contains(msg, "i/o timeout") || strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "failed to respond"):
		hint = "连接超时：请检查地址、端口以及网络/防火墙"
	case strings.Contains(msg, "no such host"):
		hint = "无法解析主机名"
	case strings.Contains(msg, "use of closed network connection") || strings.Contains(msg, "EOF"):
		hint = "连接在握手阶段被关闭（可能超时或被服务器拒绝）"
	case strings.Contains(msg, "no route to host") || strings.Contains(msg, "unreachable"):
		hint = "网络不可达"
	}
	if hint == "" {
		return err
	}
	return fmt.Errorf("%s（%s）", hint, msg)
}

// IsAuthError 判断错误是否属于重试也无法恢复的认证类错误（密码或密钥不正确、主机密钥不匹配）。
// 这类错误不应自动重试，否则可能触发服务器的防暴力破解封禁。
func IsAuthError(err error) bool {
	if err == nil {
		return false
	}
	var mismatch *HostKeyMismatchError
	if errors.As(err, &mismatch) {
		return true
	}
	var viaProxy *ProxyError
	if errors.As(err, &viaProxy) {
		return viaProxy.Stage == "auth"
	}
	msg := err.Error()
	for _, kw := range []string{"unable to authenticate", "未配置登录凭据", "私钥已加密", "密码短语不正确", "无法解析私钥", "PuTTY"} {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}

// Resolver 根据服务器 ID 构造 Endpoint。
type Resolver func(serverID string) (*Endpoint, error)

// Pool 为命令执行复用每台服务器的 SSH 连接；终端使用独立连接。
type Pool struct {
	resolve Resolver
	learn   HostKeyLearner
	Timeout time.Duration
	// OnConnect 在每次新建连接成功后调用（连接池里的连接和终端的独立连接都算）；回调里不要做耗时的事。
	OnConnect func(serverID string, c *Client)

	mu      sync.Mutex
	clients map[string]*Client
	locks   map[string]*sync.Mutex
	sftps   map[string]*sftpEntry
}

func NewPool(resolve Resolver, learn HostKeyLearner) *Pool {
	return &Pool{
		resolve: resolve,
		learn:   learn,
		Timeout: 12 * time.Second,
		clients: map[string]*Client{},
		locks:   map[string]*sync.Mutex{},
		sftps:   map[string]*sftpEntry{},
	}
}

func (p *Pool) lockFor(id string) *sync.Mutex {
	p.mu.Lock()
	defer p.mu.Unlock()
	l := p.locks[id]
	if l == nil {
		l = &sync.Mutex{}
		p.locks[id] = l
	}
	return l
}

func (p *Pool) Get(ctx context.Context, serverID string) (*Client, error) {
	l := p.lockFor(serverID)
	l.Lock()
	defer l.Unlock()
	p.mu.Lock()
	c := p.clients[serverID]
	p.mu.Unlock()
	if c != nil && !c.Closed() {
		return c, nil
	}
	c, err := p.DialFresh(ctx, serverID)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.clients[serverID] = c
	p.mu.Unlock()
	return c, nil
}

// DialFresh 新建一条不进入连接池的连接。
func (p *Pool) DialFresh(ctx context.Context, serverID string) (*Client, error) {
	ep, err := p.resolve(serverID)
	if err != nil {
		return nil, err
	}
	c, err := Dial(ctx, ep, p.learn, p.Timeout)
	if err == nil && p.OnConnect != nil {
		p.OnConnect(serverID, c)
	}
	return c, err
}

// Invalidate 关闭并移除缓存的连接（服务器配置变化或连接失效时调用）。
func (p *Pool) Invalidate(serverID string) {
	p.dropSFTP(serverID)
	p.mu.Lock()
	c := p.clients[serverID]
	delete(p.clients, serverID)
	p.mu.Unlock()
	if c != nil {
		c.Close()
	}
}

func (p *Pool) Close() {
	p.mu.Lock()
	clients := p.clients
	p.clients = map[string]*Client{}
	sftps := p.sftps
	p.sftps = map[string]*sftpEntry{}
	p.mu.Unlock()
	for _, e := range sftps {
		e.client.Close()
	}
	for _, c := range clients {
		c.Close()
	}
}

// session 从池中取连接并打开会话；会话打开失败时重连一次。
func (p *Pool) session(ctx context.Context, serverID string) (*ssh.Session, *Client, error) {
	c, err := p.Get(ctx, serverID)
	if err != nil {
		return nil, nil, err
	}
	s, err := c.NewSession()
	if err == nil {
		return s, c, nil
	}
	p.Invalidate(serverID)
	if c, err = p.Get(ctx, serverID); err != nil {
		return nil, nil, err
	}
	s, err = c.NewSession()
	if err != nil {
		return nil, nil, fmt.Errorf("打开 SSH 会话失败: %w", err)
	}
	return s, c, nil
}

// Probe 测试 TCP 可达性并返回延迟（毫秒）。
func Probe(ctx context.Context, host string, port int, timeout time.Duration) (int, error) {
	start := time.Now()
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return 0, err
	}
	conn.Close()
	return int(time.Since(start).Milliseconds()), nil
}
