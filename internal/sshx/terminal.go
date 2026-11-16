package sshx

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Emitter 把事件推送给前端。
type Emitter func(name string, data ...any)

type terminal struct {
	id       string
	serverID string
	client   *Client
	sess     *ssh.Session
	stdin    io.WriteCloser
	out      *termOutput
	once     sync.Once
}

// termOutput 合并高频的小块输出，降低事件数量；数据以 base64 发送以保证 UTF-8 多字节字符不被截断。
// 前端订阅事件后调用 Ready，在此之前的输出（登录提示、首个命令行提示符）先缓存，避免丢失。
type termOutput struct {
	mu        sync.Mutex
	buf       []byte
	recent    []byte // 最近的输出（供 AI 运维读取屏幕内容），超过上限时丢弃最早的部分
	lastWrite time.Time
	notify    chan struct{}
	done      chan struct{}
	ready     chan struct{}
	readyOnce sync.Once
	event     string
	emit      Emitter
}

func (o *termOutput) markReady() { o.readyOnce.Do(func() { close(o.ready) }) }

// waitReady 等待前端就绪；前端异常时最多等待 3 秒。
func (o *termOutput) waitReady() {
	select {
	case <-o.ready:
	case <-time.After(3 * time.Second):
	}
}

const recentLimit = 96 * 1024

func (o *termOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	o.buf = append(o.buf, p...)
	o.recent = append(o.recent, p...)
	if len(o.recent) > recentLimit*2 {
		o.recent = append([]byte(nil), o.recent[len(o.recent)-recentLimit:]...)
	}
	o.lastWrite = time.Now()
	o.mu.Unlock()
	select {
	case o.notify <- struct{}{}:
	default:
	}
	return len(p), nil
}

func (o *termOutput) flush() {
	o.mu.Lock()
	data := o.buf
	o.buf = nil
	o.mu.Unlock()
	if len(data) > 0 {
		o.emit(o.event, base64.StdEncoding.EncodeToString(data))
	}
}

func (o *termOutput) loop() {
	o.waitReady()
	o.flush()
	for {
		select {
		case <-o.notify:
			time.Sleep(6 * time.Millisecond)
			o.flush()
		case <-o.done:
			o.flush()
			return
		}
	}
}

type TerminalInfo struct {
	ID       string `json:"id"`
	ServerID string `json:"serverId"`
}

type TerminalManager struct {
	pool *Pool
	emit Emitter
	// OnClose 在某个终端结束后调用（用户关闭或连接断开）。
	OnClose func(serverID string)
	mu      sync.Mutex
	terms   map[string]*terminal
	seq     int
}

func NewTerminalManager(pool *Pool, emit Emitter) *TerminalManager {
	return &TerminalManager{pool: pool, emit: emit, terms: map[string]*terminal{}}
}

// Open 为服务器打开一个交互式终端（独立连接 + PTY）。
func (m *TerminalManager) Open(ctx context.Context, serverID string, cols, rows int) (string, error) {
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 32
	}
	cl, err := m.pool.DialFresh(ctx, serverID)
	if err != nil {
		return "", err
	}
	sess, err := cl.NewSession()
	if err != nil {
		cl.Close()
		return "", fmt.Errorf("打开会话失败: %w", err)
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := sess.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		sess.Close()
		cl.Close()
		return "", fmt.Errorf("申请伪终端失败: %w", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		cl.Close()
		return "", err
	}
	m.mu.Lock()
	m.seq++
	id := fmt.Sprintf("t%d-%d", time.Now().UnixMilli()%100000, m.seq)
	m.mu.Unlock()

	out := &termOutput{notify: make(chan struct{}, 1), done: make(chan struct{}), ready: make(chan struct{}), event: "term:data:" + id, emit: m.emit}
	sess.Stdout = out
	sess.Stderr = out
	if err := sess.Shell(); err != nil {
		sess.Close()
		cl.Close()
		return "", fmt.Errorf("启动 Shell 失败: %w", err)
	}
	t := &terminal{id: id, serverID: serverID, client: cl, sess: sess, stdin: stdin, out: out}
	m.mu.Lock()
	m.terms[id] = t
	m.mu.Unlock()
	go out.loop()
	go func() {
		err := sess.Wait()
		reason := "连接已关闭"
		if err != nil {
			if exitErr, ok := err.(*ssh.ExitError); ok {
				reason = fmt.Sprintf("会话已结束（退出码 %d）", exitErr.ExitStatus())
			} else if _, ok := err.(*ssh.ExitMissingError); !ok {
				reason = "连接中断: " + err.Error()
			}
		} else {
			reason = "会话已结束"
		}
		m.finish(id, reason)
	}()
	return id, nil
}

func (m *TerminalManager) get(id string) *terminal {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.terms[id]
}

func (m *TerminalManager) Write(id, data string) error {
	t := m.get(id)
	if t == nil {
		return fmt.Errorf("终端不存在或已关闭")
	}
	_, err := t.stdin.Write([]byte(data))
	return err
}

func (m *TerminalManager) Resize(id string, cols, rows int) error {
	t := m.get(id)
	if t == nil || cols <= 0 || rows <= 0 {
		return nil
	}
	return t.sess.WindowChange(rows, cols)
}

func (m *TerminalManager) finish(id, reason string) {
	m.mu.Lock()
	t := m.terms[id]
	delete(m.terms, id)
	m.mu.Unlock()
	if t == nil {
		return
	}
	t.once.Do(func() {
		t.sess.Close()
		t.client.Close()
		t.out.waitReady()
		close(t.out.done)
		t.out.flush()
		m.emit("term:exit:"+id, reason)
		if m.OnClose != nil {
			m.OnClose(t.serverID)
		}
	})
}

// Ready 表示前端已订阅该终端的输出事件。
func (m *TerminalManager) Ready(id string) {
	if t := m.get(id); t != nil {
		t.out.markReady()
	}
}

func (m *TerminalManager) Close(id string) {
	m.finish(id, "已关闭")
}

func (m *TerminalManager) CloseAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.terms))
	for id := range m.terms {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Close(id)
	}
}

// Recent 返回某台服务器的终端里最近的输出（多个终端时取最近有输出的那个），已整理成纯文本。
// ok 为 false 表示这台服务器当前没有打开的终端。
func (m *TerminalManager) Recent(serverID string, maxLines int) (text string, ok bool) {
	m.mu.Lock()
	var pick *terminal
	var at time.Time
	for _, t := range m.terms {
		if t.serverID != serverID {
			continue
		}
		t.out.mu.Lock()
		last := t.out.lastWrite
		t.out.mu.Unlock()
		if pick == nil || last.After(at) {
			pick, at = t, last
		}
	}
	m.mu.Unlock()
	if pick == nil {
		return "", false
	}
	pick.out.mu.Lock()
	raw := pick.out.recent
	if len(raw) > recentLimit {
		raw = raw[len(raw)-recentLimit:]
	}
	raw = append([]byte(nil), raw...)
	pick.out.mu.Unlock()
	return TailLines(CleanTerminalText(string(raw)), maxLines), true
}

// OpenServers 返回当前打开了终端的服务器 ID（去重）。
func (m *TerminalManager) OpenServers() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	seen := map[string]bool{}
	for _, t := range m.terms {
		if !seen[t.serverID] {
			seen[t.serverID] = true
			out = append(out, t.serverID)
		}
	}
	return out
}

func (m *TerminalManager) List() []TerminalInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]TerminalInfo, 0, len(m.terms))
	for _, t := range m.terms {
		out = append(out, TerminalInfo{ID: t.id, ServerID: t.serverID})
	}
	return out
}
