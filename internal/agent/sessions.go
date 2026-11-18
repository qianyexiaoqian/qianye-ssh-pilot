package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"sshpilot/internal/store"
)

type Session struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	CreatedAt    int64     `json:"createdAt"`
	UpdatedAt    int64     `json:"updatedAt"`
	SystemPrompt string    `json:"systemPrompt"`
	Messages     []Message `json:"messages"`
	Turns        int       `json:"turns"`
	InputTokens  int64     `json:"inputTokens"`
	OutputTokens int64     `json:"outputTokens"`
	ApproveAll   bool      `json:"approveAll"`
	Learned      int       `json:"learned"`
	// ServerID 不为空表示这是从某台服务器的终端里发起的对话，默认只操作这台服务器。
	ServerID string `json:"serverId,omitempty"`
	// Recalled 是这个会话里已经自动召回过的长期记忆，同一条不重复附带。
	Recalled []string `json:"recalled,omitempty"`
	// NoImageModel 记录拒收过图片的模型：之后用同一个模型时不再发送图片。
	NoImageModel string `json:"noImageModel,omitempty"`
}

type SessionSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	Messages  int    `json:"messages"`
	Preview   string `json:"preview"`
	Learned   int    `json:"learned"`
	Running   bool   `json:"running"`
	ServerID  string `json:"serverId"`
}

type SearchHit struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title"`
	Time      int64  `json:"time"`
	Role      string `json:"role"`
	Snippet   string `json:"snippet"`
	Score     int    `json:"score"`
}

type SessionStore struct {
	mu        sync.Mutex
	dir       string
	summaries map[string]SessionSummary
}

func NewSessionStore(dir string) *SessionStore {
	_ = os.MkdirAll(dir, 0o700)
	s := &SessionStore{dir: dir, summaries: map[string]SessionSummary{}}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if sess, err := s.Load(strings.TrimSuffix(e.Name(), ".json")); err == nil {
			s.summaries[sess.ID] = summarize(sess)
		}
	}
	return s
}

func (s *SessionStore) path(id string) string { return filepath.Join(s.dir, id+".json") }

func validSessionID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

func summarize(sess *Session) SessionSummary {
	preview := ""
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		m := sess.Messages[i]
		if m.Role == "assistant" && strings.TrimSpace(m.Content) != "" {
			preview = clip(m.Content, 60)
			break
		}
	}
	return SessionSummary{ID: sess.ID, Title: sess.Title, CreatedAt: sess.CreatedAt, UpdatedAt: sess.UpdatedAt,
		Messages: len(sess.Messages), Preview: preview, Learned: sess.Learned, ServerID: sess.ServerID}
}

func (s *SessionStore) Load(id string) (*Session, error) {
	if !validSessionID(id) {
		return nil, errors.New("会话 ID 无效")
	}
	b, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, errors.New("会话不存在")
	}
	var sess Session
	if err := json.Unmarshal(b, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

func (s *SessionStore) Save(sess *Session) error {
	b, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	if err := store.WriteFileAtomic(s.path(sess.ID), b, 0o600); err != nil {
		return err
	}
	s.mu.Lock()
	s.summaries[sess.ID] = summarize(sess)
	s.mu.Unlock()
	return nil
}

func (s *SessionStore) Delete(id string) error {
	if !validSessionID(id) {
		return errors.New("会话 ID 无效")
	}
	s.mu.Lock()
	delete(s.summaries, id)
	s.mu.Unlock()
	return os.Remove(s.path(id))
}

func (s *SessionStore) List() []SessionSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SessionSummary, 0, len(s.summaries))
	for _, v := range s.summaries {
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b SessionSummary) int { return int(b.UpdatedAt - a.UpdatedAt) })
	return out
}

func searchTerms(q string) []string {
	var terms []string
	for _, t := range strings.Fields(strings.ToLower(q)) {
		if utf8.RuneCountInString(t) >= 2 {
			terms = append(terms, t)
		}
	}
	return terms
}

func snippetAround(text string, idx int) string {
	r := []rune(text)
	pos := utf8.RuneCountInString(text[:idx])
	start := max(0, pos-60)
	end := min(len(r), pos+120)
	s := strings.Join(strings.Fields(string(r[start:end])), " ")
	if start > 0 {
		s = "…" + s
	}
	if end < len(r) {
		s += "…"
	}
	return s
}

// Search 在历史会话中全文检索（大小写不敏感，多个关键词取并集并按命中数排序）。
func (s *SessionStore) Search(query string, limit int, exclude string) []SearchHit {
	terms := searchTerms(query)
	if len(terms) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 8
	}
	summaries := s.List()
	if len(summaries) > 300 {
		summaries = summaries[:300]
	}
	var hits []SearchHit
	for _, sum := range summaries {
		if sum.ID == exclude {
			continue
		}
		sess, err := s.Load(sum.ID)
		if err != nil {
			continue
		}
		for _, m := range sess.Messages {
			text := m.Content
			if m.Role == "user" && m.Display != "" {
				text = m.Display
			}
			if text == "" || m.Kind == "notice" {
				continue
			}
			lower := strings.ToLower(text)
			score, first := 0, -1
			for _, t := range terms {
				if c := strings.Count(lower, t); c > 0 {
					score += 10 + min(c, 5)
					if i := strings.Index(lower, t); first < 0 || i < first {
						first = i
					}
				}
			}
			if score == 0 {
				continue
			}
			// 越近的会话权重越高
			age := time.Since(time.UnixMilli(m.CreatedAt)).Hours() / 24
			score += max(0, 10-int(age/7))
			hits = append(hits, SearchHit{SessionID: sess.ID, Title: sess.Title, Time: m.CreatedAt, Role: m.Role,
				Snippet: snippetAround(text, first), Score: score})
		}
	}
	slices.SortFunc(hits, func(a, b SearchHit) int { return b.Score - a.Score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}
