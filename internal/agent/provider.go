package agent

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"sshpilot/internal/store"
)

type ChatRequest struct {
	System    string
	Messages  []Message
	Tools     []*Tool
	MaxTokens int
	Effort    string
}

type ChatResponse struct {
	Message      Message
	StopReason   string // end_turn | tool_use | max_tokens | refusal | 其他
	InputTokens  int64
	OutputTokens int64
	Model        string
}

type StreamHandler struct {
	OnText     func(string)
	OnThinking func(string)
	OnReset    func() // 发生模型回退时，丢弃已流式显示的部分输出
	OnToolUse  func(id, name string)
}

func (h StreamHandler) text(s string) {
	if h.OnText != nil && s != "" {
		h.OnText(s)
	}
}

func (h StreamHandler) thinking(s string) {
	if h.OnThinking != nil && s != "" {
		h.OnThinking(s)
	}
}

type Provider interface {
	Name() string
	Chat(ctx context.Context, req ChatRequest, h StreamHandler) (*ChatResponse, error)
}

func isLocalURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	h := u.Hostname()
	return h == "localhost" || h == "127.0.0.1" || h == "::1" || strings.HasSuffix(h, ".local")
}

// providerFactory 是创建模型客户端的入口，测试中可替换为假实现。
var providerFactory = NewProvider

// NewProvider 根据设置创建模型客户端。
func NewProvider(cfg store.AISettings) (Provider, error) {
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("请先在「设置 → AI 模型」中选择模型")
	}
	switch cfg.Provider {
	case "openai":
		if strings.TrimSpace(cfg.BaseURL) == "" {
			return nil, errors.New("请在设置中填写接口地址（Base URL）")
		}
		if cfg.APIKey == "" && !isLocalURL(cfg.BaseURL) {
			return nil, errors.New("请先在「设置 → AI 模型」中填写 API Key")
		}
		return newOpenAI(cfg), nil
	default:
		if cfg.APIKey == "" {
			return nil, errors.New("请先在「设置 → AI 模型」中填写 Claude API Key")
		}
		return newAnthropic(cfg), nil
	}
}

// required 读取 JSON Schema 中的 required 列表。
func required(schema map[string]any) []string {
	switch v := schema["required"].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
