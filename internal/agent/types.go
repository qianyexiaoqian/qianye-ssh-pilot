package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Block 是 Anthropic 原生内容块的精简副本。thinking 块的签名必须原样回传，
// 因此助手消息在本地按块保存，回放时逐块还原。
type Block struct {
	Type      string          `json:"type"` // text | thinking | redacted_thinking | tool_use
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	Data      string          `json:"data,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
}

// Message 是与模型无关的会话消息。
type Message struct {
	ID         string     `json:"id"`
	Role       string     `json:"role"` // user | assistant | tool
	Content    string     `json:"content"`
	Display    string     `json:"display,omitempty"` // 用户消息在界面上显示的文本（不含系统提醒）
	Thinking   string     `json:"thinking,omitempty"`
	ToolCalls  []ToolCall `json:"toolCalls,omitempty"`
	ToolCallID string     `json:"toolCallId,omitempty"`
	ToolName   string     `json:"toolName,omitempty"`
	IsError    bool       `json:"isError,omitempty"`
	Meta       any        `json:"meta,omitempty"`
	Blocks     []Block    `json:"blocks,omitempty"`
	Provider   string     `json:"provider,omitempty"`
	Model      string     `json:"model,omitempty"`
	Kind       string     `json:"kind,omitempty"` // notice：只在界面显示，不发送给模型
	CreatedAt  int64      `json:"createdAt"`
}

// ToolResult 是工具执行结果：Content 发送给模型，Meta 供界面渲染。
type ToolResult struct {
	Content string
	Meta    any
	IsError bool
}

type Tool struct {
	Name        string
	Description string
	Schema      map[string]any
	Handler     func(ctx context.Context, tc *ToolContext, args json.RawMessage) (*ToolResult, error)
}

type ApprovalRequest struct {
	ID         string   `json:"id"`
	SessionID  string   `json:"sessionId"`
	ToolCallID string   `json:"toolCallId"`
	Tool       string   `json:"tool"`
	Title      string   `json:"title"`
	Detail     string   `json:"detail"`
	Servers    []string `json:"servers"`
	Risk       string   `json:"risk"` // safe | change | danger
	Reason     string   `json:"reason"`
}

// Event 通过 "agent:event" 推送给前端。
type Event struct {
	SessionID string           `json:"sessionId"`
	Type      string           `json:"type"`
	Text      string           `json:"text,omitempty"`
	Message   *Message         `json:"message,omitempty"`
	ToolCall  *ToolCall        `json:"toolCall,omitempty"`
	Approval  *ApprovalRequest `json:"approval,omitempty"`
	Learn     *LearnEvent      `json:"learn,omitempty"`
	Error     string           `json:"error,omitempty"`
	Status    string           `json:"status,omitempty"`
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func nowMs() int64 { return time.Now().UnixMilli() }

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func strEnum(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": values}
}

func integer(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }

func strArray(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

// Schema 辅助函数供应用层定义工具时使用。
var (
	Obj      = obj
	Str      = str
	StrEnum  = strEnum
	Int      = integer
	StrArray = strArray
)
