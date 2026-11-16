package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"sshpilot/internal/store"
)

// 这个文件把长期记忆接到对话流程里：
//   - 自动召回：每次提问前找出相关的记忆、往期对话要点和技能，附在用户消息后面；
//   - 对话要点：每轮对话结束后把“问题 → 操作 → 结论”存入长期记忆；
//   - memory / recall 工具里与长期记忆有关的部分。

// RecallItem 是随用户消息保存的一条召回记录（界面上可以展开查看）。
type RecallItem struct {
	Kind    string   `json:"kind"`
	Text    string   `json:"text"`
	Title   string   `json:"title,omitempty"`
	Servers []string `json:"servers,omitempty"` // 服务器名称
	Time    int64    `json:"time,omitempty"`
	Score   float64  `json:"score"`
}

func (a *Agent) embedSettings() store.EmbedSettings {
	if a.cfg.Embed == nil {
		return store.EmbedSettings{RecallCount: 6}
	}
	return a.cfg.Embed()
}

// memIndex 返回长期记忆，并让它使用当前设置里的向量模型（设置没有变化时没有开销）。
func (a *Agent) memIndex() *LongMemory {
	if a.Long.SetEmbedder(NewEmbedder(a.embedSettings())) {
		go func() { _ = a.Long.Reindex(context.Background()) }()
	}
	return a.Long
}

// SyncIndex 在设置变化后调用：向量模型变了就换用新的，并在后台重建索引。
func (a *Agent) SyncIndex() { a.memIndex() }

func (a *Agent) serverNames() map[string]string {
	if a.cfg.Servers == nil {
		return map[string]string{}
	}
	return a.cfg.Servers()
}

// namesOf 把服务器 ID 换成名称；已经删除的服务器不显示。
func (a *Agent) namesOf(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	all := a.serverNames()
	var out []string
	for _, id := range ids {
		if n := all[id]; n != "" {
			out = append(out, n)
		}
	}
	return out
}

// serversIn 找出文本里提到的服务器（按名称），加上会话绑定的服务器。
func (a *Agent) serversIn(sess *Session, text string) []string {
	var ids []string
	if sess != nil && sess.ServerID != "" {
		ids = append(ids, sess.ServerID)
	}
	lower := strings.ToLower(text)
	for id, name := range a.serverNames() {
		if utf8.RuneCountInString(name) >= 2 && strings.Contains(lower, strings.ToLower(name)) && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// resolveServers 把模型给出的服务器名称解析成 ID；有解析不了的名称时报错，避免记忆被标到错误的服务器上。
func (a *Agent) resolveServers(refs []string) ([]string, error) {
	var ids []string
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if a.cfg.ResolveServer == nil {
			return nil, errors.New("当前环境无法解析服务器名称")
		}
		id, ok := a.cfg.ResolveServer(ref)
		if !ok {
			var names []string
			for _, n := range a.serverNames() {
				names = append(names, n)
			}
			slices.Sort(names)
			return nil, fmt.Errorf("未找到服务器 %s。可用的服务器：%s", ref, strings.Join(names, "、"))
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (a *Agent) scopeLabel(ids []string) string {
	if names := a.namesOf(ids); len(names) > 0 {
		return "（" + strings.Join(names, "、") + "）"
	}
	return ""
}

// ---- 自动召回 ----

const recallHeader = "\n\n<system-reminder>\n以下内容由程序根据这次提问从长期记忆里自动找出，可能相关也可能无关，只作参考：与用工具查到的实际情况不一致时以实际情况为准，并用 memory 工具修正那条记忆。不要在回复里提到这段提示。\n"

// recall 为一条用户消息找出相关的长期记忆，返回附加在消息后面的文本和供界面展示的记录。
func (a *Agent) recall(ctx context.Context, sess *Session, text string) (string, []RecallItem) {
	cfg := a.embedSettings()
	if cfg.RecallOff || utf8.RuneCountInString(strings.TrimSpace(text)) < 4 {
		return "", nil
	}
	// 消息里带着密码、密钥一类的内容时不做召回：检索要把这段文字发给向量接口
	if scanContent(text) != nil {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	long := a.memIndex()
	kinds := []string{KindFact, KindEpisode}
	if long.Semantic() {
		// 技能提示只在语义向量下启用：关键词匹配只能看出“都提到了 nginx”，判断不了是不是同一类任务
		kinds = append(kinds, KindSkill)
	}
	hits := long.Search(ctx, clipRunes(text, 1500), SearchOptions{
		Kinds:          kinds,
		Limit:          max(cfg.RecallCount, 1),
		SkillLimit:     2,
		Servers:        a.serversIn(sess, text),
		ExcludeSession: sess.ID,
		MinScore:       cfg.MinScore,
	})
	// 先列记忆，再列往期对话，最后是技能；每一类内部按相关度排列
	order := map[string]int{KindFact: 0, KindEpisode: 1, KindSkill: 2}
	slices.SortStableFunc(hits, func(x, y MemHit) int { return order[x.Kind] - order[y.Kind] })
	var b strings.Builder
	var items []RecallItem
	for _, h := range hits {
		// 同一个会话里已经附带过的内容不再重复
		if slices.Contains(sess.Recalled, h.ID) {
			continue
		}
		sess.Recalled = append(sess.Recalled, h.ID)
		item := RecallItem{Kind: h.Kind, Text: h.Text, Title: h.Title, Servers: a.namesOf(h.Servers), Time: h.CreatedAt, Score: h.Score}
		switch h.Kind {
		case KindFact:
			fmt.Fprintf(&b, "【记忆】%s%s\n", a.scopeLabel(h.Servers), h.Text)
		case KindEpisode:
			fmt.Fprintf(&b, "【往期对话 %s《%s》】%s\n", time.UnixMilli(h.CreatedAt).Format("2006-01-02"), h.Title, strings.ReplaceAll(clipRunes(h.Text, 420), "\n", " "))
		case KindSkill:
			fmt.Fprintf(&b, "【相关技能】%s（需要时用 skill_view 读取全文）\n", h.Text)
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return "", nil
	}
	return recallHeader + b.String() + "</system-reminder>", items
}

// relatedMemories 找出与一段对话相关的已有长期记忆，供学习复盘对照，避免重复保存。
func (a *Agent) relatedMemories(ctx context.Context, sess *Session, msgs []Message) string {
	var q []string
	for _, m := range msgs {
		if m.Role == "user" && m.Kind != "notice" {
			text := m.Display
			if text == "" {
				text = m.Content
			}
			q = append(q, clipRunes(text, 300))
		}
	}
	query := strings.Join(q, "\n")
	if strings.TrimSpace(query) == "" {
		return "（暂无）"
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	hits := a.memIndex().Search(ctx, clipRunes(query, 1500), SearchOptions{Kinds: []string{KindFact}, Limit: 10, Servers: a.serversIn(sess, query), Loose: true, NoTouch: true})
	if len(hits) == 0 {
		return "（暂无）"
	}
	var b strings.Builder
	for _, h := range hits {
		fmt.Fprintf(&b, "- %s%s\n", a.scopeLabel(h.Servers), h.Text)
	}
	return strings.TrimSpace(b.String())
}

// ---- 对话要点 ----

func episodeID(sessionID string, turn int) string { return fmt.Sprintf("ep:%s:%d", sessionID, turn) }

// episodeOf 把一轮对话（一条用户消息及其后的全部回复）整理成“问题 → 操作 → 结论”。没有形成结论的对话不保存。
func (a *Agent) episodeOf(sess *Session, turn int, msgs []Message) (MemItem, bool) {
	var question, answer string
	var cmds, tools []string
	created := int64(0)
	for _, m := range msgs {
		if m.Kind == "notice" {
			continue
		}
		switch m.Role {
		case "user":
			if question == "" {
				question = m.Display
				if question == "" {
					question = m.Content
				}
				created = m.CreatedAt
			}
		case "assistant":
			if strings.TrimSpace(m.Content) != "" && len(m.ToolCalls) == 0 {
				answer = m.Content
			}
			for _, c := range m.ToolCalls {
				var args struct {
					Command string `json:"command"`
				}
				if c.Name == "run_command" && json.Unmarshal([]byte(c.Arguments), &args) == nil && args.Command != "" {
					if len(cmds) < 6 {
						cmds = append(cmds, clip(strings.SplitN(args.Command, "\n", 2)[0], 110))
					}
				} else if !slices.Contains(tools, c.Name) && !slices.Contains([]string{"memory", "recall", "skill_view", "skills_list", "session_search"}, c.Name) {
					tools = append(tools, c.Name)
				}
			}
		}
	}
	question, answer = strings.TrimSpace(question), plainText(answer)
	if question == "" || answer == "" || (utf8.RuneCountInString(answer) < 30 && len(cmds)+len(tools) == 0) {
		return MemItem{}, false
	}
	var b strings.Builder
	b.WriteString("问题：" + clip(question, 300))
	if len(cmds) > 0 {
		b.WriteString("\n操作：" + strings.Join(cmds, "；"))
	}
	if len(tools) > 0 {
		b.WriteString("\n用到：" + strings.Join(tools, "、"))
	}
	b.WriteString("\n结论：" + clip(answer, 450))
	if created == 0 {
		created = nowMs()
	}
	return MemItem{ID: episodeID(sess.ID, turn), Kind: KindEpisode, Text: b.String(), Servers: a.serversIn(sess, question+"\n"+strings.Join(cmds, "\n")),
		SessionID: sess.ID, Title: sess.Title, Source: "episode", CreatedAt: created, UpdatedAt: created}, true
}

// SyncEpisodes 为已有的历史会话补上对话要点（升级到带长期记忆的版本后第一次启动时会用到），返回新增的条数。
func (a *Agent) SyncEpisodes() int {
	have := a.Long.EpisodeIDs()
	list := a.Sessions.List()
	if len(list) > 400 {
		list = list[:400]
	}
	var eps []MemItem
	for _, sum := range list {
		if a.IsRunning(sum.ID) {
			continue
		}
		sess, err := a.Sessions.Load(sum.ID)
		if err != nil {
			continue
		}
		// 按用户消息切分成一轮一轮
		turn, start := 0, -1
		flush := func(end int) {
			if start < 0 {
				return
			}
			if id := episodeID(sess.ID, turn); !have[id] {
				if ep, ok := a.episodeOf(sess, turn, sess.Messages[start:end]); ok {
					eps = append(eps, ep)
				}
			}
		}
		for i, m := range sess.Messages {
			if m.Role == "user" && m.Kind != "notice" {
				flush(i)
				turn++
				start = i
			}
		}
		flush(len(sess.Messages))
	}
	return a.Long.AddEpisodes(eps)
}

// StartIndexing 在程序启动后调用：补上历史对话的要点，并为还没有向量的条目建立索引。
func (a *Agent) StartIndexing() {
	go func() {
		a.SyncEpisodes()
		_ = a.memIndex().Reindex(context.Background())
	}()
}

// ---- 工具 ----

func memHitLine(a *Agent, h MemHit) string {
	switch h.Kind {
	case KindEpisode:
		return fmt.Sprintf("· [往期对话 %s《%s》· 相关度 %.2f] %s", time.UnixMilli(h.CreatedAt).Format("2006-01-02 15:04"), h.Title, h.Score, strings.ReplaceAll(h.Text, "\n", " "))
	case KindSkill:
		return fmt.Sprintf("· [技能 · 相关度 %.2f] %s", h.Score, h.Text)
	}
	return fmt.Sprintf("· [记忆%s · 相关度 %.2f] %s", a.scopeLabel(h.Servers), h.Score, h.Text)
}

// longMemoryApply 处理 memory 工具里 target=longterm 的操作，以及把核心记忆归档到长期记忆。
func (a *Agent) longMemoryApply(ctx context.Context, tc *ToolContext, target, action, content, oldText string, servers []string) (string, error) {
	ids, err := a.resolveServers(servers)
	if err != nil {
		return "", err
	}
	long := a.memIndex()
	record := func(action, summary string) {
		a.RecordLearning(tc.SessionID(), LearnEvent{Source: tc.Source(), Kind: "longterm", Action: action, Target: "longterm", Summary: clipRunes(summary, 200)})
	}
	added := func(res AddResult) string {
		msg := "已存入长期记忆（不占核心记忆的容量，以后提到相关内容时会自动召回）。"
		if res.Similar != nil {
			msg += "\n注意：已有一条内容很接近的记忆：「" + res.Similar.Text + "」。如果说的是同一件事，请用 patch 合并，或 remove 删除其中一条。"
		}
		return msg
	}
	if target != "longterm" {
		// archive：把核心记忆里的一条移到长期记忆
		entry, err := a.Memory.Take(target, oldText)
		if err != nil {
			return "", err
		}
		res, err := long.AddFact(ctx, entry, ids, "archive", tc.SessionID())
		if err != nil {
			_, _ = a.Memory.Apply(target, "add", entry, "")
			return "", err
		}
		record("archive", entry)
		if res.Duplicate {
			return "长期记忆里已经有相同的内容，已从核心记忆中移除这一条。" + a.Memory.usageLine(target, a.Memory.View(target).Entries), nil
		}
		return "已从核心记忆移到长期记忆。" + a.Memory.usageLine(target, a.Memory.View(target).Entries), nil
	}
	switch action {
	case "add":
		res, err := long.AddFact(ctx, content, ids, tc.Source(), tc.SessionID())
		if err != nil {
			return "", err
		}
		if res.Duplicate {
			return "长期记忆里已经有相同的内容，未重复添加：「" + res.Item.Text + "」", nil
		}
		record("add", a.scopeLabel(ids)+res.Item.Text)
		return added(res), nil
	case "patch":
		if strings.TrimSpace(content) == "" {
			return "", errors.New("patch 需要 content（替换后的文本）；要删除条目请使用 remove")
		}
		var scope []string
		if len(servers) > 0 {
			scope = ids
		}
		it, err := long.PatchFact(ctx, oldText, content, scope)
		if err != nil {
			return "", err
		}
		record("patch", a.scopeLabel(it.Servers)+it.Text)
		return "长期记忆已更新：「" + it.Text + "」", nil
	case "remove":
		it, err := long.RemoveFact(oldText)
		if err != nil {
			return "", err
		}
		record("remove", it.Text)
		return "已从长期记忆中删除。", nil
	}
	return "", errors.New("target=longterm 时 action 必须是 add / patch / remove")
}

func (a *Agent) recallTool() *Tool {
	return &Tool{
		Name: "recall",
		Description: "按意思检索长期记忆和往期对话的要点，不要求关键词完全一致。每次提问时程序已经自动附上了最相关的几条；" +
			"需要查得更全、换个说法再查，或者想看某台服务器的全部记忆（只填 servers、不填 query）时调用。结果里的原文可以用作 memory 工具的 old_text。",
		Schema: obj(map[string]any{
			"query":   str("要找的内容，用一句自然的话描述，例如“web-01 上次证书续期失败的原因”"),
			"servers": strArray("只关心这些服务器时填写名称；标注了这些服务器的记忆排在前面"),
			"scope":   strEnum("检索范围：all=全部（默认）；memory=只查记忆；sessions=只查往期对话", "all", "memory", "sessions"),
			"limit":   integer("返回条数，默认 8"),
		}),
		Handler: func(ctx context.Context, tc *ToolContext, raw json.RawMessage) (*ToolResult, error) {
			var in struct {
				Query   string   `json:"query"`
				Servers []string `json:"servers"`
				Scope   string   `json:"scope"`
				Limit   int      `json:"limit"`
			}
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, err
			}
			ids, err := a.resolveServers(in.Servers)
			if err != nil {
				return nil, err
			}
			limit := min(max(in.Limit, 0), 30)
			if limit == 0 {
				limit = 8
			}
			long := a.memIndex()
			var b strings.Builder
			count := 0
			if strings.TrimSpace(in.Query) == "" {
				if len(ids) == 0 {
					return nil, errors.New("请填写 query，或者用 servers 指定要查看哪些服务器的记忆")
				}
				for _, it := range long.List(KindFact) {
					if slices.ContainsFunc(it.Servers, func(id string) bool { return slices.Contains(ids, id) }) && count < 40 {
						fmt.Fprintf(&b, "· [记忆%s] %s\n", a.scopeLabel(it.Servers), it.Text)
						count++
					}
				}
				if count == 0 {
					return &ToolResult{Content: "长期记忆里还没有标注了这些服务器的内容。"}, nil
				}
				return &ToolResult{Content: b.String(), Meta: map[string]any{"kind": "recall", "count": count}}, nil
			}
			kinds := []string{KindFact, KindEpisode}
			switch in.Scope {
			case "memory":
				kinds = []string{KindFact}
			case "sessions":
				kinds = []string{KindEpisode}
			}
			if len(ids) == 0 {
				ids = a.serversIn(tc.Session, in.Query)
			}
			hits := long.Search(ctx, in.Query, SearchOptions{Kinds: kinds, Limit: limit, Servers: ids, ExcludeSession: tc.SessionID(), Loose: true})
			if len(hits) == 0 {
				return &ToolResult{Content: "长期记忆和往期对话里没有找到相关内容。可以换一种说法再试，或用 session_search 按关键词查原始对话记录。"}, nil
			}
			for _, h := range hits {
				b.WriteString(memHitLine(a, h) + "\n")
			}
			return &ToolResult{Content: b.String(), Meta: map[string]any{"kind": "recall", "count": len(hits)}}, nil
		},
	}
}
