package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"sshpilot/internal/sshx"
	"sshpilot/internal/store"
)

func (a *App) ListScripts() []store.Script {
	out := []store.Script{}
	a.store.Read(func(d *store.Data) {
		for _, s := range d.Scripts {
			out = append(out, *s)
		}
	})
	return out
}

func normalizeScript(s *store.Script) error {
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" {
		return errors.New("请填写脚本名称")
	}
	if strings.TrimSpace(s.Content) == "" {
		return errors.New("脚本内容不能为空")
	}
	switch s.Interpreter {
	case "bash", "sh", "python3":
	default:
		s.Interpreter = "bash"
	}
	s.Content = strings.ReplaceAll(s.Content, "\r\n", "\n")
	var params []store.ScriptParam
	for _, p := range s.Params {
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" {
			continue
		}
		if !envName(p.Name) {
			return fmt.Errorf("参数名 %s 无效：只能包含字母、数字和下划线，且不能以数字开头", p.Name)
		}
		params = append(params, p)
	}
	if params == nil {
		params = []store.ScriptParam{}
	}
	s.Params = params
	if s.Tags == nil {
		s.Tags = []string{}
	}
	if s.Category == "" {
		s.Category = "自定义"
	}
	return nil
}

func envName(n string) bool {
	for i, r := range n {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || (i > 0 && r >= '0' && r <= '9')) {
			return false
		}
	}
	return n != ""
}

func (a *App) SaveScript(in store.Script) (store.Script, error) {
	if err := normalizeScript(&in); err != nil {
		return store.Script{}, err
	}
	var saved store.Script
	err := a.store.Update(func(d *store.Data) error {
		now := store.Now()
		s := d.Script(in.ID)
		if s == nil {
			if in.CreatedBy == "" {
				in.CreatedBy = "user"
			}
			in.ID, in.Builtin, in.CreatedAt = store.NewID(), false, now
			s = &store.Script{}
			d.Scripts = append(d.Scripts, s)
		} else if s.Builtin {
			return errors.New("内置脚本不能修改，请先复制为新脚本")
		} else {
			in.CreatedAt, in.CreatedBy, in.Builtin = s.CreatedAt, s.CreatedBy, false
		}
		in.UpdatedAt = now
		*s = in
		saved = in
		return nil
	})
	return saved, err
}

func (a *App) DuplicateScript(id string) (store.Script, error) {
	var src *store.Script
	a.store.Read(func(d *store.Data) {
		if s := d.Script(id); s != nil {
			cp := *s
			src = &cp
		}
	})
	if src == nil {
		return store.Script{}, errors.New("脚本不存在")
	}
	src.ID, src.Builtin, src.CreatedBy = "", false, "user"
	src.Name += "（副本）"
	src.Params = slices.Clone(src.Params)
	return a.SaveScript(*src)
}

func (a *App) DeleteScript(id string) error {
	return a.store.Update(func(d *store.Data) error {
		s := d.Script(id)
		if s == nil {
			return errors.New("脚本不存在")
		}
		if s.Builtin {
			return errors.New("内置脚本不能删除")
		}
		d.Scripts = slices.DeleteFunc(d.Scripts, func(x *store.Script) bool { return x.ID == id })
		return nil
	})
}

type RunRequest struct {
	ScriptID    string            `json:"scriptId"`
	Content     string            `json:"content"`
	Interpreter string            `json:"interpreter"`
	ServerIDs   []string          `json:"serverIds"`
	Params      map[string]string `json:"params"`
	Title       string            `json:"title"`
	TimeoutSec  int               `json:"timeoutSec"`
}

type runPlan struct {
	kind, title, scriptID, content, interp string
	env                                    map[string]string
	readOnly                               bool
}

// planRun 解析脚本与参数（必填参数校验、默认值填充）。
func (a *App) planRun(req RunRequest) (*runPlan, error) {
	p := &runPlan{kind: "command", title: strings.TrimSpace(req.Title), content: req.Content, interp: req.Interpreter, env: map[string]string{}}
	if req.ScriptID != "" {
		var sc *store.Script
		a.store.Read(func(d *store.Data) {
			if s := d.Script(req.ScriptID); s != nil {
				cp := *s
				sc = &cp
			}
		})
		if sc == nil {
			return nil, errors.New("脚本不存在")
		}
		p.kind, p.scriptID, p.content, p.interp, p.readOnly = "script", sc.ID, sc.Content, sc.Interpreter, sc.ReadOnly
		if p.title == "" {
			p.title = sc.Name
		}
		for _, param := range sc.Params {
			v, ok := req.Params[param.Name]
			if !ok || v == "" {
				v = param.Default
			}
			if param.Required && strings.TrimSpace(v) == "" {
				label := param.Label
				if label == "" {
					label = param.Name
				}
				return nil, fmt.Errorf("请填写参数「%s」", label)
			}
			p.env[param.Name] = v
		}
	}
	for k, v := range req.Params {
		if _, ok := p.env[k]; !ok && envName(k) {
			p.env[k] = v
		}
	}
	if strings.TrimSpace(p.content) == "" {
		return nil, errors.New("没有要执行的内容")
	}
	if p.title == "" {
		p.title = clipText(strings.TrimSpace(strings.SplitN(p.content, "\n", 2)[0]), 40)
	}
	return p, nil
}

func clipText(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// utf8Splitter 在流式输出中保留不完整的多字节字符，避免中文被截断成乱码。
type utf8Splitter struct{ rest []byte }

func (u *utf8Splitter) push(p []byte) string {
	buf := append(u.rest, p...)
	cut := len(buf)
	for i := len(buf) - 1; i >= 0 && i >= len(buf)-3; i-- {
		if utf8.RuneStart(buf[i]) {
			if !utf8.FullRune(buf[i:]) {
				cut = i
			}
			break
		}
	}
	u.rest = append([]byte(nil), buf[cut:]...)
	return strings.ToValidUTF8(string(buf[:cut]), "")
}

// executeRun 在多台服务器上执行脚本，stream 为 true 时把输出实时推送到前端。
func (a *App) executeRun(ctx context.Context, rec *store.RunRecord, serverIDs []string, p *runPlan, timeout time.Duration, stream bool) []store.RunResult {
	ids := uniqueIDs(serverIDs)
	results := make([]store.RunResult, len(ids))
	pos := map[string]int{}
	for i, id := range ids {
		pos[id] = i
	}
	a.parallel(ids, func(id string) {
		start := time.Now()
		var mu sync.Mutex
		var combined strings.Builder
		sp := &utf8Splitter{}
		onOutput := func(_ string, data []byte) {
			mu.Lock()
			text := sp.push(data)
			if combined.Len() < 512*1024 {
				combined.WriteString(text)
			}
			mu.Unlock()
			if stream && text != "" {
				a.emit("run:output", map[string]any{"runId": rec.ID, "serverId": id, "data": text})
			}
		}
		res, err := a.pool.RunScript(ctx, id, sshx.Script{Content: p.content, Interpreter: p.interp, Env: p.env},
			sshx.ExecOptions{Privileged: true, Timeout: timeout, OnOutput: onOutput})
		r := store.RunResult{ServerID: id, ServerName: a.serverName(id), DurationMs: time.Since(start).Milliseconds()}
		mu.Lock()
		r.Output = combined.String()
		mu.Unlock()
		if err != nil {
			r.Error, r.ExitCode = err.Error(), -1
		} else {
			r.ExitCode = res.ExitCode
		}
		results[pos[id]] = r
		if stream {
			a.emit("run:server_done", map[string]any{"runId": rec.ID, "result": r})
		}
	})
	return results
}

// RunScript 异步执行脚本（或临时命令），返回执行记录 ID。
func (a *App) RunScript(req RunRequest) (string, error) {
	ids := uniqueIDs(req.ServerIDs)
	if len(ids) == 0 {
		return "", errors.New("请选择至少一台服务器")
	}
	p, err := a.planRun(req)
	if err != nil {
		return "", err
	}
	timeout := a.commandTimeout()
	if req.TimeoutSec > 0 {
		timeout = time.Duration(req.TimeoutSec) * time.Second
	}
	rec := &store.RunRecord{ID: store.NewID(), Kind: p.kind, Title: p.title, ScriptID: p.scriptID,
		Command: clipText(p.content, 4000), Status: "running", StartedAt: store.Now(), Results: []store.RunResult{}}
	a.store.AddRun(rec)
	ctx, cancel := context.WithCancel(context.Background())
	a.runsMu.Lock()
	a.runCancels[rec.ID] = cancel
	a.runsMu.Unlock()
	a.emit("run:start", map[string]any{"runId": rec.ID, "serverIds": ids, "title": rec.Title})
	go func() {
		defer cancel()
		results := a.executeRun(ctx, rec, ids, p, timeout, true)
		final := a.store.FinishRun(rec.ID, results, ctx.Err() != nil)
		a.runsMu.Lock()
		delete(a.runCancels, rec.ID)
		a.runsMu.Unlock()
		a.emit("run:done", map[string]any{"runId": rec.ID, "record": final})
	}()
	return rec.ID, nil
}

func (a *App) CancelRun(runID string) {
	a.runsMu.Lock()
	cancel := a.runCancels[runID]
	a.runsMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *App) ListRuns(limit int) []store.RunRecord {
	if limit <= 0 {
		limit = 100
	}
	return a.store.ListRuns(limit)
}

func (a *App) GetRun(id string) (store.RunRecord, error) {
	r, ok := a.store.GetRun(id)
	if !ok {
		return r, errors.New("执行记录不存在")
	}
	return r, nil
}

// recordOp 把非脚本类的批量操作（证书部署、服务操作等）写入执行历史。
func (a *App) recordOp(kind, title, command string, results []store.RunResult) {
	rec := &store.RunRecord{ID: store.NewID(), Kind: kind, Title: title, Command: command, Status: "running", StartedAt: store.Now()}
	a.store.AddRun(rec)
	a.store.FinishRun(rec.ID, results, false)
}
