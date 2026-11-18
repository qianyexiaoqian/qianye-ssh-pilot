package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"sshpilot/internal/store"
)

type anthropicProvider struct {
	client   anthropic.Client
	cfg      store.AISettings
	official bool // 直连官方 API：可以使用 beta 特性（服务端回退、工具参数流式）
}

func newAnthropic(cfg store.AISettings) *anthropicProvider {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	base = strings.TrimSuffix(base, "/v1")
	official := base == "" || base == "https://api.anthropic.com"
	opts := []option.RequestOption{option.WithAPIKey(cfg.APIKey), option.WithMaxRetries(2)}
	if !official {
		opts = append(opts, option.WithBaseURL(base+"/"))
	}
	return &anthropicProvider{client: anthropic.NewClient(opts...), cfg: cfg, official: official}
}

func (p *anthropicProvider) Name() string { return "anthropic" }

var claudeModelRe = regexp.MustCompile(`^claude-(opus|sonnet|haiku|fable|mythos)-(\d+)(?:-(\d+))?`)

// modernClaude 判断模型是否支持自适应思考与 effort（Claude 4.6 及以后）。
func modernClaude(model string) bool {
	m := claudeModelRe.FindStringSubmatch(model)
	if m == nil {
		return false
	}
	major, _ := strconv.Atoi(m[2])
	minor := 0
	if m[3] != "" && len(m[3]) <= 2 {
		minor, _ = strconv.Atoi(m[3])
	}
	return major >= 5 || (major == 4 && minor >= 6)
}

func toolResultText(m Message) string {
	if strings.TrimSpace(m.Content) == "" {
		return "（无输出）"
	}
	return m.Content
}

func validJSONObject(s string) json.RawMessage {
	var v map[string]any
	if json.Unmarshal([]byte(s), &v) == nil {
		return json.RawMessage(s)
	}
	return json.RawMessage("{}")
}

func anthropicAssistant(m Message) anthropic.BetaMessageParam {
	var content []anthropic.BetaContentBlockParamUnion
	if m.Provider == "anthropic" && len(m.Blocks) > 0 {
		for _, b := range m.Blocks {
			switch b.Type {
			case "text":
				if b.Text != "" {
					content = append(content, anthropic.NewBetaTextBlock(b.Text))
				}
			case "thinking":
				content = append(content, anthropic.NewBetaThinkingBlock(b.Signature, b.Thinking))
			case "redacted_thinking":
				content = append(content, anthropic.NewBetaRedactedThinkingBlock(b.Data))
			case "tool_use":
				input := b.Input
				if len(input) == 0 {
					input = json.RawMessage("{}")
				}
				content = append(content, anthropic.NewBetaToolUseBlock(b.ID, input, b.Name))
			}
		}
	} else {
		if strings.TrimSpace(m.Content) != "" {
			content = append(content, anthropic.NewBetaTextBlock(m.Content))
		}
		for _, c := range m.ToolCalls {
			content = append(content, anthropic.NewBetaToolUseBlock(c.ID, validJSONObject(c.Arguments), c.Name))
		}
	}
	if len(content) == 0 {
		content = append(content, anthropic.NewBetaTextBlock("（无内容）"))
	}
	return anthropic.BetaMessageParam{Role: anthropic.BetaMessageParamRoleAssistant, Content: content}
}

// anthropicUser 生成用户消息的内容块：图片在前，文字在后。
func anthropicUser(m Message, noImages bool) []anthropic.BetaContentBlockParamUnion {
	text, images := userParts(m, noImages)
	var blocks []anthropic.BetaContentBlockParamUnion
	for _, img := range images {
		blocks = append(blocks, anthropic.NewBetaImageBlock(anthropic.BetaBase64ImageSourceParam{
			Data:      base64.StdEncoding.EncodeToString(img.Data),
			MediaType: anthropic.BetaBase64ImageSourceMediaType(img.Mime),
		}))
	}
	if strings.TrimSpace(text) == "" {
		if len(blocks) > 0 {
			return blocks
		}
		text = "（空消息）"
	}
	return append(blocks, anthropic.NewBetaTextBlock(text))
}

// toAnthropicMessages 把通用消息转换为 Messages API 格式：连续的工具结果合并进同一条 user 消息。
func toAnthropicMessages(msgs []Message, noImages bool) []anthropic.BetaMessageParam {
	var out []anthropic.BetaMessageParam
	var pending []anthropic.BetaContentBlockParamUnion
	flush := func() {
		if len(pending) > 0 {
			out = append(out, anthropic.NewBetaUserMessage(pending...))
			pending = nil
		}
	}
	for _, m := range msgs {
		if m.Kind == "notice" {
			continue
		}
		switch m.Role {
		case "user":
			blocks := append(pending, anthropicUser(m, noImages)...)
			pending = nil
			out = append(out, anthropic.NewBetaUserMessage(blocks...))
		case "assistant":
			flush()
			out = append(out, anthropicAssistant(m))
		case "tool":
			pending = append(pending, anthropic.NewBetaToolResultBlock(m.ToolCallID, toolResultText(m), m.IsError))
		}
	}
	flush()
	return out
}

func (p *anthropicProvider) Chat(ctx context.Context, req ChatRequest, h StreamHandler) (*ChatResponse, error) {
	model := p.cfg.Model
	modern := modernClaude(model)
	tools := make([]anthropic.BetaToolUnionParam, 0, len(req.Tools))
	for _, t := range req.Tools {
		tp := anthropic.BetaToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: anthropic.BetaToolInputSchemaParam{Properties: t.Schema["properties"], Required: required(t.Schema)},
		}
		if p.official {
			tp.EagerInputStreaming = anthropic.Bool(true)
		}
		tools = append(tools, anthropic.BetaToolUnionParam{OfTool: &tp})
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 64000
	}
	params := anthropic.BetaMessageNewParams{
		Model:        anthropic.Model(model),
		MaxTokens:    int64(maxTokens),
		System:       []anthropic.BetaTextBlockParam{{Text: req.System}},
		Messages:     toAnthropicMessages(req.Messages, req.NoImages),
		Tools:        tools,
		CacheControl: anthropic.NewBetaCacheControlEphemeralParam(),
	}
	if modern {
		params.Thinking = anthropic.BetaThinkingConfigParamUnion{OfAdaptive: &anthropic.BetaThinkingConfigAdaptiveParam{
			Display: anthropic.BetaThinkingConfigAdaptiveDisplaySummarized,
		}}
		if req.Effort != "" {
			params.OutputConfig = anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffort(req.Effort)}
		}
		// 安全分类器误拦截时由服务端自动切换到推荐的备用模型（Haiku 不支持回退）
		if p.official && !strings.Contains(model, "haiku") {
			params.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
			params.Betas = append(params.Betas, "server-side-fallback-2026-07-01")
		}
	}

	stream := p.client.Beta.Messages.NewStreaming(ctx, params)
	defer stream.Close()
	var msg anthropic.BetaMessage
	for stream.Next() {
		ev := stream.Current()
		if err := msg.Accumulate(ev); err != nil {
			return nil, fmt.Errorf("解析流式响应失败: %w", err)
		}
		switch ev.Type {
		case "content_block_start":
			switch ev.ContentBlock.Type {
			case "fallback":
				if h.OnReset != nil {
					h.OnReset()
				}
			case "tool_use":
				if h.OnToolUse != nil {
					h.OnToolUse(ev.ContentBlock.ID, ev.ContentBlock.Name)
				}
			}
		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				h.text(ev.Delta.Text)
			case "thinking_delta":
				h.thinking(ev.Delta.Thinking)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return nil, friendlyAnthropicError(err)
	}
	return convertAnthropic(&msg), nil
}

// convertAnthropic 把响应转换为通用消息。服务端回退时，最后一个 fallback 块之前的
// thinking / tool_use 块属于被拒绝的模型，按规范不回传、也不执行。
func convertAnthropic(msg *anthropic.BetaMessage) *ChatResponse {
	lastFallback := -1
	for i, b := range msg.Content {
		if b.Type == "fallback" {
			lastFallback = i
		}
	}
	out := Message{Role: "assistant", Provider: "anthropic", Model: string(msg.Model)}
	var text, thinking strings.Builder
	for i, b := range msg.Content {
		before := i < lastFallback
		switch b.Type {
		case "text":
			if b.Text == "" {
				continue
			}
			out.Blocks = append(out.Blocks, Block{Type: "text", Text: b.Text})
			if !before {
				text.WriteString(b.Text)
			}
		case "thinking":
			if before {
				continue
			}
			out.Blocks = append(out.Blocks, Block{Type: "thinking", Thinking: b.Thinking, Signature: b.Signature})
			if b.Thinking != "" {
				if thinking.Len() > 0 {
					thinking.WriteString("\n\n")
				}
				thinking.WriteString(b.Thinking)
			}
		case "redacted_thinking":
			if !before {
				out.Blocks = append(out.Blocks, Block{Type: "redacted_thinking", Data: b.Data})
			}
		case "tool_use":
			if before {
				continue
			}
			input := b.Input
			if len(input) == 0 {
				input = json.RawMessage("{}")
			}
			out.Blocks = append(out.Blocks, Block{Type: "tool_use", ID: b.ID, Name: b.Name, Input: input})
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: b.ID, Name: b.Name, Arguments: string(input)})
		}
	}
	out.Content = text.String()
	out.Thinking = thinking.String()
	return &ChatResponse{
		Message:      out,
		StopReason:   string(msg.StopReason),
		InputTokens:  msg.Usage.InputTokens + msg.Usage.CacheReadInputTokens + msg.Usage.CacheCreationInputTokens,
		OutputTokens: msg.Usage.OutputTokens,
		Model:        string(msg.Model),
	}
}

func apiErrorMessage(raw string) string {
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(raw), &env) == nil && env.Error.Message != "" {
		return env.Error.Message
	}
	if len(raw) > 300 {
		raw = raw[:300]
	}
	return raw
}

func friendlyAnthropicError(err error) error {
	var apierr *anthropic.Error
	if !errors.As(err, &apierr) {
		if errors.Is(err, context.Canceled) {
			return err
		}
		return fmt.Errorf("无法连接 Claude API：%v", err)
	}
	detail := apiErrorMessage(apierr.RawJSON())
	var hint string
	switch apierr.StatusCode {
	case 400:
		hint = "请求无效"
	case 401:
		hint = "API Key 无效"
	case 403:
		hint = "没有权限访问该模型"
	case 404:
		hint = "模型不存在或当前账号不可用"
	case 413:
		hint = "对话内容过长，请新建会话"
	case 429:
		hint = "请求过于频繁或额度不足"
	case 500, 502, 503:
		hint = "Claude 服务暂时异常"
	case 529:
		hint = "Claude 服务繁忙，请稍后重试"
	default:
		hint = "调用失败"
	}
	return &APIError{Status: apierr.StatusCode, Msg: fmt.Sprintf("%s（HTTP %d）：%s", hint, apierr.StatusCode, detail)}
}
