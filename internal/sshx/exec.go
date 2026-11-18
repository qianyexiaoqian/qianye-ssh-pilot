package sshx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

type ExecOptions struct {
	Stdin      io.Reader
	Privileged bool // 非 root 用户且启用 sudo 时自动通过 sudo 执行
	Timeout    time.Duration
	OnOutput   func(stream string, data []byte)
	MaxCapture int
}

type ExecResult struct {
	Stdout   string        `json:"stdout"`
	Stderr   string        `json:"stderr"`
	ExitCode int           `json:"exitCode"`
	Duration time.Duration `json:"duration"`
}

// Combined 返回 stdout 与 stderr 合并后的输出。
func (r *ExecResult) Combined() string {
	switch {
	case r.Stderr == "":
		return r.Stdout
	case r.Stdout == "":
		return r.Stderr
	default:
		return strings.TrimRight(r.Stdout, "\n") + "\n" + r.Stderr
	}
}

type captureWriter struct {
	mu     *sync.Mutex
	buf    bytes.Buffer
	max    int
	over   bool
	stream string
	cb     func(string, []byte)
}

func (w *captureWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf.Len() < w.max {
		n := min(len(p), w.max-w.buf.Len())
		w.buf.Write(p[:n])
		if n < len(p) {
			w.over = true
		}
	} else {
		w.over = true
	}
	if w.cb != nil {
		w.cb(w.stream, p)
	}
	return len(p), nil
}

func (w *captureWriter) String() string {
	// 命令被取消、连接又没有回应时，读取输出的协程可能还在往里写
	w.mu.Lock()
	defer w.mu.Unlock()
	s := strings.ToValidUTF8(w.buf.String(), "")
	if w.over {
		s += "\n…（输出过长，已截断）"
	}
	return s
}

// ShellQuote 返回 POSIX shell 单引号转义后的字符串。
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func needsSudo(ep *Endpoint, privileged bool) bool {
	return privileged && ep.UseSudo && !ep.IsRoot()
}

func sudoPassword(ep *Endpoint) string {
	if ep.SudoPassword != "" {
		return ep.SudoPassword
	}
	return ep.Password
}

// sudoPrefix 返回 sudo 前缀以及需要写入 stdin 的口令。
func sudoPrefix(ep *Endpoint) (string, io.Reader) {
	if pw := sudoPassword(ep); pw != "" {
		return "sudo -S -p '' ", strings.NewReader(pw + "\n")
	}
	return "sudo -n ", nil
}

// Exec 在服务器上执行一条命令。
func (p *Pool) Exec(ctx context.Context, serverID, command string, opt ExecOptions) (*ExecResult, error) {
	sess, c, err := p.session(ctx, serverID)
	if err != nil {
		return nil, err
	}
	cmd, stdin := command, opt.Stdin
	if needsSudo(c.Endpoint(), opt.Privileged) {
		prefix, pw := sudoPrefix(c.Endpoint())
		cmd = prefix + "sh -c " + ShellQuote(command)
		if pw != nil {
			if stdin != nil {
				stdin = io.MultiReader(pw, stdin)
			} else {
				stdin = pw
			}
		}
	}
	return run(ctx, c, sess, cmd, stdin, opt)
}

func run(ctx context.Context, c *Client, sess *ssh.Session, cmd string, stdin io.Reader, opt ExecOptions) (*ExecResult, error) {
	// 关闭会话要给服务器发消息：连接没有回应时不让它拖住调用方
	defer func() { go sess.Close() }()
	maxCap := opt.MaxCapture
	if maxCap <= 0 {
		maxCap = 2 << 20
	}
	var mu sync.Mutex
	stdout := &captureWriter{mu: &mu, max: maxCap, stream: "stdout", cb: opt.OnOutput}
	stderr := &captureWriter{mu: &mu, max: maxCap, stream: "stderr", cb: opt.OnOutput}
	sess.Stdout = stdout
	sess.Stderr = stderr
	if stdin != nil {
		sess.Stdin = stdin
	}
	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	start := time.Now()
	if _, err := await(ctx, c, func() (struct{}, error) { return struct{}{}, sess.Start(cmd) }, nil); err != nil {
		if errors.Is(err, ErrNoReply) || ctx.Err() != nil {
			return nil, err
		}
		return nil, fmt.Errorf("启动命令失败: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	// stop 结束还在运行的命令：让服务器杀掉它并关闭会话，等它收尾。等不到（连接已经没有回应）就把连接关掉，
	// 不再等下去——否则“超时”和“停止”在一条断掉的连接上都不起作用。
	stop := func() {
		go func() {
			_ = sess.Signal(ssh.SIGKILL)
			sess.Close()
		}()
		grace := time.NewTimer(StopGrace)
		defer grace.Stop()
		select {
		case <-done:
		case <-grace.C:
			// 等不到会话收尾。连接上已经什么数据都收不到了：它多半已经失效，关掉；
			// 还在收到数据说明服务器只是慢，连接留着，只是不再等这一个会话
			if c.quietFor() >= StopGrace {
				c.Close()
				select {
				case <-done:
				case <-time.After(StopGrace):
				}
			}
		}
	}
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		stop()
		err = errors.New("已取消")
	case <-timer.C:
		stop()
		err = fmt.Errorf("执行超时（%s）", timeout)
	case <-c.done:
		// 连接断了（保活没有回应，或者被别处关闭）
		select {
		case err = <-done:
		case <-time.After(StopGrace):
			err = ErrNoReply
		}
	}
	res := &ExecResult{Stdout: stdout.String(), Stderr: stderr.String(), Duration: time.Since(start)}
	var exitErr *ssh.ExitError
	var missing *ssh.ExitMissingError
	// 没等到命令的退出状态、连接又已经断开：命令多半没有执行完，不能当作“执行了，只是没有退出码”
	if errors.As(err, &missing) && c.Closed() {
		err = errors.New("连接中断，命令可能没有执行完")
	}
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		res.ExitCode = exitErr.ExitStatus()
		err = nil
	case errors.As(err, &missing):
		res.ExitCode = -1
		err = nil
	default:
		res.ExitCode = -1
	}
	return res, err
}

var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Script 描述一次脚本执行。
type Script struct {
	Content     string
	Interpreter string // bash | sh | python3
	Env         map[string]string
}

func interpreterCmd(name string) (bin, stdinFlag string) {
	switch name {
	case "sh":
		return "sh", "-s"
	case "python3", "python":
		return "python3", "-"
	default:
		return "bash", "-s"
	}
}

func envPrefix(env map[string]string) (string, error) {
	keys := make([]string, 0, len(env))
	for k := range env {
		if !envNameRe.MatchString(k) {
			return "", fmt.Errorf("非法的参数名: %s", k)
		}
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "=" + ShellQuote(env[k]) + " ")
	}
	return b.String(), nil
}

// RunScript 执行脚本：普通情况下通过 stdin 传入；需要 sudo 时以 base64 参数传入，
// 避免 sudo 口令与脚本内容在 stdin 中混在一起。
func (p *Pool) RunScript(ctx context.Context, serverID string, sc Script, opt ExecOptions) (*ExecResult, error) {
	envs, err := envPrefix(sc.Env)
	if err != nil {
		return nil, err
	}
	interp := sc.Interpreter
	res, err := p.runScriptOnce(ctx, serverID, sc.Content, interp, envs, opt)
	// 极简系统（如 Alpine）没有 bash 时退回 sh
	if err == nil && interp != "sh" && interp != "python3" && res.ExitCode == 127 &&
		strings.Contains(res.Stderr, "bash") && strings.Contains(res.Stderr, "not found") && res.Stdout == "" {
		return p.runScriptOnce(ctx, serverID, sc.Content, "sh", envs, opt)
	}
	return res, err
}

func (p *Pool) runScriptOnce(ctx context.Context, serverID, content, interp, envs string, opt ExecOptions) (*ExecResult, error) {
	sess, c, err := p.session(ctx, serverID)
	if err != nil {
		return nil, err
	}
	bin, flag := interpreterCmd(interp)
	env := ""
	if envs != "" {
		env = "env " + envs
	}
	if needsSudo(c.Endpoint(), opt.Privileged) {
		prefix, pw := sudoPrefix(c.Endpoint())
		b64 := base64.StdEncoding.EncodeToString([]byte(content))
		cmd := fmt.Sprintf(`%s%s%s -c "$(echo '%s' | base64 -d)"`, prefix, env, bin, b64)
		return run(ctx, c, sess, cmd, pw, opt)
	}
	return run(ctx, c, sess, env+bin+" "+flag, strings.NewReader(content), opt)
}

// ReadFile 读取远程文件（最多 maxBytes 字节）。
func (p *Pool) ReadFile(ctx context.Context, serverID, path string, maxBytes int) (string, error) {
	if maxBytes <= 0 {
		maxBytes = 256 << 10
	}
	cmd := fmt.Sprintf("if [ -f %[1]s ]; then head -c %[2]d %[1]s; else echo 'SSHPILOT_NOFILE' >&2; exit 2; fi", ShellQuote(path), maxBytes)
	res, err := p.Exec(ctx, serverID, cmd, ExecOptions{Privileged: true, Timeout: 30 * time.Second})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		if strings.Contains(res.Stderr, "SSHPILOT_NOFILE") {
			return "", fmt.Errorf("文件不存在: %s", path)
		}
		return "", fmt.Errorf("读取 %s 失败: %s", path, strings.TrimSpace(res.Stderr))
	}
	return res.Stdout, nil
}

// WriteFile 通过 base64 写入远程文件（先写临时文件再原子替换）。
func (p *Pool) WriteFile(ctx context.Context, serverID, path string, data []byte, mode string, backupSuffix string) error {
	if mode == "" {
		mode = "644"
	}
	var b strings.Builder
	q := ShellQuote(path)
	fmt.Fprintf(&b, "set -e\nmkdir -p \"$(dirname %s)\"\n", q)
	if backupSuffix != "" {
		fmt.Fprintf(&b, "[ -f %[1]s ] && cp -p %[1]s %[1]s%[2]s || true\n", q, ShellQuote(backupSuffix))
	}
	fmt.Fprintf(&b, "tmp=%s.sshpilot-tmp\n", q)
	fmt.Fprintf(&b, "echo '%s' | base64 -d > \"$tmp\"\n", base64.StdEncoding.EncodeToString(data))
	fmt.Fprintf(&b, "chmod %s \"$tmp\"\nmv -f \"$tmp\" %s\n", ShellQuote(mode), q)
	res, err := p.RunScript(ctx, serverID, Script{Content: b.String(), Interpreter: "sh"}, ExecOptions{Privileged: true, Timeout: time.Minute})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("写入 %s 失败: %s", path, strings.TrimSpace(res.Combined()))
	}
	return nil
}

// tailBuffer 只保留最近写入的一小段内容（用于在出错时附带 stderr）。
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(strings.ToValidUTF8(string(t.buf), ""))
}

// Stream 执行一条长时间运行的命令（不设超时），把标准输出逐行交给 onLine。
// 命令自行结束且退出码为 0 时返回 nil；ctx 取消时结束远端命令并返回 ctx.Err()。
func (p *Pool) Stream(ctx context.Context, serverID, command string, onLine func(line string)) error {
	sess, _, err := p.session(ctx, serverID)
	if err != nil {
		return err
	}
	defer sess.Close()
	stdout, err := sess.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := &tailBuffer{max: 1024}
	sess.Stderr = stderr
	if err := sess.Start(command); err != nil {
		return fmt.Errorf("启动命令失败: %w", err)
	}
	done := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			onLine(strings.TrimRight(sc.Text(), "\r"))
		}
		done <- sess.Wait()
	}()
	select {
	case err := <-done:
		if err == nil {
			return nil
		}
		var exitErr *ssh.ExitError
		if errors.As(err, &exitErr) {
			if msg := stderr.String(); msg != "" {
				return fmt.Errorf("命令退出（退出码 %d）: %s", exitErr.ExitStatus(), msg)
			}
			return fmt.Errorf("命令退出（退出码 %d）", exitErr.ExitStatus())
		}
		return fmt.Errorf("连接中断: %w", err)
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		sess.Close()
		<-done
		return ctx.Err()
	}
}
