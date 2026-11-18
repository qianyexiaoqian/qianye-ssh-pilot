package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pkg/sftp"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"sshpilot/internal/agent"
	"sshpilot/internal/sshx"
)

// ---- 随消息发给 AI 的图片和文件（界面一侧） ----

// AttachResult 是一次添加附件的结果：成功的和没能添加的（每个一句原因）。
type AttachResult struct {
	Added  []agent.Attachment `json:"added"`
	Errors []string           `json:"errors"`
}

func (a *App) attachPaths(paths []string) AttachResult {
	res := AttachResult{Added: []agent.Attachment{}, Errors: []string{}}
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		att, err := a.agent.Attach.AddPath(p)
		if err != nil {
			res.Errors = append(res.Errors, err.Error())
			continue
		}
		res.Added = append(res.Added, att)
	}
	return res
}

// AgentPickFiles 打开系统的文件选择框，把选中的图片或文件添加为附件。
func (a *App) AgentPickFiles() (AttachResult, error) {
	if err := a.guard(); err != nil {
		return AttachResult{}, err
	}
	paths, err := runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择要发给 AI 的图片或文件"})
	if err != nil {
		return AttachResult{}, err
	}
	return a.attachPaths(paths), nil
}

// AgentAttachPaths 把拖进窗口的文件添加为附件。
func (a *App) AgentAttachPaths(paths []string) (AttachResult, error) {
	if err := a.guard(); err != nil {
		return AttachResult{}, err
	}
	return a.attachPaths(paths), nil
}

// AgentAttachData 把粘贴进来的内容（截图、复制的图片）添加为附件；data 是 base64。
func (a *App) AgentAttachData(name, data string) (agent.Attachment, error) {
	if err := a.guard(); err != nil {
		return agent.Attachment{}, err
	}
	if len(data) > base64.StdEncoding.EncodedLen(agent.MaxPastedBytes)+16 {
		return agent.Attachment{}, fmt.Errorf("粘贴的内容超过 %d MB：请把文件拖进来，或点回形针按钮选择", agent.MaxPastedBytes>>20)
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return agent.Attachment{}, errors.New("粘贴的内容无法读取")
	}
	return a.agent.Attach.AddData(name, raw)
}

// AgentDiscardAttachment 丢弃一个还没有发出去的附件。
func (a *App) AgentDiscardAttachment(id string) { a.agent.Attach.Discard(id) }

// assetHandler 处理界面里不属于打包资源的请求：对话里的图片 /attachment/<会话 ID 或 draft>/<附件 ID>。
// 和对话记录一样不受界面锁定的限制（锁定时界面被锁屏盖住；已经显示的对话在解锁后不应该出现裂图）。
func (a *App) assetHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /attachment/{scope}/{id}", func(w http.ResponseWriter, r *http.Request) {
		if a.agent == nil {
			http.NotFound(w, r)
			return
		}
		scope, id := r.PathValue("scope"), r.PathValue("id")
		var att agent.Attachment
		found := false
		if scope == "draft" {
			scope = ""
			att, found = a.agent.Attach.Draft(id)
		} else if sess, err := a.agent.Sessions.Load(scope); err == nil {
			for _, m := range sess.Messages {
				for _, x := range m.Attachments {
					if x.ID == id {
						att, found = x, true
					}
				}
			}
		}
		if !found || att.Kind != agent.AttachImage {
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(a.agent.Attach.Stored(scope, att))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", att.Mime)
		// 附件的 ID 不会重复使用，内容也不会变
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		_, _ = w.Write(data)
	})
	return mux
}

// ---- AI 使用附件的工具 ----

func (a *App) attachTools() []*agent.Tool {
	return []*agent.Tool{
		{
			Name: "read_attachment",
			Description: "阅读用户随消息发来的文本文件（配置、日志、脚本、代码等）。文件在用户的电脑上，不在服务器上。较短的文件内容已经直接附在用户消息里，不需要再读；" +
				"较长的文件用 start_line / max_lines 分段阅读。二进制文件（压缩包、安装包、可执行文件）不能阅读，要处理它们请用 upload_attachment 传到服务器上。",
			Schema: agent.Obj(map[string]any{
				"attachment": agent.Str("附件的 ID（用户消息里 <attachments> 列出的），也可以填文件名"),
				"start_line": agent.Int("从第几行开始，默认 1"),
				"max_lines":  agent.Int("最多读多少行，默认 400"),
			}, "attachment"),
			Handler: func(ctx context.Context, tc *agent.ToolContext, raw json.RawMessage) (*agent.ToolResult, error) {
				var in struct {
					Attachment string `json:"attachment"`
					StartLine  int    `json:"start_line"`
					MaxLines   int    `json:"max_lines"`
				}
				if err := parseArgs(raw, &in); err != nil {
					return nil, err
				}
				att, err := agent.FindAttachment(tc.Session, in.Attachment)
				if err != nil {
					return nil, err
				}
				if att.Kind == agent.AttachImage {
					return nil, fmt.Errorf("「%s」是图片，已经随消息提供，不需要读取", att.Name)
				}
				if !att.Text {
					return nil, fmt.Errorf("「%s」是二进制文件，不能直接阅读。可以用 upload_attachment 把它传到服务器上再处理", att.Name)
				}
				local, err := a.agent.Attach.Local(tc.SessionID(), att)
				if err != nil {
					return nil, err
				}
				text, truncated, err := agent.ReadText(local)
				if err != nil {
					return nil, fmt.Errorf("读取「%s」失败：%v", att.Name, err)
				}
				if agent.IsPrivateKeyText(text) {
					return nil, fmt.Errorf("「%s」%s", att.Name, agent.PrivateKeyHint)
				}
				lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
				start := max(in.StartLine, 1)
				if start > len(lines) {
					return nil, fmt.Errorf("「%s」一共只有 %d 行", att.Name, len(lines))
				}
				count := in.MaxLines
				if count <= 0 {
					count = 400
				}
				end := min(start-1+count, len(lines))
				// 工具结果有长度上限：太长时少给几行，让模型接着读，而不是从中间截断
				body := strings.Join(lines[start-1:end], "\n")
				for utf8.RuneCountInString(body) > 12000 && end > start {
					end = start - 1 + max((end-start+1)*2/3, 1)
					body = strings.Join(lines[start-1:end], "\n")
				}
				head := fmt.Sprintf("%s（第 %d–%d 行，共 %d 行", att.Name, start, end, len(lines))
				if truncated {
					head += "；文件很大，只读取了开头的一部分，完整内容请传到服务器上用 grep / tail 查看"
				}
				if end < len(lines) {
					head += fmt.Sprintf("；后面还有，接着读请把 start_line 设为 %d", end+1)
				}
				return &agent.ToolResult{Content: head + "）\n" + clipText(body, 14000),
					Meta: map[string]any{"kind": "attachment", "name": att.Name, "lines": end - start + 1}}, nil
			},
		},
		{
			Name: "upload_attachment",
			Description: "把用户随消息发来的文件或图片从用户的电脑上传到服务器（SFTP）。用于部署安装包、网站压缩包、配置文件、证书等，需要用户批准。" +
				"上传使用登录用户的权限：目标目录没有写入权限时，先传到 /tmp，再用 run_command 以 sudo 移动到最终位置。传上去之后的解压、安装、设置权限等用 run_command 完成。",
			Schema: agent.Obj(map[string]any{
				"attachment": agent.Str("附件的 ID（用户消息里 <attachments> 列出的），也可以填文件名"),
				"servers":    agent.StrArray("服务器名称或 ID 列表，可以同时传到多台"),
				"path":       agent.Str("服务器上的目标位置（绝对路径）。以 / 结尾或是已有的目录时，文件放进这个目录并保留原名；否则作为完整的文件路径。已有的同名文件会被覆盖"),
				"mode":       agent.Str("上传后设置的权限，例如 755；不填时保留已有文件的权限，新文件使用默认权限"),
			}, "attachment", "servers", "path"),
			Handler: func(ctx context.Context, tc *agent.ToolContext, raw json.RawMessage) (*agent.ToolResult, error) {
				var in struct {
					Attachment string   `json:"attachment"`
					Servers    []string `json:"servers"`
					Path       string   `json:"path"`
					Mode       string   `json:"mode"`
				}
				if err := parseArgs(raw, &in); err != nil {
					return nil, err
				}
				att, err := agent.FindAttachment(tc.Session, in.Attachment)
				if err != nil {
					return nil, err
				}
				in.Path = strings.TrimSpace(in.Path)
				if !strings.HasPrefix(in.Path, "/") {
					return nil, errors.New("path 必须是绝对路径")
				}
				var mode os.FileMode
				if strings.TrimSpace(in.Mode) != "" {
					if mode, err = sshx.ParsePerm(in.Mode); err != nil {
						return nil, err
					}
				}
				servers, err := a.targets(tc, in.Servers)
				if err != nil {
					return nil, err
				}
				local, err := a.agent.Attach.Local(tc.SessionID(), att)
				if err != nil {
					return nil, err
				}
				info, err := os.Stat(local)
				if err != nil {
					return nil, fmt.Errorf("读取「%s」失败：%v", att.Name, err)
				}
				risk := agent.RiskChange
				if criticalWriteRe.MatchString(in.Path) {
					risk = agent.RiskDanger
				}
				detail := fmt.Sprintf("本地文件：%s（%s）\n上传到：%s", local, humanSize(info.Size()), in.Path)
				if mode != 0 {
					detail += "\n权限：" + sshx.OctalPerm(mode)
				}
				if !tc.Approve(agent.ApprovalRequest{Tool: "upload_attachment", Title: "上传文件 " + att.Name, Detail: detail, Servers: names(servers), Risk: risk, Reason: "已有的同名文件会被覆盖"}) {
					return denied(), nil
				}
				type result struct {
					Server string `json:"server"`
					Path   string `json:"path"`
					Error  string `json:"error,omitempty"`
				}
				results := make([]result, len(servers))
				pos := map[string]int{}
				for i, s := range servers {
					pos[s.ID] = i
				}
				start := time.Now()
				a.parallel(ids(servers), func(id string) {
					i := pos[id]
					results[i].Server = servers[i].Name
					err := a.pool.WithSFTP(ctx, id, func(cl *sftp.Client) error {
						remote := in.Path
						if st, err := cl.Stat(remote); strings.HasSuffix(remote, "/") || (err == nil && st.IsDir()) {
							remote = path.Join(remote, path.Base(strings.ReplaceAll(att.Name, "\\", "/")))
						}
						results[i].Path = remote
						if err := cl.MkdirAll(path.Dir(remote)); err != nil {
							return err
						}
						if err := sshx.UploadFile(ctx, cl, local, remote, nil); err != nil {
							return err
						}
						if mode != 0 {
							return cl.Chmod(remote, mode)
						}
						return nil
					})
					if err != nil {
						results[i].Error = err.Error()
						if os.IsPermission(err) || strings.Contains(strings.ToLower(err.Error()), "permission denied") {
							results[i].Error += "（登录用户没有写入权限：可以先传到 /tmp，再用 run_command 以 sudo 移动过去）"
						}
					}
				})
				var b strings.Builder
				failed := 0
				for _, r := range results {
					if r.Error != "" {
						failed++
						fmt.Fprintf(&b, "- %s：失败，%s\n", r.Server, r.Error)
					} else {
						fmt.Fprintf(&b, "- %s：已上传到 %s\n", r.Server, r.Path)
					}
				}
				head := fmt.Sprintf("%s（%s），用时 %.1fs\n", att.Name, humanSize(info.Size()), time.Since(start).Seconds())
				return &agent.ToolResult{Content: head + b.String(), IsError: failed == len(results),
					Meta: map[string]any{"kind": "upload", "name": att.Name, "size": info.Size(), "results": results}}, nil
			},
		},
	}
}
