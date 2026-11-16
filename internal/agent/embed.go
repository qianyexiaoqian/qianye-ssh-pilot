package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"

	"sshpilot/internal/store"
)

// Embedder 把文本变成向量，长期记忆靠它按“意思”而不是按字面查找。
// 有两种实现：apiEmbedder 调用 OpenAI 兼容的 /embeddings 接口（语义向量）；
// lexicalEmbedder 是程序内置的关键词向量，不需要联网，在没有配置向量模型时使用。
type Embedder interface {
	// ID 标识一个向量空间：接口地址、模型或维度变了，以前算好的向量就不能再用。
	ID() string
	// Label 是显示给用户看的名称。
	Label() string
	// Semantic 表示是否为语义向量（false 为内置的关键词匹配）。
	Semantic() bool
	// Embed 返回与 texts 一一对应的单位向量。
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

const (
	embedBatch    = 10   // 阿里云百炼 text-embedding-v4 单次最多 10 条，按最小的限制分批
	embedMaxRunes = 4000 // 单条文本的长度上限（各家接口的上限从 512 到 8192 token 不等）
)

// EmbedConfigured 判断向量模型是否已经填好（本地接口可以不填 API Key）。
func EmbedConfigured(cfg store.EmbedSettings) bool {
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.Model) == "" {
		return false
	}
	return cfg.APIKey != "" || isLocalURL(cfg.BaseURL)
}

// NewEmbedder 根据设置创建向量模型；没有配置时返回内置的关键词向量。
func NewEmbedder(cfg store.EmbedSettings) Embedder {
	if !EmbedConfigured(cfg) {
		return lexicalEmbedder{}
	}
	return newAPIEmbedder(cfg)
}

// ---- OpenAI 兼容接口 ----

type apiEmbedder struct {
	url   string
	key   string
	model string
	dims  int
	http  *http.Client
}

func newAPIEmbedder(cfg store.EmbedSettings) *apiEmbedder {
	u := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if !strings.HasSuffix(u, "/embeddings") {
		u += "/embeddings"
	}
	return &apiEmbedder{url: u, key: strings.TrimSpace(cfg.APIKey), model: strings.TrimSpace(cfg.Model), dims: max(cfg.Dimensions, 0),
		http: &http.Client{Timeout: 60 * time.Second}}
}

func (e *apiEmbedder) ID() string {
	return fmt.Sprintf("api|%s|%s|%d", e.url, e.model, e.dims)
}

func (e *apiEmbedder) Label() string { return e.model }

func (e *apiEmbedder) Semantic() bool { return true }

func (e *apiEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += embedBatch {
		batch := make([]string, 0, embedBatch)
		for _, t := range texts[start:min(start+embedBatch, len(texts))] {
			t = strings.TrimSpace(t)
			if t == "" {
				t = "（空）" // 接口不接受空字符串
			}
			if r := []rune(t); len(r) > embedMaxRunes {
				t = string(r[:embedMaxRunes])
			}
			batch = append(batch, t)
		}
		vecs, err := e.request(ctx, batch)
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

func (e *apiEmbedder) request(ctx context.Context, batch []string) ([][]float32, error) {
	body := map[string]any{"model": e.model, "input": batch, "encoding_format": "float"}
	if e.dims > 0 {
		body["dimensions"] = e.dims
	}
	raw, _ := json.Marshal(body)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 800 * time.Millisecond):
			}
		}
		vecs, retry, err := e.once(ctx, raw, len(batch))
		if err == nil {
			return vecs, nil
		}
		lastErr = err
		if !retry || ctx.Err() != nil {
			break
		}
	}
	return nil, lastErr
}

type embedDatum struct {
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

// once 发送一次请求；retry 表示这个错误值得重试（限流、服务端错误、网络抖动）。
func (e *apiEmbedder) once(ctx context.Context, raw []byte, want int) (vecs [][]float32, retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(raw))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.key != "" {
		req.Header.Set("Authorization", "Bearer "+e.key)
	}
	resp, err := e.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		return nil, true, fmt.Errorf("连接向量接口失败：%w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode != http.StatusOK {
		retry = resp.StatusCode == 429 || resp.StatusCode >= 500
		return nil, retry, fmt.Errorf("向量接口返回 %d：%s", resp.StatusCode, strings.TrimSpace(apiErrorMessage(string(data))))
	}
	var parsed struct {
		Data []embedDatum `json:"data"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, false, fmt.Errorf("向量接口返回的内容无法解析：%w", err)
	}
	if len(parsed.Data) != want {
		return nil, false, fmt.Errorf("向量接口返回了 %d 条结果，应为 %d 条", len(parsed.Data), want)
	}
	slices.SortStableFunc(parsed.Data, func(a, b embedDatum) int { return a.Index - b.Index })
	vecs = make([][]float32, want)
	for i, d := range parsed.Data {
		if len(d.Embedding) == 0 {
			return nil, false, errors.New("向量接口返回了空向量，请确认填写的是向量（embedding）模型")
		}
		if i > 0 && len(d.Embedding) != len(vecs[0]) {
			return nil, false, errors.New("向量接口返回的向量维度不一致")
		}
		vecs[i] = normalize(d.Embedding)
	}
	return vecs, false, nil
}

// ---- 内置的关键词向量 ----

// lexicalEmbedder 是不联网的关键词匹配：英文与数字按单词（web-01、/etc/nginx、8080），中文按相邻两个字。
// 它不理解同义词，但对服务器名、路径、端口这类运维里最常见的检索词很准。
// 长期记忆在这种模式下直接比较稀疏的词向量（lexSparse），不经过 Embed；Embed 只是为了满足接口，
// 用“哈希技巧”把词映射到固定维度。
type lexicalEmbedder struct{}

const lexDims = 768

func (lexicalEmbedder) ID() string { return "lexical|v1" }

func (lexicalEmbedder) Label() string { return "内置关键词匹配" }

func (lexicalEmbedder) Semantic() bool { return false }

func (lexicalEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = lexVector(t)
	}
	return out, nil
}

// 这些字单独出现时没有检索价值，包含它们的“相邻两个字”通常也只是跨词的碎片（“的负”“了一”）
const lexStopRunes = "的了是在和有不这那我你他它们个也就都而及与或一上下中为以到对把被让请吗呢吧啊着过会能要可很还等么又再给从向才只"

// 这些词在提问和记录里到处都是，不能说明两段文字讲的是同一件事
var lexStopWords = func() map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields("帮忙 看看 什么 怎么 怎样 如何 原因 问题 操作 结论 用到 情况 需要 现在 这次 上次 之前 以前 以后 已经 然后 因为 所以 如果 但是 时候 知道 告诉 进行 通过 使用 目前 是否 多少 哪个 哪些 哪里 处理 麻烦 谢谢 一直 应该 可能 就是 出现 发现 检查 查看 确认 帮助 多半 左右 属于 正常 服务 导致 恢复") {
		out[w] = true
	}
	return out
}()

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r)
}

func isWordRune(r rune) bool {
	return r < 128 && (r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
}

// isJoinRune 是可以出现在一个“单词”中间的符号：web-01、nginx.conf、/etc/nginx/ssl、10.0.0.8:443。
func isJoinRune(r rune) bool {
	return r == '-' || r == '_' || r == '.' || r == '/' || r == ':'
}

// lexTokens 把文本切成带权重的词。
func lexTokens(s string) map[string]float32 {
	tf := map[string]float32{}
	add := func(tok string, w float32) {
		if tok != "" {
			tf[tok] += w
		}
	}
	rs := []rune(strings.ToLower(s))
	for i := 0; i < len(rs); {
		r := rs[i]
		switch {
		case isWordRune(r) || (r == '/' && i+1 < len(rs) && isWordRune(rs[i+1])):
			j := i
			for j < len(rs) && (isWordRune(rs[j]) || isJoinRune(rs[j])) {
				j++
			}
			word := strings.TrimRight(string(rs[i:j]), "-_./:")
			add("w:"+word, 2)
			// 路径、带连字符的名称再拆开：/etc/nginx/ssl 也能被 "nginx" 找到
			parts := strings.FieldsFunc(word, func(r rune) bool { return isJoinRune(r) })
			if len(parts) > 1 {
				for _, p := range parts {
					// web-01、db-01 里的 "01" 这类很短的纯数字片段不单独成词，否则名字相似的服务器会互相匹配
					if len(p) >= 2 && !(len(p) < 3 && strings.Trim(p, "0123456789") == "") {
						add("w:"+p, 1)
					}
				}
			}
			i = j
		case isCJK(r):
			stop := strings.ContainsRune(lexStopRunes, r)
			if !stop {
				add("c:"+string(r), 0.3)
			}
			if i+1 < len(rs) && isCJK(rs[i+1]) && !stop && !strings.ContainsRune(lexStopRunes, rs[i+1]) {
				if bi := string(rs[i : i+2]); !lexStopWords[bi] {
					add("b:"+bi, 1)
				}
			}
			i++
		default:
			i++
		}
	}
	return tf
}

// lexWeight 把累计的词频换成权重：取对数，避免一个词重复很多次时盖过其他词。
func lexWeight(w float32) float32 {
	if w < 1 {
		return w
	}
	return float32(1 + math.Log(float64(w)))
}

// lexSparse 把文本变成归一化的稀疏词向量（词 → 权重）。
func lexSparse(s string) map[string]float32 {
	v := lexTokens(s)
	var sum float64
	for tok, w := range v {
		w = lexWeight(w)
		v[tok] = w
		sum += float64(w) * float64(w)
	}
	if sum > 0 {
		inv := float32(1 / math.Sqrt(sum))
		for tok := range v {
			v[tok] *= inv
		}
	}
	return v
}

// sparseDot 计算两个归一化稀疏向量的余弦相似度。
func sparseDot(a, b map[string]float32) float64 {
	if len(a) > len(b) {
		a, b = b, a
	}
	var sum float32
	for tok, x := range a {
		sum += x * b[tok]
	}
	return float64(sum)
}

func lexVector(s string) []float32 {
	v := make([]float32, lexDims)
	for tok, w := range lexTokens(s) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(tok))
		sum := h.Sum32()
		val := lexWeight(w)
		if sum&0x80000000 != 0 {
			val = -val
		}
		v[sum%lexDims] += val
	}
	return normalize(v)
}

// lexCalibration 是内置关键词匹配的相似度分布（固定值，启动时算一次）。
func lexCalibration() Calibration {
	var cal Calibration
	for _, c := range calibrationSet {
		q := lexSparse(c.query)
		cal.Related += sparseDot(q, lexSparse(c.related))
		cal.Unrelated += sparseDot(q, lexSparse(c.unrelated))
	}
	n := float64(len(calibrationSet))
	cal.Related /= n
	cal.Unrelated /= n
	return cal
}

// identTokens 取出文本里的英文与数字单词（服务器名、路径、端口等标识符），用来给语义检索做字面加权。
func identTokens(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for tok := range lexTokens(s) {
		if strings.HasPrefix(tok, "w:") && len(tok) >= 5 {
			out[tok] = struct{}{}
		}
	}
	return out
}

// ---- 向量运算 ----

func normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return v
	}
	inv := float32(1 / math.Sqrt(sum))
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x * inv
	}
	return out
}

// dot 计算两个单位向量的余弦相似度。
func dot(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var sum float32
	for i := range a {
		sum += a[i] * b[i]
	}
	return float64(sum)
}

// ---- 校准 ----

// 不同向量模型的相似度分布差别很大（有的“相关”是 0.75，有的只有 0.45），固定的阈值不通用。
// 这里用几组“问题 ↔ 相关记忆 / 无关记忆”量出这个模型的分布，阈值取两者之间。
var calibrationSet = []struct{ query, related, unrelated string }{
	{"nginx 报 502 怎么排查",
		"web-01 的 Nginx 把请求转发到本机 8080 端口的 Java 服务，出现 502 多半是后端进程退出了",
		"db-01 每周日凌晨 3 点做 MySQL 全量备份，备份文件放在 /data/backup"},
	{"磁盘空间不够了",
		"app-02 的 /var/log 增长很快，磁盘告警时先清理 journal 和旧的应用日志",
		"用户偏好先给结论再给细节，重启生产服务前要先确认"},
	{"证书快过期了怎么续期",
		"站点证书由 certbot 在 web-01 上自动续期，续期后要同步到其他服务器并 reload nginx",
		"redis-01 的最大内存设置为 4GB，淘汰策略是 allkeys-lru"},
}

// Calibration 是一个向量模型的相似度分布。
type Calibration struct {
	Related   float64 `json:"related"`   // 相关内容的平均相似度
	Unrelated float64 `json:"unrelated"` // 无关内容的平均相似度
}

func (c Calibration) valid() bool { return c.Related > c.Unrelated }

// threshold 返回召回的相关度下限；loose 用于用户或 AI 主动检索，条件放宽一些。
func (c Calibration) threshold(loose bool) float64 {
	if !c.valid() {
		return 0.5
	}
	k := 0.45
	if loose {
		k = 0.2
	}
	return c.Unrelated + k*(c.Related-c.Unrelated)
}

// Calibrate 量出向量模型区分相关与无关内容的能力。
func Calibrate(ctx context.Context, e Embedder) (Calibration, error) {
	texts := make([]string, 0, len(calibrationSet)*3)
	for _, c := range calibrationSet {
		texts = append(texts, c.query, c.related, c.unrelated)
	}
	vecs, err := e.Embed(ctx, texts)
	if err != nil {
		return Calibration{}, err
	}
	var cal Calibration
	for i := range calibrationSet {
		cal.Related += dot(vecs[i*3], vecs[i*3+1])
		cal.Unrelated += dot(vecs[i*3], vecs[i*3+2])
	}
	n := float64(len(calibrationSet))
	cal.Related /= n
	cal.Unrelated /= n
	return cal, nil
}
