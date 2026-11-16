package agent

import (
	"bufio"
	"encoding/json"
	"os"
	"slices"
	"sync"
)

// LearnEvent 记录一次“学习”：记忆写入、技能沉淀或脚本保存。
type LearnEvent struct {
	Time      int64  `json:"time"`
	Source    string `json:"source"` // agent | review | user
	Kind      string `json:"kind"`   // memory | user | skill | script
	Action    string `json:"action"`
	Target    string `json:"target"`
	Summary   string `json:"summary"`
	SessionID string `json:"sessionId"`
}

type LearnLog struct {
	mu   sync.Mutex
	path string
}

func NewLearnLog(path string) *LearnLog { return &LearnLog{path: path} }

func (l *LearnLog) Add(e LearnEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(e)
	_, _ = f.Write(append(b, '\n'))
}

// Recent 返回最近的学习记录（新的在前）。
func (l *LearnLog) Recent(limit int) []LearnEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.Open(l.path)
	if err != nil {
		return []LearnEvent{}
	}
	defer f.Close()
	var all []LearnEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var e LearnEvent
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			all = append(all, e)
		}
	}
	slices.Reverse(all)
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	if all == nil {
		all = []LearnEvent{}
	}
	return all
}
