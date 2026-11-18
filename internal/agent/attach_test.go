package agent

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf16"

	"golang.org/x/image/bmp"
	"golang.org/x/text/encoding/simplifiedchinese"

	"sshpilot/internal/store"
)

func testImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 7), uint8(y * 5), uint8(x + y), 255})
		}
	}
	return img
}

func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// withOrientation 在 JPEG 的开头插入一段只记录拍摄方向的 EXIF。
func withOrientation(jpg []byte, o uint16) []byte {
	tiff := []byte("II*\x00\x08\x00\x00\x00\x01\x00")
	entry := make([]byte, 12)
	binary.LittleEndian.PutUint16(entry[0:], 0x0112)
	binary.LittleEndian.PutUint16(entry[2:], 3)
	binary.LittleEndian.PutUint32(entry[4:], 1)
	binary.LittleEndian.PutUint16(entry[8:], o)
	body := append(append([]byte("Exif\x00\x00"), tiff...), entry...)
	body = append(body, 0, 0, 0, 0)
	seg := []byte{0xFF, 0xE1, byte((len(body) + 2) >> 8), byte(len(body) + 2)}
	return append(append(append([]byte{}, jpg[:2]...), append(seg, body...)...), jpg[2:]...)
}

func TestPrepareImage(t *testing.T) {
	// 尺寸合适的小图原样使用
	small := pngBytes(t, testImage(64, 32))
	out, mime, w, h, err := prepareImage(small)
	if err != nil || mime != "image/png" || w != 64 || h != 32 || !bytes.Equal(out, small) {
		t.Fatalf("small png: %v %s %dx%d same=%v", err, mime, w, h, bytes.Equal(out, small))
	}
	// 大图缩到长边 2000
	out, mime, w, h, err = prepareImage(pngBytes(t, testImage(3000, 1500)))
	if err != nil || w != 2000 || h != 1000 {
		t.Fatalf("large png: %v %s %dx%d", err, mime, w, h)
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(out)); err != nil || cfg.Width != 2000 || cfg.Height != 1000 {
		t.Fatalf("large png output: %v %+v", err, cfg)
	}
	// BMP 转成模型接受的格式
	var b bytes.Buffer
	if err := bmp.Encode(&b, testImage(40, 20)); err != nil {
		t.Fatal(err)
	}
	if _, mime, w, h, err = prepareImage(b.Bytes()); err != nil || mime != "image/png" || w != 40 || h != 20 {
		t.Fatalf("bmp: %v %s %dx%d", err, mime, w, h)
	}
	// 手机竖着拍的照片：按 EXIF 的方向摆正，宽高对调
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			c := color.RGBA{255, 255, 255, 255}
			if x < 20 {
				c = color.RGBA{0, 0, 0, 255} // 左半边是黑的
			}
			img.Set(x, y, c)
		}
	}
	b.Reset()
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	rotated := withOrientation(b.Bytes(), 6)
	if got := exifOrientation(rotated); got != 6 {
		t.Fatalf("exif orientation = %d", got)
	}
	out, mime, w, h, err = prepareImage(rotated)
	if err != nil || mime != "image/jpeg" || w != 20 || h != 40 {
		t.Fatalf("rotated jpeg: %v %s %dx%d", err, mime, w, h)
	}
	turned, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	// 顺时针转 90° 后，原来的左半边（黑）到了上半边
	if r, _, _, _ := turned.At(10, 5).RGBA(); r > 0x4000 {
		t.Errorf("top half should be dark after rotation, got %#x", r)
	}
	if r, _, _, _ := turned.At(10, 35).RGBA(); r < 0xC000 {
		t.Errorf("bottom half should be light after rotation, got %#x", r)
	}
	if _, _, _, _, err := prepareImage([]byte("not an image")); err == nil {
		t.Error("garbage should be rejected")
	}
}

func TestAttachStore(t *testing.T) {
	dir := t.TempDir()
	src := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(src, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// 上次没发出去的附件在启动时清掉
	_ = os.MkdirAll(filepath.Join(dir, draftDir), 0o700)
	_ = os.WriteFile(filepath.Join(dir, draftDir, "stale.png"), []byte("x"), 0o600)
	s := NewAttachStore(dir, false)
	if _, err := os.Stat(filepath.Join(dir, draftDir, "stale.png")); err == nil {
		t.Error("stale drafts should be removed on start")
	}

	gbk, _ := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("# 配置文件\nlisten 80;\n"))
	u16 := []byte{0xFF, 0xFE}
	for _, u := range utf16.Encode([]rune("服务已启动\r\n")) {
		u16 = binary.LittleEndian.AppendUint16(u16, u)
	}
	cases := []struct {
		name string
		data []byte
		text bool
	}{
		{"nginx.conf", []byte("server {\n  listen 80;\n}\n"), true},
		{"gbk.txt", gbk, true},
		{"utf16.log", u16, true},
		{"app.tar.gz", append([]byte{0x1f, 0x8b, 0x08, 0x00, 0x00}, bytes.Repeat([]byte{0x00, 0xff, 0x13}, 400)...), false},
		{"empty.txt", nil, true},
	}
	for _, c := range cases {
		att, err := s.AddPath(write(c.name, c.data))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if att.Kind != AttachFile || att.Text != c.text || att.File != "" || att.Size != int64(len(c.data)) || att.Name != c.name {
			t.Errorf("%s: %+v", c.name, att)
		}
	}
	if text, _, err := ReadText(filepath.Join(src, "gbk.txt")); err != nil || !strings.Contains(text, "配置文件") {
		t.Errorf("gbk text = %q, %v", text, err)
	}
	if text, _, err := ReadText(filepath.Join(src, "utf16.log")); err != nil || text != "服务已启动\n" {
		t.Errorf("utf16 text = %q, %v", text, err)
	}
	if _, err := s.AddPath(src); err == nil || !strings.Contains(err.Error(), "文件夹") {
		t.Errorf("directory: %v", err)
	}
	if _, err := s.AddPath(filepath.Join(src, "missing")); err == nil {
		t.Error("missing file should fail")
	}

	// 图片：处理后在数据目录里保存一份，同时记得原图的位置
	shot := write("截图.png", pngBytes(t, testImage(2400, 1200)))
	img, err := s.AddPath(shot)
	if err != nil || img.Kind != AttachImage || img.Width != 2000 || img.Height != 1000 || img.File == "" || img.Path != shot {
		t.Fatalf("image: %v %+v", err, img)
	}
	if _, err := os.Stat(s.Stored("", img)); err != nil {
		t.Fatalf("image copy missing: %v", err)
	}
	// 扩展名是图片、内容不是：当作普通文件
	fake, err := s.AddPath(write("fake.png", []byte("this is not a png")))
	if err != nil || fake.Kind != AttachFile || !fake.Text {
		t.Errorf("fake image: %v %+v", err, fake)
	}

	// 粘贴进来的内容没有原始位置：整个保存一份
	pasted, err := s.AddData("image.png", pngBytes(t, testImage(30, 30)))
	if err != nil || pasted.Kind != AttachImage || pasted.Path != "" || pasted.File == "" {
		t.Fatalf("pasted image: %v %+v", err, pasted)
	}
	note, err := s.AddData(`C:\tmp\..\笔记.txt`, []byte("重启前先确认业务低峰"))
	if err != nil || note.Name != "笔记.txt" || !note.Text || !strings.HasSuffix(note.File, ".txt") {
		t.Fatalf("pasted file: %v %+v", err, note)
	}
	if _, err := s.AddData("big.bin", make([]byte, MaxPastedBytes+1)); err == nil {
		t.Error("oversized paste should be rejected")
	}

	// 丢弃：保存的那一份跟着删除，原文件不动
	s.Discard(pasted.ID)
	if _, err := os.Stat(s.Stored("", pasted)); err == nil {
		t.Error("discarded copy should be removed")
	}
	if _, ok := s.Draft(pasted.ID); ok {
		t.Error("discarded draft should be forgotten")
	}

	// 发出去：归会话所有，保存的文件移到会话的目录
	if _, err := s.take("sess1", []string{img.ID, "ffffffffffffffff"}); err == nil {
		t.Error("unknown attachment id should fail")
	}
	if _, ok := s.Draft(img.ID); !ok {
		t.Fatal("a failed send must leave the drafts in place")
	}
	got, err := s.take("sess1", []string{img.ID, note.ID})
	if err != nil || len(got) != 2 {
		t.Fatalf("take: %v %+v", err, got)
	}
	for _, att := range got {
		if _, err := os.Stat(s.Stored("sess1", att)); err != nil {
			t.Errorf("%s not moved: %v", att.Name, err)
		}
	}
	if _, ok := s.Draft(img.ID); ok {
		t.Error("sent attachment should leave the drafts")
	}
	// 原图还在时用原图（上传到服务器的是原图），原图没了用保存的那一份
	if p, err := s.Local("sess1", got[0]); err != nil || p != shot {
		t.Errorf("local = %s, %v", p, err)
	}
	_ = os.Remove(shot)
	if p, err := s.Local("sess1", got[0]); err != nil || p != s.Stored("sess1", got[0]) {
		t.Errorf("local after removing the original = %s, %v", p, err)
	}
	gone := Attachment{ID: "0000000000000000", Name: "gone.zip", Path: filepath.Join(src, "gone.zip")}
	if _, err := s.Local("sess1", gone); err == nil || !strings.Contains(err.Error(), "不在原来的位置") {
		t.Errorf("missing original: %v", err)
	}
	s.RemoveSession("sess1")
	if _, err := os.Stat(filepath.Join(dir, "sess1")); err == nil {
		t.Error("session attachments should be removed")
	}
	if p := s.Stored("../x", got[0]); p != "" {
		t.Errorf("invalid session id must not resolve: %s", p)
	}
}

// imageProvider 记录每次请求；rejectImages 为 true 时像不支持图片的模型一样拒绝带图片的请求。
type imageProvider struct {
	mu           sync.Mutex
	rejectImages bool
	reqs         []ChatRequest
}

func (p *imageProvider) Name() string { return "fake" }

func (p *imageProvider) Chat(_ context.Context, req ChatRequest, _ StreamHandler) (*ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reqs = append(p.reqs, req)
	if p.rejectImages && !req.NoImages && hasImageData(req.Messages) {
		return nil, &APIError{Status: 400, Msg: "模型接口返回 HTTP 400：unknown variant `image_url`, expected `text`"}
	}
	return &ChatResponse{Message: Message{Role: "assistant", Content: "收到"}, StopReason: "end_turn"}, nil
}

func (p *imageProvider) last() ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reqs[len(p.reqs)-1]
}

func TestSendWithAttachments(t *testing.T) {
	p := &imageProvider{}
	old := providerFactory
	providerFactory = func(store.AISettings) (Provider, error) { return p, nil }
	t.Cleanup(func() { providerFactory = old })
	events := make(chan Event, 256)
	settings := store.AISettings{Provider: "openai", Model: "vision-model", MaxSteps: 4}
	a := New(Config{DataDir: t.TempDir(), Settings: func() store.AISettings { return settings }, Emit: func(_ string, data ...any) {
		if e, ok := data[0].(Event); ok {
			events <- e
		}
	}})
	src := t.TempDir()
	conf := filepath.Join(src, "nginx.conf")
	_ = os.WriteFile(conf, []byte("server {\n  listen 8080;\n}\n"), 0o600)
	big := filepath.Join(src, "access.log")
	_ = os.WriteFile(big, []byte(strings.Repeat("GET /index.html 200\n", 5000)), 0o600)
	pkg := filepath.Join(src, "app.tar.gz")
	_ = os.WriteFile(pkg, append([]byte{0x1f, 0x8b, 0, 0, 0}, make([]byte, 2048)...), 0o600)
	var ids []string
	for _, f := range []string{conf, big, pkg} {
		att, err := a.Attach.AddPath(f)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, att.ID)
	}
	shot, err := a.Attach.AddData("image.png", pngBytes(t, testImage(120, 80)))
	if err != nil {
		t.Fatal(err)
	}
	ids = append(ids, shot.ID)

	sess, _ := a.NewSession()
	// 只发附件、不写字也可以
	if err := a.Send(sess.ID, "", ids...); err != nil {
		t.Fatal(err)
	}
	var user *Message
	waitFor(t, events, "done", func(e Event) {
		if e.Type == "user" {
			user = e.Message
		}
	})
	if user == nil || len(user.Attachments) != 4 || user.Display != "" {
		t.Fatalf("user message: %+v", user)
	}
	req := p.last()
	msg := req.Messages[0]
	for _, want := range []string{"<attachments>", ids[0] + "「nginx.conf」", "内容附在下面", "listen 8080;", "「access.log」：文本文件", "内容用 read_attachment 阅读", "「app.tar.gz」：二进制文件", "「image.png」：图片，120×80", "upload_attachment"} {
		if !strings.Contains(msg.Content, want) {
			t.Errorf("message for the model lacks %q:\n%s", want, msg.Content)
		}
	}
	if strings.Contains(msg.Content, "GET /index.html") {
		t.Error("a large text file must not be inlined")
	}
	text, images := userParts(msg, req.NoImages)
	if len(images) != 1 || len(images[0].Data) == 0 || images[0].Mime != "image/png" || strings.Contains(text, "没有发给你") {
		t.Fatalf("image for the model: %d images, text %q", len(images), text)
	}
	// 会话记录里只有附件的信息，没有图片内容；标题用第一个附件的名字
	saved, _ := a.Sessions.Load(sess.ID)
	if saved.Title != "nginx.conf" {
		t.Errorf("title = %q", saved.Title)
	}
	raw, _ := os.ReadFile(a.Sessions.path(sess.ID))
	if len(raw) > 20000 || !strings.Contains(string(raw), `"attachments"`) {
		t.Errorf("session file: %d bytes", len(raw))
	}
	if _, err := os.Stat(a.Attach.Stored(sess.ID, saved.Messages[0].Attachments[3])); err != nil {
		t.Errorf("image not stored with the session: %v", err)
	}
	if att, err := FindAttachment(saved, "NGINX.CONF"); err != nil || att.ID != ids[0] {
		t.Errorf("find by name: %v %+v", err, att)
	}
	if _, err := FindAttachment(saved, "nope"); err == nil || !strings.Contains(err.Error(), "app.tar.gz") {
		t.Errorf("find missing: %v", err)
	}
	// 历史里的图片下一轮接着发
	_ = a.Send(sess.ID, "这个端口对吗")
	waitFor(t, events, "done", nil)
	if _, images := userParts(p.last().Messages[0], false); len(images) != 1 {
		t.Error("images in history should be sent again")
	}
	if shown(saved.Messages[0]) != "（附件：nginx.conf、access.log、app.tar.gz、image.png）" {
		t.Errorf("shown = %q", shown(saved.Messages[0]))
	}

	// 不支持图片的模型：去掉图片重试，告诉用户，之后同一个模型不再发图片
	p.rejectImages = true
	before := len(p.reqs)
	_ = a.Send(sess.ID, "再看看")
	var notices []string
	waitFor(t, events, "done", func(e Event) {
		if e.Type == "notice" {
			notices = append(notices, e.Message.Content)
		}
		if e.Type == "error" {
			t.Errorf("unexpected error event: %s", e.Error)
		}
	})
	if len(p.reqs)-before != 2 || !p.last().NoImages {
		t.Fatalf("expected one rejected request and one retry without images, got %d", len(p.reqs)-before)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "vision-model") || !strings.Contains(notices[0], "没有接受图片") {
		t.Errorf("notices = %q", notices)
	}
	if text, images := userParts(p.last().Messages[0], true); len(images) != 0 || !strings.Contains(text, "[图片「image.png」没有发给你：当前模型不能查看图片]") {
		t.Errorf("placeholder missing: %q", text)
	}
	before = len(p.reqs)
	_ = a.Send(sess.ID, "继续")
	waitFor(t, events, "done", nil)
	if len(p.reqs)-before != 1 || !p.last().NoImages || hasImageData(p.last().Messages) {
		t.Errorf("the same model should not be sent images again (%d requests)", len(p.reqs)-before)
	}
	// 换了模型就重新尝试
	settings.Model = "another-model"
	p.rejectImages = false
	_ = a.Send(sess.ID, "换了模型")
	waitFor(t, events, "done", nil)
	if p.last().NoImages || !hasImageData(p.last().Messages) {
		t.Error("a different model should get the images again")
	}

	// 失效的附件：消息不发出，会话不会卡在“处理中”
	if err := a.Send(sess.ID, "x", "ffffffffffffffff"); err == nil {
		t.Error("unknown attachment should fail")
	}
	if a.IsRunning(sess.ID) {
		t.Error("a failed send must not leave the session running")
	}
	if err := a.DeleteSession(sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(a.Attach.dir, sess.ID)); err == nil {
		t.Error("deleting the session should remove its attachments")
	}
}

func TestImagesInProviderRequests(t *testing.T) {
	data := []byte("PNGDATA")
	msgs := []Message{
		{Role: "user", Content: "看这张图", Attachments: []Attachment{{ID: "a1", Name: "a.png", Kind: AttachImage, Mime: "image/png", Data: data}, {ID: "f1", Name: "a.conf", Kind: AttachFile, Text: true}}},
		{Role: "assistant", Content: "好的"},
		{Role: "user", Content: "", Attachments: []Attachment{{ID: "a2", Name: "b.jpg", Kind: AttachImage, Mime: "image/jpeg", Data: data}}},
	}
	// OpenAI 兼容接口：content 是「文字 + 图片」数组
	b, _ := json.Marshal(toOpenAIMessages("sys", msgs, false))
	for _, want := range []string{`{"text":"看这张图","type":"text"}`, `"image_url":{"url":"data:image/png;base64,UE5HREFUQQ=="}`, `"url":"data:image/jpeg;base64,`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("openai request lacks %s:\n%s", want, b)
		}
	}
	b, _ = json.Marshal(toOpenAIMessages("sys", msgs, true))
	if strings.Contains(string(b), "image_url") || !strings.Contains(string(b), "[图片「a.png」没有发给你：当前模型不能查看图片]") {
		t.Errorf("openai request without images:\n%s", b)
	}
	// Claude：图片块在前，文字在后；只有图片时不带空的文字块
	b, _ = json.Marshal(toAnthropicMessages(msgs, false))
	s := string(b)
	if !strings.Contains(s, `"media_type":"image/png"`) || !strings.Contains(s, `"data":"UE5HREFUQQ=="`) || strings.Index(s, `"type":"image"`) > strings.Index(s, "看这张图") {
		t.Errorf("anthropic request:\n%s", s)
	}
	if strings.Contains(s, `"text":""`) || strings.Contains(s, "空消息") {
		t.Errorf("image-only message should carry no empty text block:\n%s", s)
	}
	b, _ = json.Marshal(toAnthropicMessages(msgs, true))
	if strings.Contains(string(b), `"type":"image"`) || !strings.Contains(string(b), "没有发给你") {
		t.Errorf("anthropic request without images:\n%s", b)
	}
}

func TestRejectsImages(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{&APIError{Status: 400, Msg: "unknown variant `image_url`"}, true},
		{&APIError{Status: 422, Msg: "Failed to deserialize the JSON body"}, true},
		{&APIError{Status: 500, Msg: "this model does not support image input"}, true},
		{&APIError{Status: 500, Msg: "internal error"}, false},
		{&APIError{Status: 401, Msg: "API Key 无效"}, false},
		{&APIError{Status: 429, Msg: "请求过于频繁"}, false},
		{context.DeadlineExceeded, false},
	} {
		if got := rejectsImages(c.err); got != c.want {
			t.Errorf("%v: got %v, want %v", c.err, got, c.want)
		}
	}
}

func TestImageBudget(t *testing.T) {
	dir := t.TempDir()
	s := NewAttachStore(dir, false)
	_ = os.MkdirAll(filepath.Join(dir, "sess1"), 0o700)
	var msgs []Message
	for i := 0; i < maxRequestImages+5; i++ {
		name := string(rune('a'+i)) + ".png"
		_ = os.WriteFile(filepath.Join(dir, "sess1", name), []byte("img"), 0o600)
		msgs = append(msgs, Message{Role: "user", Content: "图", Attachments: []Attachment{{ID: name, Name: name, Kind: AttachImage, Mime: "image/png", File: name}}})
	}
	msgs = append(msgs, Message{Role: "user", Content: "丢了", Attachments: []Attachment{{ID: "lost", Name: "lost.png", Kind: AttachImage, File: "lost.png"}}})
	out := s.withImages("sess1", msgs, "", map[string][]byte{})
	sent, omitted := 0, 0
	for i, m := range out {
		att := m.Attachments[0]
		switch {
		case len(att.Data) > 0:
			sent++
			if i < 5 {
				t.Errorf("message %d is among the oldest and should be omitted", i)
			}
		case att.Omitted != "":
			omitted++
		}
	}
	if sent != maxRequestImages || omitted != 6 {
		t.Errorf("sent %d, omitted %d", sent, omitted)
	}
	if text, _ := userParts(out[0], false); !strings.Contains(text, "较早的没有再发送") {
		t.Errorf("old image placeholder: %q", text)
	}
	if text, _ := userParts(out[len(out)-1], false); !strings.Contains(text, "已经找不到了") {
		t.Errorf("lost image placeholder: %q", text)
	}
	// 原来的消息不能被改动（图片内容不能进会话记录）
	if len(msgs[10].Attachments[0].Data) != 0 || msgs[0].Attachments[0].Omitted != "" {
		t.Error("withImages must not modify the session's messages")
	}
	if off := s.withImages("sess1", msgs, "当前模型不能查看图片", map[string][]byte{}); hasImageData(off) {
		t.Error("no image data expected when images are off")
	}
}

// 私钥文件的内容不发给模型：既不附在消息里，说明里也只告诉模型该用哪个工具处理它。
func TestPrivateKeyAttachmentsStayLocal(t *testing.T) {
	src := t.TempDir()
	s := NewAttachStore(t.TempDir(), false)
	add := func(name, text string) Attachment {
		p := filepath.Join(src, name)
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		att, err := s.AddPath(p)
		if err != nil {
			t.Fatal(err)
		}
		return att
	}
	secret := "MIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQgSECRETSECRETSECRET"
	cert := add("fullchain.pem", "-----BEGIN CERTIFICATE-----\nMIIBcertificatebody\n-----END CERTIFICATE-----\n")
	key := add("privkey.pem", "-----BEGIN PRIVATE KEY-----\n"+secret+"\n-----END PRIVATE KEY-----\n")
	both := add("site.pem", "-----BEGIN CERTIFICATE-----\nMIIBother\n-----END CERTIFICATE-----\n-----BEGIN EC PRIVATE KEY-----\n"+secret+"\n-----END EC PRIVATE KEY-----\n")
	ssh := add("id_ed25519", "-----BEGIN OPENSSH PRIVATE KEY-----\n"+secret+"\n-----END OPENSSH PRIVATE KEY-----\n")
	note := s.attachNote("", []Attachment{cert, key, both, ssh})
	if strings.Contains(note, secret) {
		t.Fatalf("private key content leaked into the message: %s", note)
	}
	if !strings.Contains(note, "MIIBcertificatebody") {
		t.Error("the certificate itself is public and should still be inlined")
	}
	for _, att := range []Attachment{key, both, ssh} {
		if !strings.Contains(note, att.ID+"「"+att.Name+"」：私钥文件") {
			t.Errorf("%s should be listed as a private key file: %s", att.Name, note)
		}
	}
	if !strings.Contains(note, "import_certificate") || !strings.Contains(note, "upload_attachment") {
		t.Errorf("the note should say how to use a private key file: %s", note)
	}
	for text, want := range map[string]bool{
		"-----BEGIN RSA PRIVATE KEY-----":       true,
		"-----BEGIN ENCRYPTED PRIVATE KEY-----": true,
		"PuTTY-User-Key-File-3: ssh-ed25519":    true,
		"-----BEGIN PUBLIC KEY-----":            false,
		"-----BEGIN CERTIFICATE-----":           false,
		"ssl_certificate_key /etc/ssl/a.key;":   false,
	} {
		if IsPrivateKeyText(text) != want {
			t.Errorf("IsPrivateKeyText(%q) should be %v", text, want)
		}
	}
}
