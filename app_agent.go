package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"sshpilot/internal/agent"
)

type SessionView struct {
	ID           string                  `json:"id"`
	Title        string                  `json:"title"`
	CreatedAt    int64                   `json:"createdAt"`
	UpdatedAt    int64                   `json:"updatedAt"`
	Messages     []agent.Message         `json:"messages"`
	Running      bool                    `json:"running"`
	Pending      []agent.ApprovalRequest `json:"pending"`
	InputTokens  int64                   `json:"inputTokens"`
	OutputTokens int64                   `json:"outputTokens"`
	ApproveAll   bool                    `json:"approveAll"`
	Learned      int                     `json:"learned"`
	ServerID     string                  `json:"serverId"`
}

func (a *App) AgentListSessions() []agent.SessionSummary { return a.agent.ListSessions() }

// AgentNewSession 新建对话。serverID 不为空时，对话绑定到这台服务器（终端里的「AI 运维」）：
// AI 默认只操作它，并且可以读取它的终端里最近的输出。
func (a *App) AgentNewSession(serverID string) (agent.SessionSummary, error) {
	focus := ""
	if serverID != "" {
		var err error
		if focus, err = a.sessionFocus(serverID); err != nil {
			return agent.SessionSummary{}, err
		}
	}
	s, err := a.agent.NewSessionFor(serverID, focus)
	if err != nil {
		return agent.SessionSummary{}, err
	}
	return agent.SessionSummary{ID: s.ID, Title: s.Title, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, ServerID: s.ServerID}, nil
}

func (a *App) AgentGetSession(id string) (SessionView, error) {
	s, err := a.agent.Sessions.Load(id)
	if err != nil {
		return SessionView{}, err
	}
	msgs := make([]agent.Message, len(s.Messages))
	for i, m := range s.Messages {
		m.Blocks = nil // 原生内容块只用于回放，不需要发送给界面
		msgs[i] = m
	}
	return SessionView{ID: s.ID, Title: s.Title, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, Messages: msgs,
		Running: a.agent.IsRunning(id), Pending: a.agent.PendingApprovals(id), InputTokens: s.InputTokens,
		OutputTokens: s.OutputTokens, ApproveAll: s.ApproveAll, Learned: s.Learned, ServerID: s.ServerID}, nil
}

func (a *App) AgentSend(id, text string) error { return a.agent.Send(id, text) }

func (a *App) AgentStop(id string) { a.agent.Stop(id) }

func (a *App) AgentDeleteSession(id string) error { return a.agent.DeleteSession(id) }

func (a *App) AgentRenameSession(id, title string) error { return a.agent.RenameSession(id, title) }

func (a *App) AgentResolveApproval(approvalID string, approved, all bool) error {
	return a.agent.ResolveApproval(approvalID, approved, all)
}

// AgentReview 手动触发“学习复盘”：让 AI 回顾整个会话并更新记忆与技能。
func (a *App) AgentReview(id string) error { return a.agent.Review(id) }

// ---- 记忆、技能与学习日志 ----

func (a *App) GetMemory() []agent.MemoryView {
	return []agent.MemoryView{a.agent.Memory.View("memory"), a.agent.Memory.View("user")}
}

func (a *App) SaveMemory(target string, entries []string) error {
	if err := a.agent.Memory.SetEntries(target, entries); err != nil {
		return err
	}
	a.agent.Learn.Add(agent.LearnEvent{Time: time.Now().UnixMilli(), Source: "user", Kind: target, Action: "edit", Target: target, Summary: "在界面中编辑了记忆"})
	return nil
}

func (a *App) ListSkills() []agent.SkillMeta { return a.agent.Skills.List() }

func (a *App) GetSkill(name string) (*agent.Skill, error) { return a.agent.Skills.Get(name, false) }

func (a *App) SaveSkill(name, content string) error {
	name = strings.TrimSpace(name)
	if err := a.agent.Skills.Save(name, content); err != nil {
		return err
	}
	a.agent.Learn.Add(agent.LearnEvent{Time: time.Now().UnixMilli(), Source: "user", Kind: "skill", Action: "edit", Target: name, Summary: "在界面中编辑了技能"})
	return nil
}

func (a *App) DeleteSkill(name string) error { return a.agent.Skills.Delete(name) }

func (a *App) GetLearningLog(limit int) []agent.LearnEvent {
	if limit <= 0 {
		limit = 100
	}
	return a.agent.Learn.Recent(limit)
}

// TestAI 用当前设置发送一条简单请求，验证模型配置是否可用。
func (a *App) TestAI() (string, error) {
	p, err := agent.NewProvider(a.aiSettings())
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	start := time.Now()
	resp, err := p.Chat(ctx, agent.ChatRequest{
		System:    "你是连通性测试助手。",
		Messages:  []agent.Message{{Role: "user", Content: "请只回复四个字：连接成功"}},
		MaxTokens: 2048,
		Effort:    "low",
	}, agent.StreamHandler{})
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(resp.Message.Content)
	if text == "" {
		return "", errors.New("模型没有返回内容")
	}
	model := resp.Model
	if model == "" {
		model = a.aiSettings().Model
	}
	return model + " · " + time.Since(start).Round(100*time.Millisecond).String() + " · " + clipText(text, 40), nil
}
