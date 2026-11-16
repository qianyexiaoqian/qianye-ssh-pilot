package ops

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"sshpilot/internal/store"
)

type Action struct {
	Type     string            `json:"type"` // script | command | deploy | service | ai
	Label    string            `json:"label"`
	ScriptID string            `json:"scriptId,omitempty"`
	Params   map[string]string `json:"params,omitempty"`
	Command  string            `json:"command,omitempty"`
	CertID   string            `json:"certId,omitempty"`
	Service  string            `json:"service,omitempty"`
	Prompt   string            `json:"prompt,omitempty"`
}

type Finding struct {
	ID         string  `json:"id"`
	Severity   string  `json:"severity"` // critical | warning | info
	Category   string  `json:"category"`
	ServerID   string  `json:"serverId"`
	ServerName string  `json:"serverName"`
	Title      string  `json:"title"`
	Detail     string  `json:"detail"`
	Suggestion string  `json:"suggestion"`
	Action     *Action `json:"action,omitempty"`
}

type ServerReport struct {
	ServerID   string      `json:"serverId"`
	ServerName string      `json:"serverName"`
	Online     bool        `json:"online"`
	Error      string      `json:"error"`
	Info       *ServerInfo `json:"info,omitempty"`
}

type Report struct {
	ID         string         `json:"id"`
	StartedAt  int64          `json:"startedAt"`
	FinishedAt int64          `json:"finishedAt"`
	Score      int            `json:"score"`
	Servers    []ServerReport `json:"servers"`
	Findings   []Finding      `json:"findings"`
	Critical   int            `json:"critical"`
	Warning    int            `json:"warning"`
	Info       int            `json:"info"`
}

var severityRank = map[string]int{"critical": 0, "warning": 1, "info": 2}

func humanDuration(sec int64) string {
	d := sec / 86400
	h := sec % 86400 / 3600
	if d > 0 {
		return fmt.Sprintf("%d 天 %d 小时", d, h)
	}
	return fmt.Sprintf("%d 小时 %d 分", h, sec%3600/60)
}

// Analyze 根据服务器指标、证书与部署状态生成巡检结论。
func Analyze(r *Report, certs []store.Certificate, targets []store.DeployTarget, servers []store.Server, warnDays int) {
	var fs []Finding
	add := func(f Finding) {
		f.ID = fmt.Sprintf("f%d", len(fs)+1)
		fs = append(fs, f)
	}
	for _, sr := range r.Servers {
		if !sr.Online {
			add(Finding{Severity: "critical", Category: "连接", ServerID: sr.ServerID, ServerName: sr.ServerName,
				Title: "无法连接", Detail: sr.Error, Suggestion: "检查服务器是否在线、SSH 端口与防火墙、登录凭据是否有效。"})
			continue
		}
		in := sr.Info
		if in == nil {
			continue
		}
		for _, d := range in.Disks {
			sev := ""
			switch {
			case d.UsePct >= 95:
				sev = "critical"
			case d.UsePct >= 85:
				sev = "warning"
			}
			if sev == "" {
				continue
			}
			add(Finding{Severity: sev, Category: "磁盘", ServerID: sr.ServerID, ServerName: sr.ServerName,
				Title:      fmt.Sprintf("磁盘 %s 使用率 %.0f%%", d.Mount, d.UsePct),
				Detail:     fmt.Sprintf("%s 已用 %s / %s", d.Device, humanBytes(d.Used), humanBytes(d.Size)),
				Suggestion: "分析占用最大的目录，清理日志、缓存或旧备份；必要时扩容。",
				Action:     &Action{Type: "script", Label: "分析占用", ScriptID: "builtin-disk-usage", Params: map[string]string{"DIR": d.Mount, "DEPTH": "2"}}})
		}
		if mp := in.MemUsedPct(); mp >= 92 {
			add(Finding{Severity: "critical", Category: "内存", ServerID: sr.ServerID, ServerName: sr.ServerName,
				Title: fmt.Sprintf("内存使用率 %.0f%%", mp), Detail: fmt.Sprintf("可用 %s / %s", humanBytes(in.MemAvailable), humanBytes(in.MemTotal)),
				Suggestion: "查看内存占用最高的进程，确认是否存在泄漏；考虑增加 swap 或内存。",
				Action:     &Action{Type: "script", Label: "查看进程", ScriptID: "builtin-top"}})
		} else if mp >= 85 {
			add(Finding{Severity: "warning", Category: "内存", ServerID: sr.ServerID, ServerName: sr.ServerName,
				Title: fmt.Sprintf("内存使用率 %.0f%%", mp), Detail: fmt.Sprintf("可用 %s / %s", humanBytes(in.MemAvailable), humanBytes(in.MemTotal)),
				Suggestion: "关注内存增长趋势，排查占用最高的进程。",
				Action:     &Action{Type: "script", Label: "查看进程", ScriptID: "builtin-top"}})
		}
		if in.SwapTotal == 0 && in.MemTotal > 0 && in.MemTotal <= 4<<30 {
			add(Finding{Severity: "info", Category: "内存", ServerID: sr.ServerID, ServerName: sr.ServerName,
				Title: "未配置 Swap", Detail: fmt.Sprintf("内存 %s，没有 swap 时内存耗尽会直接触发 OOM", humanBytes(in.MemTotal)),
				Suggestion: "小内存服务器建议配置 1~2G swap 作为缓冲。",
				Action:     &Action{Type: "script", Label: "创建 Swap", ScriptID: "builtin-swap", Params: map[string]string{"SIZE": "2G"}}})
		} else if sp := in.SwapUsedPct(); sp >= 60 {
			add(Finding{Severity: "warning", Category: "内存", ServerID: sr.ServerID, ServerName: sr.ServerName,
				Title: fmt.Sprintf("Swap 使用率 %.0f%%", sp), Suggestion: "Swap 使用偏高说明物理内存长期不足，可能导致性能下降。",
				Action: &Action{Type: "script", Label: "查看进程", ScriptID: "builtin-top"}})
		}
		if in.CPUs > 0 && len(in.Load) == 3 {
			per := in.Load[1] / float64(in.CPUs)
			if per >= 1.5 {
				sev := "warning"
				if per >= 3 {
					sev = "critical"
				}
				add(Finding{Severity: sev, Category: "负载", ServerID: sr.ServerID, ServerName: sr.ServerName,
					Title:      fmt.Sprintf("系统负载偏高（5 分钟 %.2f / %d 核）", in.Load[1], in.CPUs),
					Detail:     fmt.Sprintf("CPU 使用率 %.0f%%", in.CPUUsage),
					Suggestion: "排查占用 CPU 或 IO 的进程，检查是否有异常任务或流量突增。",
					Action:     &Action{Type: "script", Label: "查看进程", ScriptID: "builtin-top"}})
			}
		}
		if len(in.FailedUnits) > 0 {
			svc := strings.TrimSuffix(in.FailedUnits[0], ".service")
			add(Finding{Severity: "warning", Category: "服务", ServerID: sr.ServerID, ServerName: sr.ServerName,
				Title:      fmt.Sprintf("%d 个服务处于失败状态", len(in.FailedUnits)),
				Detail:     strings.Join(in.FailedUnits, "、"),
				Suggestion: "查看失败服务的日志定位原因，修复后重启。",
				Action:     &Action{Type: "script", Label: "查看日志", ScriptID: "builtin-failed-services", Service: svc}})
		}
		if in.Uptime > 365*86400 {
			add(Finding{Severity: "info", Category: "系统", ServerID: sr.ServerID, ServerName: sr.ServerName,
				Title: "已连续运行 " + humanDuration(in.Uptime), Detail: "内核 " + in.Kernel,
				Suggestion: "长时间未重启意味着内核安全补丁可能未生效，建议择机更新并重启。"})
		}
	}

	serverName := map[string]string{}
	for _, s := range servers {
		serverName[s.ID] = s.Name
		if s.AuthType == "password" {
			add(Finding{Severity: "info", Category: "安全", ServerID: s.ID, ServerName: s.Name,
				Title: "使用密码登录", Suggestion: "建议生成密钥并绑定到服务器后改为密钥登录，再关闭 sshd 的密码认证。"})
		}
	}

	now := time.Now()
	for _, c := range certs {
		days := int(time.UnixMilli(c.NotAfter).Sub(now).Hours() / 24)
		name := c.Name
		if name == "" && len(c.Domains) > 0 {
			name = c.Domains[0]
		}
		switch {
		case days < 0:
			add(Finding{Severity: "critical", Category: "证书", Title: fmt.Sprintf("证书 %s 已过期 %d 天", name, -days),
				Detail: strings.Join(c.Domains, ", "), Suggestion: "立即更新证书并同步到所有部署目标。", Action: &Action{Type: "deploy", Label: "去更新", CertID: c.ID}})
		case days <= 7:
			add(Finding{Severity: "critical", Category: "证书", Title: fmt.Sprintf("证书 %s 将在 %d 天后过期", name, days),
				Detail: strings.Join(c.Domains, ", "), Suggestion: "尽快续期并同步。", Action: &Action{Type: "deploy", Label: "去更新", CertID: c.ID}})
		case days <= warnDays:
			add(Finding{Severity: "warning", Category: "证书", Title: fmt.Sprintf("证书 %s 将在 %d 天后过期", name, days),
				Detail: strings.Join(c.Domains, ", "), Suggestion: "安排续期，续期后一键同步到部署目标。", Action: &Action{Type: "deploy", Label: "查看证书", CertID: c.ID}})
		}
		for _, t := range targets {
			if t.CertID != c.ID {
				continue
			}
			switch {
			case t.LastStatus == "failed":
				add(Finding{Severity: "warning", Category: "证书", ServerID: t.ServerID, ServerName: serverName[t.ServerID],
					Title: fmt.Sprintf("证书 %s 上次部署失败", name), Detail: t.LastMessage,
					Suggestion: "检查部署目标配置后重新同步。", Action: &Action{Type: "deploy", Label: "重新同步", CertID: c.ID}})
			case t.LastFingerprint != "" && t.LastFingerprint != c.Fingerprint:
				add(Finding{Severity: "warning", Category: "证书", ServerID: t.ServerID, ServerName: serverName[t.ServerID],
					Title: fmt.Sprintf("证书 %s 有新版本未同步", name), Detail: "服务器上仍是旧版本证书",
					Suggestion: "一键同步到该服务器。", Action: &Action{Type: "deploy", Label: "立即同步", CertID: c.ID}})
			case t.LastFingerprint == "":
				add(Finding{Severity: "info", Category: "证书", ServerID: t.ServerID, ServerName: serverName[t.ServerID],
					Title: fmt.Sprintf("证书 %s 尚未部署到该目标", name), Action: &Action{Type: "deploy", Label: "去部署", CertID: c.ID}})
			}
		}
	}

	slices.SortStableFunc(fs, func(a, b Finding) int { return severityRank[a.Severity] - severityRank[b.Severity] })
	r.Findings = fs
	score := 100
	r.Critical, r.Warning, r.Info = 0, 0, 0
	for _, f := range fs {
		switch f.Severity {
		case "critical":
			r.Critical++
			score -= 15
		case "warning":
			r.Warning++
			score -= 6
		default:
			r.Info++
			score -= 1
		}
	}
	r.Score = max(score, 0)
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// ReportPrompt 把巡检报告整理成给 AI 分析的文本。
func ReportPrompt(r *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "以下是刚完成的服务器巡检结果（%s，健康分 %d）。请结合你的记忆与技能，给出按优先级排序的运维建议：先说明最需要处理的问题和原因，再给出具体可执行的命令或操作；需要更多信息时可以直接用工具诊断（只读命令优先）。\n\n",
		time.UnixMilli(r.FinishedAt).Format("2006-01-02 15:04"), r.Score)
	b.WriteString("## 服务器指标\n")
	for _, s := range r.Servers {
		if !s.Online || s.Info == nil {
			fmt.Fprintf(&b, "- %s：无法连接（%s）\n", s.ServerName, s.Error)
			continue
		}
		in := s.Info
		var disks []string
		for _, d := range in.Disks {
			disks = append(disks, fmt.Sprintf("%s %.0f%%", d.Mount, d.UsePct))
		}
		fmt.Fprintf(&b, "- %s（%s）：CPU %.0f%%，负载 %.2f/%.2f/%.2f（%d 核），内存 %.0f%%，磁盘 %s，运行 %s",
			s.ServerName, in.OS, in.CPUUsage, in.Load[0], in.Load[1], in.Load[2], in.CPUs, in.MemUsedPct(), strings.Join(disks, " / "), humanDuration(in.Uptime))
		if len(in.FailedUnits) > 0 {
			fmt.Fprintf(&b, "，失败服务：%s", strings.Join(in.FailedUnits, ", "))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n## 规则检查发现的问题\n")
	if len(r.Findings) == 0 {
		b.WriteString("（无）\n")
	}
	for _, f := range r.Findings {
		target := ""
		if f.ServerName != "" {
			target = "[" + f.ServerName + "] "
		}
		fmt.Fprintf(&b, "- (%s) %s%s", f.Severity, target, f.Title)
		if f.Detail != "" {
			fmt.Fprintf(&b, "：%s", f.Detail)
		}
		b.WriteString("\n")
	}
	return b.String()
}
