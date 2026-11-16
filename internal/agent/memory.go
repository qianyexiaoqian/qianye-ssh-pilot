package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"sshpilot/internal/store"
)

// 记忆分为两个文件（与 Hermes 一致）：MEMORY.md 记录环境与经验，USER.md 记录用户画像。
// 条目之间用 § 分隔；容量有上限，超出时由智能体自行合并或删除。
const entrySep = "\n§\n"

var memoryTargets = map[string]struct {
	file  string
	label string
	limit int
}{
	"memory": {"MEMORY.md", "MEMORY（环境与经验笔记）", 3000},
	"user":   {"USER.md", "USER（用户画像）", 1500},
}

type MemoryView struct {
	Target  string   `json:"target"`
	Label   string   `json:"label"`
	Entries []string `json:"entries"`
	Used    int      `json:"used"`
	Limit   int      `json:"limit"`
}

type MemoryStore struct {
	mu  sync.Mutex
	dir string
}

func NewMemoryStore(dir string) *MemoryStore {
	_ = os.MkdirAll(dir, 0o700)
	return &MemoryStore{dir: dir}
}

func (m *MemoryStore) entriesLocked(target string) []string {
	t, ok := memoryTargets[target]
	if !ok {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(m.dir, t.file))
	if err != nil {
		return []string{}
	}
	var out []string
	for _, e := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), entrySep) {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}

func usage(entries []string) int {
	return utf8.RuneCountInString(strings.Join(entries, entrySep))
}

func (m *MemoryStore) saveLocked(target string, entries []string) error {
	t := memoryTargets[target]
	return store.WriteFileAtomic(filepath.Join(m.dir, t.file), []byte(strings.Join(entries, entrySep)+"\n"), 0o600)
}

func (m *MemoryStore) View(target string) MemoryView {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := memoryTargets[target]
	e := m.entriesLocked(target)
	return MemoryView{Target: target, Label: t.label, Entries: e, Used: usage(e), Limit: t.limit}
}

func (m *MemoryStore) usageLine(target string, entries []string) string {
	t := memoryTargets[target]
	u := usage(entries)
	return fmt.Sprintf("%s 使用 %d/%d 字符（%d%%）", strings.ToUpper(target), u, t.limit, u*100/t.limit)
}

// Snapshot 生成注入系统提示词的记忆块（会话开始时冻结，保证提示词缓存稳定）。
func (m *MemoryStore) Snapshot() string {
	var b strings.Builder
	for _, target := range []string{"memory", "user"} {
		v := m.View(target)
		fmt.Fprintf(&b, "══ %s [%d%% — %d/%d 字符] ══\n", v.Label, v.Used*100/v.Limit, v.Used, v.Limit)
		if len(v.Entries) == 0 {
			b.WriteString("（暂无）\n")
		} else {
			b.WriteString(strings.Join(v.Entries, entrySep) + "\n")
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

var (
	zeroWidthRe  = regexp.MustCompile(`[\x{200B}-\x{200F}\x{202A}-\x{202E}\x{2060}-\x{2064}\x{FEFF}]`)
	secretRe     = regexp.MustCompile(`(?i)(password|passwd|pwd|密码|口令|api[_-]?key|secret|token)\s*[:=：]\s*\S{4,}`)
	longBase64Re = regexp.MustCompile(`[A-Za-z0-9+/=]{160,}`)
	injectionRes = []string{"ignore previous instructions", "ignore all previous", "disregard previous", "忽略之前的指令", "忽略以上指令", "忽略所有指令", "new system prompt"}
)

// scanContent 拒绝疑似提示注入、隐藏字符、密钥与大段数据。
func scanContent(content string) error {
	if zeroWidthRe.MatchString(content) {
		return errors.New("内容包含不可见字符，已拒绝")
	}
	lower := strings.ToLower(content)
	for _, p := range injectionRes {
		if strings.Contains(lower, p) {
			return errors.New("内容疑似提示注入，已拒绝")
		}
	}
	if strings.Contains(content, "PRIVATE KEY") || secretRe.MatchString(content) || longBase64Re.MatchString(content) {
		return errors.New("不要在记忆中保存密码、密钥或大段原始数据")
	}
	return nil
}

// Apply 执行 memory 工具的 add / patch / remove 操作，返回给模型的结果描述。
func (m *MemoryStore) Apply(target, action, content, oldText string) (string, error) {
	t, ok := memoryTargets[target]
	if !ok {
		return "", errors.New("target 必须是 memory 或 user")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entries := m.entriesLocked(target)
	content = strings.TrimSpace(content)
	oldText = strings.TrimSpace(oldText)

	find := func() (int, error) {
		if oldText == "" {
			return -1, errors.New("需要提供 old_text 来定位条目")
		}
		if i := slices.Index(entries, oldText); i >= 0 {
			return i, nil
		}
		idx := -1
		for i, e := range entries {
			if strings.Contains(e, oldText) {
				if idx >= 0 {
					return -1, errors.New("old_text 匹配到多个条目，请提供更具体的片段")
				}
				idx = i
			}
		}
		if idx < 0 {
			return -1, errors.New("没有找到包含 old_text 的条目")
		}
		return idx, nil
	}

	switch action {
	case "add":
		if content == "" {
			return "", errors.New("content 不能为空")
		}
		if err := scanContent(content); err != nil {
			return "", err
		}
		if slices.Contains(entries, content) {
			return "该条目已存在，未重复添加。" + m.usageLine(target, entries), nil
		}
		next := append(slices.Clone(entries), content)
		if u := usage(next); u > t.limit {
			return "", fmt.Errorf("超出容量：添加后为 %d/%d 字符。这条内容如果不是每次对话都要用到，请改存到 target=longterm（容量不限，按需自动召回）；否则先用 action=archive 把不常用的条目移到长期记忆，或用 patch 合并、remove 删除过时条目后再试", u, t.limit)
		}
		entries = next
	case "patch":
		if content == "" {
			return "", errors.New("patch 需要 content（替换后的文本）；要删除条目请使用 remove")
		}
		i, err := find()
		if err != nil {
			return "", err
		}
		var updated string
		if entries[i] == oldText {
			updated = content
		} else {
			if strings.Count(entries[i], oldText) != 1 {
				return "", errors.New("old_text 在条目中出现多次，请提供更长的片段")
			}
			updated = strings.Replace(entries[i], oldText, content, 1)
		}
		if err := scanContent(updated); err != nil {
			return "", err
		}
		next := slices.Clone(entries)
		next[i] = updated
		if u := usage(next); u > t.limit {
			return "", fmt.Errorf("超出容量：修改后为 %d/%d 字符，请精简内容，或用 action=archive 把不常用的条目移到长期记忆", u, t.limit)
		}
		entries = next
	case "remove":
		i, err := find()
		if err != nil {
			return "", err
		}
		entries = slices.Delete(slices.Clone(entries), i, i+1)
	case "replace":
		return "", errors.New("不支持 replace，请使用 patch（old_text + content）")
	default:
		return "", errors.New("action 必须是 add / patch / remove / archive")
	}
	if err := m.saveLocked(target, entries); err != nil {
		return "", err
	}
	return "已保存（新内容将在下一个会话的系统提示中生效）。" + m.usageLine(target, entries), nil
}

// Take 取出并删除 oldText 所在的条目（把它移到长期记忆时使用）。
func (m *MemoryStore) Take(target, oldText string) (string, error) {
	if _, ok := memoryTargets[target]; !ok {
		return "", errors.New("target 必须是 memory 或 user")
	}
	oldText = strings.TrimSpace(oldText)
	if oldText == "" {
		return "", errors.New("需要提供 old_text 来指明要归档哪一条")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entries := m.entriesLocked(target)
	idx := slices.Index(entries, oldText)
	if idx < 0 {
		for i, e := range entries {
			if strings.Contains(e, oldText) {
				if idx >= 0 {
					return "", errors.New("old_text 匹配到多个条目，请提供更具体的片段")
				}
				idx = i
			}
		}
	}
	if idx < 0 {
		return "", errors.New("没有找到包含 old_text 的条目")
	}
	entry := entries[idx]
	return entry, m.saveLocked(target, slices.Delete(slices.Clone(entries), idx, idx+1))
}

// SetEntries 由用户在界面中直接编辑记忆。
func (m *MemoryStore) SetEntries(target string, entries []string) error {
	t, ok := memoryTargets[target]
	if !ok {
		return errors.New("未知的记忆类型")
	}
	var clean []string
	for _, e := range entries {
		if e = strings.TrimSpace(e); e != "" {
			clean = append(clean, e)
		}
	}
	if u := usage(clean); u > t.limit {
		return fmt.Errorf("超出容量：%d/%d 字符", u, t.limit)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveLocked(target, clean)
}

// Files 返回记忆文件内容（用于备份）。
func (m *MemoryStore) Files() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	for _, t := range memoryTargets {
		if b, err := os.ReadFile(filepath.Join(m.dir, t.file)); err == nil {
			out["memory/"+t.file] = string(b)
		}
	}
	return out
}
