package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/pkg/sftp"

	"sshpilot/internal/agent"
	"sshpilot/internal/sshx"
	"sshpilot/internal/store"
)

// AI 替用户操作程序本身的功能：添加和整理服务器、把密码登录换成密钥登录、把服务器上的文件下载到本机、保存快捷命令。
// 这些事以前只能由用户在各个页面里自己点；对不熟悉运维的用户来说，“帮我把这台机器加进来”“把这个文件下载给我”才是自然的说法。

// aiDownloadDir 是 AI 把服务器上的文件下载到本机时保存的位置：「下载」文件夹里的 QianYe SSH Pilot 目录。
// 位置固定，不由模型指定（模型不应该能往本机任意位置写文件）；环境变量 SSHPILOT_DOWNLOAD_DIR 可以改到别处。
func aiDownloadDir() (string, error) {
	dir := strings.TrimSpace(os.Getenv("SSHPILOT_DOWNLOAD_DIR"))
	if dir == "" {
		base := systemDownloadsDir()
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			base = filepath.Join(home, "Downloads")
		}
		dir = filepath.Join(base, "QianYe SSH Pilot")
	}
	return dir, os.MkdirAll(dir, 0o755)
}

// bigDownload 以上的下载先请用户确认（占用磁盘和时间）。
var bigDownload int64 = 200 << 20

// findKey 按 ID 或名称在密钥库里找私钥。
func (a *App) findKey(ref string) (KeyView, error) {
	ref = strings.TrimSpace(ref)
	keys := a.ListKeys()
	for _, k := range keys {
		if k.ID == ref || strings.EqualFold(k.Name, ref) {
			return k, nil
		}
	}
	if len(keys) == 0 {
		return KeyView{}, errors.New("密钥库里还没有私钥")
	}
	var names []string
	for _, k := range keys {
		names = append(names, k.Name)
	}
	return KeyView{}, fmt.Errorf("没有找到私钥 %s。密钥库里的私钥：%s", ref, strings.Join(names, "、"))
}

// serverInputOf 把已保存的服务器还原成保存时的输入，用于只修改其中几项。密码留空表示沿用已保存的。
func serverInputOf(s store.Server) ServerInput {
	in := ServerInput{ID: s.ID, Name: s.Name, Host: s.Host, Port: s.Port, Username: s.Username, AuthType: s.AuthType, KeyID: s.KeyID,
		Group: s.Group, Tags: slices.Clone(s.Tags), JumpServerID: s.JumpServerID, UseSudo: s.UseSudo, Notes: s.Notes}
	if s.Proxy != nil && s.JumpServerID == "" {
		in.Proxy = ProxyInput{Type: s.Proxy.Type, Host: s.Proxy.Host, Port: s.Proxy.Port, Username: s.Proxy.Username, KeepPassword: true}
	}
	return in
}

func authLabel(a *App, in ServerInput) string {
	if in.AuthType == "password" {
		return "密码"
	}
	if k, err := a.findKey(in.KeyID); err == nil {
		return "密钥「" + k.Name + "」"
	}
	return "密钥"
}

func (a *App) manageTools() []*agent.Tool {
	return []*agent.Tool{
		{
			Name: "save_server",
			Description: "把一台服务器添加到程序里，或修改已有服务器的信息（名称、分组、标签、备注、地址、端口、用户、登录方式、sudo、跳板机）。" +
				"用户给出 IP / 域名和登录信息让你“加进来”，或让你整理分组、打标签、改名、写备注时调用，不要让用户自己去「服务器」页填写。\n" +
				"- 修改已有的服务器：server 填它的名称或 ID，只写要改的项，其余保持不变。只改名称、分组、标签、备注不需要批准。\n" +
				"- 新增：至少要有 host；登录方式二选一——password（用户在对话里给出的密码）或 key（密钥库里私钥的名称，用 list_keys 查看）。用户没有给密码、也没说用哪把密钥时先问清楚，不要猜。\n" +
				"- 新增和改动连接信息需要用户批准，保存后会自动测试连接并把结果告诉你。\n" +
				"- 不能删除服务器：需要删除时请用户在「服务器」页卡片右上角的菜单里操作。",
			Schema: agent.Obj(map[string]any{
				"server":      agent.Str("要修改的已有服务器：名称或 ID。新增时不填"),
				"name":        agent.Str("显示名称；新增时不填就用地址"),
				"host":        agent.Str("IP 或域名"),
				"port":        agent.Int("SSH 端口，默认 22"),
				"username":    agent.Str("登录用户，默认 root"),
				"password":    agent.Str("登录密码（改用密码登录）"),
				"key":         agent.Str("密钥库里私钥的名称或 ID（改用密钥登录）"),
				"group":       agent.Str("分组；填空字符串表示移出分组"),
				"tags":        agent.StrArray("标签（整体替换）"),
				"notes":       agent.Str("备注：这台服务器的用途、注意事项"),
				"use_sudo":    map[string]any{"type": "boolean", "description": "非 root 用户执行特权操作时是否通过 sudo 提权"},
				"jump_server": agent.Str("经哪台已添加的服务器中转连接（名称或 ID）；填空字符串表示直连"),
			}),
			Handler: func(_ context.Context, tc *agent.ToolContext, raw json.RawMessage) (*agent.ToolResult, error) {
				var arg struct {
					Server     string    `json:"server"`
					Name       *string   `json:"name"`
					Host       *string   `json:"host"`
					Port       *int      `json:"port"`
					Username   *string   `json:"username"`
					Password   *string   `json:"password"`
					Key        *string   `json:"key"`
					Group      *string   `json:"group"`
					Tags       *[]string `json:"tags"`
					Notes      *string   `json:"notes"`
					UseSudo    *bool     `json:"use_sudo"`
					JumpServer *string   `json:"jump_server"`
				}
				if err := parseArgs(raw, &arg); err != nil {
					return nil, err
				}
				var old *store.Server
				in := ServerInput{Port: 22, Username: "root"}
				if ref := strings.TrimSpace(arg.Server); ref != "" {
					found, err := a.resolveServerRefs([]string{ref})
					if err != nil {
						return nil, err
					}
					if len(found) != 1 {
						return nil, errors.New("一次只能修改一台服务器")
					}
					a.store.Read(func(d *store.Data) {
						if s := d.Server(found[0].ID); s != nil {
							cp := *s
							old = &cp
						}
					})
					if old == nil {
						return nil, errors.New("服务器不存在或已删除")
					}
					in = serverInputOf(*old)
				}
				before := in

				// 逐项套用模型给出的改动；changes 是给用户看的说明，conn 表示动到了连接方式
				var changes []string
				conn := old == nil
				set := func(label string, was, now string, connection bool) {
					if was == now {
						return
					}
					if old == nil {
						changes = append(changes, fmt.Sprintf("%s：%s", label, now))
					} else {
						changes = append(changes, fmt.Sprintf("%s：%s → %s", label, orDash(was), orDash(now)))
					}
					conn = conn || connection
				}
				if arg.Host != nil {
					in.Host = strings.TrimSpace(*arg.Host)
				}
				if arg.Port != nil && *arg.Port > 0 {
					in.Port = *arg.Port
				}
				if arg.Username != nil && strings.TrimSpace(*arg.Username) != "" {
					in.Username = strings.TrimSpace(*arg.Username)
				}
				if arg.Name != nil && strings.TrimSpace(*arg.Name) != "" {
					in.Name = strings.TrimSpace(*arg.Name)
				}
				if in.Host == "" {
					return nil, errors.New("请给出服务器的地址（host）；要修改已有的服务器请填 server")
				}
				if in.Name == "" {
					in.Name = in.Host
				}
				if old == nil {
					var dup string
					a.store.Read(func(d *store.Data) {
						for _, s := range d.Servers {
							if strings.EqualFold(s.Host, in.Host) && s.Port == in.Port && s.Username == in.Username {
								dup = s.Name
							}
						}
					})
					if dup != "" {
						return nil, fmt.Errorf("这台服务器已经添加过了：「%s」（%s@%s:%d）。要修改它，请填 server=\"%s\"", dup, in.Username, in.Host, in.Port, dup)
					}
				}
				set("名称", before.Name, in.Name, false)
				set("地址", fmt.Sprintf("%s@%s:%d", before.Username, before.Host, before.Port), fmt.Sprintf("%s@%s:%d", in.Username, in.Host, in.Port), true)

				password, key := "", ""
				if arg.Password != nil {
					password = *arg.Password
				}
				if arg.Key != nil {
					key = strings.TrimSpace(*arg.Key)
				}
				switch {
				case password != "" && key != "":
					return nil, errors.New("登录方式只能选一种：password 或 key")
				case password != "":
					in.AuthType, in.Password, in.KeyID = "password", password, ""
					changes = append(changes, fmt.Sprintf("登录方式：密码（%s，共 %d 位）", strings.Repeat("•", 8), len([]rune(password))))
					conn = true
				case key != "":
					k, err := a.findKey(key)
					if err != nil {
						return nil, err
					}
					in.AuthType, in.KeyID = "key", k.ID
					set("登录方式", authLabel(a, before), "密钥「"+k.Name+"」", true)
				case old == nil:
					return nil, errors.New("新增服务器需要登录方式：password（用户给出的登录密码）或 key（密钥库里私钥的名称，用 list_keys 查看）。用户都没有提供时，请先问用户")
				}
				if arg.Group != nil {
					in.Group = strings.TrimSpace(*arg.Group)
				}
				set("分组", before.Group, in.Group, false)
				if arg.Tags != nil {
					in.Tags = *arg.Tags
				}
				set("标签", strings.Join(before.Tags, ", "), strings.Join(in.Tags, ", "), false)
				if arg.Notes != nil {
					in.Notes = strings.TrimSpace(*arg.Notes)
				}
				set("备注", before.Notes, in.Notes, false)
				if arg.UseSudo != nil {
					in.UseSudo = *arg.UseSudo
				}
				onOff := map[bool]string{true: "启用", false: "不启用"}
				set("sudo 提权", onOff[before.UseSudo], onOff[in.UseSudo], true)
				if arg.JumpServer != nil {
					in.JumpServerID = ""
					if ref := strings.TrimSpace(*arg.JumpServer); ref != "" && ref != "none" && ref != "直连" {
						jump, err := a.resolveServerRefs([]string{ref})
						if err != nil {
							return nil, err
						}
						in.JumpServerID = jump[0].ID
						in.Proxy = ProxyInput{}
					}
				}
				jumpName := func(id string) string {
					if id == "" {
						return "直连"
					}
					return "经跳板机 " + a.serverName(id)
				}
				if before.JumpServerID != in.JumpServerID {
					set("线路", jumpName(before.JumpServerID), jumpName(in.JumpServerID), true)
				}
				if len(changes) == 0 {
					return &agent.ToolResult{Content: fmt.Sprintf("服务器「%s」已经是这样了，没有需要修改的地方。", in.Name)}, nil
				}

				title, risk, reason := "添加服务器 "+in.Name, agent.RiskChange, "保存后会测试一次连接"
				if old != nil {
					title = "修改服务器 " + old.Name
					if !conn {
						risk, reason = agent.RiskSafe, "只改动程序里记录的信息，不影响服务器"
					}
				}
				if !tc.Approve(agent.ApprovalRequest{Tool: "save_server", Title: title, Detail: strings.Join(changes, "\n"), Risk: risk, Reason: reason}) {
					return denied(), nil
				}
				saved, err := a.SaveServer(in)
				if err != nil {
					return nil, err
				}
				a.emit("servers:changed")
				var b strings.Builder
				if old == nil {
					fmt.Fprintf(&b, "已添加服务器「%s」（ID %s，%s@%s:%d）。\n", saved.Name, saved.ID, saved.Username, saved.Host, saved.Port)
				} else {
					fmt.Fprintf(&b, "已修改服务器「%s」：\n%s\n", saved.Name, strings.Join(changes, "\n"))
				}
				meta := map[string]any{"kind": "server_save", "id": saved.ID, "name": saved.Name, "created": old == nil}
				if conn {
					res := a.TestServer(saved.ID)
					meta["ok"] = res.OK
					if res.OK {
						fmt.Fprintf(&b, "连接测试：%s", res.Message)
						if res.OS != "" {
							fmt.Fprintf(&b, "，系统 %s", res.OS)
						}
						b.WriteString("。\n")
					} else {
						fmt.Fprintf(&b, "连接测试没有通过：%s\n服务器信息已经保存。请核对地址、端口、用户名和密码；改正后再调用 save_server（server=\"%s\"）修改。\n", res.Message, saved.Name)
					}
					a.emit("servers:changed")
				}
				return &agent.ToolResult{Content: b.String(), Meta: meta}, nil
			},
		},
		{
			Name:        "list_keys",
			Description: "列出密钥库里的 SSH 私钥（名称、类型、指纹、哪些服务器在用它登录）。添加服务器或切换登录密钥前，需要知道有哪些私钥可用时调用。只读，不会返回私钥内容。",
			Schema:      agent.Obj(map[string]any{}),
			Handler: func(_ context.Context, _ *agent.ToolContext, _ json.RawMessage) (*agent.ToolResult, error) {
				keys := a.ListKeys()
				if len(keys) == 0 {
					return &agent.ToolResult{Content: "密钥库里还没有私钥。需要密钥登录时，用 setup_key_login 会自动生成一把。"}, nil
				}
				var b strings.Builder
				for _, k := range keys {
					var used []string
					for _, id := range k.UsedBy {
						used = append(used, a.serverName(id))
					}
					fmt.Fprintf(&b, "- %s｜ID %s｜%s｜%s｜用于登录：%s\n", k.Name, k.ID, k.Type, k.Fingerprint, orDash(strings.Join(used, "、")))
				}
				return &agent.ToolResult{Content: b.String()}, nil
			},
		},
		{
			Name: "setup_key_login",
			Description: "把服务器从密码登录换成密钥登录（更安全，也不怕密码泄露）：生成一把新密钥（或用密钥库里已有的），把公钥加到服务器的 authorized_keys，" +
				"用新密钥试登录成功后才切换；验证失败不改动任何设置。私钥只保存在用户的电脑上，不经过你。需要用户批准。\n" +
				"这个工具不会关闭服务器的密码登录。用户要求彻底禁用密码登录时，等切换成功后，再用 run_command 修改 sshd_config（PasswordAuthentication no）、sshd -t 检查并重载 sshd。",
			Schema: agent.Obj(map[string]any{
				"servers": agent.StrArray("服务器名称或 ID 列表"),
				"key":     agent.Str("使用密钥库里已有的私钥（名称或 ID）；不填就新生成一把 ed25519 密钥"),
			}, "servers"),
			Handler: func(_ context.Context, tc *agent.ToolContext, raw json.RawMessage) (*agent.ToolResult, error) {
				var in struct {
					Servers []string `json:"servers"`
					Key     string   `json:"key"`
				}
				if err := parseArgs(raw, &in); err != nil {
					return nil, err
				}
				servers, err := a.targets(tc, in.Servers)
				if err != nil {
					return nil, err
				}
				keyID, keyDesc := "", "新生成一把 ed25519 密钥"
				if strings.TrimSpace(in.Key) != "" {
					k, err := a.findKey(in.Key)
					if err != nil {
						return nil, err
					}
					keyID, keyDesc = k.ID, "密钥库里的「"+k.Name+"」"
				}
				detail := "使用的密钥：" + keyDesc + "\n把公钥加到服务器的 authorized_keys → 用新密钥试登录 → 成功后改用密钥登录\n试登录失败时保持原来的登录方式；不会关闭服务器的密码登录"
				if !tc.Approve(agent.ApprovalRequest{Tool: "setup_key_login", Title: fmt.Sprintf("把 %d 台服务器改用密钥登录", len(servers)), Detail: detail, Servers: names(servers), Risk: agent.RiskChange,
					Reason: "私钥只保存在这台电脑上"}) {
					return denied(), nil
				}
				generated := false
				if keyID == "" {
					name := "ai-key-" + time.Now().Format("20060102-1504")
					if len(servers) == 1 {
						name = servers[0].Name + "-key"
					}
					k, err := a.GenerateKey(GenerateKeyRequest{Name: name, Kind: "ed25519"})
					if err != nil {
						return nil, fmt.Errorf("生成密钥失败：%v", err)
					}
					keyID, keyDesc, generated = k.ID, "新生成的「"+k.Name+"」", true
				}
				results := a.RotateKey(keyID, ids(servers), false)
				ok := 0
				var b strings.Builder
				for _, r := range results {
					mark := "失败"
					if r.OK {
						mark = "成功"
						ok++
					}
					fmt.Fprintf(&b, "- %s：%s，%s\n", r.ServerName, mark, clipText(r.Message, 400))
				}
				if ok == 0 && generated {
					// 一台都没成功：新生成的密钥没有任何用处，不留在密钥库里
					_ = a.DeleteKey(keyID)
				}
				a.emit("servers:changed")
				head := fmt.Sprintf("使用的密钥：%s。%d 台成功，%d 台失败。\n", keyDesc, ok, len(results)-ok)
				if ok > 0 {
					head += "已切换的服务器以后用密钥登录；服务器上的密码登录仍然开着。\n"
				}
				return &agent.ToolResult{Content: head + b.String(), IsError: ok == 0, Meta: map[string]any{"kind": "key_login", "results": results}}, nil
			},
		},
		{
			Name: "download_file",
			Description: "把服务器上的文件或文件夹下载到用户的电脑（SFTP），保存在「下载」文件夹里的 QianYe SSH Pilot 目录下，不会覆盖已有的文件。" +
				"用户说“下载下来”“导出给我”“发给我”“备份到本地”时调用，完成后把保存的位置告诉用户。" +
				"只是想看内容用 read_file，不要下载；很大的目录先在服务器上用 tar 打包成一个文件再下载。" +
				"下载用的是登录用户的权限：读不到的文件，先用 run_command 以 sudo 复制到 /tmp 并改成登录用户可读，再下载。",
			Schema: agent.Obj(map[string]any{
				"server": agent.Str("服务器名称或 ID"),
				"paths":  agent.StrArray("要下载的文件或文件夹的绝对路径"),
			}, "paths"),
			Handler: func(ctx context.Context, tc *agent.ToolContext, raw json.RawMessage) (*agent.ToolResult, error) {
				var in struct {
					Server string   `json:"server"`
					Paths  []string `json:"paths"`
					Path   string   `json:"path"`
				}
				if err := parseArgs(raw, &in); err != nil {
					return nil, err
				}
				var remotes []string
				for _, p := range append(in.Paths, in.Path) {
					if p = strings.TrimSpace(p); p == "" {
						continue
					}
					if !strings.HasPrefix(p, "/") {
						return nil, fmt.Errorf("路径必须是绝对路径：%s", p)
					}
					remotes = append(remotes, path.Clean(p))
				}
				if len(remotes) == 0 {
					return nil, errors.New("请给出要下载的文件路径（paths）")
				}
				servers, err := a.targets(tc, []string{in.Server})
				if err != nil {
					return nil, err
				}
				if len(servers) != 1 {
					return nil, errors.New("一次只能从一台服务器下载")
				}
				s := servers[0]
				dir, err := aiDownloadDir()
				if err != nil {
					return nil, fmt.Errorf("无法使用本机的下载文件夹：%v", err)
				}
				readHint := func(err error) error {
					if os.IsPermission(err) || strings.Contains(strings.ToLower(err.Error()), "permission denied") {
						return fmt.Errorf("%v（登录用户没有读取权限：先用 run_command 以 sudo 复制到 /tmp 并改成登录用户可读，再下载）", err)
					}
					return err
				}

				// 先看清楚要下载什么、有多大，再动手
				type top struct {
					Name  string `json:"name"`
					Path  string `json:"path"`
					Size  int64  `json:"size"`
					Files int    `json:"files"`
					IsDir bool   `json:"isDir"`
				}
				var items []sshx.TransferItem
				var tops []*top
				skipped := 0
				taken := map[string]bool{}
				err = a.pool.WithSFTP(ctx, s.ID, func(cl *sftp.Client) error {
					var err error
					items, skipped, err = sshx.PlanDownload(cl, remotes, func(remote string) string {
						local := uniqueLocal(dir, sshx.SafeLocalName(path.Base(remote)), taken)
						tops = append(tops, &top{Name: filepath.Base(local), Path: local})
						return local
					})
					return err
				})
				if err != nil {
					return nil, readHint(err)
				}
				var total int64
				files := 0
				for _, it := range items {
					// 每个文件算到它所属的那个顶层项目上
					for _, t := range tops {
						if it.Dst == t.Path || strings.HasPrefix(it.Dst, t.Path+string(filepath.Separator)) {
							if it.IsDir {
								t.IsDir = t.IsDir || it.Dst == t.Path
							} else {
								t.Size += it.Size
								t.Files++
							}
							break
						}
					}
					if !it.IsDir {
						total += it.Size
						files++
					}
				}
				risk, reason := agent.RiskSafe, "保存到这台电脑，不会发给 AI 模型"
				if total > bigDownload {
					risk, reason = agent.RiskChange, "文件比较大，下载需要一些时间和磁盘空间"
				}
				detail := fmt.Sprintf("%s\n保存到：%s\n共 %d 个文件，%s", strings.Join(remotes, "\n"), dir, files, humanSize(total))
				if !tc.Approve(agent.ApprovalRequest{Tool: "download_file", Title: "下载到这台电脑", Detail: detail, Servers: []string{s.Name}, Risk: risk, Reason: reason}) {
					return denied(), nil
				}
				start := time.Now()
				done := 0
				err = a.pool.WithSFTP(ctx, s.ID, func(cl *sftp.Client) error {
					for _, it := range items {
						if it.IsDir {
							if err := os.MkdirAll(it.Dst, 0o755); err != nil {
								return err
							}
							continue
						}
						if err := os.MkdirAll(filepath.Dir(it.Dst), 0o755); err != nil {
							return err
						}
						if err := sshx.DownloadFile(ctx, cl, it.Src, it.Dst, nil); err != nil {
							return fmt.Errorf("%s：%w", it.Src, err)
						}
						done++
					}
					return nil
				})
				var b strings.Builder
				if err != nil {
					fmt.Fprintf(&b, "下载中断：%v\n已经下载了 %d / %d 个文件，保存在 %s。\n", readHint(err), done, files, dir)
					return &agent.ToolResult{Content: b.String(), IsError: true, Meta: map[string]any{"kind": "download", "dir": dir, "server": s.Name, "files": done, "size": 0, "items": []*top{}}}, nil
				}
				fmt.Fprintf(&b, "已从 %s 下载到用户的电脑（%d 个文件，%s，用时 %.1fs）：\n", s.Name, files, humanSize(total), time.Since(start).Seconds())
				for _, t := range tops {
					if t.IsDir {
						fmt.Fprintf(&b, "- %s（文件夹，%d 个文件，%s）\n", t.Path, t.Files, humanSize(t.Size))
					} else {
						fmt.Fprintf(&b, "- %s（%s）\n", t.Path, humanSize(t.Size))
					}
				}
				if skipped > 0 {
					fmt.Fprintf(&b, "跳过了 %d 个特殊文件（设备、套接字、指向目录的链接）。\n", skipped)
				}
				return &agent.ToolResult{Content: b.String(), Meta: map[string]any{"kind": "download", "dir": dir, "server": s.Name, "files": files, "size": total, "items": tops}}, nil
			},
		},
		{
			Name: "save_quick_command",
			Description: "把一条常用命令保存到终端旁边的「快捷命令」面板，用户以后点一下就能把它填进终端。用户说“加到快捷命令”“以后想一键执行”时调用。需要用户批准。" +
				"需要带参数、要在多台服务器上批量执行的，用 save_script 存到脚本库更合适。",
			Schema: agent.Obj(map[string]any{
				"name":        agent.Str("显示名称，几个字说明用途"),
				"command":     agent.Str("命令内容"),
				"group":       agent.Str("分组，例如 系统 / Docker / Nginx（可选）"),
				"description": agent.Str("补充说明（可选）"),
				"auto_enter":  map[string]any{"type": "boolean", "description": "点击后是否直接回车执行；默认只填进终端，由用户自己回车"},
			}, "name", "command"),
			Handler: func(_ context.Context, tc *agent.ToolContext, raw json.RawMessage) (*agent.ToolResult, error) {
				var in struct {
					Name        string `json:"name"`
					Command     string `json:"command"`
					Group       string `json:"group"`
					Description string `json:"description"`
					AutoEnter   bool   `json:"auto_enter"`
				}
				if err := parseArgs(raw, &in); err != nil {
					return nil, err
				}
				if strings.TrimSpace(in.Command) == "" {
					return nil, errors.New("command 不能为空")
				}
				risk, _ := agent.ClassifyCommand(in.Command)
				reason := "点击后填进终端，由你自己回车执行"
				if in.AutoEnter {
					reason = "点击后直接执行"
				}
				if !tc.Approve(agent.ApprovalRequest{Tool: "save_quick_command", Title: "保存快捷命令「" + strings.TrimSpace(in.Name) + "」", Detail: in.Command, Risk: agent.RiskChange, Reason: reason}) {
					return denied(), nil
				}
				// 会改动系统的命令如果设成了直接执行，点击时先让用户确认一次
				q, err := a.SaveQuickCommand(store.QuickCommand{Name: in.Name, Command: in.Command, Group: in.Group, Description: in.Description,
					AutoEnter: in.AutoEnter, Confirm: in.AutoEnter && risk != agent.RiskSafe})
				if err != nil {
					return nil, err
				}
				a.emit("quick:changed")
				return &agent.ToolResult{Content: fmt.Sprintf("已保存快捷命令「%s」。用户在终端页点右上角的「快捷命令」就能看到它。", q.Name), Meta: map[string]any{"kind": "quick", "id": q.ID, "name": q.Name}}, nil
			},
		},
	}
}
