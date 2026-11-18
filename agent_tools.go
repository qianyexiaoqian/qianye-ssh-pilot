package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"sshpilot/internal/agent"
	"sshpilot/internal/ops"
	"sshpilot/internal/sshx"
	"sshpilot/internal/store"
)

// resolveServerRefs 按 ID、名称或主机地址查找服务器；"all" 表示全部服务器。
func (a *App) resolveServerRefs(refs []string) ([]store.Server, error) {
	var all []store.Server
	a.store.Read(func(d *store.Data) {
		for _, s := range d.Servers {
			all = append(all, *s)
		}
	})
	if len(refs) == 0 {
		return nil, errors.New("请指定服务器")
	}
	var out []store.Server
	var missing []string
	add := func(s store.Server) {
		if !slices.ContainsFunc(out, func(x store.Server) bool { return x.ID == s.ID }) {
			out = append(out, s)
		}
	}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "all" || ref == "*" || ref == "全部" {
			for _, s := range all {
				add(s)
			}
			continue
		}
		i := slices.IndexFunc(all, func(s store.Server) bool {
			return s.ID == ref || strings.EqualFold(s.Name, ref) || s.Host == ref
		})
		if i < 0 {
			missing = append(missing, ref)
			continue
		}
		add(all[i])
	}
	if len(missing) > 0 {
		var names []string
		for _, s := range all {
			names = append(names, s.Name)
		}
		return nil, fmt.Errorf("未找到服务器：%s。可用的服务器：%s", strings.Join(missing, "、"), strings.Join(names, "、"))
	}
	return out, nil
}

// targets 解析工具调用里的服务器参数；没有填写时使用会话绑定的服务器（从终端的「AI 运维」发起的对话）。
func (a *App) targets(tc *agent.ToolContext, refs []string) ([]store.Server, error) {
	refs = slices.DeleteFunc(slices.Clone(refs), func(r string) bool { return strings.TrimSpace(r) == "" })
	if len(refs) == 0 {
		if id := tc.ServerID(); id != "" {
			refs = []string{id}
		}
	}
	return a.resolveServerRefs(refs)
}

// sessionFocus 生成绑定到一台服务器的会话追加在系统提示词后的说明。
func (a *App) sessionFocus(serverID string) (string, error) {
	var s *store.Server
	a.store.Read(func(d *store.Data) {
		if x := d.Server(serverID); x != nil {
			cp := *x
			s = &cp
		}
	})
	if s == nil {
		return "", errors.New("服务器不存在或已删除")
	}
	return fmt.Sprintf(`# 当前终端
这次对话是用户在服务器「%[1]s」（%[2]s@%[3]s:%[4]d）的终端旁边的「AI 运维」面板里发起的，用户正在操作这台服务器。
- 用户没有指明服务器时，所有操作都针对「%[1]s」，工具的服务器参数填 "%[1]s"；只有用户明确提到其他服务器时才操作别的服务器。
- 用户说“这台”“当前服务器”“刚才的报错”“终端里的输出”，指的都是这台服务器和它的终端。需要看用户终端里刚刚执行的命令和输出时，调用 read_terminal。
- 你执行的命令走独立的会话，不会出现在用户的终端里，也不继承用户终端当前所在的目录和环境变量：需要时用绝对路径或在命令里先 cd。
- 面板比较窄：回复先给结论，尽量简短；建议用户自己执行的命令放在代码块里，用户可以一键填入终端。`, s.Name, s.Username, s.Host, s.Port), nil
}

func names(servers []store.Server) []string {
	out := make([]string, len(servers))
	for i, s := range servers {
		out[i] = s.Name
	}
	return out
}

func ids(servers []store.Server) []string {
	out := make([]string, len(servers))
	for i, s := range servers {
		out[i] = s.ID
	}
	return out
}

type cmdResultMeta struct {
	ServerID   string `json:"serverId"`
	ServerName string `json:"serverName"`
	ExitCode   int    `json:"exitCode"`
	Output     string `json:"output"`
	Error      string `json:"error"`
	DurationMs int64  `json:"durationMs"`
}

// formatResults 把多台服务器的执行结果整理成给模型的文本和给界面的结构化数据。
func formatResults(results []store.RunResult) (string, []cmdResultMeta) {
	per := max(2500, 14000/max(len(results), 1))
	var b strings.Builder
	meta := make([]cmdResultMeta, 0, len(results))
	for _, r := range results {
		status := fmt.Sprintf("退出码 %d", r.ExitCode)
		if r.Error != "" {
			status = "失败：" + r.Error
		}
		fmt.Fprintf(&b, "## %s（%s，%.1fs）\n", r.ServerName, status, float64(r.DurationMs)/1000)
		out := strings.TrimRight(r.Output, "\n")
		if out == "" {
			out = "（无输出）"
		}
		b.WriteString("```\n" + store.TruncateMiddle(out, per) + "\n```\n")
		meta = append(meta, cmdResultMeta{r.ServerID, r.ServerName, r.ExitCode, store.TruncateMiddle(r.Output, 20000), r.Error, r.DurationMs})
	}
	return b.String(), meta
}

var sensitiveReadRe = regexp.MustCompile(`(?i)(id_rsa|id_ed25519|id_ecdsa|\.key$|privkey|private|shadow|\.p12$|\.pfx$|credentials|\.env$)`)
var criticalWriteRe = regexp.MustCompile(`(authorized_keys|/etc/ssh/sshd_config|/etc/sudoers|/etc/fstab|/etc/passwd|/etc/shadow|/etc/group|/boot/)`)

func denied() *agent.ToolResult {
	return &agent.ToolResult{Content: "用户拒绝了该操作，没有执行。请询问用户原因或换一种方式。", IsError: true, Meta: map[string]any{"kind": "denied"}}
}

func parseArgs(raw json.RawMessage, v any) error {
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("参数格式错误: %w", err)
	}
	return nil
}

func (a *App) runForAgent(ctx context.Context, servers []store.Server, p *runPlan, timeout time.Duration) []store.RunResult {
	rec := &store.RunRecord{ID: store.NewID(), Kind: "agent", Title: "AI · " + p.title, ScriptID: p.scriptID,
		Command: clipText(p.content, 4000), Status: "running", StartedAt: store.Now()}
	a.store.AddRun(rec)
	results := a.executeRun(ctx, rec, ids(servers), p, timeout, false)
	a.store.FinishRun(rec.ID, results, ctx.Err() != nil)
	return results
}

func (a *App) opsTools() []*agent.Tool {
	return slices.Concat(a.serverTools(), a.targetTools(), a.certTools(), a.attachTools(), a.manageTools())
}

func (a *App) serverTools() []*agent.Tool {
	return []*agent.Tool{
		{
			Name:        "list_servers",
			Description: "列出受管服务器（名称、ID、地址、分组、标签、系统、在线状态）。需要确认服务器名称或筛选服务器时调用。",
			Schema:      agent.Obj(map[string]any{"keyword": agent.Str("按名称、地址、分组或标签筛选（可选）")}),
			Handler: func(_ context.Context, _ *agent.ToolContext, raw json.RawMessage) (*agent.ToolResult, error) {
				var in struct {
					Keyword string `json:"keyword"`
				}
				_ = json.Unmarshal(raw, &in)
				kw := strings.ToLower(strings.TrimSpace(in.Keyword))
				var b strings.Builder
				n := 0
				for _, s := range a.ListServers() {
					text := strings.ToLower(strings.Join(append([]string{s.Name, s.Host, s.Group, s.OS, s.Notes}, s.Tags...), " "))
					if kw != "" && !strings.Contains(text, kw) {
						continue
					}
					n++
					state := map[string]string{"online": "在线", "offline": "离线"}[s.LastStatus]
					if state == "" {
						state = "未知"
					}
					fmt.Fprintf(&b, "- %s｜ID %s｜%s@%s:%d｜分组 %s｜标签 %s｜%s｜%s", s.Name, s.ID, s.Username, s.Host, s.Port,
						orDash(s.Group), orDash(strings.Join(s.Tags, ",")), orDash(s.OS), state)
					if s.UseSudo {
						b.WriteString("｜sudo")
					}
					b.WriteString("\n")
				}
				if n == 0 {
					return &agent.ToolResult{Content: "没有匹配的服务器。"}, nil
				}
				return &agent.ToolResult{Content: fmt.Sprintf("共 %d 台：\n%s", n, b.String())}, nil
			},
		},
		{
			Name: "run_command",
			Description: "在一台或多台服务器上执行 shell 命令并返回输出（bash，非交互）。用于诊断与运维操作。" +
				"只读命令会直接执行；有修改的命令会请求用户批准。servers 可以填服务器名称、ID 或地址，[\"all\"] 表示全部。",
			Schema: agent.Obj(map[string]any{
				"servers":     agent.StrArray("目标服务器名称或 ID 列表"),
				"command":     agent.Str("要执行的命令，可以是多行脚本"),
				"reason":      agent.Str("一句话说明执行目的（展示给用户）"),
				"timeout_sec": agent.Int("超时秒数，默认 120，最大 600"),
			}, "servers", "command"),
			Handler: func(ctx context.Context, tc *agent.ToolContext, raw json.RawMessage) (*agent.ToolResult, error) {
				var in struct {
					Servers    []string `json:"servers"`
					Command    string   `json:"command"`
					Reason     string   `json:"reason"`
					TimeoutSec int      `json:"timeout_sec"`
				}
				if err := parseArgs(raw, &in); err != nil {
					return nil, err
				}
				if strings.TrimSpace(in.Command) == "" {
					return nil, errors.New("command 不能为空")
				}
				servers, err := a.targets(tc, in.Servers)
				if err != nil {
					return nil, err
				}
				risk, why := agent.ClassifyCommand(in.Command)
				title := fmt.Sprintf("在 %d 台服务器上执行命令", len(servers))
				if !tc.Approve(agent.ApprovalRequest{Tool: "run_command", Title: title, Detail: in.Command, Servers: names(servers), Risk: risk, Reason: strings.TrimSpace(in.Reason + " " + why)}) {
					return denied(), nil
				}
				timeout := time.Duration(min(max(in.TimeoutSec, 10), 600)) * time.Second
				if in.TimeoutSec == 0 {
					timeout = 120 * time.Second
				}
				results := a.runForAgent(ctx, servers, &runPlan{kind: "agent", title: clipText(in.Command, 60), content: in.Command, interp: "bash"}, timeout)
				text, meta := formatResults(results)
				return &agent.ToolResult{Content: text, Meta: map[string]any{"kind": "command", "command": in.Command, "risk": risk, "results": meta}}, nil
			},
		},
		{
			Name:        "read_file",
			Description: "读取服务器上的文本文件（配置、日志等）。tail_lines>0 时只读取末尾若干行，适合查看日志。读取私钥等敏感文件需要用户批准。",
			Schema: agent.Obj(map[string]any{
				"server":     agent.Str("服务器名称或 ID"),
				"path":       agent.Str("文件绝对路径"),
				"tail_lines": agent.Int("只读取末尾行数（可选）"),
				"max_bytes":  agent.Int("最大读取字节数，默认 65536"),
			}, "server", "path"),
			Handler: func(ctx context.Context, tc *agent.ToolContext, raw json.RawMessage) (*agent.ToolResult, error) {
				var in struct {
					Server    string `json:"server"`
					Path      string `json:"path"`
					TailLines int    `json:"tail_lines"`
					MaxBytes  int    `json:"max_bytes"`
				}
				if err := parseArgs(raw, &in); err != nil {
					return nil, err
				}
				servers, err := a.targets(tc, []string{in.Server})
				if err != nil {
					return nil, err
				}
				s := servers[0]
				if sensitiveReadRe.MatchString(in.Path) {
					if !tc.Approve(agent.ApprovalRequest{Tool: "read_file", Title: "读取敏感文件", Detail: in.Path, Servers: []string{s.Name}, Risk: agent.RiskChange, Reason: "文件内容将发送给 AI 模型"}) {
						return denied(), nil
					}
				}
				var content string
				if in.TailLines > 0 {
					res, err := a.pool.Exec(ctx, s.ID, fmt.Sprintf("tail -n %d %s", min(in.TailLines, 2000), sshx.ShellQuote(in.Path)), sshx.ExecOptions{Privileged: true, Timeout: 30 * time.Second})
					if err != nil {
						return nil, err
					}
					if res.ExitCode != 0 {
						return nil, errors.New(strings.TrimSpace(res.Combined()))
					}
					content = res.Stdout
				} else {
					max := in.MaxBytes
					if max <= 0 || max > 512*1024 {
						max = 64 * 1024
					}
					content, err = a.pool.ReadFile(ctx, s.ID, in.Path, max)
					if err != nil {
						return nil, err
					}
				}
				return &agent.ToolResult{Content: fmt.Sprintf("%s:%s\n```\n%s\n```", s.Name, in.Path, store.TruncateMiddle(content, 30000)),
					Meta: map[string]any{"kind": "file", "server": s.Name, "path": in.Path}}, nil
			},
		},
		{
			Name:        "write_file",
			Description: "在服务器上写入（覆盖）一个文本文件，默认先备份原文件为 .bak-时间戳。总是需要用户批准。修改配置后记得做语法检测。",
			Schema: agent.Obj(map[string]any{
				"server":  agent.Str("服务器名称或 ID"),
				"path":    agent.Str("文件绝对路径"),
				"content": agent.Str("文件完整内容"),
				"mode":    agent.Str("权限，例如 644（默认）"),
				"backup":  map[string]any{"type": "boolean", "description": "是否备份原文件，默认 true"},
			}, "server", "path", "content"),
			Handler: func(ctx context.Context, tc *agent.ToolContext, raw json.RawMessage) (*agent.ToolResult, error) {
				var in struct {
					Server  string `json:"server"`
					Path    string `json:"path"`
					Content string `json:"content"`
					Mode    string `json:"mode"`
					Backup  *bool  `json:"backup"`
				}
				if err := parseArgs(raw, &in); err != nil {
					return nil, err
				}
				if !strings.HasPrefix(in.Path, "/") {
					return nil, errors.New("path 必须是绝对路径")
				}
				servers, err := a.targets(tc, []string{in.Server})
				if err != nil {
					return nil, err
				}
				s := servers[0]
				risk := agent.RiskChange
				if criticalWriteRe.MatchString(in.Path) {
					risk = agent.RiskDanger
				}
				if !tc.Approve(agent.ApprovalRequest{Tool: "write_file", Title: "写入文件 " + in.Path, Detail: clipText(in.Content, 4000), Servers: []string{s.Name}, Risk: risk, Reason: fmt.Sprintf("%d 字节", len(in.Content))}) {
					return denied(), nil
				}
				suffix := ""
				if in.Backup == nil || *in.Backup {
					suffix = ".bak-" + time.Now().Format("20060102-150405")
				}
				if err := a.pool.WriteFile(ctx, s.ID, in.Path, []byte(in.Content), in.Mode, suffix); err != nil {
					return nil, err
				}
				msg := "已写入 " + in.Path
				if suffix != "" {
					msg += "（原文件如存在已备份为 " + in.Path + suffix + "）"
				}
				return &agent.ToolResult{Content: msg, Meta: map[string]any{"kind": "file_write", "server": s.Name, "path": in.Path}}, nil
			},
		},
		{
			Name:        "server_status",
			Description: "采集服务器的系统概况：系统版本、运行时长、CPU、负载、内存、磁盘、失败的服务、占用最高的进程。排查性能或健康问题时首先调用。",
			Schema:      agent.Obj(map[string]any{"servers": agent.StrArray("服务器名称或 ID 列表，[\"all\"] 表示全部")}, "servers"),
			Handler: func(ctx context.Context, tc *agent.ToolContext, raw json.RawMessage) (*agent.ToolResult, error) {
				var in struct {
					Servers []string `json:"servers"`
				}
				if err := parseArgs(raw, &in); err != nil {
					return nil, err
				}
				servers, err := a.targets(tc, in.Servers)
				if err != nil {
					return nil, err
				}
				parts := make([]string, len(servers))
				pos := map[string]int{}
				for i, s := range servers {
					pos[s.ID] = i
				}
				a.parallel(ids(servers), func(id string) {
					s := servers[pos[id]]
					info, err := ops.CollectInfo(ctx, a.pool, id)
					if err != nil {
						parts[pos[id]] = fmt.Sprintf("## %s\n无法采集：%v", s.Name, err)
						return
					}
					parts[pos[id]] = describeInfo(s.Name, info)
				})
				return &agent.ToolResult{Content: strings.Join(parts, "\n\n"), Meta: map[string]any{"kind": "status", "count": len(servers)}}, nil
			},
		},
		{
			Name:        "service_control",
			Description: "管理 systemd 服务：status 查看状态（直接执行），start / stop / restart / reload / enable / disable 需要用户批准。可同时操作多台服务器。",
			Schema: agent.Obj(map[string]any{
				"servers": agent.StrArray("服务器名称或 ID 列表"),
				"service": agent.Str("服务名，例如 nginx"),
				"action":  agent.StrEnum("操作", "status", "start", "stop", "restart", "reload", "enable", "disable"),
			}, "servers", "service", "action"),
			Handler: func(ctx context.Context, tc *agent.ToolContext, raw json.RawMessage) (*agent.ToolResult, error) {
				var in struct {
					Servers []string `json:"servers"`
					Service string   `json:"service"`
					Action  string   `json:"action"`
				}
				if err := parseArgs(raw, &in); err != nil {
					return nil, err
				}
				svc := strings.TrimSuffix(strings.TrimSpace(in.Service), ".service")
				if err := ops.ValidateService(svc, in.Action); err != nil {
					return nil, err
				}
				servers, err := a.targets(tc, in.Servers)
				if err != nil {
					return nil, err
				}
				if in.Action == "status" {
					var b strings.Builder
					for _, s := range servers {
						out, err := a.ServiceStatus(s.ID, svc)
						if err != nil {
							out = err.Error()
						}
						fmt.Fprintf(&b, "## %s\n```\n%s\n```\n", s.Name, store.TruncateMiddle(out, 4000))
					}
					return &agent.ToolResult{Content: b.String()}, nil
				}
				risk, why := agent.ClassifyCommand("systemctl " + in.Action + " " + svc)
				if risk == agent.RiskSafe {
					risk = agent.RiskChange
				}
				if !tc.Approve(agent.ApprovalRequest{Tool: "service_control", Title: fmt.Sprintf("%s 服务 %s", in.Action, svc), Detail: "systemctl " + in.Action + " " + svc, Servers: names(servers), Risk: risk, Reason: why}) {
					return denied(), nil
				}
				results := a.ServiceAction(ids(servers), svc, in.Action)
				var b strings.Builder
				for _, r := range results {
					mark := "成功"
					if !r.OK {
						mark = "失败"
					}
					fmt.Fprintf(&b, "- %s：%s %s\n", r.ServerName, mark, clipText(r.Message, 500))
				}
				return &agent.ToolResult{Content: b.String(), Meta: map[string]any{"kind": "service", "service": svc, "action": in.Action, "results": results}}, nil
			},
		},
		{
			Name:        "list_certificates",
			Description: "列出证书库中的 TLS 证书：域名、签发者、到期时间，以及每个部署目标（服务器、路径、部署后怎样重载服务）和它的同步状态。",
			Schema:      agent.Obj(map[string]any{}),
			Handler: func(_ context.Context, _ *agent.ToolContext, _ json.RawMessage) (*agent.ToolResult, error) {
				list := a.ListCerts()
				if len(list) == 0 {
					return &agent.ToolResult{Content: "证书库为空。服务器上已有的证书可以用 find_certificates 查找、import_certificate 导入；用户也可以把证书和私钥文件直接发过来。"}, nil
				}
				var b strings.Builder
				for _, c := range list {
					fmt.Fprintf(&b, "## %s（ID %s）\n域名：%s｜签发：%s｜到期：%s（剩 %d 天）\n", c.Name, c.ID, strings.Join(c.Domains, ", "), c.Issuer,
						time.UnixMilli(c.NotAfter).Format("2006-01-02"), c.Days)
					if c.SourceServerName != "" && c.Source != nil {
						fmt.Fprintf(&b, "来源：%s:%s\n", c.SourceServerName, c.Source.CertPath)
					}
					for _, t := range c.Targets {
						state := "未部署"
						switch {
						case t.Synced:
							state = "已同步"
						case t.LastStatus == "failed":
							state = "上次失败：" + clipText(t.LastMessage, 80)
						case t.LastFingerprint != "":
							state = "版本落后"
						}
						fmt.Fprintf(&b, "- 目标 %s：%s → %s｜%s\n", t.ID, t.ServerName, t.CertPath, state)
						for _, line := range strings.Split(strings.TrimSpace(describeTarget(t.DeployTarget)), "\n")[1:] {
							b.WriteString("  " + line + "\n")
						}
					}
					if len(c.Targets) == 0 {
						b.WriteString("- （没有部署目标：需要部署时用 save_deploy_target 添加）\n")
					}
				}
				return &agent.ToolResult{Content: b.String()}, nil
			},
		},
		{
			Name: "deploy_certificate",
			Description: "把证书库中的证书同步到它的部署目标：自动备份旧文件、上传、校验哈希、执行配置检测、重载服务并做线上 TLS 验证，失败自动回滚。需要用户批准。" +
				"certificate 可填证书名称、ID 或域名；servers 可选，用于只同步部分服务器上的目标。证书还没有部署目标、或目标的路径不对时，先用 save_deploy_target 添加或修改。",
			Schema: agent.Obj(map[string]any{
				"certificate": agent.Str("证书名称、ID 或其中一个域名"),
				"servers":     agent.StrArray("只部署到这些服务器上的目标（可选）"),
			}, "certificate"),
			Handler: func(ctx context.Context, tc *agent.ToolContext, raw json.RawMessage) (*agent.ToolResult, error) {
				var in struct {
					Certificate string   `json:"certificate"`
					Servers     []string `json:"servers"`
				}
				if err := parseArgs(raw, &in); err != nil {
					return nil, err
				}
				cert, err := a.findCert(in.Certificate)
				if err != nil {
					return nil, err
				}
				var filter []string
				if len(in.Servers) > 0 {
					servers, err := a.resolveServerRefs(in.Servers)
					if err != nil {
						return nil, err
					}
					filter = ids(servers)
				}
				var targetIDs, desc []string
				for _, t := range cert.Targets {
					if len(filter) == 0 || slices.Contains(filter, t.ServerID) {
						targetIDs = append(targetIDs, t.ID)
						desc = append(desc, fmt.Sprintf("%s → %s", t.ServerName, t.CertPath))
					}
				}
				if len(targetIDs) == 0 {
					return nil, errors.New("这张证书在这些服务器上还没有部署目标。用 save_deploy_target 添加（要填服务器、证书和私钥在服务器上的路径、部署后要重载的服务；加上 deploy=true 可以保存后立即部署），不需要用户自己去「证书」页填写")
				}
				if !tc.Approve(agent.ApprovalRequest{Tool: "deploy_certificate", Title: "部署证书 " + cert.Name, Detail: strings.Join(desc, "\n"), Risk: agent.RiskChange,
					Reason: fmt.Sprintf("有效期至 %s", time.UnixMilli(cert.NotAfter).Format("2006-01-02"))}) {
					return denied(), nil
				}
				results, err := a.deployTargets(ctx, cert.ID, targetIDs, nil, nil)
				if err != nil {
					return nil, err
				}
				return &agent.ToolResult{Content: deployReport(results), Meta: map[string]any{"kind": "deploy", "results": results}}, nil
			},
		},
		{
			Name:        "list_scripts",
			Description: "列出脚本库中的脚本（ID、名称、分类、参数、是否只读）。用户想执行常用操作时先查看是否已有现成脚本。",
			Schema:      agent.Obj(map[string]any{"keyword": agent.Str("按名称、描述或分类筛选（可选）")}),
			Handler: func(_ context.Context, _ *agent.ToolContext, raw json.RawMessage) (*agent.ToolResult, error) {
				var in struct {
					Keyword string `json:"keyword"`
				}
				_ = json.Unmarshal(raw, &in)
				kw := strings.ToLower(strings.TrimSpace(in.Keyword))
				var b strings.Builder
				for _, s := range a.ListScripts() {
					if kw != "" && !strings.Contains(strings.ToLower(s.Name+s.Description+s.Category), kw) {
						continue
					}
					var params []string
					for _, p := range s.Params {
						params = append(params, fmt.Sprintf("%s=%s", p.Name, p.Default))
					}
					ro := ""
					if s.ReadOnly {
						ro = "｜只读"
					}
					fmt.Fprintf(&b, "- %s｜ID %s｜%s%s｜参数 %s｜%s\n", s.Name, s.ID, s.Category, ro, orDash(strings.Join(params, ", ")), s.Description)
				}
				if b.Len() == 0 {
					return &agent.ToolResult{Content: "没有匹配的脚本。"}, nil
				}
				return &agent.ToolResult{Content: b.String()}, nil
			},
		},
		{
			Name:        "run_script",
			Description: "在服务器上执行脚本库中的脚本，params 为脚本参数。只读脚本直接执行，其余需要用户批准。",
			Schema: agent.Obj(map[string]any{
				"script":  agent.Str("脚本名称或 ID"),
				"servers": agent.StrArray("服务器名称或 ID 列表"),
				"params":  map[string]any{"type": "object", "description": "脚本参数，键为参数名", "additionalProperties": map[string]any{"type": "string"}},
			}, "script", "servers"),
			Handler: func(ctx context.Context, tc *agent.ToolContext, raw json.RawMessage) (*agent.ToolResult, error) {
				var in struct {
					Script  string            `json:"script"`
					Servers []string          `json:"servers"`
					Params  map[string]string `json:"params"`
				}
				if err := parseArgs(raw, &in); err != nil {
					return nil, err
				}
				var scriptID string
				for _, s := range a.ListScripts() {
					if s.ID == in.Script || strings.EqualFold(s.Name, strings.TrimSpace(in.Script)) {
						scriptID = s.ID
						break
					}
				}
				if scriptID == "" {
					return nil, fmt.Errorf("没有找到脚本 %s", in.Script)
				}
				servers, err := a.targets(tc, in.Servers)
				if err != nil {
					return nil, err
				}
				p, err := a.planRun(RunRequest{ScriptID: scriptID, Params: in.Params})
				if err != nil {
					return nil, err
				}
				risk := agent.RiskChange
				if p.readOnly {
					risk = agent.RiskSafe
				}
				detail := p.content
				if len(p.env) > 0 {
					var kv []string
					for k, v := range p.env {
						kv = append(kv, k+"="+v)
					}
					slices.Sort(kv)
					detail = "# 参数：" + strings.Join(kv, " ") + "\n" + detail
				}
				if !tc.Approve(agent.ApprovalRequest{Tool: "run_script", Title: "执行脚本「" + p.title + "」", Detail: clipText(detail, 4000), Servers: names(servers), Risk: risk}) {
					return denied(), nil
				}
				results := a.runForAgent(ctx, servers, p, a.commandTimeout())
				text, meta := formatResults(results)
				return &agent.ToolResult{Content: text, Meta: map[string]any{"kind": "command", "command": "脚本：" + p.title, "risk": risk, "results": meta}}, nil
			},
		},
		{
			Name:        "save_script",
			Description: "把一段可复用的命令或脚本保存到脚本库，方便用户以后一键在多台服务器上执行。传入 id 表示更新之前由你创建的脚本。",
			Schema: agent.Obj(map[string]any{
				"id":          agent.Str("要更新的脚本 ID（可选）"),
				"name":        agent.Str("脚本名称"),
				"description": agent.Str("用途说明"),
				"content":     agent.Str("脚本内容；参数通过环境变量读取，例如 $DOMAIN"),
				"interpreter": agent.StrEnum("解释器，默认 bash", "bash", "sh", "python3"),
				"category":    agent.Str("分类，例如 系统 / 磁盘 / 网络 / 证书 / 服务 / 容器 / 安全"),
				"read_only":   map[string]any{"type": "boolean", "description": "脚本是否只读（不修改系统），只读脚本由 AI 调用时无需批准"},
				"params": map[string]any{"type": "array", "description": "参数定义", "items": agent.Obj(map[string]any{
					"name": agent.Str("参数名（环境变量名）"), "label": agent.Str("显示名称"), "default": agent.Str("默认值"),
					"required": map[string]any{"type": "boolean"},
				}, "name")},
			}, "name", "content"),
			Handler: func(_ context.Context, tc *agent.ToolContext, raw json.RawMessage) (*agent.ToolResult, error) {
				var in struct {
					ID          string              `json:"id"`
					Name        string              `json:"name"`
					Description string              `json:"description"`
					Content     string              `json:"content"`
					Interpreter string              `json:"interpreter"`
					Category    string              `json:"category"`
					ReadOnly    bool                `json:"read_only"`
					Params      []store.ScriptParam `json:"params"`
				}
				if err := parseArgs(raw, &in); err != nil {
					return nil, err
				}
				if in.ID != "" {
					var owner string
					a.store.Read(func(d *store.Data) {
						if s := d.Script(in.ID); s != nil {
							owner = s.CreatedBy
						}
					})
					if owner != "agent" {
						return nil, errors.New("只能更新由 AI 创建的脚本；如需修改用户脚本请新建一个")
					}
				}
				s, err := a.SaveScript(store.Script{ID: in.ID, Name: in.Name, Description: in.Description, Content: in.Content, Interpreter: in.Interpreter,
					Category: in.Category, ReadOnly: in.ReadOnly, Params: in.Params, CreatedBy: "agent"})
				if err != nil {
					return nil, err
				}
				action := "create"
				if in.ID != "" {
					action = "edit"
				}
				a.agent.RecordLearning(tc.SessionID(), agent.LearnEvent{Source: tc.Source(), Kind: "script", Action: action, Target: s.Name, Summary: s.Description})
				return &agent.ToolResult{Content: fmt.Sprintf("已保存到脚本库：%s（ID %s）", s.Name, s.ID), Meta: map[string]any{"kind": "script", "id": s.ID, "name": s.Name}}, nil
			},
		},
		{
			Name:        "read_terminal",
			Description: "读取用户终端窗口里最近的内容（用户刚执行的命令和它们的输出）。用户提到“刚才的报错”“终端里的输出”“帮我看看这个”而没有贴出内容时调用。只能读取当前打开了终端的服务器。",
			Schema: agent.Obj(map[string]any{
				"server": agent.Str("服务器名称或 ID；从终端发起的对话可以不填，默认是当前终端的服务器"),
				"lines":  agent.Int("读取末尾多少行，默认 80，最大 400"),
			}),
			Handler: func(_ context.Context, tc *agent.ToolContext, raw json.RawMessage) (*agent.ToolResult, error) {
				var in struct {
					Server string `json:"server"`
					Lines  int    `json:"lines"`
				}
				_ = json.Unmarshal(raw, &in)
				servers, err := a.targets(tc, []string{in.Server})
				if err != nil {
					return nil, err
				}
				if len(servers) != 1 {
					return nil, errors.New("一次只能读取一台服务器的终端")
				}
				lines := 80
				if in.Lines > 0 {
					lines = min(in.Lines, 400)
				}
				text, ok := a.terms.Recent(servers[0].ID, lines)
				if !ok {
					return &agent.ToolResult{Content: fmt.Sprintf("服务器 %s 当前没有打开的终端，无法读取。可以用 run_command 直接查询需要的信息。", servers[0].Name), IsError: true}, nil
				}
				if strings.TrimSpace(text) == "" {
					return &agent.ToolResult{Content: "终端里还没有输出。"}, nil
				}
				text = store.TruncateMiddle(text, 12000)
				return &agent.ToolResult{Content: fmt.Sprintf("服务器 %s 的终端最近 %d 行以内的内容：\n```\n%s\n```", servers[0].Name, lines, text),
					Meta: map[string]any{"kind": "terminal", "server": servers[0].Name}}, nil
			},
		},
		{
			Name:        "run_inspection",
			Description: "对服务器做一次全面巡检（磁盘、内存、负载、失败服务、证书到期与同步状态等），返回按严重程度排序的问题清单。用户要求体检、巡检或给出运维建议时调用。",
			Schema:      agent.Obj(map[string]any{"servers": agent.StrArray("服务器名称或 ID 列表，不填表示全部")}),
			Handler: func(_ context.Context, _ *agent.ToolContext, raw json.RawMessage) (*agent.ToolResult, error) {
				var in struct {
					Servers []string `json:"servers"`
				}
				_ = json.Unmarshal(raw, &in)
				var filter []string
				if len(in.Servers) > 0 {
					servers, err := a.resolveServerRefs(in.Servers)
					if err != nil {
						return nil, err
					}
					filter = ids(servers)
				}
				r, err := a.RunInspection(filter)
				if err != nil {
					return nil, err
				}
				a.emit("inspect:done", r)
				return &agent.ToolResult{Content: ops.ReportPrompt(r), Meta: map[string]any{"kind": "inspection", "score": r.Score, "critical": r.Critical, "warning": r.Warning}}, nil
			},
		},
	}
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func describeInfo(name string, in *ops.ServerInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s（%s，内核 %s）\n", name, in.OS, in.Kernel)
	fmt.Fprintf(&b, "运行 %s｜CPU %d 核 %.0f%%｜负载 %.2f %.2f %.2f\n", (time.Duration(in.Uptime) * time.Second).Truncate(time.Minute), in.CPUs, in.CPUUsage, in.Load[0], in.Load[1], in.Load[2])
	fmt.Fprintf(&b, "内存 %.0f%%（可用 %s / %s）", in.MemUsedPct(), humanSize(in.MemAvailable), humanSize(in.MemTotal))
	if in.SwapTotal > 0 {
		fmt.Fprintf(&b, "｜Swap %.0f%%", in.SwapUsedPct())
	}
	b.WriteString("\n磁盘：")
	for i, d := range in.Disks {
		if i > 0 {
			b.WriteString("，")
		}
		fmt.Fprintf(&b, "%s %.0f%%（%s/%s）", d.Mount, d.UsePct, humanSize(d.Used), humanSize(d.Size))
	}
	if len(in.FailedUnits) > 0 {
		fmt.Fprintf(&b, "\n失败服务：%s", strings.Join(in.FailedUnits, ", "))
	}
	if len(in.TopProcs) > 0 {
		b.WriteString("\nCPU 占用最高：")
		for i, p := range in.TopProcs {
			if i > 0 {
				b.WriteString("，")
			}
			fmt.Fprintf(&b, "%s(%d) %.1f%%", p.Command, p.PID, p.CPU)
		}
	}
	return b.String()
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGTPE"[exp])
}
