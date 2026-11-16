package sshx

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

type AuthorizedKey struct {
	Line        string `json:"line"`
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
	Comment     string `json:"comment"`
	Options     string `json:"options"`
	Blob        string `json:"blob"`
}

// 这些操作针对登录用户自己的 ~/.ssh，因此不使用 sudo。
func (p *Pool) runUserScript(ctx context.Context, serverID, script string) (*ExecResult, error) {
	return p.RunScript(ctx, serverID, Script{Content: script, Interpreter: "sh"}, ExecOptions{Timeout: 30 * time.Second})
}

func (p *Pool) ListAuthorizedKeys(ctx context.Context, serverID string) ([]AuthorizedKey, error) {
	res, err := p.runUserScript(ctx, serverID, `cat ~/.ssh/authorized_keys 2>/dev/null; exit 0`)
	if err != nil {
		return nil, err
	}
	var out []AuthorizedKey
	sc := bufio.NewScanner(strings.NewReader(res.Stdout))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pk, comment, options, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			continue
		}
		parts := strings.Fields(string(ssh.MarshalAuthorizedKey(pk)))
		out = append(out, AuthorizedKey{
			Line:        line,
			Type:        describeKey(pk),
			Fingerprint: ssh.FingerprintSHA256(pk),
			Comment:     comment,
			Options:     strings.Join(options, ","),
			Blob:        parts[1],
		})
	}
	return out, nil
}

// AddAuthorizedKey 把公钥追加到 authorized_keys（已存在则跳过）。返回是否新增。
func (p *Pool) AddAuthorizedKey(ctx context.Context, serverID, line string) (bool, error) {
	blob := KeyBlob(line)
	if blob == "" {
		return false, errors.New("公钥格式无效")
	}
	script := fmt.Sprintf(`umask 077
mkdir -p ~/.ssh && chmod 700 ~/.ssh || exit 1
f=~/.ssh/authorized_keys
touch "$f" && chmod 600 "$f" || exit 1
if grep -qF %[1]s "$f"; then echo SSHPILOT_EXISTS; exit 0; fi
if [ -s "$f" ] && [ -n "$(tail -c 1 "$f")" ]; then echo >> "$f"; fi
printf '%%s\n' %[2]s >> "$f" || exit 1
command -v restorecon >/dev/null 2>&1 && restorecon -R ~/.ssh >/dev/null 2>&1
echo SSHPILOT_ADDED`, ShellQuote(blob), ShellQuote(strings.TrimSpace(line)))
	res, err := p.runUserScript(ctx, serverID, script)
	if err != nil {
		return false, err
	}
	switch {
	case strings.Contains(res.Stdout, "SSHPILOT_ADDED"):
		return true, nil
	case strings.Contains(res.Stdout, "SSHPILOT_EXISTS"):
		return false, nil
	default:
		return false, fmt.Errorf("写入 authorized_keys 失败: %s", strings.TrimSpace(res.Combined()))
	}
}

// RemoveAuthorizedKey 从 authorized_keys 中删除包含该公钥主体的行。返回是否删除。
func (p *Pool) RemoveAuthorizedKey(ctx context.Context, serverID, blob string) (bool, error) {
	if blob == "" {
		return false, errors.New("公钥为空")
	}
	script := fmt.Sprintf(`f=~/.ssh/authorized_keys
[ -f "$f" ] || { echo SSHPILOT_NOTFOUND; exit 0; }
grep -qF %[1]s "$f" || { echo SSHPILOT_NOTFOUND; exit 0; }
cp -p "$f" "$f.sshpilot.bak" || exit 1
grep -vF %[1]s "$f" > "$f.sshpilot.tmp"
cat "$f.sshpilot.tmp" > "$f" && rm -f "$f.sshpilot.tmp" || exit 1
echo SSHPILOT_REMOVED`, ShellQuote(blob))
	res, err := p.runUserScript(ctx, serverID, script)
	if err != nil {
		return false, err
	}
	switch {
	case strings.Contains(res.Stdout, "SSHPILOT_REMOVED"):
		return true, nil
	case strings.Contains(res.Stdout, "SSHPILOT_NOTFOUND"):
		return false, nil
	default:
		return false, fmt.Errorf("修改 authorized_keys 失败: %s", strings.TrimSpace(res.Combined()))
	}
}
