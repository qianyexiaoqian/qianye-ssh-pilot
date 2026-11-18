package main

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"net"
	"net/http"
	"regexp"
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

// mockUploadDir 是假模型调用 upload_attachment 时填写的目标目录；测试和演示环境把它指到假服务器的主目录下面。
var mockUploadDir = "/tmp/sshpilot-demo/uploads/"

// mockTargetArgs 是假模型调用 save_deploy_target 时填写的参数（JSON）：用户说“把……证书部署到……”时，
// 假模型先看证书库，再用这组参数添加部署目标并部署。为空时不走这个流程。
var mockTargetArgs = ""

// mockImportArgs 是假模型调用 import_certificate 时填写的参数（JSON）：用户说“把这台的证书收进证书中心”时，
// 假模型先在服务器上找证书，再用这组参数导入。为空时不走这个流程。
var mockImportArgs = ""

// mockDownloadArgs 是假模型调用 download_file 时填写的参数（JSON）：用户说“把……下载下来”时使用。
var mockDownloadArgs = ""

// mockServerArgs 是假模型调用 save_server 时填写的参数（JSON）：用户说“把这台服务器加进来”时使用。
var mockServerArgs = ""

// mockAttachRe 匹配附件说明里的一行：ID、文件名和类型。
var mockAttachRe = regexp.MustCompile(`- ([a-f0-9]{16})「([^」]+)」：(文本文件|二进制文件|图片)([^\n]*)`)

// mockContent 取出一条消息的文字和图片数量（带图片时 content 是数组）。
func mockContent(c any) (text string, images int) {
	switch v := c.(type) {
	case string:
		return v, 0
	case []any:
		for _, p := range v {
			part, _ := p.(map[string]any)
			switch part["type"] {
			case "text":
				s, _ := part["text"].(string)
				text += s
			case "image_url":
				images++
			}
		}
	}
	return text, images
}

// startMockModel 启动一个按对话进度返回预设内容的 OpenAI 兼容流式接口（仅用于界面验收）。
// 模型名为 mock-novision 时，像不支持图片的模型一样拒绝带图片的请求。
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
			Model    string `json:"model"`
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
		turnText, turnImages := "", 0
		for _, m := range body.Messages {
			if m.Role == "user" {
				turnText, turnImages = mockContent(m.Content)
				if turnImages > 0 && body.Model == "mock-novision" {
					http.Error(w, `{"error":{"message":"Failed to deserialize the JSON body into the target type: messages[1]: unknown variant `+"`image_url`, expected `text`"+`","type":"invalid_request_error"}}`, http.StatusBadRequest)
					return
				}
			}
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
		done := func() {
			send(map[string]any{}, "stop")
			fmt.Fprint(w, "data: [DONE]\n\n")
			flush()
		}
		// 用户随消息发来了图片或文件：看图、阅读较长的文本、或者按用户说的传到服务器
		if own, _, ok := strings.Cut(turnText, "<attachments>"); ok {
			var file, long []string
			for _, m := range mockAttachRe.FindAllStringSubmatch(turnText, -1) {
				if m[3] != "图片" && file == nil {
					file = m
				}
				if strings.Contains(m[4], "read_attachment") && long == nil {
					long = m
				}
			}
			wantUpload := file != nil && (strings.Contains(own, "上传") || strings.Contains(own, "传到") || strings.Contains(own, "部署"))
			switch {
			case lastTool == "upload_attachment":
				text("**已经传上去了**，文件在服务器的 `" + mockUploadDir + "` 目录里。\n\n接下来可以解压或安装，例如：\n\n```bash\nls -l " + mockUploadDir + "\n```\n")
			case lastTool == "read_attachment":
				text("这份文件我读了开头的部分，格式正常，没有看到报错。需要我接着往后读，或者传到服务器上用 grep 统计吗？")
			case wantUpload:
				think("用户要把文件放到服务器上，用 upload_attachment 传过去。")
				text("好的，我把「" + file[2] + "」传到服务器上，")
				args := map[string]any{"attachment": file[1], "servers": []string{"web-01"}, "path": mockUploadDir}
				if bound {
					args["servers"] = []string{}
				}
				b, _ := json.Marshal(args)
				call("upload_attachment", string(b))
				fmt.Fprint(w, "data: [DONE]\n\n")
				flush()
				return
			case long != nil:
				think("文件比较长，内容没有直接附在消息里，先读开头的部分。")
				text("这个文件比较长，我先读一下开头的部分。")
				call("read_attachment", `{"attachment":"`+long[1]+`","max_lines":20}`)
				fmt.Fprint(w, "data: [DONE]\n\n")
				flush()
				return
			case turnImages > 0:
				text(fmt.Sprintf("我看到了你发来的 %d 张图片。图里是一段终端输出，最后一行报的是 `Permission denied`，多半是当前用户对那个目录没有写入权限。", turnImages))
			case file != nil:
				text("我看了你发来的「" + file[2] + "」，内容已经附在消息里了：配置的写法没有问题。需要的话我可以把它传到服务器上替换现有的配置。")
			default:
				text("图片我这边没有收到内容，只看到了文件名。")
			}
			done()
			return
		}
		toolOnly := func() {
			fmt.Fprint(w, "data: [DONE]\n\n")
			flush()
		}
		// 只看用户自己说的话：后面可能跟着程序附上的长期记忆，里面什么词都可能有
		said, _, _ := strings.Cut(turnText, "<system-reminder>")
		has := func(words ...string) bool {
			for _, word := range words {
				if strings.Contains(said, word) {
					return true
				}
			}
			return false
		}
		// 让 AI 把服务器上的证书收进证书中心：在服务器上找 → 导入 → 结论
		if mockImportArgs != "" && has("证书中心", "证书库") && has("导入", "收进", "上传到", "下载") && !has("部署到") {
			switch lastTool {
			case "":
				think("先看看这台服务器上有哪些证书、放在哪里。")
				text("我先找一下这张证书在服务器上的位置。")
				call("find_certificates", `{"server":"db-01"}`)
			case "find_certificates":
				think("证书和私钥的路径清楚了，直接收进证书库，私钥由程序读取。")
				text("找到了，我把它收进证书中心，")
				call("import_certificate", mockImportArgs)
			default:
				text("**已经收进证书中心了**：以后这张证书在服务器上续期，跟我说一声“更新一下这张证书”就行。需要的话我可以接着把它部署到其他服务器。")
				send(map[string]any{}, "stop")
			}
			toolOnly()
			return
		}
		// 让 AI 把服务器上的文件下载到这台电脑
		if mockDownloadArgs != "" && has("下载", "导出给我") {
			if lastTool == "" {
				think("用户要把服务器上的文件拿到自己的电脑上，用 download_file。")
				text("好的，我把它下载到你的电脑上，")
				call("download_file", mockDownloadArgs)
			} else {
				text("**下载好了**，文件在「下载」文件夹的 QianYe SSH Pilot 目录里，点上面的「打开文件夹」就能看到。")
				send(map[string]any{}, "stop")
			}
			toolOnly()
			return
		}
		// 让 AI 把一台服务器加进来
		if mockServerArgs != "" && has("加进来", "添加服务器", "加一台") {
			if lastTool == "" {
				think("地址和登录方式都给了，直接添加，保存后会自动测试连接。")
				text("好的，我把这台服务器加进来，")
				call("save_server", mockServerArgs)
			} else {
				text("**已经加好了**，连接测试通过。在「服务器」页就能看到它，也可以直接让我在上面做事。")
				send(map[string]any{}, "stop")
			}
			toolOnly()
			return
		}
		// 让 AI 把证书部署到一个还没有部署目标的地方：看证书库 → 添加部署目标并部署 → 结论
		if mockTargetArgs != "" && strings.Contains(turnText, "证书") && strings.Contains(turnText, "部署到") {
			switch lastTool {
			case "":
				think("先看看证书库里这张证书的情况。")
				text("我先看一下证书库。")
				call("list_certificates", `{}`)
			case "list_certificates":
				think("这张证书在那台服务器上还没有部署目标，直接添加并部署。")
				text("这张证书在那台服务器上还没有部署目标，我来添加并部署，")
				call("save_deploy_target", mockTargetArgs)
			default:
				text("**证书已经部署好了**：新的部署目标已经保存，以后证书更新时也会同步到这里。")
				send(map[string]any{}, "stop")
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
			flush()
			return
		}
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
