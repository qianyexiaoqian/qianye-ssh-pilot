package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"sshpilot/internal/store"
)

// 技能是程序性记忆：skills/<name>/SKILL.md，格式兼容 agentskills.io（YAML frontmatter + Markdown 正文）。
type SkillMeta struct {
	Name        string   `json:"name" yaml:"name"`
	Description string   `json:"description" yaml:"description"`
	Version     int      `json:"version" yaml:"version"`
	Tags        []string `json:"tags" yaml:"tags,omitempty"`
	CreatedBy   string   `json:"createdBy" yaml:"created_by,omitempty"`
	UpdatedAt   string   `json:"updatedAt" yaml:"updated_at,omitempty"`
	Uses        int      `json:"uses" yaml:"-"`
	LastUsed    int64    `json:"lastUsed" yaml:"-"`
	// Pinned：用户固定的技能，不会被自动归档，AI 不能删除。Stale：很久没有用到。ArchivedAt：归档时间（只对已归档的技能有值）。
	Pinned     bool  `json:"pinned" yaml:"-"`
	Stale      bool  `json:"stale" yaml:"-"`
	ArchivedAt int64 `json:"archivedAt" yaml:"-"`
}

type Skill struct {
	SkillMeta
	Content string `json:"content"`
	Body    string `json:"body"`
}

var skillNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

const maxSkillBody = 24000

type skillStats struct {
	Uses     int   `json:"uses"`
	LastUsed int64 `json:"lastUsed"`
	Pinned   bool  `json:"pinned,omitempty"`
}

type SkillStore struct {
	mu  sync.Mutex
	dir string
}

func NewSkillStore(dir string) *SkillStore {
	_ = os.MkdirAll(dir, 0o700)
	return &SkillStore{dir: dir}
}

func (s *SkillStore) path(name string) string { return filepath.Join(s.dir, name, "SKILL.md") }

func splitFrontmatter(content string) (SkillMeta, string, error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	var meta SkillMeta
	if !strings.HasPrefix(content, "---\n") {
		return meta, content, errors.New("缺少 YAML frontmatter")
	}
	rest := content[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return meta, content, errors.New("frontmatter 没有结束标记 ---")
	}
	if err := yaml.Unmarshal([]byte(rest[:end]), &meta); err != nil {
		return meta, content, fmt.Errorf("frontmatter 解析失败: %w", err)
	}
	body := strings.TrimLeft(rest[end+4:], "\n")
	return meta, body, nil
}

func renderSkill(meta SkillMeta, body string) string {
	fm, _ := yaml.Marshal(meta)
	return "---\n" + string(fm) + "---\n\n" + strings.TrimSpace(body) + "\n"
}

func (s *SkillStore) loadStats() map[string]skillStats {
	stats := map[string]skillStats{}
	if b, err := os.ReadFile(filepath.Join(s.dir, ".stats.json")); err == nil {
		_ = json.Unmarshal(b, &stats)
	}
	return stats
}

func (s *SkillStore) saveStats(stats map[string]skillStats) {
	if b, err := json.Marshal(stats); err == nil {
		_ = store.WriteFileAtomic(filepath.Join(s.dir, ".stats.json"), b, 0o600)
	}
}

func (s *SkillStore) List() []SkillMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, _ := os.ReadDir(s.dir)
	stats := s.loadStats()
	out := []SkillMeta{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		b, err := os.ReadFile(s.path(e.Name()))
		if err != nil {
			continue
		}
		meta, _, err := splitFrontmatter(string(b))
		if err != nil {
			meta = SkillMeta{Description: "（frontmatter 无效）"}
		}
		meta.Name = e.Name()
		meta.Uses = stats[e.Name()].Uses
		meta.LastUsed = stats[e.Name()].LastUsed
		meta.Pinned = stats[e.Name()].Pinned
		meta.Stale = time.Since(s.lastActivity(e.Name(), stats[e.Name()])) > skillStaleAfter
		out = append(out, meta)
	}
	slices.SortFunc(out, func(a, b SkillMeta) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Get 读取技能全文；trackUse 为 true 时记录使用次数。
func (s *SkillStore) Get(name string, trackUse bool) (*Skill, error) {
	if !skillNameRe.MatchString(name) {
		return nil, fmt.Errorf("技能名无效: %s", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path(name))
	if err != nil {
		return nil, fmt.Errorf("技能 %s 不存在", name)
	}
	meta, body, _ := splitFrontmatter(string(b))
	meta.Name = name
	stats := s.loadStats()
	if trackUse {
		st := stats[name]
		st.Uses++
		st.LastUsed = nowMs()
		stats[name] = st
		s.saveStats(stats)
	}
	meta.Uses = stats[name].Uses
	meta.LastUsed = stats[name].LastUsed
	meta.Pinned = stats[name].Pinned
	return &Skill{SkillMeta: meta, Content: string(b), Body: body}, nil
}

func validateSkill(meta SkillMeta, body string) error {
	if strings.TrimSpace(meta.Description) == "" {
		return errors.New("技能需要 description（一句话说明用途）")
	}
	if utf8.RuneCountInString(meta.Description) > 200 {
		return errors.New("description 过长（≤200 字）")
	}
	if strings.TrimSpace(body) == "" {
		return errors.New("技能正文不能为空")
	}
	if len(body) > maxSkillBody {
		return fmt.Errorf("技能正文过长（%d 字节，上限 %d），请精简或拆分", len(body), maxSkillBody)
	}
	if zeroWidthRe.MatchString(body) || zeroWidthRe.MatchString(meta.Description) {
		return errors.New("内容包含不可见字符，已拒绝")
	}
	return nil
}

func today() string { return time.Now().Format("2006-01-02") }

// Create 新建技能。content 可以是完整 SKILL.md（含 frontmatter），也可以只是正文（此时需要 description）。
func (s *SkillStore) Create(name, content, description, createdBy string) error {
	if !skillNameRe.MatchString(name) {
		return errors.New("技能名只能包含小写字母、数字和连字符，例如 nginx-cert-replace")
	}
	meta, body, err := splitFrontmatter(content)
	if err != nil {
		meta, body = SkillMeta{}, content
	}
	if description != "" {
		meta.Description = description
	}
	meta.Name = name
	meta.Version = 1
	meta.CreatedBy = createdBy
	meta.UpdatedAt = today()
	if err := validateSkill(meta, body); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.path(name)); err == nil {
		return fmt.Errorf("技能 %s 已存在，请使用 patch 或 edit 更新", name)
	}
	return store.WriteFileAtomic(s.path(name), []byte(renderSkill(meta, body)), 0o600)
}

func (s *SkillStore) write(name string, update func(meta *SkillMeta, body string) (string, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path(name))
	if err != nil {
		return fmt.Errorf("技能 %s 不存在", name)
	}
	meta, body, _ := splitFrontmatter(string(b))
	newBody, err := update(&meta, body)
	if err != nil {
		return err
	}
	meta.Name = name
	meta.Version++
	meta.UpdatedAt = today()
	if err := validateSkill(meta, newBody); err != nil {
		return err
	}
	return store.WriteFileAtomic(s.path(name), []byte(renderSkill(meta, newBody)), 0o600)
}

// Patch 对技能全文做一次精确替换（old 必须唯一出现）。
func (s *SkillStore) Patch(name, oldStr, newStr string) error {
	if oldStr == "" {
		return errors.New("patch 需要 old_string")
	}
	return s.write(name, func(meta *SkillMeta, body string) (string, error) {
		full := renderSkill(*meta, body)
		switch strings.Count(full, oldStr) {
		case 0:
			return "", errors.New("没有在技能中找到 old_string（请先 skill_view 查看当前内容）")
		case 1:
		default:
			return "", errors.New("old_string 出现了多次，请提供更长的上下文")
		}
		updated := strings.Replace(full, oldStr, newStr, 1)
		m, b, err := splitFrontmatter(updated)
		if err != nil {
			return "", fmt.Errorf("修改破坏了 frontmatter: %w", err)
		}
		version := meta.Version
		*meta = m
		meta.Version = version
		return b, nil
	})
}

// Edit 整体替换技能内容。
func (s *SkillStore) Edit(name, content, description string) error {
	return s.write(name, func(meta *SkillMeta, _ string) (string, error) {
		m, body, err := splitFrontmatter(content)
		if err != nil {
			body = content
		} else {
			version := meta.Version
			*meta = m
			meta.Version = version
		}
		if description != "" {
			meta.Description = description
		}
		return body, nil
	})
}

// Save 由用户在界面中新建或覆盖技能。
func (s *SkillStore) Save(name, content string) error {
	if !skillNameRe.MatchString(name) {
		return errors.New("技能名只能包含小写字母、数字和连字符")
	}
	if _, err := os.Stat(s.path(name)); err == nil {
		return s.Edit(name, content, "")
	}
	return s.Create(name, content, "", "user")
}

func (s *SkillStore) Delete(name string) error {
	if !skillNameRe.MatchString(name) {
		return fmt.Errorf("技能名无效: %s", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.path(name)); err != nil {
		return fmt.Errorf("技能 %s 不存在", name)
	}
	return os.RemoveAll(filepath.Join(s.dir, name))
}

// Index 生成系统提示词中的技能索引（只含名称与描述，正文按需用 skill_view 读取）。
func (s *SkillStore) Index() string {
	list := s.List()
	if len(list) == 0 {
		return "（暂无技能）"
	}
	var b strings.Builder
	for _, m := range list {
		fmt.Fprintf(&b, "- %s：%s\n", m.Name, m.Description)
	}
	return strings.TrimSpace(b.String())
}

// Files 返回全部技能文件（用于备份）。
func (s *SkillStore) Files() map[string]string {
	out := map[string]string{}
	for _, m := range s.List() {
		if b, err := os.ReadFile(s.path(m.Name)); err == nil {
			out["skills/"+m.Name+"/SKILL.md"] = string(b)
		}
	}
	return out
}

// SeedBuiltins 首次运行时写入内置技能。
func (s *SkillStore) SeedBuiltins() {
	marker := filepath.Join(s.dir, ".seeded")
	if _, err := os.Stat(marker); err == nil {
		return
	}
	for _, sk := range builtinSkills {
		if _, err := os.Stat(s.path(sk.name)); err == nil {
			continue
		}
		_ = s.Create(sk.name, sk.body, sk.description, "builtin")
	}
	_ = os.WriteFile(marker, []byte(today()), 0o600)
}

var builtinSkills = []struct{ name, description, body string }{
	{
		name:        "nginx-cert-replace",
		description: "替换 Nginx 站点的 TLS 证书并平滑重载，失败时回滚",
		body: `# 替换 Nginx 证书

## 适用场景
证书续期或更换后，需要更新某台/多台 Nginx 服务器上的证书文件。

## 步骤
1. 确认证书路径：` + "`nginx -T 2>/dev/null | grep -E 'ssl_certificate(_key)?\\s'`" + `，不要猜路径。
2. 优先使用应用内的「证书」功能：为证书添加部署目标后一键同步，它会自动备份、校验、检测配置、重载并在失败时回滚。
3. 手动操作时：先备份 ` + "`cp -p cert cert.bak-$(date +%F)`" + `，写入新文件（证书 644、私钥 600），再执行 ` + "`nginx -t`" + `。
4. 检测通过后 ` + "`systemctl reload nginx`" + `（不要用 restart，reload 不会中断连接）。

## 注意事项
- 证书文件应包含完整链（fullchain），否则部分客户端校验失败。
- 私钥与证书必须匹配，可用 ` + "`openssl x509 -noout -pubkey -in cert | sha256sum`" + ` 与 ` + "`openssl pkey -pubout -in key | sha256sum`" + ` 对比。
- nginx -t 失败时立即恢复备份，不要重载。

## 验证
` + "`echo | openssl s_client -connect 127.0.0.1:443 -servername 域名 2>/dev/null | openssl x509 -noout -dates -fingerprint -sha256`",
	},
	{
		name:        "disk-space-triage",
		description: "磁盘空间告警时定位占用来源并安全清理",
		body: `# 磁盘空间排查与清理

## 适用场景
df 显示某个挂载点使用率过高（>85%），或服务因磁盘满报错。

## 步骤
1. ` + "`df -hT`" + ` 确认是哪个挂载点；同时看 ` + "`df -i`" + ` 排除 inode 耗尽。
2. ` + "`du -xh --max-depth=2 <挂载点> 2>/dev/null | sort -rh | head -20`" + ` 逐层定位大目录。
3. 常见来源：/var/log（日志）、journal、docker（` + "`docker system df`" + `）、包缓存、core dump、旧备份。
4. 已删除但仍被进程占用的文件：` + "`lsof +L1 2>/dev/null | head`" + `，需要重启对应进程才能释放。

## 安全清理
- journal：` + "`journalctl --vacuum-time=7d`" + `
- 包缓存：` + "`apt-get clean`" + ` / ` + "`dnf clean all`" + `
- 正在写入的日志用 ` + "`truncate -s 0 文件`" + ` 清空，不要直接 rm。
- docker：` + "`docker system prune`" + `（会删除停止的容器和悬空镜像，先确认）。

## 注意事项
- 删除前先确认文件用途，业务数据和数据库文件不要动。
- 清理后再次 df 验证效果。`,
	},
	{
		name:        "service-troubleshooting",
		description: "服务启动失败或运行异常时的标准排查流程",
		body: `# 服务故障排查

## 步骤
1. 状态：` + "`systemctl status <服务> --no-pager -l`" + `，关注 Active 行和最近日志。
2. 日志：` + "`journalctl -u <服务> -n 100 --no-pager`" + `，必要时加 ` + "`--since \"30 min ago\"`" + `。
3. 配置检测：nginx ` + "`nginx -t`" + `、apache ` + "`apachectl -t`" + `、haproxy ` + "`haproxy -c -f /etc/haproxy/haproxy.cfg`" + `。
4. 端口：` + "`ss -tlnp | grep <端口>`" + ` 判断是否被占用或未监听。
5. 资源：磁盘满、内存不足（` + "`dmesg | grep -i oom`" + `）、文件描述符耗尽。
6. 修复后 ` + "`systemctl restart <服务>`" + ` 并再次检查状态与日志。

## 注意事项
- 先检测配置再重启，避免把服务从“运行但有问题”变成“完全起不来”。
- 记录根因：如果是环境特有的问题，写入记忆。`,
	},
	{
		name:        "ssh-key-rotation",
		description: "安全轮换服务器的 SSH 登录密钥，避免把自己锁在门外",
		body: `# SSH 密钥轮换

## 步骤
1. 在「密钥」页生成新的 ED25519 密钥。
2. 把新公钥「部署到服务器」（追加到 authorized_keys，不删除旧公钥）。
3. 在服务器设置中把登录密钥切换为新密钥，点击「测试连接」确认成功。
4. 确认新密钥可以登录后，再对旧公钥执行「解绑」从 authorized_keys 中移除。

## 注意事项
- 顺序不能反：必须先验证新密钥能登录，再删除旧公钥。
- 改用密钥后可考虑在 sshd_config 中设置 ` + "`PasswordAuthentication no`" + `，修改前保持一个已登录的会话，改完 ` + "`sshd -t`" + ` 检测后再 reload。`,
	},
	{
		name:        "high-load-diagnosis",
		description: "CPU 或系统负载过高时的定位方法",
		body: `# 负载过高排查

## 步骤
1. ` + "`uptime`" + ` 和 ` + "`nproc`" + `：负载持续超过核心数 1.5 倍需要关注。
2. ` + "`top -bn1 | head -20`" + ` 或 ` + "`ps -eo pid,user,%cpu,%mem,etime,cmd --sort=-%cpu | head`" + ` 找出高 CPU 进程。
3. 负载高但 CPU 不高时多为 IO 等待：` + "`vmstat 1 5`" + ` 看 wa 列，` + "`iostat -x 1 3`" + `（需要 sysstat）。
4. 检查是否有异常进程（挖矿、未知用户进程）：关注陌生命令名和 /tmp 下运行的程序。
5. Web 服务看访问量：` + "`ss -s`" + `，以及 Nginx 日志中请求最多的 IP。

## 注意事项
- 不要直接 kill 未知进程，先确认用途和所属服务。
- 发现可疑进程时同时检查 crontab 和 systemd 定时任务。`,
	},
}
