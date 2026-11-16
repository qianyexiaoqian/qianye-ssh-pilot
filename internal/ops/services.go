package ops

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"sshpilot/internal/sshx"
)

type ServiceInfo struct {
	Name        string `json:"name"`
	Load        string `json:"load"`
	Active      string `json:"active"`
	Sub         string `json:"sub"`
	Enabled     string `json:"enabled"`
	Description string `json:"description"`
}

var serviceNameRe = regexp.MustCompile(`^[A-Za-z0-9@._:-]+$`)

var allowedActions = []string{"start", "stop", "restart", "reload", "enable", "disable", "status"}

func ValidateService(name, action string) error {
	if !serviceNameRe.MatchString(name) {
		return fmt.Errorf("服务名无效: %s", name)
	}
	if action != "" && !slices.Contains(allowedActions, action) {
		return fmt.Errorf("不支持的操作: %s", action)
	}
	return nil
}

const listServicesScript = `if command -v systemctl >/dev/null 2>&1; then
  echo "==UNITS"
  systemctl list-units --type=service --all --no-legend --no-pager --plain 2>/dev/null
  echo "==FILES"
  systemctl list-unit-files --type=service --no-legend --no-pager 2>/dev/null
else
  echo "==SYSV"
  service --status-all 2>&1
fi
true`

// ListServices 列出 systemd（或 SysV）服务。
func ListServices(ctx context.Context, pool *sshx.Pool, serverID string) ([]ServiceInfo, error) {
	res, err := pool.RunScript(ctx, serverID, sshx.Script{Content: listServicesScript, Interpreter: "sh"}, sshx.ExecOptions{Timeout: 30 * time.Second})
	if err != nil {
		return nil, err
	}
	var out []ServiceInfo
	idx := map[string]int{}
	section := ""
	for _, line := range strings.Split(res.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "==") {
			section = line
			continue
		}
		f := strings.Fields(line)
		if len(f) > 0 && (f[0] == "●" || f[0] == "*") {
			f = f[1:]
		}
		switch section {
		case "==UNITS":
			if len(f) < 4 || !strings.HasSuffix(f[0], ".service") {
				continue
			}
			name := strings.TrimSuffix(f[0], ".service")
			idx[name] = len(out)
			out = append(out, ServiceInfo{Name: name, Load: f[1], Active: f[2], Sub: f[3], Description: strings.Join(f[4:], " ")})
		case "==FILES":
			if len(f) < 2 || !strings.HasSuffix(f[0], ".service") {
				continue
			}
			name := strings.TrimSuffix(f[0], ".service")
			if strings.HasSuffix(name, "@") {
				continue
			}
			if i, ok := idx[name]; ok {
				out[i].Enabled = f[1]
			} else {
				idx[name] = len(out)
				out = append(out, ServiceInfo{Name: name, Load: "loaded", Active: "inactive", Sub: "dead", Enabled: f[1]})
			}
		case "==SYSV":
			// [ + ]  cron
			if len(f) >= 4 && f[0] == "[" {
				state := "inactive"
				if f[1] == "+" {
					state = "active"
				}
				out = append(out, ServiceInfo{Name: f[3], Active: state, Sub: state})
			}
		}
	}
	slices.SortFunc(out, func(a, b ServiceInfo) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// ServiceCommand 返回执行服务操作的 shell 命令。
func ServiceCommand(name, action string) string {
	if action == "status" {
		return fmt.Sprintf("if command -v systemctl >/dev/null 2>&1; then systemctl status %[1]s --no-pager -l -n 40; else service %[1]s status; fi", name)
	}
	check := ""
	if action == "start" || action == "restart" {
		check = fmt.Sprintf(" && sleep 1 && systemctl is-active %s", name)
	}
	return fmt.Sprintf("if command -v systemctl >/dev/null 2>&1; then systemctl %[2]s %[1]s%[3]s; else service %[1]s %[2]s; fi", name, action, check)
}

func ServiceLogsCommand(name string, lines int) string {
	if lines <= 0 || lines > 2000 {
		lines = 200
	}
	return fmt.Sprintf("journalctl -u %s -n %d --no-pager -o short-iso 2>&1 || tail -n %d /var/log/syslog 2>/dev/null", name, lines, lines)
}
