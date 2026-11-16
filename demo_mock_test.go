package main

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode"
)

// mockEmbeddings 模拟 OpenAI 兼容的 /embeddings 接口：把文本里的英文单词和相邻两个汉字哈希到 64 维，
// 字面上有重叠的文本向量也接近，足够验收“向量模型”这条链路（真实模型按语义计算）。
func mockEmbeddings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Input) == 0 || len(body.Input) > 10 {
		http.Error(w, `{"error":{"message":"input must contain 1 to 10 strings"}}`, http.StatusBadRequest)
		return
	}
	if r.Header.Get("Authorization") == "" {
		http.Error(w, `{"error":{"message":"Incorrect API key provided."}}`, http.StatusUnauthorized)
		return
	}
	data := make([]map[string]any, len(body.Input))
	for i, text := range body.Input {
		data[i] = map[string]any{"index": i, "object": "embedding", "embedding": mockVector(text)}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data, "model": body.Model})
}

func mockVector(text string) []float32 {
	const dims = 64
	v := make([]float32, dims)
	add := func(tok string, weight float32) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(tok))
		v[h.Sum32()%dims] += weight
	}
	rs := []rune(strings.ToLower(text))
	for i := 0; i < len(rs); i++ {
		switch {
		case rs[i] < 128 && (unicode.IsLetter(rs[i]) || unicode.IsDigit(rs[i])):
			j := i
			for j < len(rs) && rs[j] < 128 && (unicode.IsLetter(rs[j]) || unicode.IsDigit(rs[j])) {
				j++
			}
			add(string(rs[i:j]), 2)
			i = j - 1
		case unicode.Is(unicode.Han, rs[i]) && i+1 < len(rs) && unicode.Is(unicode.Han, rs[i+1]):
			add(string(rs[i:i+2]), 1)
		}
	}
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum > 0 {
		for i := range v {
			v[i] = float32(float64(v[i]) / math.Sqrt(sum))
		}
	}
	return v
}

// startMockModel 启动一个按对话进度返回预设内容的 OpenAI 兼容流式接口（仅用于界面验收）。
func startMockModel(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/embeddings") {
			mockEmbeddings(w, r)
			return
		}
		var body struct {
			Messages []struct {
				Role      string `json:"role"`
				Content   any    `json:"content"`
				ToolCalls []struct {
					Function struct {
						Name string `json:"name"`
					} `json:"function"`
				} `json:"tool_calls"`
				Reasoning *string `json:"reasoning_content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		// 与 DeepSeek 的思考模式保持一致：带工具调用的助手消息必须把 reasoning_content 原样带回
		bound, steps := false, 0
		for _, m := range body.Messages {
			if m.Role == "system" {
				if c, ok := m.Content.(string); ok && strings.Contains(c, "# 当前终端") {
					bound = true
				}
			}
			if m.Role == "assistant" && len(m.ToolCalls) > 0 {
				steps++
				if m.Reasoning == nil || *m.Reasoning == "" {
					http.Error(w, `{"error":{"message":"Missing reasoning_content field in the assistant message at message index","type":"invalid_request_error"}}`, http.StatusBadRequest)
					return
				}
			}
		}
		lastTool := ""
		if n := len(body.Messages); n > 0 && body.Messages[n-1].Role == "tool" {
			for i := n - 1; i >= 0; i-- {
				if m := body.Messages[i]; m.Role == "assistant" && len(m.ToolCalls) > 0 {
					lastTool = m.ToolCalls[0].Function.Name
					break
				}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flush := func() {
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		send := func(delta map[string]any, finish string) {
			choice := map[string]any{"index": 0, "delta": delta}
			if finish != "" {
				choice["finish_reason"] = finish
			}
			b, _ := json.Marshal(map[string]any{"choices": []any{choice}})
			fmt.Fprintf(w, "data: %s\n\n", b)
			flush()
			time.Sleep(40 * time.Millisecond)
		}
		text := func(s string) {
			for _, part := range strings.SplitAfter(s, "，") {
				send(map[string]any{"content": part}, "")
			}
		}
		call := func(name, args string) {
			send(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_" + name, "type": "function", "function": map[string]any{"name": name, "arguments": args}}}}, "tool_calls")
		}
		think := func(s string) { send(map[string]any{"reasoning_content": s}, "") }
		if bound {
			// 从终端的「AI 运维」发起的对话：看终端 → 只读命令 → 需要批准的修改 → 结论
			switch {
			case lastTool == "":
				think("用户在终端里遇到了问题，先看看终端里刚才的输出。")
				text("我先看一下终端里刚才的输出。")
				call("read_terminal", `{"lines":40}`)
			case lastTool == "read_terminal":
				think("再确认一下这台服务器的负载和磁盘。")
				text("再看看这台服务器的负载和磁盘。")
				call("run_command", `{"command":"uptime; df -h / | tail -n 1","reason":"查看负载和磁盘"}`)
			case steps == 2:
				think("留一个标记文件，验证修改类操作需要用户批准。")
				text("我需要写一个标记文件来确认可以修改，这一步会先征求你的同意。")
				call("run_command", `{"command":"mkdir -p /tmp/sshpilot-demo && touch /tmp/sshpilot-demo/ai-was-here","reason":"写入标记文件"}`)
			default:
				text("**结论：这台服务器运行正常**，负载和磁盘都在正常范围，标记文件已写入。\n\n以后想自己确认，可以在终端里执行：\n\n```bash\nuptime && df -h /\n```\n")
				send(map[string]any{}, "stop")
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
			flush()
			return
		}
		switch lastTool {
		case "":
			send(map[string]any{"reasoning_content": "用户想了解服务器整体情况，先采集各台服务器的系统概况，再根据结果给出建议。"}, "")
			text("好的，我先采集一下所有服务器的系统概况。")
			call("server_status", `{"servers":["all"]}`)
		case "server_status":
			think("把服务器之间的关系记下来。")
			text("web-01 的角色值得记下来，方便以后排查。")
			call("memory", `{"target":"longterm","action":"add","servers":["staging-01"],"content":"staging-01 的 SSH 端口经常不可达，多半是测试环境的防火墙规则被重置"}`)
		case "memory":
			think("负载偏高，建议重载 nginx。")
			text("web-01 的负载偏高，我建议重载一下 nginx 让新配置生效。")
			call("run_command", `{"servers":["web-01"],"command":"nginx -t && systemctl reload nginx","reason":"检测配置后平滑重载 nginx"}`)
		default:
			text("## 巡检结论\n\n| 服务器 | 状态 | 建议 |\n|---|---|---|\n| web-01 | 在线，负载偏高 | 观察 nginx 进程，必要时扩容 |\n| db-01 | 在线 | 无需处理 |\n| staging-01 | **离线** | 检查 SSH 服务与防火墙 |\n\n可以用下面的命令查看 staging-01 的 SSH 状态：\n\n```bash\nsystemctl status sshd --no-pager\n```\n")
			send(map[string]any{}, "stop")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flush()
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return "http://" + ln.Addr().String()
}
