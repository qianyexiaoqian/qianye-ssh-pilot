package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"sshpilot/internal/agent"
)

// ---- 长期记忆（界面一侧） ----

// LongItem 是界面上显示的一条长期记忆。
type LongItem struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"` // fact：事实与经验；episode：往期对话的要点
	Text        string   `json:"text"`
	Servers     []string `json:"servers"`
	ServerNames []string `json:"serverNames"`
	SessionID   string   `json:"sessionId"`
	Title       string   `json:"title"`
	Source      string   `json:"source"`
	CreatedAt   int64    `json:"createdAt"`
	UpdatedAt   int64    `json:"updatedAt"`
	Hits        int      `json:"hits"`
	LastHit     int64    `json:"lastHit"`
	// Score 只在搜索结果里有值。
	Score float64 `json:"score"`
}

func (a *App) longItem(it agent.MemItem, score float64) LongItem {
	names := a.serverNameMap()
	v := LongItem{ID: it.ID, Kind: it.Kind, Text: it.Text, Servers: []string{}, ServerNames: []string{}, SessionID: it.SessionID, Title: it.Title,
		Source: it.Source, CreatedAt: it.CreatedAt, UpdatedAt: it.UpdatedAt, Hits: it.Hits, LastHit: it.LastHit, Score: score}
	for _, id := range it.Servers {
		if n := names[id]; n != "" {
			v.Servers = append(v.Servers, id)
			v.ServerNames = append(v.ServerNames, n)
		}
	}
	return v
}

// GetMemoryIndex 返回长期记忆的检索方式与索引进度。
func (a *App) GetMemoryIndex() agent.IndexStatus { return a.agent.Long.Status() }

// ListLongMemory 列出长期记忆；query 不为空时按相关度搜索。kind 为 fact 或 episode。
func (a *App) ListLongMemory(kind, query string) []LongItem {
	out := []LongItem{}
	if query = strings.TrimSpace(query); query != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, h := range a.agent.Long.Search(ctx, query, agent.SearchOptions{Kinds: []string{kind}, Limit: 30, Loose: true, NoTouch: true}) {
			out = append(out, a.longItem(h.MemItem, h.Score))
		}
		return out
	}
	list := a.agent.Long.List(kind)
	if len(list) > 500 {
		list = list[:500]
	}
	for _, it := range list {
		out = append(out, a.longItem(it, 0))
	}
	return out
}

// SaveLongMemory 新建（id 为空）或修改一条长期记忆。
func (a *App) SaveLongMemory(id, text string, serverIDs []string) (LongItem, error) {
	names := a.serverNameMap()
	serverIDs = slices.DeleteFunc(slices.Clone(serverIDs), func(id string) bool { return names[id] == "" })
	if serverIDs == nil {
		serverIDs = []string{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	it, err := a.agent.Long.Save(ctx, id, text, serverIDs, "user")
	if err != nil {
		return LongItem{}, err
	}
	action := "edit"
	if id == "" {
		action = "add"
	}
	a.agent.Learn.Add(agent.LearnEvent{Time: time.Now().UnixMilli(), Source: "user", Kind: "longterm", Action: action, Target: "longterm", Summary: clipText(it.Text, 200)})
	return a.longItem(it, 0), nil
}

func (a *App) DeleteLongMemory(id string) error { return a.agent.Long.Delete(id) }

// ReindexMemory 丢弃已有的向量并在后台重新计算。
func (a *App) ReindexMemory() error {
	if !agent.EmbedConfigured(a.settings().Embed) {
		return errors.New("还没有配置向量模型：当前使用内置的关键词匹配，不需要索引")
	}
	a.agent.SyncIndex()
	a.agent.Long.ResetIndex()
	a.agent.StartIndexing()
	return nil
}

// EmbedTest 是向量模型连通性测试的结果。
type EmbedTest struct {
	Model     string  `json:"model"`
	Dims      int     `json:"dims"`
	LatencyMs int64   `json:"latencyMs"`
	Related   float64 `json:"related"`
	Unrelated float64 `json:"unrelated"`
	Threshold float64 `json:"threshold"`
	// Warning 不为空表示接口能用，但这个模型区分不了相关与无关的内容。
	Warning string `json:"warning"`
}

// TestEmbedding 用当前保存的设置调用一次向量接口，并量出这个模型区分相关与无关内容的能力。
func (a *App) TestEmbedding() (EmbedTest, error) {
	cfg := a.settings().Embed
	if !agent.EmbedConfigured(cfg) {
		return EmbedTest{}, errors.New("请先填写接口地址、模型名称和 API Key")
	}
	e := agent.NewEmbedder(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	start := time.Now()
	vecs, err := e.Embed(ctx, []string{"连接测试"})
	if err != nil {
		return EmbedTest{}, err
	}
	res := EmbedTest{Model: cfg.Model, Dims: len(vecs[0]), LatencyMs: time.Since(start).Milliseconds()}
	cal, err := agent.Calibrate(ctx, e)
	if err != nil {
		return EmbedTest{}, err
	}
	res.Related, res.Unrelated = cal.Related, cal.Unrelated
	if cal.Related-cal.Unrelated < 0.05 {
		res.Warning = fmt.Sprintf("这个模型几乎分不出相关与无关的内容（相关 %.2f / 无关 %.2f），请确认填写的是文本向量（embedding）模型", cal.Related, cal.Unrelated)
	} else {
		res.Threshold = cal.Unrelated + 0.45*(cal.Related-cal.Unrelated)
	}
	// 测试通过：顺便让长期记忆换用这个模型并开始建立索引
	a.agent.SyncIndex()
	return res, nil
}

// ---- 技能的固定与归档 ----

func (a *App) ListArchivedSkills() []agent.SkillMeta { return a.agent.Skills.ListArchived() }

func (a *App) PinSkill(name string, pinned bool) error { return a.agent.Skills.SetPinned(name, pinned) }

func (a *App) ArchiveSkill(name string) error {
	if err := a.agent.Skills.Archive(name); err != nil {
		return err
	}
	a.agent.Learn.Add(agent.LearnEvent{Time: time.Now().UnixMilli(), Source: "user", Kind: "skill", Action: "archive", Target: name, Summary: "手动归档"})
	return nil
}

func (a *App) RestoreSkill(name string) error {
	if err := a.agent.Skills.Restore(name); err != nil {
		return err
	}
	a.agent.Learn.Add(agent.LearnEvent{Time: time.Now().UnixMilli(), Source: "user", Kind: "skill", Action: "restore", Target: name, Summary: "从归档恢复"})
	return nil
}

func (a *App) DeleteArchivedSkill(name string) error { return a.agent.Skills.DeleteArchived(name) }
