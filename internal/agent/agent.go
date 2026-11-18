package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"sshpilot/internal/store"
)

type Config struct {
	DataDir     string
	Settings    func() store.AISettings
	EnvSnapshot func() string
	Emit        func(name string, data ...any)
	Tools       []*Tool

	// ReadOnly 为 true 时不写入任何学习数据（文件管理独立窗口的进程只读取主窗口的数据）。
	ReadOnly bool
	// Embed 返回长期记忆使用的向量模型设置；为 nil 时只使用内置的关键词匹配。
	Embed func() store.EmbedSettings
	// Servers 返回全部服务器（ID → 名称），用来给长期记忆标注服务器、在文本里识别服务器名。
	Servers func() map[string]string
	// ResolveServer 把模型给出的服务器名称 / ID / 地址解析成服务器 ID。
	ResolveServer func(ref string) (id string, ok bool)
}

type approvalDecision struct {
	approved bool
	all      bool
}

type pendingApproval struct {
	req ApprovalRequest
	ch  chan approvalDecision
}

// Agent 管理会话、记忆、技能，并驱动“模型 ↔ 工具”循环。
type Agent struct {
	cfg      Config
	Memory   *MemoryStore
	Long     *LongMemory
	Skills   *SkillStore
	Sessions *SessionStore
	Attach   *AttachStore
	Learn    *LearnLog
	tools    []*Tool

	mu        sync.Mutex
	running   map[string]context.CancelFunc
	approvals map[string]*pendingApproval
	reviewing map[string]bool
	sessLocks map[string]*sync.Mutex
}

func New(cfg Config) *Agent {
	a := &Agent{
		cfg:       cfg,
		Memory:    NewMemoryStore(filepath.Join(cfg.DataDir, "memory")),
		Long:      NewLongMemory(filepath.Join(cfg.DataDir, "memory")),
		Skills:    NewSkillStore(filepath.Join(cfg.DataDir, "skills")),
		Sessions:  NewSessionStore(filepath.Join(cfg.DataDir, "sessions")),
		Attach:    NewAttachStore(filepath.Join(cfg.DataDir, "attachments"), cfg.ReadOnly),
		Learn:     NewLearnLog(filepath.Join(cfg.DataDir, "learning.jsonl")),
		running:   map[string]context.CancelFunc{},
		approvals: map[string]*pendingApproval{},
		reviewing: map[string]bool{},
		sessLocks: map[string]*sync.Mutex{},
	}
	a.Long.ReadOnly = cfg.ReadOnly
	if !cfg.ReadOnly {
		a.Skills.SeedBuiltins()
	}
	a.Long.Skills = a.Skills.List
	a.Long.Notify = func(st IndexStatus) {
		if cfg.Emit != nil {
			cfg.Emit("memory:index", st)
		}
	}
	a.memIndex()
	a.tools = append(a.learningTools(), cfg.Tools...)
	return a
}

// Close 在程序退出前保存还没有写入磁盘的内容。
func (a *Agent) Close() {
	a.StopAll()
	a.Long.Close()
}

func (a *Agent) emit(e Event) {
	if a.cfg.Emit != nil {
		a.cfg.Emit("agent:event", e)
	}
}

func (a *Agent) sessionLock(id string) *sync.Mutex {
	a.mu.Lock()
	defer a.mu.Unlock()
	l := a.sessLocks[id]
	if l == nil {
		l = &sync.Mutex{}
		a.sessLocks[id] = l
	}
	return l
}

func (a *Agent) buildSystemPrompt() string {
	env := "（无）"
	if a.cfg.EnvSnapshot != nil {
		env = a.cfg.EnvSnapshot()
	}
	return BuildSystemPrompt(env, a.Memory.Snapshot(), a.Skills.Index(), time.Now())
}

func (a *Agent) NewSession() (*Session, error) { return a.NewSessionFor("", "") }

// NewSessionFor 创建绑定到一台服务器的会话；focus 是追加到系统提示词里的说明（当前服务器是谁、默认操作它）。
func (a *Agent) NewSessionFor(serverID, focus string) (*Session, error) {
	now := nowMs()
	s := &Session{ID: newID(), Title: "新对话", CreatedAt: now, UpdatedAt: now, Messages: []Message{}, ServerID: serverID}
	s.SystemPrompt = a.buildSystemPrompt()
	if focus = strings.TrimSpace(focus); focus != "" {
		s.SystemPrompt += "\n\n" + focus
	}
	return s, a.Sessions.Save(s)
}

func (a *Agent) IsRunning(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.running[id]
	return ok
}

func (a *Agent) ListSessions() []SessionSummary {
	list := a.Sessions.List()
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range list {
		_, list[i].Running = a.running[list[i].ID]
	}
	return list
}

func (a *Agent) DeleteSession(id string) error {
	a.Stop(id)
	a.Long.RemoveSession(id)
	a.Attach.RemoveSession(id)
	return a.Sessions.Delete(id)
}

func (a *Agent) RenameSession(id, title string) error {
	l := a.sessionLock(id)
	l.Lock()
	defer l.Unlock()
	s, err := a.Sessions.Load(id)
	if err != nil {
		return err
	}
	s.Title = strings.TrimSpace(title)
	return a.Sessions.Save(s)
}

// Send 追加一条用户消息并在后台运行一轮对话。attachIDs 是随消息发送的附件（先用 Attach 添加）。
func (a *Agent) Send(sessionID, text string, attachIDs ...string) error {
	text = strings.TrimSpace(text)
	if text == "" && len(attachIDs) == 0 {
		return errors.New("消息不能为空")
	}
	cfg := a.cfg.Settings()
	provider, err := providerFactory(cfg)
	if err != nil {
		return err
	}
	a.mu.Lock()
	if _, ok := a.running[sessionID]; ok {
		a.mu.Unlock()
		return errors.New("该会话正在处理上一条消息")
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.running[sessionID] = cancel
	a.mu.Unlock()
	// 附件从这里开始归这个会话所有；失败时消息没有发出，附件留在输入框里
	atts, err := a.Attach.take(sessionID, attachIDs)
	if err != nil {
		a.mu.Lock()
		delete(a.running, sessionID)
		a.mu.Unlock()
		cancel()
		return err
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				a.emit(Event{SessionID: sessionID, Type: "error", Error: fmt.Sprintf("内部错误: %v", r)})
			}
			a.mu.Lock()
			delete(a.running, sessionID)
			a.mu.Unlock()
			cancel()
			a.emit(Event{SessionID: sessionID, Type: "done"})
		}()
		a.runTurn(ctx, sessionID, text, atts, provider, cfg)
	}()
	return nil
}

// Stop 中止正在进行的对话（同时拒绝其等待中的审批）。
func (a *Agent) Stop(sessionID string) {
	a.mu.Lock()
	cancel := a.running[sessionID]
	for id, p := range a.approvals {
		if p.req.SessionID == sessionID {
			select {
			case p.ch <- approvalDecision{approved: false}:
			default:
			}
			delete(a.approvals, id)
		}
	}
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *Agent) StopAll() {
	a.mu.Lock()
	ids := make([]string, 0, len(a.running))
	for id := range a.running {
		ids = append(ids, id)
	}
	a.mu.Unlock()
	for _, id := range ids {
		a.Stop(id)
	}
}

func (a *Agent) tool(name string) *Tool {
	for _, t := range a.tools {
		if t.Name == name {
			return t
		}
	}
	return nil
}

func titleFrom(text string, atts []Attachment) string {
	line := strings.TrimSpace(strings.SplitN(text, "\n", 2)[0])
	if line == "" && len(atts) > 0 {
		// 只发了附件：文件用文件名，图片（多半是截图）的名字没有意义
		line = atts[0].Name
		if atts[0].Kind == AttachImage {
			line = "图片对话"
		}
	}
	return clip(line, 24)
}

// chat 调用模型。带着图片的请求被接口拒绝时（多半是模型不支持图片输入），去掉图片再试一次；
// 再试成功就返回 dropped=true，由调用方告诉用户。
func chat(ctx context.Context, provider Provider, req ChatRequest, h StreamHandler) (resp *ChatResponse, dropped bool, err error) {
	resp, err = provider.Chat(ctx, req, h)
	if err == nil || req.NoImages || ctx.Err() != nil || !rejectsImages(err) || !hasImageData(req.Messages) {
		return resp, false, err
	}
	if h.OnReset != nil {
		h.OnReset()
	}
	req.NoImages = true
	if resp2, err2 := provider.Chat(ctx, req, h); err2 == nil {
		return resp2, true, nil
	}
	return nil, false, err
}

func (a *Agent) runTurn(ctx context.Context, sessionID, text string, atts []Attachment, provider Provider, cfg store.AISettings) {
	lock := a.sessionLock(sessionID)
	lock.Lock()
	defer lock.Unlock()

	sess, err := a.Sessions.Load(sessionID)
	if err != nil {
		a.emit(Event{SessionID: sessionID, Type: "error", Error: err.Error()})
		return
	}
	if sess.SystemPrompt == "" {
		sess.SystemPrompt = a.buildSystemPrompt()
	}
	sess.Turns++
	if sess.Turns == 1 || sess.Title == "" || sess.Title == "新对话" {
		sess.Title = titleFrom(text, atts)
		a.emit(Event{SessionID: sessionID, Type: "title", Text: sess.Title})
	}
	content := text
	if cfg.NudgeInterval > 0 && sess.Turns%cfg.NudgeInterval == 0 {
		content += nudgeText
	}
	// 附件的说明（和较短的文本文件的内容）跟在用户的话后面，只给模型看
	content += a.Attach.attachNote(sessionID, atts)
	turnStart := len(sess.Messages)
	userMsg := Message{ID: newID(), Role: "user", Content: content, Display: text, Attachments: atts, CreatedAt: nowMs()}
	sess.Messages = append(sess.Messages, userMsg)
	sess.UpdatedAt = nowMs()
	_ = a.Sessions.Save(sess)
	a.emit(Event{SessionID: sessionID, Type: "user", Message: &userMsg})

	// 自动召回：把与这次提问相关的长期记忆、往期对话要点和技能附在用户消息后面（放在消息里而不是系统提示词里，不影响提示词缓存）
	if block, items := a.recall(ctx, sess, text); block != "" {
		last := &sess.Messages[len(sess.Messages)-1]
		last.Content += block
		last.Meta = map[string]any{"recall": items}
		_ = a.Sessions.Save(sess)
		a.emit(Event{SessionID: sessionID, Type: "recall", Message: last})
	}

	notice := func(text string) {
		m := Message{ID: newID(), Role: "assistant", Kind: "notice", Content: text, CreatedAt: nowMs()}
		sess.Messages = append(sess.Messages, m)
		a.emit(Event{SessionID: sessionID, Type: "notice", Message: &m})
	}

	toolCalls := 0
	maxSteps := max(cfg.MaxSteps, 1)
	// 这个模型已经拒收过图片时不再发送；换了模型会重新尝试
	imagesOff := ""
	if sess.NoImageModel != "" && sess.NoImageModel == cfg.Model {
		imagesOff = "当前模型不能查看图片"
	}
	imageCache := map[string][]byte{}
	for step := 0; step < maxSteps; step++ {
		a.emit(Event{SessionID: sessionID, Type: "step"})
		resp, dropped, err := chat(ctx, provider, ChatRequest{
			System: sess.SystemPrompt, Messages: a.Attach.withImages(sessionID, sess.Messages, imagesOff, imageCache),
			Tools: a.tools, MaxTokens: cfg.MaxTokens, Effort: cfg.Effort, NoImages: imagesOff != "",
		}, StreamHandler{
			OnText:     func(s string) { a.emit(Event{SessionID: sessionID, Type: "text", Text: s}) },
			OnThinking: func(s string) { a.emit(Event{SessionID: sessionID, Type: "thinking", Text: s}) },
			OnReset:    func() { a.emit(Event{SessionID: sessionID, Type: "reset"}) },
		})
		if dropped {
			imagesOff = "当前模型不能查看图片"
			sess.NoImageModel = cfg.Model
			notice(fmt.Sprintf("当前模型（%s）没有接受图片，这次是按没有图片处理的。想让 AI 看图，请在「设置 → AI 模型」里换成支持图片输入的模型。", cfg.Model))
		}
		if err != nil {
			if ctx.Err() != nil {
				notice("已停止")
			} else {
				a.emit(Event{SessionID: sessionID, Type: "error", Error: err.Error()})
				notice("出错了：" + err.Error())
			}
			break
		}
		sess.InputTokens += resp.InputTokens
		sess.OutputTokens += resp.OutputTokens
		msg := resp.Message
		msg.ID = newID()
		msg.CreatedAt = nowMs()
		if resp.StopReason == "refusal" {
			// 被安全策略拒绝：丢弃部分输出，以一条普通回复结束本轮
			msg = Message{ID: msg.ID, Role: "assistant", Content: "（该请求被模型的安全策略拒绝，没有执行任何操作。可以换一种描述方式再试。）", CreatedAt: msg.CreatedAt}
			sess.Messages = append(sess.Messages, msg)
			a.emit(Event{SessionID: sessionID, Type: "assistant", Message: &msg})
			break
		}
		sess.Messages = append(sess.Messages, msg)
		sess.UpdatedAt = nowMs()
		_ = a.Sessions.Save(sess)
		a.emit(Event{SessionID: sessionID, Type: "assistant", Message: &msg})
		if len(msg.ToolCalls) == 0 {
			break
		}

		for _, call := range msg.ToolCalls {
			call := call
			a.emit(Event{SessionID: sessionID, Type: "tool_start", ToolCall: &call})
			var res *ToolResult
			switch {
			case resp.StopReason == "max_tokens":
				res = &ToolResult{Content: "输出达到 max_tokens 上限被截断，工具没有执行。请缩短参数或分步完成。", IsError: true}
			case ctx.Err() != nil:
				res = &ToolResult{Content: "用户已停止，工具没有执行。", IsError: true}
			default:
				res = a.execTool(ctx, sess, call, false)
			}
			tm := Message{ID: newID(), Role: "tool", ToolCallID: call.ID, ToolName: call.Name,
				Content: store.TruncateMiddle(res.Content, 16000), IsError: res.IsError, Meta: res.Meta, CreatedAt: nowMs()}
			sess.Messages = append(sess.Messages, tm)
			a.emit(Event{SessionID: sessionID, Type: "tool_end", Message: &tm})
		}
		toolCalls += len(msg.ToolCalls)
		sess.UpdatedAt = nowMs()
		_ = a.Sessions.Save(sess)
		if ctx.Err() != nil {
			notice("已停止")
			break
		}
		if step == maxSteps-1 {
			notice(fmt.Sprintf("已达到单轮最大步骤数（%d），先暂停在这里。回复“继续”可以接着处理。", maxSteps))
		}
	}
	sess.UpdatedAt = nowMs()
	_ = a.Sessions.Save(sess)

	if ctx.Err() == nil {
		// 把这一轮的“问题 → 操作 → 结论”存入长期记忆，以后遇到类似的问题可以按意思找回来
		if ep, ok := a.episodeOf(sess, sess.Turns, sess.Messages[turnStart:]); ok {
			go a.memIndex().PutEpisode(context.Background(), ep)
		}
	}
	if cfg.AutoLearn && ctx.Err() == nil && toolCalls >= max(cfg.ReviewThreshold, 1) {
		go a.review(sessionID, turnStart, cfg)
	}
}

func (a *Agent) execTool(ctx context.Context, sess *Session, call ToolCall, background bool) (res *ToolResult) {
	defer func() {
		if r := recover(); r != nil {
			res = &ToolResult{Content: fmt.Sprintf("工具执行异常: %v", r), IsError: true}
		}
	}()
	t := a.tool(call.Name)
	if t == nil {
		return &ToolResult{Content: "未知工具: " + call.Name, IsError: true}
	}
	args := json.RawMessage(call.Arguments)
	var probe map[string]any
	if json.Unmarshal(args, &probe) != nil {
		b, _ := json.Marshal(map[string]string{"INVALID_JSON": call.Arguments})
		return &ToolResult{Content: string(b), IsError: true}
	}
	tc := &ToolContext{Agent: a, Session: sess, ToolCallID: call.ID, Background: background, ctx: ctx}
	r, err := t.Handler(ctx, tc, args)
	if err != nil {
		return &ToolResult{Content: "错误：" + err.Error(), IsError: true}
	}
	if r == nil {
		return &ToolResult{Content: "完成"}
	}
	return r
}

// ToolContext 提供工具执行时需要的会话信息与审批能力。
type ToolContext struct {
	Agent      *Agent
	Session    *Session
	ToolCallID string
	Background bool
	ctx        context.Context
}

func (tc *ToolContext) SessionID() string {
	if tc.Session == nil {
		return ""
	}
	return tc.Session.ID
}

// ServerID 返回会话绑定的服务器（没有绑定时为空）。
func (tc *ToolContext) ServerID() string {
	if tc == nil || tc.Session == nil {
		return ""
	}
	return tc.Session.ServerID
}

// Source 表示学习事件来源：主对话为 agent，后台复盘为 review。
func (tc *ToolContext) Source() string {
	if tc.Background {
		return "review"
	}
	return "agent"
}

// Approve 根据审批模式与风险等级决定是否需要用户确认。
func (tc *ToolContext) Approve(req ApprovalRequest) bool {
	if tc.Background {
		return false
	}
	a := tc.Agent
	mode := a.cfg.Settings().ApprovalMode
	if req.Risk != RiskDanger {
		if mode == "auto" || tc.Session.ApproveAll || (mode != "always" && req.Risk == RiskSafe) {
			return true
		}
	}
	req.ID = newID()
	req.SessionID = tc.Session.ID
	req.ToolCallID = tc.ToolCallID
	p := &pendingApproval{req: req, ch: make(chan approvalDecision, 1)}
	a.mu.Lock()
	a.approvals[req.ID] = p
	a.mu.Unlock()
	a.emit(Event{SessionID: req.SessionID, Type: "approval", Approval: &req})

	var cancelled <-chan struct{}
	if tc.ctx != nil {
		cancelled = tc.ctx.Done()
	}
	var d approvalDecision
	select {
	case d = <-p.ch:
	case <-cancelled:
	case <-time.After(20 * time.Minute):
	}
	a.mu.Lock()
	delete(a.approvals, req.ID)
	a.mu.Unlock()
	status := "rejected"
	if d.approved {
		status = "approved"
		if d.all && req.Risk != RiskDanger {
			tc.Session.ApproveAll = true
		}
	}
	a.emit(Event{SessionID: req.SessionID, Type: "approval_resolved", Approval: &req, Status: status})
	return d.approved
}

func (a *Agent) ResolveApproval(id string, approved, all bool) error {
	a.mu.Lock()
	p := a.approvals[id]
	a.mu.Unlock()
	if p == nil {
		return errors.New("审批请求已失效")
	}
	select {
	case p.ch <- approvalDecision{approved: approved, all: all}:
	default:
	}
	return nil
}

// PendingApprovals 返回会话中等待批准的请求（页面刷新后恢复显示）。
func (a *Agent) PendingApprovals(sessionID string) []ApprovalRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []ApprovalRequest{}
	for _, p := range a.approvals {
		if p.req.SessionID == sessionID {
			out = append(out, p.req)
		}
	}
	return out
}

// RecordLearning 记录一次学习事件并通知界面。
func (a *Agent) RecordLearning(sessionID string, e LearnEvent) {
	e.Time = nowMs()
	e.SessionID = sessionID
	a.Learn.Add(e)
	a.emit(Event{SessionID: sessionID, Type: "learning", Learn: &e})
}

// Review 手动触发对整个会话的学习复盘。
func (a *Agent) Review(sessionID string) error {
	cfg := a.cfg.Settings()
	if _, err := providerFactory(cfg); err != nil {
		return err
	}
	sess, err := a.Sessions.Load(sessionID)
	if err != nil {
		return err
	}
	if len(sess.Messages) == 0 {
		return errors.New("会话还没有内容")
	}
	go a.review(sessionID, 0, cfg)
	return nil
}

// review 是后台学习循环：让模型阅读刚完成的对话，把值得保留的知识写入记忆与技能库。
func (a *Agent) review(sessionID string, from int, cfg store.AISettings) {
	a.mu.Lock()
	if a.reviewing[sessionID] {
		a.mu.Unlock()
		return
	}
	a.reviewing[sessionID] = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.reviewing, sessionID)
		a.mu.Unlock()
	}()

	sess, err := a.Sessions.Load(sessionID)
	if err != nil || from >= len(sess.Messages) {
		return
	}
	provider, err := providerFactory(cfg)
	if err != nil {
		return
	}
	a.emit(Event{SessionID: sessionID, Type: "review_start"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var tools []*Tool
	for _, t := range a.tools {
		switch t.Name {
		case "memory", "recall", "skills_list", "skill_view", "skill_manage":
			tools = append(tools, t)
		}
	}
	system := BuildReviewPrompt(a.Memory.Snapshot(), a.relatedMemories(ctx, sess, sess.Messages[from:]), a.Skills.Index())
	msgs := []Message{{Role: "user", Content: "以下是需要复盘的对话记录：\n\n" + Transcript(sess.Messages[from:], 24000) + "\n\n请判断是否有需要保存的知识，并使用工具完成保存。"}}
	learnedBefore := len(a.Learn.Recent(0))
	summary := ""
	for step := 0; step < 8; step++ {
		resp, err := provider.Chat(ctx, ChatRequest{System: system, Messages: msgs, Tools: tools, MaxTokens: 16000, Effort: "low"}, StreamHandler{})
		if err != nil {
			a.emit(Event{SessionID: sessionID, Type: "review_done", Error: err.Error()})
			return
		}
		m := resp.Message
		msgs = append(msgs, m)
		if strings.TrimSpace(m.Content) != "" {
			summary = strings.TrimSpace(m.Content)
		}
		if len(m.ToolCalls) == 0 || resp.StopReason == "refusal" {
			break
		}
		for _, call := range m.ToolCalls {
			r := a.execTool(ctx, sess, call, true)
			msgs = append(msgs, Message{Role: "tool", ToolCallID: call.ID, ToolName: call.Name, Content: r.Content, IsError: r.IsError})
		}
	}
	learned := len(a.Learn.Recent(0)) - learnedBefore
	if learned > 0 {
		l := a.sessionLock(sessionID)
		l.Lock()
		if s, err := a.Sessions.Load(sessionID); err == nil {
			s.Learned += learned
			_ = a.Sessions.Save(s)
		}
		l.Unlock()
	}
	a.emit(Event{SessionID: sessionID, Type: "review_done", Text: summary, Status: fmt.Sprint(learned)})
}

// Tools 返回全部工具（供调试或界面展示）。
func (a *Agent) ToolNames() []string {
	out := make([]string, 0, len(a.tools))
	for _, t := range a.tools {
		out = append(out, t.Name)
	}
	return out
}
