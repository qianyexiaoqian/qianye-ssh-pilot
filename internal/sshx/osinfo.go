package sshx

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
)

// osProbeScript 识别系统名称，结果以 @os= 开头单独占一行（登录时 shell 自己打印的欢迎语不会混进来）。
// 脚本里不能出现单引号：整体放进 sh -c '…' 执行，登录 shell 是 fish、csh 时也能用。
const osProbeScript = `n=; ` +
	`for f in /etc/os-release /usr/lib/os-release; do if [ -z "$n" ] && [ -r "$f" ]; then n=$(. "$f" 2>/dev/null; echo "${PRETTY_NAME:-$NAME}"); fi; done; ` +
	`[ -n "$n" ] || n=$(head -n1 /etc/redhat-release 2>/dev/null); ` +
	`[ -n "$n" ] || n=$(uname -sr 2>/dev/null); ` +
	`echo "@os=$n"`

// output 在连接上执行一条命令并返回标准输出；超时或出错时返回已经拿到的内容。
func (c *Client) output(cmd string, timeout time.Duration) (string, error) {
	s, err := c.OpenSession(context.Background())
	if err != nil {
		return "", err
	}
	defer func() { go s.Close() }()
	type result struct {
		out []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		out, err := s.Output(cmd)
		ch <- result{out, err}
	}()
	select {
	case r := <-ch:
		return string(r.out), r.err
	case <-time.After(timeout):
		return "", errors.New("执行超时")
	}
}

func cleanOSName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if r := []rune(s); len(r) > 80 {
		s = string(r[:80])
	}
	return s
}

// ParseOSProbe 从识别脚本的输出里取出系统名称。
func ParseOSProbe(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "@os="); ok {
			return cleanOSName(v)
		}
	}
	return ""
}

// DetectOS 在已建立的连接上识别系统名称（例如 "Ubuntu 22.04.4 LTS"），识别不出来时返回空字符串。
// 不依赖登录 shell 的类型；Windows 的 OpenSSH 服务没有 sh，改用 ver 命令。
func DetectOS(c *Client, timeout time.Duration) string {
	out, _ := c.output("sh -c "+ShellQuote(osProbeScript), timeout)
	if name := ParseOSProbe(out); name != "" {
		return name
	}
	out, _ = c.output("cmd /c ver", timeout)
	for _, line := range strings.Split(out, "\n") {
		if line = cleanOSName(line); strings.Contains(line, "Windows") {
			return line
		}
	}
	return ""
}
