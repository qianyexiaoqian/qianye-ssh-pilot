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
	"sshpilot/internal/certs"
	"sshpilot/internal/store"
)

// AI 管理证书的部署目标：证书要放到哪台服务器的哪个路径、部署后怎样让服务生效。
// 以前只能由用户在「证书」页填写，AI 遇到没有部署目标的证书只能停下来等；现在 AI 查清路径后可以自己添加或修改，
// 照样要用户批准。私钥始终由程序直接写到服务器上，不经过模型。

var (
	targetModeRe    = regexp.MustCompile(`^[0-7]{3,4}$`)
	targetOwnerRe   = regexp.MustCompile(`^[A-Za-z0-9_.-]+(:[A-Za-z0-9_.-]+)?$`)
	targetServiceRe = regexp.MustCompile(`^[A-Za-z0-9@._:-]+$`)
)

// findCert 按 ID、名称或域名查找证书。
func (a *App) findCert(ref string) (*CertView, error) {
	ref = strings.TrimSpace(ref)
	list := a.ListCerts()
	for _, c := range list {
		if c.ID == ref || strings.EqualFold(c.Name, ref) || slices.Contains(c.Domains, ref) {
			return &c, nil
		}
	}
	var names []string
	for _, c := range list {
		names = append(names, c.Name)
	}
	if len(names) == 0 {
		return nil, errors.New("证书库是空的：先用 import_certificate 把证书收进来（服务器上的证书用 find_certificates 查找路径，或让用户把证书和私钥文件发过来）")
	}
	return nil, fmt.Errorf("没有找到证书 %s。证书库里的证书：%s", ref, strings.Join(names, "、"))
}

// deployReport 把部署结果整理成给模型看的文字。
func deployReport(results []certs.DeployResult) string {
	var b strings.Builder
	for _, r := range results {
		mark := "✅"
		if !r.Success {
			mark = "❌"
		}
		fmt.Fprintf(&b, "%s %s：%s\n", mark, r.ServerName, r.Message)
		for _, s := range r.Steps {
			if s.Status == "failed" || s.Status == "warning" {
				fmt.Fprintf(&b, "   - %s（%s）：%s\n", s.Label, s.Status, clipText(s.Message, 400))
			}
		}
	}
	return b.String()
}

// targetPreset 是常见服务的部署方式：证书文件的格式、部署后怎样检测配置、怎样让服务用上新证书。
// 与「证书」页添加部署目标时的预设保持一致（frontend/src/pages/Certs.tsx 的 presetValues）。
type targetPreset struct {
	Format, TestCommand string
	Services            []string
	Action              string
}

var targetPresets = map[string]targetPreset{
	"nginx":   {"fullchain", "nginx -t", []string{"nginx"}, "reload"},
	"apache":  {"fullchain", "apachectl configtest", []string{"apache2"}, "reload"},
	"haproxy": {"combined", "haproxy -c -f /etc/haproxy/haproxy.cfg", []string{"haproxy"}, "reload"},
	// XBoard 节点：没有 reload，换证书后要重启；证书路径由 /etc/xboard-node/config.yml 里的 cert_file / key_file 决定
	"xboard": {"fullchain", "", []string{"xboard-node"}, "restart"},
}

var certFormatLabel = map[string]string{"fullchain": "完整证书链", "leaf": "只有站点证书", "combined": "证书和私钥合并在一个文件"}

// describeTarget 把部署目标写成几行说明：给用户看的审批内容，也是给模型看的证书列表。
func describeTarget(t store.DeployTarget) string {
	var b strings.Builder
	fmt.Fprintf(&b, "证书文件：%s（%s）\n", t.CertPath, certFormatLabel[t.CertFormat])
	if t.CertFormat != "combined" {
		fmt.Fprintf(&b, "私钥文件：%s\n", t.KeyPath)
	}
	if t.ChainPath != "" {
		fmt.Fprintf(&b, "证书链文件：%s\n", t.ChainPath)
	}
	fmt.Fprintf(&b, "权限：证书 %s，私钥 %s", t.CertMode, t.KeyMode)
	if t.Owner != "" {
		fmt.Fprintf(&b, "，属主 %s", t.Owner)
	}
	b.WriteString("\n")
	if t.TestCommand != "" {
		fmt.Fprintf(&b, "部署后先检测配置：%s\n", t.TestCommand)
	}
	if t.ServiceAction != "none" && len(t.Services) > 0 {
		fmt.Fprintf(&b, "然后 %s 服务：%s\n", t.ServiceAction, strings.Join(t.Services, "、"))
	} else {
		b.WriteString("不重载任何服务\n")
	}
	if t.PostCommand != "" {
		fmt.Fprintf(&b, "最后执行：%s\n", t.PostCommand)
	}
	if t.VerifyPort > 0 {
		fmt.Fprintf(&b, "线上验证：连接 %d 端口，确认返回的是新证书\n", t.VerifyPort)
	}
	if !t.Backup {
		b.WriteString("不备份原来的文件\n")
	}
	return b.String()
}

func (a *App) targetTools() []*agent.Tool {
	return []*agent.Tool{{
		Name: "save_deploy_target",
		Description: "为证书库里的证书添加部署目标，或修改、删除已有的部署目标。部署目标记录的是：证书放到哪台服务器的哪个路径、部署后怎样让服务用上新证书。" +
			"用户想把证书部署到一个还没有部署目标的位置、或者目标的路径和重载方式不对时，直接用这个工具处理，不要让用户自己去「证书」页填写。需要用户批准。\n" +
			"- 保存前先查清楚：用 run_command 看服务的配置，确认它实际读取的证书和私钥路径，以及让它重新加载证书的方式。\n" +
			"- 常见的服务用 preset 一步填好部署后的操作：nginx（nginx -t 后 reload nginx）、apache（configtest 后 reload apache2）、haproxy（证书和私钥合并成一个文件，reload haproxy）、" +
			"xboard（XBoard 节点 xboard-node：没有 reload，部署后 restart xboard-node；证书路径以 /etc/xboard-node/config.yml 里的 cert_file / key_file 为准）。用了 preset 仍然可以单独覆盖其中某一项。\n" +
			"- 修改已有的目标：填 target_id（list_certificates 里列出的），只写要改的项，其余保持不变。\n" +
			"- deploy=true 表示保存后立即部署到这个目标（备份旧文件、上传、校验、检测配置、重载服务，失败自动回滚），用户只需要批准一次。\n" +
			"- 保存的目标以后一直有效：证书更新后的一键同步和自动同步也会同步到这里。",
		Schema: agent.Obj(map[string]any{
			"certificate":    agent.Str("证书名称、ID 或其中一个域名"),
			"action":         agent.StrEnum("save=添加或修改（默认）；remove=删除这个部署目标（服务器上已经部署的文件不会删除）", "save", "remove"),
			"target_id":      agent.Str("要修改或删除的部署目标 ID；添加新目标时不填"),
			"server":         agent.Str("服务器名称或 ID（添加时必填）"),
			"preset":         agent.StrEnum("按常见服务的做法填写 cert_format、test_command、services、service_action；不属于这几种服务时不填", "nginx", "apache", "haproxy", "xboard"),
			"cert_path":      agent.Str("证书文件在服务器上的绝对路径（添加时必填），例如 /etc/nginx/ssl/example.com.pem"),
			"key_path":       agent.Str("私钥文件在服务器上的绝对路径；cert_format=combined 时不需要"),
			"chain_path":     agent.Str("单独存放中间证书链的文件路径（可选，大多数服务不需要）"),
			"cert_format":    agent.StrEnum("证书文件的内容：fullchain=站点证书加中间证书（默认，Nginx 等大多数服务）；leaf=只有站点证书；combined=证书和私钥合并在一个文件（HAProxy）", "fullchain", "leaf", "combined"),
			"services":       agent.StrArray("部署后要重载的 systemd 服务名，例如 [\"nginx\"]；不需要重载时传空数组"),
			"service_action": agent.StrEnum("对这些服务做什么：reload（默认）、restart（服务不支持 reload 时）、none", "reload", "restart", "none"),
			"test_command":   agent.Str("重载服务之前先执行的配置检测命令，例如 nginx -t；检测不通过会自动回滚。没有就留空"),
			"post_command":   agent.Str("最后执行的命令（可选），用于 systemd 服务名表达不了的情况，例如 docker restart 某个容器"),
			"cert_mode":      agent.Str("证书文件的权限，默认 644"),
			"key_mode":       agent.Str("私钥文件的权限，默认 600"),
			"owner":          agent.Str("文件属主，例如 nginx 或 www-data:www-data；不填则不修改"),
			"backup":         map[string]any{"type": "boolean", "description": "部署时是否备份原来的文件，默认 true"},
			"verify_port":    agent.Int("部署后从用户的电脑连接服务器的这个端口，确认返回的是新证书（例如 443）；0 或不填表示不验证"),
			"verify_domain":  agent.Str("线上验证时使用的域名（SNI），默认用证书的第一个域名"),
			"deploy":         map[string]any{"type": "boolean", "description": "保存后立即把证书部署到这个目标，默认 false"},
		}, "certificate"),
		Handler: func(ctx context.Context, tc *agent.ToolContext, raw json.RawMessage) (*agent.ToolResult, error) {
			var in struct {
				Certificate   string    `json:"certificate"`
				Action        string    `json:"action"`
				TargetID      string    `json:"target_id"`
				Server        string    `json:"server"`
				Preset        string    `json:"preset"`
				CertPath      *string   `json:"cert_path"`
				KeyPath       *string   `json:"key_path"`
				ChainPath     *string   `json:"chain_path"`
				CertFormat    *string   `json:"cert_format"`
				Services      *[]string `json:"services"`
				ServiceAction *string   `json:"service_action"`
				TestCommand   *string   `json:"test_command"`
				PostCommand   *string   `json:"post_command"`
				CertMode      *string   `json:"cert_mode"`
				KeyMode       *string   `json:"key_mode"`
				Owner         *string   `json:"owner"`
				Backup        *bool     `json:"backup"`
				VerifyPort    *int      `json:"verify_port"`
				VerifyDomain  *string   `json:"verify_domain"`
				Deploy        bool      `json:"deploy"`
			}
			if err := parseArgs(raw, &in); err != nil {
				return nil, err
			}
			cert, err := a.findCert(in.Certificate)
			if err != nil {
				return nil, err
			}
			find := func(match func(TargetView) bool) *TargetView {
				for i := range cert.Targets {
					if match(cert.Targets[i]) {
						return &cert.Targets[i]
					}
				}
				return nil
			}
			var existing *TargetView
			if id := strings.TrimSpace(in.TargetID); id != "" {
				if existing = find(func(t TargetView) bool { return t.ID == id }); existing == nil {
					var known []string
					for _, t := range cert.Targets {
						known = append(known, fmt.Sprintf("%s（%s → %s）", t.ID, t.ServerName, t.CertPath))
					}
					if len(known) == 0 {
						return nil, fmt.Errorf("证书 %s 还没有部署目标：添加新目标时不要填 target_id", cert.Name)
					}
					return nil, fmt.Errorf("证书 %s 没有 ID 为 %s 的部署目标。现有的目标：%s", cert.Name, id, strings.Join(known, "；"))
				}
			}

			if in.Action == "remove" {
				if existing == nil {
					return nil, errors.New("删除部署目标需要填 target_id")
				}
				if !tc.Approve(agent.ApprovalRequest{Tool: "save_deploy_target", Title: "删除证书 " + cert.Name + " 的部署目标", Servers: []string{existing.ServerName}, Risk: agent.RiskChange,
					Detail: existing.CertPath + "\n以后证书更新时不再同步到这里；服务器上已经部署的文件不会删除。"}) {
					return denied(), nil
				}
				if err := a.DeleteTarget(existing.ID); err != nil {
					return nil, err
				}
				return &agent.ToolResult{Content: fmt.Sprintf("已删除部署目标：%s → %s", existing.ServerName, existing.CertPath)}, nil
			}

			// 要保存的目标：修改时从原来的开始，添加时从默认值开始
			t := store.DeployTarget{CertID: cert.ID, Preset: "custom", CertFormat: "fullchain", CertMode: "644", KeyMode: "600", Backup: true, Services: []string{}, ServiceAction: "none"}
			serverName := ""
			if existing == nil && in.CertPath != nil {
				// 同一台服务器、同一个路径已经有目标：当作修改它，而不是报“已存在”
				if servers, err := a.targets(tc, []string{in.Server}); err == nil {
					path := strings.TrimSpace(*in.CertPath)
					existing = find(func(x TargetView) bool { return x.ServerID == servers[0].ID && x.CertPath == path })
				}
			}
			if existing != nil {
				t, serverName = existing.DeployTarget, existing.ServerName
			}
			if existing == nil || strings.TrimSpace(in.Server) != "" {
				servers, err := a.targets(tc, []string{in.Server})
				if err != nil {
					return nil, err
				}
				if len(servers) != 1 {
					return nil, errors.New("一个部署目标只能对应一台服务器；要部署到多台，请分别添加")
				}
				t.ServerID, serverName = servers[0].ID, servers[0].Name
			}
			if name := strings.TrimSpace(in.Preset); name != "" && name != "custom" {
				p, ok := targetPresets[name]
				if !ok {
					return nil, errors.New("preset 只能是 nginx、apache、haproxy 或 xboard；其他服务不填 preset，直接写 services、service_action 等")
				}
				t.Preset, t.CertFormat, t.TestCommand, t.Services, t.ServiceAction = name, p.Format, p.TestCommand, slices.Clone(p.Services), p.Action
			}
			set := func(dst *string, src *string) {
				if src != nil {
					*dst = strings.TrimSpace(*src)
				}
			}
			set(&t.CertPath, in.CertPath)
			set(&t.KeyPath, in.KeyPath)
			set(&t.ChainPath, in.ChainPath)
			set(&t.CertFormat, in.CertFormat)
			set(&t.TestCommand, in.TestCommand)
			set(&t.PostCommand, in.PostCommand)
			set(&t.CertMode, in.CertMode)
			set(&t.KeyMode, in.KeyMode)
			set(&t.Owner, in.Owner)
			set(&t.VerifyDomain, in.VerifyDomain)
			if in.Backup != nil {
				t.Backup = *in.Backup
			}
			if in.VerifyPort != nil {
				t.VerifyPort = *in.VerifyPort
			}
			if in.Services != nil {
				t.Services = slices.DeleteFunc(slices.Clone(*in.Services), func(s string) bool { return strings.TrimSpace(s) == "" })
				// 给了服务、没说怎么处理：按 reload
				if in.ServiceAction == nil && len(t.Services) > 0 && t.ServiceAction == "none" {
					t.ServiceAction = "reload"
				}
			}
			set(&t.ServiceAction, in.ServiceAction)
			if len(t.Services) == 0 {
				t.ServiceAction = "none"
			}

			switch {
			case t.CertPath == "":
				return nil, errors.New("请填写 cert_path：证书文件在服务器上的绝对路径")
			case certFormatLabel[t.CertFormat] == "":
				return nil, errors.New("cert_format 只能是 fullchain、leaf 或 combined")
			case t.CertFormat != "combined" && t.KeyPath == "":
				return nil, errors.New("请填写 key_path：私钥文件在服务器上的绝对路径（cert_format=combined 时才不需要）")
			case !slices.Contains([]string{"reload", "restart", "none"}, t.ServiceAction):
				return nil, errors.New("service_action 只能是 reload、restart 或 none")
			case !targetModeRe.MatchString(t.CertMode) || !targetModeRe.MatchString(t.KeyMode):
				return nil, errors.New("cert_mode / key_mode 要写成 644、600 这样的八进制权限")
			case t.Owner != "" && !targetOwnerRe.MatchString(t.Owner):
				return nil, errors.New("owner 要写成 用户 或 用户:用户组，例如 www-data:www-data")
			case t.VerifyPort < 0 || t.VerifyPort > 65535:
				return nil, errors.New("verify_port 不是有效的端口")
			}
			for _, s := range t.Services {
				if !targetServiceRe.MatchString(s) {
					return nil, fmt.Errorf("服务名 %q 不合法：这里只填 systemd 的服务名，其他命令写在 post_command 里", s)
				}
			}
			if t.CertFormat == "combined" {
				t.KeyPath = ""
			}

			verb := "添加"
			if existing != nil {
				verb = "修改"
			}
			title := fmt.Sprintf("%s证书 %s 的部署目标", verb, cert.Name)
			detail := describeTarget(t)
			if in.Deploy {
				title += "，并立即部署"
				detail += "保存后立即部署：备份旧文件、上传、校验、检测配置、重载服务，失败自动回滚\n"
			}
			risk := agent.RiskChange
			if criticalWriteRe.MatchString(t.CertPath) || criticalWriteRe.MatchString(t.KeyPath) || criticalWriteRe.MatchString(t.ChainPath) {
				risk = agent.RiskDanger
			}
			if !tc.Approve(agent.ApprovalRequest{Tool: "save_deploy_target", Title: title, Detail: detail, Servers: []string{serverName}, Risk: risk,
				Reason: fmt.Sprintf("证书有效期至 %s", time.UnixMilli(cert.NotAfter).Format("2006-01-02"))}) {
				return denied(), nil
			}
			saved, err := a.SaveTarget(t)
			if err != nil {
				return nil, err
			}
			head := fmt.Sprintf("已%s部署目标（ID %s）：%s → %s\n", verb, saved.ID, serverName, saved.CertPath)
			meta := map[string]any{"kind": "deploy_target", "target": saved.ID, "server": serverName}
			if !in.Deploy {
				return &agent.ToolResult{Content: head + "还没有部署：需要部署时调用 deploy_certificate。", Meta: meta}, nil
			}
			results, err := a.deployTargets(ctx, cert.ID, []string{saved.ID}, nil, nil)
			if err != nil {
				return nil, fmt.Errorf("部署目标已经保存，但部署没有执行：%w", err)
			}
			failed := len(results) == 0 || !results[0].Success
			meta["kind"], meta["results"] = "deploy", results
			return &agent.ToolResult{Content: head + deployReport(results), IsError: failed, Meta: meta}, nil
		},
	}}
}
