package agent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"sshpilot/internal/store"
)

// 长期记忆：不设容量上限、按相关度检索的知识库，与常驻系统提示词的 MEMORY.md / USER.md（核心记忆，容量很小）互补。
//
//	fact     事实与经验，可以标注它是关于哪些服务器的
//	episode  每一轮对话的“问题 → 操作 → 结论”要点，对话结束后自动生成
//
// 技能的名称与描述也放进同一个向量空间（不落盘到条目里），用来在提问时提示相关的技能。
const (
	KindFact    = "fact"
	KindEpisode = "episode"
	KindSkill   = "skill"

	maxFactRunes    = 600
	maxEpisodeRunes = 900
	maxFacts        = 3000
	maxEpisodes     = 3000
)

type MemItem struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Text string `json:"text"`
	// Servers 是相关服务器的 ID；为空表示通用知识。
	Servers   []string `json:"servers"`
	SessionID string   `json:"sessionId,omitempty"`
	// Title：对话要点所在会话的标题；技能的名称。
	Title     string `json:"title,omitempty"`
	Source    string `json:"source,omitempty"` // agent | review | user | archive
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	// Hits 是被召回的次数，LastHit 是最近一次被召回的时间。
	Hits    int   `json:"hits"`
	LastHit int64 `json:"lastHit"`
}

type MemHit struct {
	MemItem
	Score float64 `json:"score"`
}

// IndexStatus 描述长期记忆当前用什么方式检索，以及向量索引的进度。
type IndexStatus struct {
	Mode      string  `json:"mode"` // semantic：语义向量；lexical：内置关键词匹配
	Model     string  `json:"model"`
	Dims      int     `json:"dims"`
	Facts     int     `json:"facts"`
	Episodes  int     `json:"episodes"`
	Indexed   int     `json:"indexed"`
	Total     int     `json:"total"`
	Indexing  bool    `json:"indexing"`
	Error     string  `json:"error"`
	Threshold float64 `json:"threshold"`
	Related   float64 `json:"related"`
	Unrelated float64 `json:"unrelated"`
}

type LongMemory struct {
	mu    sync.Mutex
	dir   string
	items []*MemItem

	emb    Embedder
	vecs   map[string][]float32 // 当前向量模型算出的向量，语义模式下保存在 vectors.bin
	dims   int
	fileOK bool // vectors.bin 的文件头与当前的向量模型一致，可以直接追加
	cal    Calibration

	lex    map[string]map[string]float32  // 内置关键词匹配用的稀疏词向量（随用随算）
	idents map[string]map[string]struct{} // 条目里的标识符（服务器名、路径等）
	lexCal Calibration

	dirty     bool
	saveTimer *time.Timer
	indexing  bool
	lastErr   string
	// downUntil：向量接口刚刚失败过，这段时间内直接用关键词匹配，不让每次提问都等到超时。
	downUntil time.Time

	// Skills 返回当前的技能；Notify 在索引进度变化时调用。
	Skills func() []SkillMeta
	Notify func(IndexStatus)
	// ReadOnly 为 true 时只读取、不写磁盘也不建立索引（文件管理独立窗口的进程：数据由主窗口负责）。
	ReadOnly bool
}

func NewLongMemory(dir string) *LongMemory {
	_ = os.MkdirAll(dir, 0o700)
	m := &LongMemory{dir: dir, emb: lexicalEmbedder{}, vecs: map[string][]float32{}, lex: map[string]map[string]float32{}, idents: map[string]map[string]struct{}{}}
	m.lexCal = lexCalibration()
	m.loadItems()
	return m
}

func (m *LongMemory) itemsPath() string { return filepath.Join(m.dir, "longterm.json") }
func (m *LongMemory) vecPath() string   { return filepath.Join(m.dir, "vectors.bin") }

type longFile struct {
	Version int        `json:"version"`
	Items   []*MemItem `json:"items"`
}

func (m *LongMemory) loadItems() {
	m.items = nil
	b, err := os.ReadFile(m.itemsPath())
	if err != nil {
		return
	}
	var f longFile
	if json.Unmarshal(b, &f) != nil {
		return
	}
	for _, it := range f.Items {
		if it != nil && it.ID != "" && strings.TrimSpace(it.Text) != "" && (it.Kind == KindFact || it.Kind == KindEpisode) {
			m.items = append(m.items, it)
		}
	}
}

func (m *LongMemory) encodeItemsLocked() []byte {
	items := m.items
	if items == nil {
		items = []*MemItem{}
	}
	b, _ := json.Marshal(longFile{Version: 1, Items: items})
	return b
}

func (m *LongMemory) saveLocked() {
	m.dirty = false
	if m.ReadOnly {
		return
	}
	_ = store.WriteFileAtomic(m.itemsPath(), m.encodeItemsLocked(), 0o600)
}

// saveSoonLocked 合并短时间内的多次修改（例如每次召回都会更新命中次数）。
func (m *LongMemory) saveSoonLocked() {
	m.dirty = true
	if m.saveTimer != nil {
		return
	}
	m.saveTimer = time.AfterFunc(1500*time.Millisecond, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.saveTimer = nil
		if m.dirty {
			m.saveLocked()
		}
	})
}

// Close 把还没写入磁盘的修改保存下来。
func (m *LongMemory) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveTimer != nil {
		m.saveTimer.Stop()
		m.saveTimer = nil
	}
	if m.dirty {
		m.saveLocked()
	}
}

// Files 返回长期记忆的内容（用于备份；向量可以重新计算，不放进备份）。
func (m *LongMemory) Files() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.items) == 0 {
		return map[string]string{}
	}
	return map[string]string{"memory/longterm.json": string(m.encodeItemsLocked())}
}

// Reload 在恢复备份后重新读取磁盘上的内容。
func (m *LongMemory) Reload() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveTimer != nil {
		m.saveTimer.Stop()
		m.saveTimer = nil
	}
	m.dirty = false
	m.loadItems()
	m.lex = map[string]map[string]float32{}
	m.idents = map[string]map[string]struct{}{}
}

// ---- 向量文件 ----

// vectors.bin：第一行是 JSON 文件头，后面是一条条记录（2 字节 ID 长度 + ID + 维度 × 4 字节 float32）。
// 新向量追加在末尾，同一个 ID 以最后一条为准；清理过期向量或校准结果变化时整个重写。
type vecHeader struct {
	ID   string      `json:"id"`
	Dims int         `json:"dims"`
	Cal  Calibration `json:"cal"`
}

func (m *LongMemory) loadVectorsLocked() {
	m.vecs, m.dims, m.fileOK, m.cal = map[string][]float32{}, 0, false, Calibration{}
	f, err := os.Open(m.vecPath())
	if err != nil {
		return
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	line, err := r.ReadBytes('\n')
	if err != nil {
		return
	}
	var h vecHeader
	if json.Unmarshal(line, &h) != nil || h.ID != m.emb.ID() || h.Dims < 0 || h.Dims > 16384 {
		return
	}
	m.cal = h.Cal
	if h.Dims == 0 {
		return // 只保存了校准结果，还没有向量
	}
	m.dims, m.fileOK = h.Dims, true
	buf := make([]byte, h.Dims*4)
	for {
		var n uint16
		if binary.Read(r, binary.LittleEndian, &n) != nil {
			return
		}
		id := make([]byte, n)
		if _, err := io.ReadFull(r, id); err != nil {
			return
		}
		if _, err := io.ReadFull(r, buf); err != nil {
			return // 上次写到一半：忽略这条不完整的记录
		}
		v := make([]float32, h.Dims)
		for i := range v {
			v[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[i*4:]))
		}
		m.vecs[string(id)] = v
	}
}

func vecRecord(buf *bytes.Buffer, id string, v []float32) {
	_ = binary.Write(buf, binary.LittleEndian, uint16(len(id)))
	buf.WriteString(id)
	var b [4]byte
	for _, x := range v {
		binary.LittleEndian.PutUint32(b[:], math.Float32bits(x))
		buf.Write(b[:])
	}
}

func (m *LongMemory) rewriteVectorsLocked() {
	if !m.emb.Semantic() || m.ReadOnly {
		return
	}
	var buf bytes.Buffer
	h, _ := json.Marshal(vecHeader{ID: m.emb.ID(), Dims: m.dims, Cal: m.cal})
	buf.Write(h)
	buf.WriteByte('\n')
	for id, v := range m.vecs {
		vecRecord(&buf, id, v)
	}
	m.fileOK = store.WriteFileAtomic(m.vecPath(), buf.Bytes(), 0o600) == nil && m.dims > 0
}

// storeVectorsLocked 记下新算出的向量并写入磁盘。
func (m *LongMemory) storeVectorsLocked(ids []string, vecs [][]float32) {
	if !m.emb.Semantic() {
		return
	}
	var buf bytes.Buffer
	for i, id := range ids {
		v := vecs[i]
		if m.dims == 0 {
			m.dims = len(v)
			m.fileOK = false
		}
		if len(v) != m.dims || len(id) > 60000 {
			continue
		}
		m.vecs[id] = v
		vecRecord(&buf, id, v)
	}
	if m.ReadOnly {
		return
	}
	if !m.fileOK {
		m.rewriteVectorsLocked()
		return
	}
	f, err := os.OpenFile(m.vecPath(), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		m.rewriteVectorsLocked()
		return
	}
	defer f.Close()
	_, _ = f.Write(buf.Bytes())
}

// SetEmbedder 切换向量模型；模型没有变化时什么都不做。返回是否发生了切换。
func (m *LongMemory) SetEmbedder(e Embedder) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.emb.ID() == e.ID() {
		// 向量空间没变，但 API Key 换了：换用新的客户端，已有的向量继续有效
		old, ok1 := m.emb.(*apiEmbedder)
		cur, ok2 := e.(*apiEmbedder)
		if !ok1 || !ok2 || old.key == cur.key {
			return false
		}
		m.emb = e
		m.lastErr, m.downUntil = "", time.Time{}
		return true
	}
	m.emb = e
	m.lastErr, m.downUntil = "", time.Time{}
	m.vecs, m.dims, m.fileOK, m.cal = map[string][]float32{}, 0, false, Calibration{}
	if e.Semantic() {
		m.loadVectorsLocked()
	}
	return true
}

// ---- 条目 ----

func textHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:5])
}

func normText(s string) string { return strings.Join(strings.Fields(s), " ") }

func (m *LongMemory) forgetVectorLocked(id string) {
	delete(m.vecs, id)
	delete(m.lex, id)
	delete(m.idents, id)
}

func (m *LongMemory) lexVecLocked(it *MemItem) map[string]float32 {
	v, ok := m.lex[it.ID]
	if !ok {
		v = lexSparse(it.Text)
		m.lex[it.ID] = v
	}
	return v
}

func (m *LongMemory) identsLocked(it *MemItem) map[string]struct{} {
	v, ok := m.idents[it.ID]
	if !ok {
		v = identTokens(it.Text)
		m.idents[it.ID] = v
	}
	return v
}

// skillItems 把技能的名称与描述包装成可以检索的条目（ID 里带内容摘要，描述改了就会重新计算向量）。
func (m *LongMemory) skillItems() []*MemItem {
	if m.Skills == nil {
		return nil
	}
	var out []*MemItem
	for _, s := range m.Skills() {
		text := s.Name + "：" + s.Description
		out = append(out, &MemItem{ID: "skill:" + s.Name + ":" + textHash(text), Kind: KindSkill, Title: s.Name, Text: text})
	}
	return out
}

func (m *LongMemory) countLocked(kind string) int {
	n := 0
	for _, it := range m.items {
		if it.Kind == kind {
			n++
		}
	}
	return n
}

// trimLocked 超出条数上限时丢弃最旧的条目（对话要点按时间，事实按最近一次用到的时间）。
func (m *LongMemory) trimLocked(kind string, limit int) {
	n := m.countLocked(kind)
	if n <= limit {
		return
	}
	var same []*MemItem
	for _, it := range m.items {
		if it.Kind == kind {
			same = append(same, it)
		}
	}
	slices.SortFunc(same, func(a, b *MemItem) int {
		return int(max(a.UpdatedAt, a.LastHit)/1000 - max(b.UpdatedAt, b.LastHit)/1000)
	})
	drop := map[string]bool{}
	for _, it := range same[:n-limit] {
		drop[it.ID] = true
		m.forgetVectorLocked(it.ID)
	}
	m.items = slices.DeleteFunc(m.items, func(it *MemItem) bool { return drop[it.ID] })
}

func cleanServers(ids []string) []string {
	out := []string{}
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func validateFact(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("内容不能为空")
	}
	if n := utf8.RuneCountInString(text); n > maxFactRunes {
		return "", fmt.Errorf("单条记忆过长（%d 字，上限 %d）：请拆成几条，或只保留结论", n, maxFactRunes)
	}
	return text, scanContent(text)
}

// embedOne 计算一段文本的向量；向量接口不可用时返回 nil（稍后由后台索引补上），不影响保存。
func (m *LongMemory) embedOne(ctx context.Context, emb Embedder, text string) []float32 {
	if !emb.Semantic() {
		return nil
	}
	m.mu.Lock()
	down := time.Now().Before(m.downUntil)
	m.mu.Unlock()
	if down {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	vecs, err := emb.Embed(ctx, []string{text})
	if err != nil {
		m.mu.Lock()
		if m.emb.ID() == emb.ID() {
			m.lastErr = err.Error()
			m.downUntil = time.Now().Add(time.Minute)
		}
		m.mu.Unlock()
		return nil
	}
	return vecs[0]
}

// Semantic 返回当前是否在使用语义向量（false 表示内置的关键词匹配）。
func (m *LongMemory) Semantic() bool { return m.embedder().Semantic() }

func (m *LongMemory) embedder() Embedder {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.emb
}

// AddResult 是新增一条事实的结果。Similar 是已有的、内容很接近的条目。
type AddResult struct {
	Item      MemItem
	Duplicate bool
	Similar   *MemItem
}

// AddFact 新增一条长期记忆。内容与已有条目几乎相同时不重复添加；比较接近时照常添加，并把相近的条目返回给调用方。
func (m *LongMemory) AddFact(ctx context.Context, text string, servers []string, source, sessionID string) (AddResult, error) {
	text, err := validateFact(text)
	if err != nil {
		return AddResult{}, err
	}
	servers = cleanServers(servers)
	emb := m.embedder()
	vec := m.embedOne(ctx, emb, text)

	m.mu.Lock()
	defer m.mu.Unlock()
	semantic := vec != nil && m.emb.ID() == emb.ID()
	cal := m.lexCal
	if semantic {
		cal = m.cal
	}
	var qs map[string]float32
	if !semantic {
		qs = lexSparse(text)
	}
	var nearest *MemItem
	best := 0.0
	for _, it := range m.items {
		if it.Kind != KindFact {
			continue
		}
		if normText(it.Text) == normText(text) {
			return AddResult{Item: *it, Duplicate: true}, nil
		}
		var s float64
		if semantic {
			s = dot(vec, m.vecs[it.ID])
		} else {
			s = sparseDot(qs, m.lexVecLocked(it))
		}
		if s > best {
			best, nearest = s, it
		}
	}
	res := AddResult{}
	if nearest != nil {
		sameScope := slices.Equal(slices.Sorted(slices.Values(nearest.Servers)), slices.Sorted(slices.Values(servers)))
		// 字面上几乎一样、标注的服务器也一样：视为重复
		if best >= 0.97 && sameScope {
			return AddResult{Item: *nearest, Duplicate: true}, nil
		}
		related := 0.6
		if cal.valid() {
			related = cal.Related
		}
		if best >= related+0.5*(1-related) {
			cp := *nearest
			res.Similar = &cp
		}
	}
	now := nowMs()
	it := &MemItem{ID: newID(), Kind: KindFact, Text: text, Servers: servers, SessionID: sessionID, Source: source, CreatedAt: now, UpdatedAt: now}
	m.items = append(m.items, it)
	m.trimLocked(KindFact, maxFacts)
	if semantic {
		m.storeVectorsLocked([]string{it.ID}, [][]float32{vec})
	}
	m.saveLocked()
	res.Item = *it
	return res, nil
}

func (m *LongMemory) findFactLocked(oldText string) (*MemItem, error) {
	oldText = strings.TrimSpace(oldText)
	if oldText == "" {
		return nil, errors.New("需要提供 old_text 来定位条目")
	}
	var found *MemItem
	for _, it := range m.items {
		if it.Kind != KindFact {
			continue
		}
		if it.Text == oldText {
			return it, nil
		}
		if strings.Contains(it.Text, oldText) {
			if found != nil {
				return nil, errors.New("old_text 匹配到多个条目，请提供更具体的片段")
			}
			found = it
		}
	}
	if found == nil {
		return nil, errors.New("长期记忆里没有找到包含 old_text 的条目（可以先用 recall 查看原文）")
	}
	return found, nil
}

// PatchFact 把包含 oldText 的那条事实里的这段文字换成 newText；servers 不为 nil 时同时修改标注的服务器。
func (m *LongMemory) PatchFact(ctx context.Context, oldText, newText string, servers []string) (MemItem, error) {
	m.mu.Lock()
	it, err := m.findFactLocked(oldText)
	if err != nil {
		m.mu.Unlock()
		return MemItem{}, err
	}
	oldText = strings.TrimSpace(oldText)
	updated := strings.TrimSpace(newText)
	if it.Text != oldText {
		if strings.Count(it.Text, oldText) != 1 {
			m.mu.Unlock()
			return MemItem{}, errors.New("old_text 在条目中出现多次，请提供更长的片段")
		}
		updated = strings.Replace(it.Text, oldText, updated, 1)
	}
	id := it.ID
	m.mu.Unlock()
	return m.Save(ctx, id, updated, servers, "")
}

// RemoveFact 删除包含 oldText 的那条事实。
func (m *LongMemory) RemoveFact(oldText string) (MemItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, err := m.findFactLocked(oldText)
	if err != nil {
		return MemItem{}, err
	}
	cp := *it
	m.deleteLocked(it.ID)
	return cp, nil
}

func (m *LongMemory) deleteLocked(id string) {
	m.items = slices.DeleteFunc(m.items, func(it *MemItem) bool { return it.ID == id })
	m.forgetVectorLocked(id)
	m.saveLocked()
}

// Delete 按 ID 删除一条记忆（事实或对话要点）。
func (m *LongMemory) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !slices.ContainsFunc(m.items, func(it *MemItem) bool { return it.ID == id }) {
		return errors.New("这条记忆已经不存在")
	}
	m.deleteLocked(id)
	return nil
}

// Save 新建（id 为空）或修改一条事实。servers 为 nil 表示不修改标注的服务器。
func (m *LongMemory) Save(ctx context.Context, id, text string, servers []string, source string) (MemItem, error) {
	if id == "" {
		res, err := m.AddFact(ctx, text, servers, source, "")
		if err != nil {
			return MemItem{}, err
		}
		if res.Duplicate {
			return res.Item, errors.New("已经有一条内容相同的记忆")
		}
		return res.Item, nil
	}
	text, err := validateFact(text)
	if err != nil {
		return MemItem{}, err
	}
	emb := m.embedder()
	vec := m.embedOne(ctx, emb, text)
	m.mu.Lock()
	defer m.mu.Unlock()
	i := slices.IndexFunc(m.items, func(it *MemItem) bool { return it.ID == id && it.Kind == KindFact })
	if i < 0 {
		return MemItem{}, errors.New("这条记忆已经不存在")
	}
	it := m.items[i]
	if it.Text != text {
		m.forgetVectorLocked(id)
		it.Text = text
		if vec != nil && m.emb.ID() == emb.ID() {
			m.storeVectorsLocked([]string{id}, [][]float32{vec})
		}
	}
	if servers != nil {
		it.Servers = cleanServers(servers)
	}
	it.UpdatedAt = nowMs()
	m.saveLocked()
	return *it, nil
}

// PutEpisode 保存一轮对话的要点（ID 相同则覆盖）。
func (m *LongMemory) PutEpisode(ctx context.Context, ep MemItem) {
	ep.Kind = KindEpisode
	ep.Text = clipRunes(strings.TrimSpace(ep.Text), maxEpisodeRunes)
	if ep.ID == "" || ep.Text == "" || scanContent(ep.Text) != nil {
		return
	}
	ep.Servers = cleanServers(ep.Servers)
	emb := m.embedder()
	vec := m.embedOne(ctx, emb, ep.Text)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.putEpisodeLocked(&ep)
	if vec != nil && m.emb.ID() == emb.ID() {
		m.storeVectorsLocked([]string{ep.ID}, [][]float32{vec})
	}
	m.saveSoonLocked()
}

func (m *LongMemory) putEpisodeLocked(ep *MemItem) {
	if ep.UpdatedAt == 0 {
		ep.UpdatedAt = ep.CreatedAt
	}
	if i := slices.IndexFunc(m.items, func(it *MemItem) bool { return it.ID == ep.ID }); i >= 0 {
		old := m.items[i]
		ep.Hits, ep.LastHit = old.Hits, old.LastHit
		if old.Text != ep.Text {
			m.forgetVectorLocked(ep.ID)
		}
		m.items[i] = ep
		return
	}
	m.items = append(m.items, ep)
	m.trimLocked(KindEpisode, maxEpisodes)
}

// AddEpisodes 批量补上历史会话的要点（不立即计算向量，由后台索引处理），返回新增的条数。
func (m *LongMemory) AddEpisodes(eps []MemItem) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	added := 0
	for i := range eps {
		ep := eps[i]
		ep.Kind = KindEpisode
		ep.Text = clipRunes(strings.TrimSpace(ep.Text), maxEpisodeRunes)
		if ep.ID == "" || ep.Text == "" || scanContent(ep.Text) != nil || slices.ContainsFunc(m.items, func(it *MemItem) bool { return it.ID == ep.ID }) {
			continue
		}
		ep.Servers = cleanServers(ep.Servers)
		m.putEpisodeLocked(&ep)
		added++
	}
	if added > 0 {
		m.saveLocked()
	}
	return added
}

// EpisodeIDs 返回已有的对话要点的 ID。
func (m *LongMemory) EpisodeIDs() map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]bool{}
	for _, it := range m.items {
		if it.Kind == KindEpisode {
			out[it.ID] = true
		}
	}
	return out
}

// RemoveSession 删除一个会话留下的对话要点（会话被删除时调用）。
func (m *LongMemory) RemoveSession(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(m.items)
	for _, it := range m.items {
		if it.Kind == KindEpisode && it.SessionID == sessionID {
			m.forgetVectorLocked(it.ID)
		}
	}
	m.items = slices.DeleteFunc(m.items, func(it *MemItem) bool { return it.Kind == KindEpisode && it.SessionID == sessionID })
	if len(m.items) != n {
		m.saveLocked()
	}
}

// List 返回某一类条目，最近更新的在前。
func (m *LongMemory) List(kind string) []MemItem {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []MemItem{}
	for _, it := range m.items {
		if kind == "" || it.Kind == kind {
			out = append(out, *it)
		}
	}
	slices.SortFunc(out, func(a, b MemItem) int { return int(b.UpdatedAt/1000 - a.UpdatedAt/1000) })
	return out
}

// ---- 检索 ----

type SearchOptions struct {
	// Kinds 为空时检索事实与对话要点。
	Kinds []string
	Limit int
	// SkillLimit 大于 0 时，技能单独计数、最多返回这么多个，不占 Limit 的名额。
	SkillLimit int
	// Servers 是当前相关的服务器 ID：标注了这些服务器的条目排得更靠前。
	Servers []string
	// ExcludeSession 排除某个会话自己的对话要点（它们本来就在上下文里）。
	ExcludeSession string
	// Loose 用于主动检索：相关度下限放宽。
	Loose bool
	// MinScore 大于 0 时取代自动校准得到的下限。
	MinScore float64
	// NoTouch 为 true 时不计入命中次数（界面里的搜索）。
	NoTouch bool
}

// Search 按相关度检索长期记忆。向量接口不可用时自动退回内置的关键词匹配。
func (m *LongMemory) Search(ctx context.Context, query string, opt SearchOptions) []MemHit {
	query = strings.TrimSpace(query)
	if query == "" {
		return []MemHit{}
	}
	emb := m.embedder()
	semantic := emb.Semantic()
	var qv []float32
	if semantic {
		if qv = m.embedOne(ctx, emb, query); qv == nil {
			semantic = false
		}
	}
	qs := lexSparse(query)
	qIdent := identTokens(query)
	kinds := opt.Kinds
	if len(kinds) == 0 {
		kinds = []string{KindFact, KindEpisode}
	}
	var skills []*MemItem
	if slices.Contains(kinds, KindSkill) {
		skills = m.skillItems()
	}

	m.mu.Lock()
	if semantic && m.emb.ID() != emb.ID() {
		semantic = false
	}
	cal := m.lexCal
	if semantic {
		cal = m.cal
	}
	threshold := opt.MinScore
	if threshold <= 0 {
		threshold = cal.threshold(opt.Loose)
	}
	span := 0.3
	if cal.valid() {
		span = cal.Related - cal.Unrelated
	}
	missing := false
	var hits []MemHit
	for _, it := range slices.Concat(m.items, skills) {
		if !slices.Contains(kinds, it.Kind) || (opt.ExcludeSession != "" && it.SessionID == opt.ExcludeSession) {
			continue
		}
		var score float64
		if semantic {
			v := m.vecs[it.ID]
			if v == nil {
				missing = true
				continue
			}
			score = dot(qv, v)
		} else {
			score = sparseDot(qs, m.lexVecLocked(it))
		}
		if semantic && len(qIdent) > 0 {
			// 语义向量对服务器名、路径、端口这类标识符不敏感：字面上出现了就加一点分
			shared := 0
			for tok := range m.identsLocked(it) {
				if _, ok := qIdent[tok]; ok {
					shared++
				}
			}
			score += 0.1 * span * float64(min(shared, 2))
		}
		if len(opt.Servers) > 0 && len(it.Servers) > 0 {
			if slices.ContainsFunc(it.Servers, func(id string) bool { return slices.Contains(opt.Servers, id) }) {
				score += 0.15 * span
			} else {
				score -= 0.08 * span
			}
		}
		if score < threshold {
			continue
		}
		hits = append(hits, MemHit{MemItem: *it, Score: score})
	}
	slices.SortFunc(hits, func(a, b MemHit) int {
		switch {
		case a.Score > b.Score:
			return -1
		case a.Score < b.Score:
			return 1
		}
		return int(b.UpdatedAt/1000 - a.UpdatedAt/1000)
	})
	if !opt.Loose && len(hits) > 1 {
		// 已经有很匹配的内容时，分数差得远的多半只是沾了点边：只保留与最佳匹配相差不到一个“区分度”的
		floor := hits[0].Score - span
		hits = slices.DeleteFunc(hits, func(h MemHit) bool { return h.Score < floor })
	}
	limit := opt.Limit
	if limit <= 0 {
		limit = 8
	}
	kept, n, ns := hits[:0], 0, 0
	for _, h := range hits {
		if h.Kind == KindSkill && opt.SkillLimit > 0 {
			if ns < opt.SkillLimit {
				kept, ns = append(kept, h), ns+1
			}
		} else if n < limit {
			kept, n = append(kept, h), n+1
		}
	}
	hits = kept
	if !opt.NoTouch && len(hits) > 0 {
		now := nowMs()
		for _, h := range hits {
			if i := slices.IndexFunc(m.items, func(it *MemItem) bool { return it.ID == h.ID }); i >= 0 {
				m.items[i].Hits++
				m.items[i].LastHit = now
			}
		}
		m.saveSoonLocked()
	}
	down := time.Now().Before(m.downUntil)
	m.mu.Unlock()
	if missing && !down {
		go func() { _ = m.Reindex(context.Background()) }()
	}
	if hits == nil {
		hits = []MemHit{}
	}
	return hits
}

// ---- 索引 ----

func (m *LongMemory) statusLocked(skills []*MemItem) IndexStatus {
	st := IndexStatus{Mode: "lexical", Model: m.emb.Label(), Facts: m.countLocked(KindFact), Episodes: m.countLocked(KindEpisode),
		Indexing: m.indexing, Error: m.lastErr}
	cal := m.lexCal
	st.Total = len(m.items) + len(skills)
	st.Indexed = st.Total
	if m.emb.Semantic() {
		st.Mode, st.Dims, cal = "semantic", m.dims, m.cal
		st.Indexed = 0
		for _, it := range slices.Concat(m.items, skills) {
			if m.vecs[it.ID] != nil {
				st.Indexed++
			}
		}
	}
	st.Related, st.Unrelated = cal.Related, cal.Unrelated
	if cal.valid() {
		st.Threshold = cal.threshold(false)
	}
	return st
}

func (m *LongMemory) Status() IndexStatus {
	skills := m.skillItems()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked(skills)
}

func (m *LongMemory) notify(skills []*MemItem) {
	if m.Notify == nil {
		return
	}
	m.mu.Lock()
	st := m.statusLocked(skills)
	m.mu.Unlock()
	m.Notify(st)
}

// Reindex 为还没有向量的条目计算向量（切换向量模型、恢复备份或接口曾经失败之后）。同一时间只运行一个。
func (m *LongMemory) Reindex(ctx context.Context) error {
	skills := m.skillItems()
	m.mu.Lock()
	emb := m.emb
	if m.indexing || !emb.Semantic() || m.ReadOnly {
		m.mu.Unlock()
		return nil
	}
	m.indexing = true
	m.lastErr, m.downUntil = "", time.Time{}
	m.mu.Unlock()

	fail := func(err error) error {
		m.mu.Lock()
		m.indexing = false
		if m.emb.ID() == emb.ID() {
			m.lastErr = err.Error()
			m.downUntil = time.Now().Add(time.Minute)
		}
		m.mu.Unlock()
		m.notify(skills)
		return err
	}
	for {
		m.mu.Lock()
		if m.emb.ID() != emb.ID() {
			// 设置在索引过程中变了：停下来，按新的向量模型重新开始
			m.indexing = false
			m.mu.Unlock()
			return m.Reindex(ctx)
		}
		if !m.cal.valid() {
			// 先量出这个模型的相似度分布（用户点了「重建索引」时也会回到这里重新校准）
			m.mu.Unlock()
			cal, err := Calibrate(ctx, emb)
			if err != nil {
				return fail(err)
			}
			if !cal.valid() {
				return fail(errors.New("这个模型分不出相关与无关的内容，请确认填写的是文本向量（embedding）模型"))
			}
			m.mu.Lock()
			if m.emb.ID() == emb.ID() {
				m.cal = cal
				m.rewriteVectorsLocked()
			}
			m.mu.Unlock()
			continue
		}
		var ids, texts []string
		for _, it := range slices.Concat(m.items, skills) {
			if m.vecs[it.ID] == nil {
				ids, texts = append(ids, it.ID), append(texts, it.Text)
				if len(ids) == embedBatch {
					break
				}
			}
		}
		m.mu.Unlock()
		if len(ids) == 0 {
			break
		}
		vecs, err := emb.Embed(ctx, texts)
		if err != nil {
			return fail(err)
		}
		m.mu.Lock()
		if m.emb.ID() == emb.ID() {
			// 只保留索引期间内容没有变化的条目
			var okIDs []string
			var okVecs [][]float32
			for i, id := range ids {
				cur := slices.IndexFunc(m.items, func(it *MemItem) bool { return it.ID == id })
				if strings.HasPrefix(id, "skill:") || (cur >= 0 && m.items[cur].Text == texts[i]) {
					okIDs, okVecs = append(okIDs, id), append(okVecs, vecs[i])
				}
			}
			m.storeVectorsLocked(okIDs, okVecs)
			// 接口返回的向量维度与之前不一致时一条也存不进去：停下来，避免无休止地重复请求
			if len(okIDs) > 0 && !slices.ContainsFunc(okIDs, func(id string) bool { return m.vecs[id] != nil }) {
				m.mu.Unlock()
				return fail(errors.New("向量接口返回的维度与已有索引不一致，请在设置里点击「重建索引」"))
			}
		}
		m.mu.Unlock()
		m.notify(skills)
	}
	// 清理已经没有对应条目的向量
	m.mu.Lock()
	if m.emb.ID() == emb.ID() {
		alive := map[string]bool{}
		for _, it := range slices.Concat(m.items, skills) {
			alive[it.ID] = true
		}
		stale := 0
		for id := range m.vecs {
			if !alive[id] {
				delete(m.vecs, id)
				stale++
			}
		}
		if stale > 0 {
			m.rewriteVectorsLocked()
		}
	}
	m.indexing = false
	m.mu.Unlock()
	m.notify(skills)
	return nil
}

// ResetIndex 丢弃已有的向量并重新计算（用户点击「重建索引」）。
func (m *LongMemory) ResetIndex() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vecs, m.dims, m.fileOK, m.cal, m.lastErr = map[string][]float32{}, 0, false, Calibration{}, ""
	_ = os.Remove(m.vecPath())
}
