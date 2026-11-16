package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"sshpilot/internal/store"
)

// openaiProvider 适配 OpenAI 兼容的 /chat/completions 接口（DeepSeek、通义千问、Kimi、智谱、Ollama 等）。
type openaiProvider struct {
	cfg  store.AISettings
	url  string
	http *http.Client
}

func newOpenAI(cfg store.AISettings) *openaiProvider {
	u := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if !strings.HasSuffix(u, "/chat/completions") {
		u += "/chat/completions"
	}
	return &openaiProvider{cfg: cfg, url: u, http: &http.Client{Timeout: 10 * time.Minute}}
}

func (p *openaiProvider) Name() string { return "openai" }

func msgSize(m Message) int {
	n := len(m.Content) + 64
	for _, c := range m.ToolCalls {
		n += len(c.Arguments) + len(c.Name)
	}
	return n
}

// trimForContext 按字符预算保留最近的若干轮对话（以用户消息为边界，保证工具调用与结果成对）。
// 被丢弃的各轮不会直接消失：它们的“问题 → 结论”要点会附在保留下来的第一条用户消息前面。
func trimForContext(msgs []Message, budget int) []Message {
	if budget <= 0 {
		return msgs
	}
	var starts []int
	for i, m := range msgs {
		if m.Role == "user" {
			starts = append(starts, i)
		}
	}
	if len(starts) == 0 {
		return msgs
	}
	total := 0
	keep := starts[len(starts)-1]
	for i := len(msgs) - 1; i >= 0; i-- {
		total += msgSize(msgs[i])
		if msgs[i].Role == "user" {
			if total > budget && i != starts[len(starts)-1] {
				break
			}
			keep = i
		}
	}
	if keep == 0 {
		return msgs
	}
	out := slices.Clone(msgs[keep:])
	if digest := digestTurns(msgs[:keep]); digest != "" {
		out[0].Content = "<system-reminder>\n更早的对话已经超出上下文预算，下面是它们的要点（需要细节时用 session_search 查看原始记录）：\n" + digest + "</system-reminder>\n\n" + out[0].Content
	}
	return out
}

// digestTurns 把若干轮对话压缩成每轮一行的“问题 → 结论”。
func digestTurns(msgs []Message) string {
	type turn struct{ q, a string }
	var turns []turn
	for _, m := range msgs {
		if m.Kind == "notice" {
			continue
		}
		switch m.Role {
		case "user":
			q := m.Display
			if q == "" {
				q = m.Content
			}
			turns = append(turns, turn{q: q})
		case "assistant":
			if len(turns) > 0 && strings.TrimSpace(m.Content) != "" {
				turns[len(turns)-1].a = m.Content
			}
		}
	}
	if len(turns) > 15 {
		turns = turns[len(turns)-15:]
	}
	var b strings.Builder
	for _, t := range turns {
		line := "- 用户：" + clip(t.q, 120)
		if t.a != "" {
			line += " → 助手：" + clip(t.a, 240)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func toOpenAIMessages(system string, msgs []Message) []map[string]any {
	out := []map[string]any{{"role": "system", "content": system}}
	for _, m := range msgs {
		if m.Kind == "notice" {
			continue
		}
		switch m.Role {
		case "user":
			out = append(out, map[string]any{"role": "user", "content": m.Content})
		case "assistant":
			item := map[string]any{"role": "assistant"}
			if r := reasoningOf(m); r != "" {
				item["reasoning_content"] = r
			}
			if len(m.ToolCalls) > 0 {
				calls := make([]map[string]any, 0, len(m.ToolCalls))
				for _, c := range m.ToolCalls {
					calls = append(calls, map[string]any{
						"id": c.ID, "type": "function",
						"function": map[string]any{"name": c.Name, "arguments": string(validJSONObject(c.Arguments))},
					})
				}
				item["tool_calls"] = calls
				item["content"] = nil
				if m.Content != "" {
					item["content"] = m.Content
				}
			} else {
				item["content"] = m.Content
			}
			out = append(out, item)
		case "tool":
			out = append(out, map[string]any{"role": "tool", "tool_call_id": m.ToolCallID, "content": toolResultText(m)})
		}
	}
	return out
}

// reasoningBlock 标记“模型用 reasoning_content 字段返回的思考内容”。DeepSeek 的思考模式在带工具调用时
// 要求把它原样带回后续请求，否则返回 400；没有用这个字段返回思考内容的服务不会收到它。
const reasoningBlock = "reasoning_content"

func reasoningOf(m Message) string {
	if m.Provider != "openai" {
		return ""
	}
	for _, b := range m.Blocks {
		if b.Type == reasoningBlock {
			return b.Thinking
		}
	}
	return ""
}

type oaChunk struct {
	Choices []struct {
		Delta struct {
			Content          *string `json:"content"`
			ReasoningContent *string `json:"reasoning_content"`
			Reasoning        *string `json:"reasoning"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
	Model string `json:"model"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (p *openaiProvider) Chat(ctx context.Context, req ChatRequest, h StreamHandler) (*ChatResponse, error) {
	msgs := trimForContext(req.Messages, p.cfg.ContextChars)
	body := map[string]any{
		"model":    p.cfg.Model,
		"messages": toOpenAIMessages(req.System, msgs),
		"stream":   true,
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{
				"name": t.Name, "description": t.Description, "parameters": t.Schema,
			}})
		}
		body["tools"] = tools
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if p.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	}
	resp, err := p.http.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, fmt.Errorf("无法连接模型接口 %s：%v", p.url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		hint := ""
		if resp.StatusCode == http.StatusBadRequest && strings.Contains(string(raw), "reasoning_content") {
			hint = "（这个对话里有其他模型留下的记录，当前模型无法接着处理，请新建一个对话）"
		}
		return nil, fmt.Errorf("模型接口返回 HTTP %d：%s%s", resp.StatusCode, apiErrorMessage(string(raw)), hint)
	}

	out := Message{Role: "assistant", Provider: "openai", Model: p.cfg.Model}
	var text, reasoning, echo strings.Builder
	type pendingCall struct{ id, name, args string }
	calls := map[int]*pendingCall{}
	var order []int
	finish := ""
	var usageIn, usageOut int64

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var ch oaChunk
		if json.Unmarshal([]byte(data), &ch) != nil {
			continue
		}
		if ch.Error != nil {
			return nil, fmt.Errorf("模型接口错误：%s", ch.Error.Message)
		}
		if ch.Model != "" {
			out.Model = ch.Model
		}
		if ch.Usage != nil {
			usageIn, usageOut = ch.Usage.PromptTokens, ch.Usage.CompletionTokens
		}
		for _, c := range ch.Choices {
			if c.Delta.Content != nil {
				text.WriteString(*c.Delta.Content)
				h.text(*c.Delta.Content)
			}
			for _, r := range []*string{c.Delta.ReasoningContent, c.Delta.Reasoning} {
				if r != nil {
					reasoning.WriteString(*r)
					h.thinking(*r)
				}
			}
			if c.Delta.ReasoningContent != nil {
				echo.WriteString(*c.Delta.ReasoningContent)
			}
			for _, tc := range c.Delta.ToolCalls {
				pc := calls[tc.Index]
				if pc == nil {
					pc = &pendingCall{}
					calls[tc.Index] = pc
					order = append(order, tc.Index)
				}
				if tc.ID != "" {
					pc.id = tc.ID
				}
				if tc.Function.Name != "" {
					if pc.name == "" && h.OnToolUse != nil {
						h.OnToolUse(pc.id, tc.Function.Name)
					}
					pc.name += tc.Function.Name
				}
				pc.args += tc.Function.Arguments
			}
			if c.FinishReason != nil && *c.FinishReason != "" {
				finish = *c.FinishReason
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("读取流式响应失败：%v", err)
	}
	slices.Sort(order)
	for n, idx := range order {
		pc := calls[idx]
		if pc.name == "" {
			continue
		}
		if pc.id == "" {
			pc.id = fmt.Sprintf("call_%d_%s", n, newID())
		}
		if strings.TrimSpace(pc.args) == "" {
			pc.args = "{}"
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: pc.id, Name: pc.name, Arguments: pc.args})
	}
	out.Content = text.String()
	out.Thinking = reasoning.String()
	if echo.Len() > 0 {
		out.Blocks = []Block{{Type: reasoningBlock, Thinking: echo.String()}}
	}
	stop := "end_turn"
	switch finish {
	case "tool_calls", "function_call":
		stop = "tool_use"
	case "length":
		stop = "max_tokens"
	case "content_filter":
		stop = "refusal"
	}
	if len(out.ToolCalls) > 0 && stop == "end_turn" {
		stop = "tool_use"
	}
	return &ChatResponse{Message: out, StopReason: stop, InputTokens: usageIn, OutputTokens: usageOut, Model: out.Model}, nil
}
