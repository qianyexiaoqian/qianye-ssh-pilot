package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (a *Agent) learningTools() []*Tool {
	return []*Tool{
		{
			Name: "memory",
			Description: "读写跨会话的持久记忆，分三处：target=memory 是核心记忆里的环境总体情况与关键约定，target=user 是用户的偏好与对你工作方式的要求，" +
				"这两处每次对话都会完整出现在系统提示里，容量很小；target=longterm 是长期记忆，容量不限，平时不占提示词，提到相关内容时由程序自动召回——" +
				"具体到某台服务器的事实（角色、路径、端口、版本、已知问题及根因、处理过的故障）都放这里，并用 servers 标明是哪些服务器。" +
				"发现值得长期保存的信息、或被用户纠正时调用；不要保存临时状态、原始输出、密码与密钥。" +
				"action=add 新增条目；patch 用 old_text 定位条目并把该片段替换为 content；remove 删除 old_text 所在条目；" +
				"archive 把 memory / user 里 old_text 所在的条目移到长期记忆（核心记忆快满时用它腾出空间，而不是直接删掉）。",
			Schema: obj(map[string]any{
				"target":   strEnum("memory=核心记忆·环境；user=核心记忆·用户画像；longterm=长期记忆", "memory", "user", "longterm"),
				"action":   strEnum("操作类型", "add", "patch", "remove", "archive"),
				"content":  str("add 时为新条目全文；patch 时为替换后的文本"),
				"old_text": str("patch / remove / archive 时用于唯一定位条目的原文片段"),
				"servers":  strArray("这条记忆是关于哪些服务器的（填名称）。只在 target=longterm 或 action=archive 时使用；通用的知识不填"),
			}, "target", "action"),
			Handler: func(ctx context.Context, tc *ToolContext, raw json.RawMessage) (*ToolResult, error) {
				var in struct {
					Target  string   `json:"target"`
					Action  string   `json:"action"`
					Content string   `json:"content"`
					OldText string   `json:"old_text"`
					Servers []string `json:"servers"`
				}
				if err := json.Unmarshal(raw, &in); err != nil {
					return nil, err
				}
				if in.Target == "longterm" || in.Action == "archive" {
					msg, err := a.longMemoryApply(ctx, tc, in.Target, in.Action, in.Content, in.OldText, in.Servers)
					if err != nil {
						return nil, err
					}
					return &ToolResult{Content: msg, Meta: map[string]any{"kind": "memory", "target": "longterm", "action": in.Action, "content": in.Content}}, nil
				}
				msg, err := a.Memory.Apply(in.Target, in.Action, in.Content, in.OldText)
				if err != nil {
					return nil, err
				}
				if !strings.HasPrefix(msg, "该条目已存在") {
					summary := in.Content
					if in.Action == "remove" {
						summary = in.OldText
					}
					a.RecordLearning(tc.SessionID(), LearnEvent{Source: tc.Source(), Kind: in.Target, Action: in.Action, Target: in.Target, Summary: clipRunes(summary, 200)})
				}
				return &ToolResult{Content: msg, Meta: map[string]any{"kind": "memory", "target": in.Target, "action": in.Action, "content": in.Content}}, nil
			},
		},
		{
			Name:        "skills_list",
			Description: "列出技能库中的全部技能（名称与描述）。会话中途新建的技能不会出现在系统提示的索引里，需要时调用本工具查看。",
			Schema:      obj(map[string]any{}),
			Handler: func(_ context.Context, _ *ToolContext, _ json.RawMessage) (*ToolResult, error) {
				list := a.Skills.List()
				if len(list) == 0 {
					return &ToolResult{Content: "技能库为空"}, nil
				}
				var b strings.Builder
				for _, m := range list {
					fmt.Fprintf(&b, "- %s（v%d）：%s\n", m.Name, m.Version, m.Description)
				}
				return &ToolResult{Content: b.String()}, nil
			},
		},
		{
			Name:        "skill_view",
			Description: "读取一个技能的完整内容。处理任务前，如果技能索引中有相关技能，先调用本工具阅读并遵循其中的步骤与注意事项。",
			Schema:      obj(map[string]any{"name": str("技能名称")}, "name"),
			Handler: func(_ context.Context, _ *ToolContext, raw json.RawMessage) (*ToolResult, error) {
				var in struct {
					Name string `json:"name"`
				}
				if err := json.Unmarshal(raw, &in); err != nil {
					return nil, err
				}
				sk, err := a.Skills.Get(strings.TrimSpace(in.Name), true)
				if err != nil {
					return nil, err
				}
				return &ToolResult{Content: sk.Content, Meta: map[string]any{"kind": "skill_view", "name": sk.Name}}, nil
			},
		},
		{
			Name: "skill_manage",
			Description: "管理技能库（程序性记忆）。完成复杂的多步骤任务、踩坑后找到正确做法、或做法被用户纠正后，把可复用的流程写成技能；" +
				"使用技能时发现更好的做法就 patch 更新。技能写“经验而非日志”：适用场景、步骤、注意事项、验证方法。" +
				"action=create 需要 name、description、content（Markdown 正文）；patch 需要 old_string 与 new_string（对 SKILL.md 全文做唯一替换）；edit 用 content 整体替换；delete 删除。",
			Schema: obj(map[string]any{
				"action":      strEnum("操作类型", "create", "patch", "edit", "delete"),
				"name":        str("技能名：小写字母、数字、连字符，例如 nginx-cert-replace"),
				"description": str("一句话说明技能用途（create 必填）"),
				"content":     str("create / edit 时的 Markdown 正文"),
				"old_string":  str("patch 时要替换的原文（必须唯一出现）"),
				"new_string":  str("patch 时的新文本"),
			}, "action", "name"),
			Handler: func(_ context.Context, tc *ToolContext, raw json.RawMessage) (*ToolResult, error) {
				var in struct {
					Action      string `json:"action"`
					Name        string `json:"name"`
					Description string `json:"description"`
					Content     string `json:"content"`
					OldString   string `json:"old_string"`
					NewString   string `json:"new_string"`
				}
				if err := json.Unmarshal(raw, &in); err != nil {
					return nil, err
				}
				name := strings.TrimSpace(in.Name)
				var err error
				var summary string
				switch in.Action {
				case "create":
					err = a.Skills.Create(name, in.Content, in.Description, tc.Source())
					summary = in.Description
				case "patch":
					err = a.Skills.Patch(name, in.OldString, in.NewString)
					summary = "更新：" + clipRunes(in.NewString, 120)
				case "edit":
					err = a.Skills.Edit(name, in.Content, in.Description)
					summary = "重写技能内容"
				case "delete":
					if a.Skills.IsPinned(name) {
						return nil, errors.New("这个技能被用户固定了，不能删除；可以用 patch 改进它")
					}
					err = a.Skills.Delete(name)
					summary = "删除技能"
				default:
					err = errors.New("action 必须是 create / patch / edit / delete")
				}
				if err != nil {
					return nil, err
				}
				a.RecordLearning(tc.SessionID(), LearnEvent{Source: tc.Source(), Kind: "skill", Action: in.Action, Target: name, Summary: summary})
				return &ToolResult{Content: fmt.Sprintf("技能 %s 已%s。", name, map[string]string{"create": "创建", "patch": "更新", "edit": "更新", "delete": "删除"}[in.Action]),
					Meta: map[string]any{"kind": "skill", "action": in.Action, "name": name}}, nil
			},
		},
		{
			Name:        "session_search",
			Description: "按关键词检索以往对话的原始记录（包括当时的命令输出）。先用 recall 按意思找到是哪次对话，需要看当时的细节时再用本工具；多个关键词用空格分隔。",
			Schema:      obj(map[string]any{"query": str("关键词，多个用空格分隔"), "limit": integer("返回条数，默认 8")}, "query"),
			Handler: func(_ context.Context, tc *ToolContext, raw json.RawMessage) (*ToolResult, error) {
				var in struct {
					Query string `json:"query"`
					Limit int    `json:"limit"`
				}
				if err := json.Unmarshal(raw, &in); err != nil {
					return nil, err
				}
				hits := a.Sessions.Search(in.Query, in.Limit, tc.SessionID())
				if len(hits) == 0 {
					return &ToolResult{Content: "没有找到相关的历史对话。"}, nil
				}
				var b strings.Builder
				for _, h := range hits {
					role := map[string]string{"user": "用户", "assistant": "助手", "tool": "工具结果"}[h.Role]
					fmt.Fprintf(&b, "· [%s]《%s》%s：%s\n", time.UnixMilli(h.Time).Format("2006-01-02 15:04"), h.Title, role, h.Snippet)
				}
				return &ToolResult{Content: b.String(), Meta: map[string]any{"kind": "search", "count": len(hits)}}, nil
			},
		},
		a.recallTool(),
	}
}
