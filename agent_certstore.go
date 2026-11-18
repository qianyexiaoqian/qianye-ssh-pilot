package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"sshpilot/internal/agent"
	"sshpilot/internal/certs"
	"sshpilot/internal/store"
)

// AI 管理证书库：在服务器上找证书、把证书导入证书库（来自服务器上的文件或用户发来的附件）、查看线上实际返回的证书。
// 以前 AI 只能部署证书库里已有的证书，用户说“把这台机器上的证书收进证书中心”时它无能为力；
// 现在由程序直接读取证书和私钥并保存，私钥不经过模型。

var certSourceLabel = map[string]string{"nginx": "Nginx", "apache": "Apache", "haproxy": "HAProxy", "xboard": "XBoard 节点", "1panel": "1Panel 网站", "letsencrypt": "Let's Encrypt"}

func dateOf(ms int64) string { return time.UnixMilli(ms).Format("2006-01-02") }

// certByFingerprint 在证书库里找指纹相同的证书（同一张证书的同一个版本）。
func (a *App) certByFingerprint(fp string) *CertView {
	for _, c := range a.ListCerts() {
		if c.Fingerprint == fp {
			return &c
		}
	}
	return nil
}

// certBySameDomains 在证书库里找域名完全相同的证书（多半是同一张证书的另一个版本）。
func (a *App) certBySameDomains(domains []string) *CertView {
	if len(domains) == 0 {
		return nil
	}
	for _, c := range a.ListCerts() {
		if sameDomains(c.Domains, domains) {
			return &c
		}
	}
	return nil
}

// attachmentPEM 读取用户发来的证书 / 私钥附件的内容（留在本机，不发给模型）。
func (a *App) attachmentPEM(tc *agent.ToolContext, refs ...string) ([]string, error) {
	var texts []string
	seen := map[string]bool{}
	for _, ref := range refs {
		if ref = strings.TrimSpace(ref); ref == "" {
			continue
		}
		att, err := agent.FindAttachment(tc.Session, ref)
		if err != nil {
			return nil, err
		}
		if seen[att.ID] {
			continue
		}
		seen[att.ID] = true
		if att.Kind == agent.AttachImage || !att.Text {
			return nil, fmt.Errorf("「%s」不是 PEM 文本文件。.pfx / .p12 / .jks 这类格式要先转换成 PEM（fullchain.pem + privkey.pem，或 .crt + .key）", att.Name)
		}
		local, err := a.agent.Attach.Local(tc.SessionID(), att)
		if err != nil {
			return nil, err
		}
		text, _, err := agent.ReadText(local)
		if err != nil {
			return nil, fmt.Errorf("读取「%s」失败：%v", att.Name, err)
		}
		texts = append(texts, text)
	}
	return texts, nil
}

func describeParsed(p *certs.Parsed) string {
	return fmt.Sprintf("域名：%s\n签发者：%s\n有效期：%s 至 %s（剩 %d 天）", strings.Join(p.Domains, ", "), p.Issuer, dateOf(p.NotBefore), dateOf(p.NotAfter), certDays(p.NotAfter))
}

func (a *App) certTools() []*agent.Tool {
	return []*agent.Tool{
		{
			Name: "find_certificates",
			Description: "在服务器上查找正在使用的 TLS 证书文件：读取 Nginx / Apache / HAProxy / XBoard 节点的配置、1Panel 面板的网站证书目录和 /etc/letsencrypt/live，" +
				"返回每张证书的证书文件与私钥文件路径、域名、到期时间，以及它在不在证书库里。用户想把服务器上的证书收进证书库、或想知道证书放在哪里时先调用它；" +
				"这里没有列出的证书再用 run_command 查找（看服务的配置文件，或按文件名搜索）。只读，不需要批准。",
			Schema: agent.Obj(map[string]any{"server": agent.Str("服务器名称或 ID；从终端发起的对话可以不填")}),
			Handler: func(_ context.Context, tc *agent.ToolContext, raw json.RawMessage) (*agent.ToolResult, error) {
				var in struct {
					Server string `json:"server"`
				}
				_ = json.Unmarshal(raw, &in)
				servers, err := a.targets(tc, []string{in.Server})
				if err != nil {
					return nil, err
				}
				if len(servers) != 1 {
					return nil, errors.New("一次只能查找一台服务器")
				}
				s := servers[0]
				found, err := a.DiscoverCerts(s.ID)
				if err != nil {
					return nil, err
				}
				if len(found) == 0 {
					return &agent.ToolResult{Content: fmt.Sprintf("在 %s 上没有找到证书：没有 Nginx / Apache / HAProxy / XBoard 节点的证书配置，也没有 1Panel 网站证书和 /etc/letsencrypt/live。"+
						"证书可能放在别的位置，可以用 run_command 查找：看相关服务的配置文件，或者 find / -xdev \\( -name '*.pem' -o -name '*.crt' \\) 2>/dev/null | head -50。", s.Name)}, nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "在 %s 上找到 %d 张证书：\n", s.Name, len(found))
				for _, d := range found {
					label := certSourceLabel[d.Source]
					if label == "" {
						label = d.Source
					}
					fmt.Fprintf(&b, "- [%s] 证书 %s", label, d.CertPath)
					switch {
					case d.KeyPath == "":
						b.WriteString("｜私钥路径未知")
					case d.KeyPath == d.CertPath:
						b.WriteString("｜私钥在同一个文件里")
					default:
						fmt.Fprintf(&b, "｜私钥 %s", d.KeyPath)
					}
					if d.Error != "" {
						fmt.Fprintf(&b, "｜%s\n", d.Error)
						continue
					}
					fmt.Fprintf(&b, "｜域名 %s｜签发 %s｜到期 %s（剩 %d 天）", strings.Join(d.Domains, ", "), d.Issuer, dateOf(d.NotAfter), certDays(d.NotAfter))
					switch {
					case d.MatchCertID == "":
						b.WriteString("｜证书库：还没有")
					case d.SameVersion:
						fmt.Fprintf(&b, "｜证书库：已有「%s」，同一个版本", d.MatchCertName)
					default:
						note := "证书库里的版本不同"
						if c, err := a.GetCert(d.MatchCertID); err == nil {
							if d.NotAfter > c.NotAfter {
								note = fmt.Sprintf("服务器上的更新（证书库里的到期 %s），可以用 import_certificate 更新证书库", dateOf(c.NotAfter))
							} else if d.NotAfter == c.NotAfter {
								note = "证书库里的版本不同（到期时间相同）"
							} else {
								note = fmt.Sprintf("证书库里的更新（到期 %s），可以部署过来", dateOf(c.NotAfter))
							}
						}
						fmt.Fprintf(&b, "｜证书库：已有「%s」，%s", d.MatchCertName, note)
					}
					b.WriteString("\n")
				}
				return &agent.ToolResult{Content: b.String(), Meta: map[string]any{"kind": "cert_find", "server": s.Name, "count": len(found)}}, nil
			},
		},
		{
			Name: "import_certificate",
			Description: "把证书收进证书库（证书中心）。证书和私钥由程序直接读取、加密保存在用户的电脑上，不经过你——不要用 read_file、cat 或 read_attachment 去读私钥。\n" +
				"- 从服务器导入：填 server、cert_path、key_path（路径先用 find_certificates 查，查不到再用 run_command 找）。会记下来源，以后证书在那台服务器上续期了，只填 certificate 再调用一次就能把新版本拉过来。\n" +
				"- 从用户发来的附件导入：填 cert_attachment 和 key_attachment（证书和私钥在同一个文件里时只填 cert_attachment）。\n" +
				"- certificate 填证书库里已有证书的名称、ID 或域名，表示用新版本替换它（续期），它的部署目标保持不变；这种替换需要用户批准。\n" +
				"- 只想改证书在证书库里显示的名称：填 certificate 和 name。\n" +
				"导入以后要部署到别的服务器，用 save_deploy_target（deploy=true）。",
			Schema: agent.Obj(map[string]any{
				"server":           agent.Str("证书所在的服务器名称或 ID"),
				"cert_path":        agent.Str("服务器上证书文件的绝对路径（最好是包含中间证书的 fullchain）"),
				"key_path":         agent.Str("服务器上私钥文件的绝对路径；证书和私钥合并在一个文件里时可以不填"),
				"chain_path":       agent.Str("中间证书单独放在一个文件里时填它的路径（可选）"),
				"cert_attachment":  agent.Str("证书附件的 ID 或文件名"),
				"key_attachment":   agent.Str("私钥附件的 ID 或文件名"),
				"chain_attachment": agent.Str("中间证书附件的 ID 或文件名（可选）"),
				"certificate":      agent.Str("要更新的已有证书：名称、ID 或其中一个域名（可选）"),
				"name":             agent.Str("证书在证书库里显示的名称；不填时用第一个域名"),
				"separate":         map[string]any{"type": "boolean", "description": "证书库里已经有域名相同的证书时，仍然作为另一张独立的证书保存"},
			}),
			Handler: func(ctx context.Context, tc *agent.ToolContext, raw json.RawMessage) (*agent.ToolResult, error) {
				var in struct {
					Server          string `json:"server"`
					CertPath        string `json:"cert_path"`
					KeyPath         string `json:"key_path"`
					ChainPath       string `json:"chain_path"`
					CertAttachment  string `json:"cert_attachment"`
					KeyAttachment   string `json:"key_attachment"`
					ChainAttachment string `json:"chain_attachment"`
					Certificate     string `json:"certificate"`
					Name            string `json:"name"`
					Separate        bool   `json:"separate"`
				}
				if err := parseArgs(raw, &in); err != nil {
					return nil, err
				}
				in.CertPath, in.KeyPath, in.ChainPath, in.Name = strings.TrimSpace(in.CertPath), strings.TrimSpace(in.KeyPath), strings.TrimSpace(in.ChainPath), strings.TrimSpace(in.Name)
				var existing *CertView
				if ref := strings.TrimSpace(in.Certificate); ref != "" {
					c, err := a.findCert(ref)
					if err != nil {
						return nil, err
					}
					existing = c
				}
				fromServer, fromFiles := in.CertPath != "", strings.TrimSpace(in.CertAttachment) != ""
				if fromServer && fromFiles {
					return nil, errors.New("证书的来源只能选一种：服务器上的文件（server + cert_path + key_path），或者用户发来的附件（cert_attachment + key_attachment）")
				}
				if !fromServer && !fromFiles {
					if existing == nil {
						return nil, errors.New("请说明证书从哪里来：服务器上的文件（server + cert_path + key_path），或者用户发来的附件（cert_attachment + key_attachment）")
					}
					return a.refreshForAgent(tc, existing, in.Name)
				}

				var p *certs.Parsed
				var src *store.CertSource
				from := "用户发来的文件"
				if fromServer {
					servers, err := a.targets(tc, []string{in.Server})
					if err != nil {
						return nil, err
					}
					if len(servers) != 1 {
						return nil, errors.New("一次只能从一台服务器导入")
					}
					s := servers[0]
					src = &store.CertSource{ServerID: s.ID, CertPath: in.CertPath, KeyPath: in.KeyPath, ChainPath: in.ChainPath}
					pctx, cancel := context.WithTimeout(ctx, time.Minute)
					defer cancel()
					if p, err = certs.Pull(pctx, a.pool, s.ID, *src); err != nil {
						return nil, fmt.Errorf("从 %s 读取证书失败：%v", s.Name, err)
					}
					from = s.Name + ":" + in.CertPath
				} else {
					texts, err := a.attachmentPEM(tc, in.CertAttachment, in.ChainAttachment, in.KeyAttachment)
					if err != nil {
						return nil, err
					}
					if p, err = certs.Parse(texts...); err != nil {
						return nil, err
					}
				}

				if p.HasKey && !p.KeyMatches {
					return nil, errors.New("私钥与证书不匹配：请确认给出的私钥是这张证书对应的那一个")
				}
				// 证书库里已经有同一张证书（同一个版本）：不重复导入，只把来源和名称补上
				same := a.certByFingerprint(p.Fingerprint)
				if !p.HasKey && same == nil && existing == nil {
					return nil, errors.New("缺少私钥：证书要和它的私钥一起导入。从服务器导入请填 key_path，从附件导入请填 key_attachment")
				}
				if same != nil && (existing == nil || existing.ID == same.ID) {
					existing = same
				}
				if existing == nil && !in.Separate {
					if dup := a.certBySameDomains(p.Domains); dup != nil {
						return &agent.ToolResult{IsError: true, Content: fmt.Sprintf("证书库里已经有一张域名相同的证书「%s」（到期 %s），要导入的这张到期 %s。"+
							"这通常是同一张证书续期前后的两个版本：加上 certificate=\"%s\" 重新调用，就会把它更新成这个版本并保留部署目标。确实要作为另一张独立的证书保存，加上 separate=true 和一个便于区分的 name。",
							dup.Name, dateOf(dup.NotAfter), dateOf(p.NotAfter), dup.Name)}, nil
					}
				}

				id, title, risk, reason := "", "把证书导入证书库", agent.RiskSafe, "证书和私钥加密保存在这台电脑上，不会发给 AI 模型"
				detail := describeParsed(p) + "\n来源：" + from
				if existing != nil {
					id = existing.ID
					if existing.Fingerprint != p.Fingerprint {
						title, risk = "更新证书库里的证书「"+existing.Name+"」", agent.RiskChange
						detail = fmt.Sprintf("证书库里现在的版本：到期 %s，域名 %s\n换成：\n%s", dateOf(existing.NotAfter), strings.Join(existing.Domains, ", "), detail)
						if n := len(existing.Targets); n > 0 {
							reason = fmt.Sprintf("它有 %d 个部署目标，之后同步时部署的就是新版本", n)
						}
					}
				}
				if !tc.Approve(agent.ApprovalRequest{Tool: "import_certificate", Title: title, Detail: detail, Risk: risk, Reason: reason}) {
					return denied(), nil
				}
				view, err := a.saveParsed(id, in.Name, "", p, src)
				if err != nil {
					return nil, err
				}
				a.emit("certs:changed")

				var b strings.Builder
				switch {
				case existing == nil:
					fmt.Fprintf(&b, "已导入证书库：「%s」（ID %s）\n", view.Name, view.ID)
				case existing.Fingerprint == p.Fingerprint:
					fmt.Fprintf(&b, "证书库里已经有这张证书：「%s」（ID %s），内容相同，没有重复导入。\n", view.Name, view.ID)
				default:
					fmt.Fprintf(&b, "已把证书库里的「%s」（ID %s）更新为新版本。\n", view.Name, view.ID)
				}
				b.WriteString(describeParsed(p) + "\n")
				if view.SourceServerName != "" && view.Source != nil {
					fmt.Fprintf(&b, "来源：%s:%s（以后在那台服务器上续期了，只填 certificate 再调用一次 import_certificate 就能更新）\n", view.SourceServerName, view.Source.CertPath)
				}
				for _, w := range p.Warnings {
					fmt.Fprintf(&b, "注意：%s\n", w)
				}
				stale := 0
				for _, t := range view.Targets {
					if !t.Synced {
						stale++
					}
				}
				switch {
				case len(view.Targets) == 0:
					b.WriteString("它还没有部署目标：需要部署到其他服务器时用 save_deploy_target（deploy=true）。\n")
				case stale > 0:
					fmt.Fprintf(&b, "它有 %d 个部署目标还不是这个版本：用 deploy_certificate 同步过去。\n", stale)
				}
				return &agent.ToolResult{Content: b.String(), Meta: map[string]any{"kind": "cert_import", "id": view.ID, "name": view.Name, "updated": existing != nil}}, nil
			},
		},
		{
			Name: "check_tls",
			Description: "从用户的电脑连接 host:port 做一次 TLS 握手，查看线上实际返回的证书：域名、签发者、到期时间、证书链是否完整、是否受信任，以及它和证书库里的证书是不是同一个版本。" +
				"部署证书后确认是否生效、排查浏览器的证书报错、或者想知道某个网站的证书什么时候到期时调用。只读，不需要批准。",
			Schema: agent.Obj(map[string]any{
				"host": agent.Str("域名或 IP；也可以填受管服务器的名称，表示连接那台服务器"),
				"port": agent.Int("端口，默认 443"),
				"sni":  agent.Str("握手时使用的域名（SNI）。host 填的是 IP 或服务器名称时，用它指定要查看哪个站点的证书"),
			}, "host"),
			Handler: func(_ context.Context, _ *agent.ToolContext, raw json.RawMessage) (*agent.ToolResult, error) {
				var in struct {
					Host string `json:"host"`
					Port int    `json:"port"`
					SNI  string `json:"sni"`
				}
				if err := parseArgs(raw, &in); err != nil {
					return nil, err
				}
				host := strings.TrimSpace(in.Host)
				a.store.Read(func(d *store.Data) {
					for _, s := range d.Servers {
						if s.ID == host || strings.EqualFold(s.Name, host) {
							host = s.Host
							break
						}
					}
				})
				if in.Port <= 0 {
					in.Port = 443
				}
				info, err := a.CheckTLS(host, in.Port, in.SNI)
				if err != nil {
					return &agent.ToolResult{IsError: true, Content: fmt.Sprintf("连接 %s:%d 失败：%v。可能是端口没有开放、服务没有启动，或者这个端口上不是 TLS 服务。", host, in.Port, err)}, nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%s 线上返回的证书（%s，握手 %d ms", info.Address, info.Protocol, info.LatencyMs)
				if info.SNI != "" {
					fmt.Fprintf(&b, "，SNI %s", info.SNI)
				}
				fmt.Fprintf(&b, "）：\n域名：%s\n签发者：%s\n有效期：%s 至 %s（剩 %d 天）\n", strings.Join(info.Domains, ", "), info.Issuer, dateOf(info.NotBefore), dateOf(info.NotAfter), certDays(info.NotAfter))
				if info.ChainCount > 0 {
					fmt.Fprintf(&b, "证书链：带 %d 张中间证书\n", info.ChainCount)
				} else {
					b.WriteString("证书链：没有带中间证书（部分客户端会因此报证书不受信任，应当部署 fullchain）\n")
				}
				if info.Trusted {
					b.WriteString("校验：受信任，域名匹配\n")
				} else {
					fmt.Fprintf(&b, "校验：没有通过——%s\n", info.VerifyError)
				}
				if c := a.certByFingerprint(info.Fingerprint); c != nil {
					fmt.Fprintf(&b, "证书库：和「%s」是同一个版本\n", c.Name)
				} else if c := a.certBySameDomains(info.Domains); c != nil {
					fmt.Fprintf(&b, "证书库：有域名相同的「%s」，但版本不同（证书库里的到期 %s）——线上用的不是证书库里的这个版本\n", c.Name, dateOf(c.NotAfter))
				} else {
					b.WriteString("证书库：没有这张证书\n")
				}
				return &agent.ToolResult{Content: b.String(), Meta: map[string]any{"kind": "tls", "address": info.Address, "trusted": info.Trusted, "days": certDays(info.NotAfter)}}, nil
			},
		},
	}
}

// refreshForAgent 处理 import_certificate 只给了 certificate 的情况：带着新名称就是改名，否则从记录的来源服务器把新版本拉过来。
func (a *App) refreshForAgent(tc *agent.ToolContext, cert *CertView, name string) (*agent.ToolResult, error) {
	if name != "" && name != cert.Name {
		if err := a.UpdateCertMeta(cert.ID, name, cert.Notes); err != nil {
			return nil, err
		}
		a.emit("certs:changed")
		return &agent.ToolResult{Content: fmt.Sprintf("已把证书「%s」改名为「%s」。", cert.Name, name), Meta: map[string]any{"kind": "cert_import", "id": cert.ID, "name": name, "updated": true}}, nil
	}
	var b strings.Builder
	if cert.Source == nil {
		return nil, fmt.Errorf("证书「%s」没有记录来源服务器，不能自动更新：请给出新证书的位置（server + cert_path + key_path），或让用户把证书和私钥文件发过来", cert.Name)
	}
	if !tc.Approve(agent.ApprovalRequest{Tool: "import_certificate", Title: "从来源服务器更新证书「" + cert.Name + "」", Detail: cert.SourceServerName + ":" + cert.Source.CertPath, Risk: agent.RiskSafe,
		Reason: "来源服务器上的证书有变化时，更新证书库里的版本"}) {
		return denied(), nil
	}
	r, err := a.RefreshCert(cert.ID)
	if err != nil {
		return nil, fmt.Errorf("从 %s 读取证书失败：%v", cert.SourceServerName, err)
	}
	a.emit("certs:changed")
	fmt.Fprintf(&b, "%s：%s（现在到期 %s，剩 %d 天）。\n", r.Cert.Name, r.Message, dateOf(r.Cert.NotAfter), r.Cert.Days)
	if r.Changed && len(r.Cert.Targets) > 0 {
		fmt.Fprintf(&b, "它有 %d 个部署目标还是旧版本：用 deploy_certificate 同步过去。\n", len(r.Cert.Targets))
	}
	return &agent.ToolResult{Content: b.String(), Meta: map[string]any{"kind": "cert_import", "id": r.Cert.ID, "name": r.Cert.Name, "updated": r.Changed}}, nil
}
