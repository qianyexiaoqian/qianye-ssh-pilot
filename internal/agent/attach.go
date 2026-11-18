package agent

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"

	"sshpilot/internal/store"
)

// 用户随消息附带的图片和文件。
//   - 图片：缩到合适的大小后在数据目录里保存一份，随消息发给模型（需要模型支持图片输入），也显示在对话里。
//   - 粘贴进来的文件：没有原始位置，同样保存一份。
//   - 从磁盘选择或拖进来的文件：只记下位置，不复制。AI 用 read_attachment 阅读、用 upload_attachment 传到服务器时再读取。

const (
	AttachImage = "image"
	AttachFile  = "file"

	// MaxAttachments 是一条消息最多能带的附件数量。
	MaxAttachments = 10
	// MaxPastedBytes 是粘贴进来的内容（图片或文件）的大小上限：它们要整个经过界面传给后端。
	MaxPastedBytes = 25 << 20

	maxImageBytes   = 40 << 20 // 原始图片文件的大小上限，更大的当作普通文件
	inlineTextRunes = 12000    // 不超过这么长的文本文件直接附在消息里，模型不用再调用工具
	inlineTextTotal = 24000    // 一条消息里直接附带的文本总量
	readTextBytes   = 16 << 20 // read_attachment 最多读取文件开头这么多内容
)

type Attachment struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"` // image | file
	Mime   string `json:"mime,omitempty"`
	Size   int64  `json:"size"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	// Text 表示这是文本文件，模型可以阅读它的内容。
	Text bool `json:"text,omitempty"`
	// File 不为空表示数据目录里保存了一份（图片、粘贴进来的内容），值是保存的文件名。
	File string `json:"file,omitempty"`
	// Path 是文件在这台电脑上原来的位置（从磁盘选择或拖进来的）。
	Path string `json:"path,omitempty"`

	// 下面两项只在一次模型请求里临时填充，不保存。
	// Data 是发给模型的图片内容；Omitted 不为空表示这张图片这次没有发给模型，值是原因。
	Data    []byte `json:"-"`
	Omitted string `json:"-"`
}

var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true}

// AttachStore 管理附件：还没有发出去的放在 drafts 目录，发出后移到所属会话的目录。
type AttachStore struct {
	dir    string
	mu     sync.Mutex
	drafts map[string]Attachment
}

const draftDir = "drafts"

func NewAttachStore(dir string, readOnly bool) *AttachStore {
	s := &AttachStore{dir: dir, drafts: map[string]Attachment{}}
	if !readOnly {
		// 上次没有发出去的附件
		_ = os.RemoveAll(filepath.Join(dir, draftDir))
	}
	return s
}

var attachIDRe = regexp.MustCompile(`^[a-f0-9]{8,32}$`)

func (s *AttachStore) storedPath(scope string, att Attachment) string {
	if att.File == "" || att.File != filepath.Base(att.File) {
		return ""
	}
	return filepath.Join(s.dir, scope, att.File)
}

// Stored 返回数据目录里保存的那一份的位置；没有保存时返回空。sessionID 为空表示还没有发出去的附件。
func (s *AttachStore) Stored(sessionID string, att Attachment) string {
	if sessionID == "" {
		return s.storedPath(draftDir, att)
	}
	if !validSessionID(sessionID) {
		return ""
	}
	return s.storedPath(sessionID, att)
}

// Local 返回读取这个附件内容的位置：原文件还在就用原文件（图片的原图），否则用保存的那一份。
func (s *AttachStore) Local(sessionID string, att Attachment) (string, error) {
	if att.Path != "" {
		if info, err := os.Stat(att.Path); err == nil && info.Mode().IsRegular() {
			return att.Path, nil
		}
	}
	if p := s.Stored(sessionID, att); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	if att.Path != "" {
		return "", fmt.Errorf("文件「%s」已经不在原来的位置（%s），请让用户重新发一次", att.Name, att.Path)
	}
	return "", fmt.Errorf("附件「%s」的内容已经找不到了，请让用户重新发一次", att.Name)
}

func (s *AttachStore) remember(att Attachment) (Attachment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.drafts) >= 200 {
		return Attachment{}, errors.New("等待发送的附件太多了，请先发送或移除一些")
	}
	s.drafts[att.ID] = att
	return att, nil
}

// save 把内容写进 drafts 目录，返回保存的文件名。
func (s *AttachStore) save(id, ext string, data []byte) (string, error) {
	dir := filepath.Join(s.dir, draftDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := id + ext
	return name, store.WriteFileAtomic(filepath.Join(dir, name), data, 0o600)
}

var safeExtRe = regexp.MustCompile(`^\.[a-z0-9]{1,8}$`)

func cleanName(name string) string {
	name = strings.TrimSpace(filepath.Base(strings.ReplaceAll(name, "\\", "/")))
	if name == "" || name == "." || name == "/" {
		return "未命名"
	}
	return name
}

// AddPath 添加磁盘上的一个文件。图片会处理后保存一份，其余文件只记下位置。
func (s *AttachStore) AddPath(path string) (Attachment, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Attachment{}, fmt.Errorf("读取不到「%s」", filepath.Base(path))
	}
	if info.IsDir() {
		return Attachment{}, fmt.Errorf("「%s」是文件夹：请先打包成压缩文件再添加", filepath.Base(path))
	}
	if !info.Mode().IsRegular() {
		return Attachment{}, fmt.Errorf("「%s」不是普通文件", filepath.Base(path))
	}
	att := Attachment{ID: newID(), Name: filepath.Base(path), Kind: AttachFile, Size: info.Size(), Path: path}
	if imageExts[strings.ToLower(filepath.Ext(path))] && info.Size() <= maxImageBytes {
		if data, err := os.ReadFile(path); err == nil {
			if img, err := s.image(att, data); err == nil {
				return s.remember(img)
			}
		}
		// 不是能识别的图片：当作普通文件
	}
	att.Text = sniffText(path)
	return s.remember(att)
}

// AddData 添加一段没有原始位置的内容（粘贴进来的截图或文件）。
func (s *AttachStore) AddData(name string, data []byte) (Attachment, error) {
	if len(data) == 0 {
		return Attachment{}, errors.New("内容是空的")
	}
	if len(data) > MaxPastedBytes {
		return Attachment{}, fmt.Errorf("粘贴的内容超过 %d MB：请把文件拖进来，或点回形针按钮选择", MaxPastedBytes>>20)
	}
	att := Attachment{ID: newID(), Name: cleanName(name), Kind: AttachFile, Size: int64(len(data))}
	if img, err := s.image(att, data); err == nil {
		return s.remember(img)
	}
	ext := strings.ToLower(filepath.Ext(att.Name))
	if !safeExtRe.MatchString(ext) {
		ext = ""
	}
	file, err := s.save(att.ID, ext, data)
	if err != nil {
		return Attachment{}, err
	}
	att.File = file
	att.Text = looksLikeText(data[:min(len(data), 8192)])
	return s.remember(att)
}

// image 把图片处理成适合发给模型的大小并保存，返回补全后的附件。
func (s *AttachStore) image(att Attachment, data []byte) (Attachment, error) {
	out, mime, w, h, err := prepareImage(data)
	if err != nil {
		return Attachment{}, err
	}
	ext := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}[mime]
	file, err := s.save(att.ID, ext, out)
	if err != nil {
		return Attachment{}, err
	}
	att.Kind, att.Mime, att.Width, att.Height, att.File, att.Text = AttachImage, mime, w, h, file, false
	return att, nil
}

// Draft 返回一个还没有发出去的附件。
func (s *AttachStore) Draft(id string) (Attachment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	att, ok := s.drafts[id]
	return att, ok
}

// Discard 丢弃一个还没有发出去的附件。
func (s *AttachStore) Discard(id string) {
	s.mu.Lock()
	att, ok := s.drafts[id]
	delete(s.drafts, id)
	s.mu.Unlock()
	if ok {
		if p := s.Stored("", att); p != "" {
			_ = os.Remove(p)
		}
	}
}

// take 把一组附件交给一个会话：保存的文件移到会话自己的目录。有任何一个不存在时什么都不做。
func (s *AttachStore) take(sessionID string, ids []string) ([]Attachment, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > MaxAttachments {
		return nil, fmt.Errorf("一条消息最多带 %d 个附件", MaxAttachments)
	}
	if !validSessionID(sessionID) {
		return nil, errors.New("会话 ID 无效")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Attachment, 0, len(ids))
	for _, id := range ids {
		att, ok := s.drafts[id]
		if !ok || !attachIDRe.MatchString(id) {
			return nil, errors.New("有附件已经失效，请移除后重新添加")
		}
		out = append(out, att)
	}
	for _, att := range out {
		if att.File != "" {
			dir := filepath.Join(s.dir, sessionID)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, err
			}
			if err := os.Rename(filepath.Join(s.dir, draftDir, att.File), filepath.Join(dir, att.File)); err != nil {
				return nil, fmt.Errorf("保存附件「%s」失败：%v", att.Name, err)
			}
		}
		delete(s.drafts, att.ID)
	}
	return out, nil
}

// RemoveSession 删除一个会话保存的全部附件。
func (s *AttachStore) RemoveSession(sessionID string) {
	if validSessionID(sessionID) && sessionID != draftDir {
		_ = os.RemoveAll(filepath.Join(s.dir, sessionID))
	}
}

// ---- 文本文件 ----

func looksLikeText(head []byte) bool {
	if len(head) == 0 {
		return true
	}
	if bytes.HasPrefix(head, []byte{0xFF, 0xFE}) || bytes.HasPrefix(head, []byte{0xFE, 0xFF}) {
		return true // UTF-16（带 BOM）
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return false
	}
	text := head
	if !utf8.Valid(trimPartialRune(head)) {
		// 中文 Windows 上常见的 GBK / GB18030
		decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(head)
		if err != nil {
			return false
		}
		text = decoded
	}
	// 文本里的控制字符很少；二进制内容即使碰巧能解码，也会有一大堆
	ctrl, bad := 0, 0
	for _, r := range string(text) {
		switch {
		case r == utf8.RuneError:
			bad++
		case r < 0x20 && r != '\n' && r != '\r' && r != '\t' && r != '\f' && r != 0x1b:
			ctrl++
		}
	}
	n := utf8.RuneCount(text)
	return n > 0 && ctrl*50 <= n && bad*20 <= n
}

// trimPartialRune 去掉末尾被截断的半个 UTF-8 字符（只读了文件开头一段时会出现）。
func trimPartialRune(b []byte) []byte {
	for i := 0; i < utf8.UTFMax && i < len(b); i++ {
		if r, size := utf8.DecodeLastRune(b[:len(b)-i]); r != utf8.RuneError || size > 1 {
			return b[:len(b)-i]
		}
	}
	return b
}

func sniffText(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 8192)
	n, _ := io.ReadFull(f, head)
	return looksLikeText(head[:n])
}

// decodeText 把文本文件的内容转成 UTF-8（识别 BOM、UTF-16 和 GB18030），换行统一成 \n。
func decodeText(b []byte) string {
	var s string
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		s = string(b[3:])
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}), bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		var order binary.ByteOrder = binary.LittleEndian
		if b[0] == 0xFE {
			order = binary.BigEndian
		}
		b = b[2:]
		u := make([]uint16, len(b)/2)
		for i := range u {
			u[i] = order.Uint16(b[i*2:])
		}
		s = string(utf16.Decode(u))
	case utf8.Valid(b):
		s = string(b)
	default:
		if decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(b); err == nil {
			s = string(decoded)
		} else {
			s = strings.ToValidUTF8(string(b), "�")
		}
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// ReadText 读取文本附件的内容。文件很大时只读开头一段，truncated 为 true。
func ReadText(path string) (text string, truncated bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, readTextBytes+1))
	if err != nil {
		return "", false, err
	}
	if len(b) > readTextBytes {
		b, truncated = trimPartialRune(b[:readTextBytes]), true
	}
	return decodeText(b), truncated, nil
}

var privateKeyRe = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----|PuTTY-User-Key-File-`)

// IsPrivateKeyText 判断一段文本里是不是带着私钥（PEM、OpenSSH、PuTTY 格式）。
// 私钥文件的内容不发给模型：导入证书库、上传到服务器都由程序直接读取文件。
func IsPrivateKeyText(text string) bool { return privateKeyRe.MatchString(text) }

// PrivateKeyHint 告诉模型私钥附件不能阅读，以及该怎样使用它。
const PrivateKeyHint = "里面有私钥，为了安全，内容不提供给你：要导入证书库用 import_certificate，要放到服务器上用 upload_attachment"

// ---- 附件在对话里的呈现 ----

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// attachNote 生成附在用户消息后面、给模型看的附件说明；较短的文本文件直接把内容带上。
func (s *AttachStore) attachNote(sessionID string, atts []Attachment) string {
	if len(atts) == 0 {
		return ""
	}
	var list, inline strings.Builder
	total, hasFile, hasImage := 0, false, false
	for _, att := range atts {
		switch {
		case att.Kind == AttachImage:
			hasImage = true
			fmt.Fprintf(&list, "- %s「%s」：图片，%d×%d\n", att.ID, att.Name, att.Width, att.Height)
		case att.Text:
			hasFile = true
			kind, note := "文本文件", "内容用 read_attachment 阅读"
			if att.Size <= inlineTextRunes*4 {
				if p, err := s.Local(sessionID, att); err == nil {
					if text, _, err := ReadText(p); err == nil {
						if IsPrivateKeyText(text) {
							kind, note = "私钥文件", PrivateKeyHint
						} else if n := utf8.RuneCountInString(text); n <= inlineTextRunes && total+n <= inlineTextTotal {
							total += n
							note = "内容附在下面"
							fmt.Fprintf(&inline, "\n<attachment id=%q name=%q>\n%s\n</attachment>\n", att.ID, att.Name, strings.TrimRight(text, "\n"))
						}
					}
				}
			}
			fmt.Fprintf(&list, "- %s「%s」：%s，%s，%s\n", att.ID, att.Name, kind, humanBytes(att.Size), note)
		default:
			hasFile = true
			fmt.Fprintf(&list, "- %s「%s」：二进制文件，%s，不能直接阅读\n", att.ID, att.Name, humanBytes(att.Size))
		}
	}
	var b strings.Builder
	b.WriteString("\n\n<attachments>\n用户随这条消息附带了下面的内容。它们在用户自己的电脑上，不在任何服务器上；每行开头是附件的 ID。\n")
	b.WriteString(list.String())
	if hasImage {
		b.WriteString("图片已经附在这条消息里，可以直接看。\n")
	}
	if hasFile {
		b.WriteString("需要把文件放到服务器上（部署、安装、替换配置等）时调用 upload_attachment；文本文件可以用 read_attachment 分段阅读。\n")
	}
	b.WriteString("</attachments>\n")
	b.WriteString(inline.String())
	return b.String()
}

// FindAttachment 在会话里按 ID 或文件名查找附件；名字相同的有多个时取最近发的那个。
func FindAttachment(sess *Session, ref string) (Attachment, error) {
	ref = strings.TrimSpace(ref)
	var names []string
	var byName *Attachment
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		for j := range sess.Messages[i].Attachments {
			att := &sess.Messages[i].Attachments[j]
			if att.ID == ref {
				return *att, nil
			}
			if byName == nil && strings.EqualFold(att.Name, ref) {
				byName = att
			}
			names = append(names, att.ID+"「"+att.Name+"」")
		}
	}
	if byName != nil {
		return *byName, nil
	}
	if len(names) == 0 {
		return Attachment{}, errors.New("这个对话里没有附件：请用户先把文件发过来（点输入框的回形针按钮，或把文件拖进对话）")
	}
	return Attachment{}, fmt.Errorf("没有找到附件「%s」。这个对话里的附件：%s", ref, strings.Join(names, "、"))
}

// shown 返回用户消息在界面上显示的内容；只发了附件没有文字时，用附件的名字代替。
func shown(m Message) string {
	text := m.Display
	if text == "" && len(m.Attachments) == 0 {
		text = m.Content
	}
	if len(m.Attachments) == 0 {
		return text
	}
	names := make([]string, len(m.Attachments))
	for i, att := range m.Attachments {
		names[i] = att.Name
	}
	return strings.TrimSpace(text + "（附件：" + strings.Join(names, "、") + "）")
}

// ---- 发给模型的图片 ----

const (
	maxRequestImages     = 20       // 一次请求最多带这么多张图片，更早的用文字代替
	maxRequestImageBytes = 12 << 20 // 一次请求里图片的总大小
)

// withImages 返回一份消息副本：最近的图片填上内容，放不下的更早的图片标记为省略。
// off 不为空表示这次请求不带图片（值是原因）。cache 在一轮对话的多次请求之间复用，避免反复读文件。
func (s *AttachStore) withImages(sessionID string, msgs []Message, off string, cache map[string][]byte) []Message {
	has := false
	for _, m := range msgs {
		if len(m.Attachments) > 0 {
			has = true
			break
		}
	}
	if !has {
		return msgs
	}
	out := make([]Message, len(msgs))
	copy(out, msgs)
	count, size := 0, 0
	for i := len(out) - 1; i >= 0; i-- {
		if len(out[i].Attachments) == 0 {
			continue
		}
		atts := make([]Attachment, len(out[i].Attachments))
		copy(atts, out[i].Attachments)
		for j := len(atts) - 1; j >= 0; j-- {
			att := &atts[j]
			if att.Kind != AttachImage {
				continue
			}
			if off != "" {
				att.Omitted = off
				continue
			}
			data, ok := cache[att.ID]
			if !ok {
				data, _ = os.ReadFile(s.Stored(sessionID, *att))
				cache[att.ID] = data
			}
			switch {
			case len(data) == 0:
				att.Omitted = "图片文件已经找不到了"
			case count >= maxRequestImages || size+len(data) > maxRequestImageBytes:
				att.Omitted = "对话里的图片太多，较早的没有再发送"
			default:
				att.Data = data
				count++
				size += len(data)
			}
		}
		out[i].Attachments = atts
	}
	return out
}

// imagePlaceholder 是没有发给模型的图片在消息里的文字说明。
func imagePlaceholder(att Attachment, noImages bool) string {
	reason := att.Omitted
	if reason == "" || noImages {
		reason = "当前模型不能查看图片"
	}
	return fmt.Sprintf("[图片「%s」没有发给你：%s]", att.Name, reason)
}

// userParts 把一条用户消息拆成发给模型的文字和图片。没有发出去的图片在文字里留一句说明。
func userParts(m Message, noImages bool) (text string, images []Attachment) {
	text = m.Content
	for _, att := range m.Attachments {
		if att.Kind != AttachImage {
			continue
		}
		if len(att.Data) > 0 && !noImages {
			images = append(images, att)
		} else {
			text += "\n" + imagePlaceholder(att, noImages)
		}
	}
	return text, images
}

func hasImageData(msgs []Message) bool {
	for _, m := range msgs {
		for _, att := range m.Attachments {
			if len(att.Data) > 0 {
				return true
			}
		}
	}
	return false
}
