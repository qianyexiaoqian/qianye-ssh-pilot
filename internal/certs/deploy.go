package certs

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"sshpilot/internal/sshx"
	"sshpilot/internal/store"
)

type Step struct {
	TargetID string `json:"targetId"`
	Step     string `json:"step"`
	Label    string `json:"label"`
	Status   string `json:"status"` // running | ok | failed | skipped | warning
	Message  string `json:"message"`
	Time     int64  `json:"time"`
}

type DeployResult struct {
	TargetID    string `json:"targetId"`
	ServerID    string `json:"serverId"`
	ServerName  string `json:"serverName"`
	Success     bool   `json:"success"`
	Message     string `json:"message"`
	Fingerprint string `json:"fingerprint"`
	RolledBack  bool   `json:"rolledBack"`
	TLSStatus   string `json:"tlsStatus"` // ok | mismatch | unreachable | skipped
	Steps       []Step `json:"steps"`
	DurationMs  int64  `json:"durationMs"`
}

type plannedFile struct {
	path string
	data []byte
	mode string
}

var (
	modeRe    = regexp.MustCompile(`^[0-7]{3,4}$`)
	ownerRe   = regexp.MustCompile(`^[A-Za-z0-9_.-]+(:[A-Za-z0-9_.-]+)?$`)
	serviceRe = regexp.MustCompile(`^[A-Za-z0-9@._:-]+$`)
)

func validPath(p string) error {
	if !strings.HasPrefix(p, "/") {
		return fmt.Errorf("路径必须是绝对路径: %s", p)
	}
	if strings.ContainsAny(p, "\n\r\x00") {
		return fmt.Errorf("路径包含非法字符: %q", p)
	}
	return nil
}

func planFiles(t store.DeployTarget, p *Parsed) ([]plannedFile, error) {
	if p.KeyPEM == "" {
		return nil, errors.New("证书缺少私钥，无法部署")
	}
	certMode, keyMode := t.CertMode, t.KeyMode
	if certMode == "" {
		certMode = "644"
	}
	if keyMode == "" {
		keyMode = "600"
	}
	if !modeRe.MatchString(certMode) || !modeRe.MatchString(keyMode) {
		return nil, errors.New("文件权限格式无效（例如 644 / 600）")
	}
	if t.CertPath == "" {
		return nil, errors.New("未设置证书路径")
	}
	var files []plannedFile
	switch t.CertFormat {
	case "leaf":
		files = append(files, plannedFile{t.CertPath, []byte(p.LeafPEM), certMode})
	case "combined":
		files = append(files, plannedFile{t.CertPath, []byte(p.FullchainPEM + p.KeyPEM), keyMode})
	default:
		files = append(files, plannedFile{t.CertPath, []byte(p.FullchainPEM), certMode})
	}
	if t.ChainPath != "" && p.ChainPEM != "" {
		files = append(files, plannedFile{t.ChainPath, []byte(p.ChainPEM), certMode})
	}
	if t.KeyPath != "" {
		files = append(files, plannedFile{t.KeyPath, []byte(p.KeyPEM), keyMode})
	} else if t.CertFormat != "combined" {
		return nil, errors.New("未设置私钥路径")
	}
	seen := map[string]bool{}
	for _, f := range files {
		if err := validPath(f.path); err != nil {
			return nil, err
		}
		if seen[f.path] {
			return nil, fmt.Errorf("多个文件使用了同一路径: %s", f.path)
		}
		seen[f.path] = true
	}
	return files, nil
}

const b64Func = `b64d() { if command -v base64 >/dev/null 2>&1; then base64 -d; else openssl base64 -d -A; fi; }
`

func writeScript(files []plannedFile, ts string, backup bool, owner string) string {
	var b strings.Builder
	b.WriteString("set -u\n" + b64Func)
	fmt.Fprintf(&b, "TS=%s\n", sshx.ShellQuote(ts))
	b.WriteString(`fail() { echo "SSHPILOT_FAIL $*"; exit 1; }` + "\n")
	for _, f := range files {
		fmt.Fprintf(&b, "f=%s\n", sshx.ShellQuote(f.path))
		if backup {
			b.WriteString(`if [ -f "$f" ]; then
  cp -p "$f" "$f.bak-$TS" || fail "备份 $f 失败"
  echo "SSHPILOT_BACKUP $f"
  ls -1t "$f".bak-* 2>/dev/null | tail -n +6 | while read -r old; do rm -f "$old"; done
else
  echo "SSHPILOT_NEW $f"
fi
`)
		} else {
			b.WriteString(`[ -f "$f" ] || echo "SSHPILOT_NEW $f"` + "\n")
		}
		b.WriteString(`mkdir -p "$(dirname "$f")" || fail "无法创建目录 $(dirname "$f")"` + "\n")
		fmt.Fprintf(&b, "echo '%s' | b64d > \"$f.sshpilot-tmp\" || fail \"写入 $f 失败\"\n", base64.StdEncoding.EncodeToString(f.data))
		fmt.Fprintf(&b, "chmod %s \"$f.sshpilot-tmp\" || fail \"设置权限失败\"\n", f.mode)
		if owner != "" {
			fmt.Fprintf(&b, "chown %s \"$f.sshpilot-tmp\" || fail \"设置属主失败\"\n", owner)
		}
		b.WriteString(`mv -f "$f.sshpilot-tmp" "$f" || fail "替换 $f 失败"` + "\n")
	}
	b.WriteString("echo SSHPILOT_DONE\n")
	return b.String()
}

func rollbackScript(backedUp, created []string, ts string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "TS=%s\n", sshx.ShellQuote(ts))
	for _, p := range backedUp {
		fmt.Fprintf(&b, "f=%s\n[ -f \"$f.bak-$TS\" ] && cp -p \"$f.bak-$TS\" \"$f\"\n", sshx.ShellQuote(p))
	}
	for _, p := range created {
		fmt.Fprintf(&b, "rm -f %s\n", sshx.ShellQuote(p))
	}
	b.WriteString("true\n")
	return b.String()
}

func serviceCommand(svc, action string) string {
	return fmt.Sprintf("if command -v systemctl >/dev/null 2>&1; then systemctl %[2]s %[1]s; else service %[1]s %[2]s; fi", svc, action)
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// Deploy 把证书部署到一个目标：备份 → 上传 → 校验 → 配置检测 → 重载服务 → 线上验证，失败时自动回滚。
func Deploy(ctx context.Context, pool *sshx.Pool, t store.DeployTarget, srv store.Server, cert store.Certificate, report func(Step)) DeployResult {
	start := time.Now()
	res := DeployResult{TargetID: t.ID, ServerID: srv.ID, ServerName: srv.Name, TLSStatus: "skipped"}
	step := func(name, label, status, msg string) {
		s := Step{TargetID: t.ID, Step: name, Label: label, Status: status, Message: msg, Time: time.Now().UnixMilli()}
		res.Steps = append(res.Steps, s)
		if report != nil {
			report(s)
		}
	}
	finish := func(ok bool, msg string) DeployResult {
		res.Success = ok
		res.Message = msg
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	run := func(script string, timeout time.Duration) (*sshx.ExecResult, error) {
		return pool.RunScript(ctx, srv.ID, sshx.Script{Content: script, Interpreter: "sh"}, sshx.ExecOptions{Privileged: true, Timeout: timeout})
	}

	p, err := Parse(cert.CertPEM, cert.KeyPEM)
	if err == nil && p.HasKey && !p.KeyMatches {
		err = errors.New("私钥与证书不匹配，已阻止部署")
	}
	if err != nil {
		step("prepare", "准备文件", "failed", err.Error())
		return finish(false, err.Error())
	}
	files, err := planFiles(t, p)
	if err == nil && t.Owner != "" && !ownerRe.MatchString(t.Owner) {
		err = errors.New("属主格式无效（例如 root:root）")
	}
	for _, s := range t.Services {
		if err == nil && !serviceRe.MatchString(s) {
			err = fmt.Errorf("服务名无效: %s", s)
		}
	}
	if err != nil {
		step("prepare", "准备文件", "failed", err.Error())
		return finish(false, err.Error())
	}
	res.Fingerprint = p.Fingerprint
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.path
	}
	step("prepare", "准备文件", "ok", strings.Join(paths, "\n"))

	ts := time.Now().Format("20060102-150405")
	label := "上传证书"
	if t.Backup {
		label = "备份并上传证书"
	}
	step("upload", label, "running", "")
	out, err := run(writeScript(files, ts, t.Backup, t.Owner), time.Minute)
	var backedUp, created []string
	if out != nil {
		for _, line := range strings.Split(out.Stdout, "\n") {
			if v, ok := strings.CutPrefix(line, "SSHPILOT_BACKUP "); ok {
				backedUp = append(backedUp, v)
			}
			if v, ok := strings.CutPrefix(line, "SSHPILOT_NEW "); ok {
				created = append(created, v)
			}
		}
	}
	rollback := func(reason string) DeployResult {
		if len(backedUp)+len(created) == 0 {
			step("rollback", "回滚", "skipped", "没有需要回滚的文件")
			return finish(false, reason)
		}
		rb, rerr := run(rollbackScript(backedUp, created, ts), time.Minute)
		if rerr != nil || rb.ExitCode != 0 {
			step("rollback", "回滚", "failed", "回滚失败，请手动检查服务器")
			return finish(false, reason+"；回滚失败")
		}
		res.RolledBack = true
		step("rollback", "回滚", "ok", "已恢复为部署前的文件")
		return finish(false, reason+"；已自动回滚")
	}
	if err != nil {
		step("upload", label, "failed", err.Error())
		return rollback(err.Error())
	}
	if out.ExitCode != 0 || !strings.Contains(out.Stdout, "SSHPILOT_DONE") {
		msg := strings.TrimSpace(out.Combined())
		if i := strings.Index(msg, "SSHPILOT_FAIL "); i >= 0 {
			msg = strings.TrimSpace(strings.SplitN(msg[i+len("SSHPILOT_FAIL "):], "\n", 2)[0])
		}
		if strings.Contains(msg, "sudo") || strings.Contains(msg, "Permission denied") {
			msg += "（权限不足：请使用 root 登录或在服务器设置中启用 sudo）"
		}
		step("upload", label, "failed", msg)
		return rollback("上传失败: " + msg)
	}
	msg := fmt.Sprintf("已写入 %d 个文件", len(files))
	if len(backedUp) > 0 {
		msg += fmt.Sprintf("，备份 %d 个旧文件（后缀 .bak-%s）", len(backedUp), ts)
	}
	step("upload", label, "ok", msg)

	// 校验远程文件哈希
	var vs strings.Builder
	for _, f := range files {
		fmt.Fprintf(&vs, "f=%s; (sha256sum \"$f\" 2>/dev/null || openssl dgst -sha256 -r \"$f\") | awk '{print $1}'\n", sshx.ShellQuote(f.path))
	}
	vout, err := run(vs.String(), 30*time.Second)
	if err != nil {
		step("verify", "校验文件", "failed", err.Error())
		return rollback("文件校验失败")
	}
	got := strings.Fields(vout.Stdout)
	for i, f := range files {
		sum := sha256.Sum256(f.data)
		if i >= len(got) || !strings.EqualFold(got[i], hex.EncodeToString(sum[:])) {
			step("verify", "校验文件", "failed", "远程文件内容与本地不一致: "+f.path)
			return rollback("文件校验失败")
		}
	}
	step("verify", "校验文件", "ok", "SHA-256 一致")

	if cmd := strings.TrimSpace(t.TestCommand); cmd != "" {
		tout, err := run(cmd, time.Minute)
		if err != nil || tout.ExitCode != 0 {
			detail := ""
			if tout != nil {
				detail = lastLines(tout.Combined(), 8)
			} else if err != nil {
				detail = err.Error()
			}
			step("test", "配置检测", "failed", detail)
			return rollback("配置检测未通过")
		}
		step("test", "配置检测", "ok", lastLines(tout.Combined(), 3))
	}

	if t.ServiceAction != "none" && len(t.Services) > 0 {
		action := t.ServiceAction
		if action == "" {
			action = "reload"
		}
		for _, svc := range t.Services {
			sout, err := run(serviceCommand(svc, action), 90*time.Second)
			if err != nil || sout.ExitCode != 0 {
				detail := ""
				if sout != nil {
					detail = lastLines(sout.Combined(), 8)
				} else {
					detail = err.Error()
				}
				step("service", fmt.Sprintf("%s %s", action, svc), "failed", detail)
				r := rollback(fmt.Sprintf("服务 %s %s 失败", svc, action))
				_, _ = run(serviceCommand(svc, action), 90*time.Second)
				return r
			}
			step("service", fmt.Sprintf("%s %s", action, svc), "ok", "")
		}
	}

	if cmd := strings.TrimSpace(t.PostCommand); cmd != "" {
		pout, err := run(cmd, 2*time.Minute)
		if err != nil || pout.ExitCode != 0 {
			detail := ""
			if pout != nil {
				detail = lastLines(pout.Combined(), 6)
			} else {
				detail = err.Error()
			}
			step("post", "后置命令", "warning", detail)
		} else {
			step("post", "后置命令", "ok", lastLines(pout.Combined(), 3))
		}
	}

	if t.VerifyPort > 0 {
		sni := t.VerifyDomain
		if sni == "" {
			sni = PrimaryDomain(cert.Domains)
		}
		time.Sleep(1500 * time.Millisecond)
		info, err := CheckTLS(ctx, srv.Host, t.VerifyPort, sni)
		switch {
		case err != nil:
			res.TLSStatus = "unreachable"
			step("tls", "线上验证", "warning", fmt.Sprintf("无法从本机连接 %s:%d 验证（%v）", srv.Host, t.VerifyPort, err))
		case info.Fingerprint == p.Fingerprint:
			res.TLSStatus = "ok"
			step("tls", "线上验证", "ok", fmt.Sprintf("%s:%d 已返回新证书", srv.Host, t.VerifyPort))
		default:
			res.TLSStatus = "mismatch"
			step("tls", "线上验证", "warning", fmt.Sprintf("%s:%d 仍返回其他证书（到期 %s），可能存在 CDN/负载均衡，或服务未重载", srv.Host, t.VerifyPort, time.UnixMilli(info.NotAfter).Format("2006-01-02")))
		}
	}
	return finish(true, "部署成功")
}

// Pull 从服务器读取证书与私钥。
func Pull(ctx context.Context, pool *sshx.Pool, serverID string, src store.CertSource) (*Parsed, error) {
	if err := validPath(src.CertPath); err != nil {
		return nil, err
	}
	certPEM, err := pool.ReadFile(ctx, serverID, src.CertPath, 512<<10)
	if err != nil {
		return nil, err
	}
	texts := []string{certPEM}
	if src.ChainPath != "" {
		chain, err := pool.ReadFile(ctx, serverID, src.ChainPath, 512<<10)
		if err != nil {
			return nil, err
		}
		texts = append(texts, chain)
	}
	if src.KeyPath != "" {
		key, err := pool.ReadFile(ctx, serverID, src.KeyPath, 128<<10)
		if err != nil {
			return nil, err
		}
		texts = append(texts, key)
	}
	return Parse(texts...)
}
