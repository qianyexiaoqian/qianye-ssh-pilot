package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"sshpilot/internal/agent"
	"sshpilot/internal/ops"
	"sshpilot/internal/sshx"
	"sshpilot/internal/store"
)

const AppVersion = "1.4.0"

type ServerStatus struct {
	Status    string `json:"status"` // online | offline | unknown
	Latency   int    `json:"latency"`
	CheckedAt int64  `json:"checkedAt"`
	Error     string `json:"error"`
}

// App 是暴露给前端的全部方法的接收者。
type App struct {
	ctx     context.Context
	dataDir string
	// dataLoc 说明 dataDir 是怎么来的（程序目录 / %AppData% / 环境变量指定），只在正常启动时设置。
	dataLoc dataLocation
	initErr string

	store   *store.Store
	pool    *sshx.Pool
	terms   *sshx.TerminalManager
	agent   *agent.Agent
	monitor *monitorHub

	runsMu     sync.Mutex
	runCancels map[string]context.CancelFunc

	statusMu sync.RWMutex
	status   map[string]ServerStatus
	probeNow chan struct{}

	reportMu   sync.Mutex
	lastReport *ops.Report
	inspectMu  sync.Mutex // 同一时间只进行一次巡检（手动与自动互斥）

	autoSyncMu sync.Mutex

	// 文件管理：uid / gid 名称缓存、上次下载目录、传输任务
	filesMu     sync.Mutex
	owners      map[string]*ownerNames
	lastSaveDir string
	xfers       *transferHub

	// 文件管理独立窗口：filesWindow 不为空表示本进程就是某台服务器的文件管理窗口（数据只读，由主窗口保存）；
	// 主窗口用 fileWins 记录已经打开的窗口。
	filesWindow string
	parentPID   int
	forceQuit   atomic.Bool
	winMu       sync.Mutex
	fileWins    []*fileWindow
}

func NewApp() *App {
	return &App{
		runCancels: map[string]context.CancelFunc{},
		status:     map[string]ServerStatus{},
		probeNow:   make(chan struct{}, 1),
		owners:     map[string]*ownerNames{},
		xfers:      &transferHub{},
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.dataLoc = currentDataLocation()
	a.dataDir = a.dataLoc.Dir
	if a.filesWindow != "" {
		st, err := store.OpenReadOnly(a.dataDir)
		if err != nil {
			a.initErr = err.Error()
			st, _ = store.OpenReadOnly(filepath.Join(os.TempDir(), "SSHPilot-empty"))
		}
		a.wire(st)
		a.startFilesWindow()
		a.startQA()
		return
	}
	st, err := store.Open(a.dataDir)
	if err != nil {
		a.initErr = err.Error()
		a.dataDir = filepath.Join(os.TempDir(), "SSHPilot-fallback")
		st, err = store.Open(a.dataDir)
		if err != nil {
			panic(err)
		}
	}
	a.wire(st)
	a.loadLastReport()
	go a.statusLoop()
	go a.autoSyncLoop()
	go a.autoInspectLoop()
	// 长期记忆：补上历史对话的要点、建立向量索引；并定期整理技能库
	a.agent.StartIndexing()
	go a.curateLoop()
	a.startQA()
}

// curateLoop 定期归档长期没有用到的 AI 沉淀技能。
func (a *App) curateLoop() {
	time.Sleep(30 * time.Second)
	for {
		a.agent.CurateSkills()
		time.Sleep(12 * time.Hour)
	}
}

// wire 用打开的数据目录装配连接池、终端、监控与智能体（启动与测试共用）。
func (a *App) wire(st *store.Store) {
	a.store = st
	a.pool = sshx.NewPool(a.resolveEndpoint, a.learnHostKey)
	a.terms = sshx.NewTerminalManager(a.pool, a.emit)
	a.monitor = newMonitorHub(a)
	a.agent = agent.New(agent.Config{
		DataDir:     a.dataDir,
		Settings:    a.aiSettings,
		EnvSnapshot: a.envSnapshot,
		Emit:        a.emit,
		Tools:       a.opsTools(),
		ReadOnly:    a.filesWindow != "",
		Embed:       func() store.EmbedSettings { return a.settings().Embed },
		Servers:     a.serverNameMap,
		ResolveServer: func(ref string) (string, bool) {
			found, err := a.resolveServerRefs([]string{ref})
			if err != nil || len(found) != 1 {
				return "", false
			}
			return found[0].ID, true
		},
	})
}

// serverNameMap 返回全部服务器的 ID → 名称。
func (a *App) serverNameMap() map[string]string {
	out := map[string]string{}
	a.store.Read(func(d *store.Data) {
		for _, s := range d.Servers {
			out[s.ID] = s.Name
		}
	})
	return out
}

func (a *App) shutdown(context.Context) {
	if a.agent != nil {
		a.agent.Close()
	}
	if a.monitor != nil {
		a.monitor.stopAll()
	}
	a.xfers.cancelAll()
	if a.terms != nil {
		a.terms.CloseAll()
	}
	if a.pool != nil {
		a.pool.Close()
	}
}

func (a *App) emit(name string, data ...any) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, name, data...)
	}
}

func (a *App) settings() store.Settings {
	var s store.Settings
	a.store.Read(func(d *store.Data) { s = d.Settings })
	return s
}

func (a *App) aiSettings() store.AISettings { return a.settings().AI }

func (a *App) concurrency() int { return max(a.settings().General.Concurrency, 1) }

func (a *App) commandTimeout() time.Duration {
	return time.Duration(max(a.settings().General.CommandTimeout, 10)) * time.Second
}

// ---- 连接 ----

func (a *App) resolveEndpoint(id string) (*sshx.Endpoint, error) { return a.endpoint(id, 0) }

func (a *App) endpoint(id string, depth int) (*sshx.Endpoint, error) {
	if depth > 3 {
		return nil, errors.New("跳板机层级过深或存在循环引用")
	}
	var (
		ep     *sshx.Endpoint
		jumpID string
		err    error
	)
	a.store.Read(func(d *store.Data) {
		s := d.Server(id)
		if s == nil {
			err = errors.New("服务器不存在或已删除")
			return
		}
		ep = &sshx.Endpoint{ServerID: s.ID, Name: s.Name, Host: s.Host, Port: s.Port, User: s.Username,
			HostKey: s.HostKey, UseSudo: s.UseSudo, SudoPassword: s.SudoPassword}
		if s.AuthType == "password" {
			ep.Password = s.Password
		} else {
			k := d.Key(s.KeyID)
			if k == nil || k.PrivatePEM == "" {
				err = fmt.Errorf("服务器 %s 没有可用的私钥，请在服务器设置中选择私钥", s.Name)
				return
			}
			ep.PrivateKey, ep.Passphrase = k.PrivatePEM, k.Passphrase
		}
		jumpID = s.JumpServerID
	})
	if err != nil {
		return nil, err
	}
	if jumpID != "" && jumpID != id {
		j, err := a.endpoint(jumpID, depth+1)
		if err != nil {
			return nil, fmt.Errorf("跳板机配置错误: %w", err)
		}
		ep.Jump = j
	}
	return ep, nil
}

func (a *App) learnHostKey(serverID, key, fp string) {
	if serverID == "" {
		return
	}
	_ = a.store.Update(func(d *store.Data) error {
		if s := d.Server(serverID); s != nil && s.HostKey == "" {
			s.HostKey, s.HostKeyFP = key, fp
		}
		return nil
	})
}

// ---- 状态探测 ----

func (a *App) statusLoop() {
	time.Sleep(800 * time.Millisecond)
	for {
		a.probeAll()
		interval := time.Duration(max(a.settings().General.StatusInterval, 15)) * time.Second
		select {
		case <-time.After(interval):
		case <-a.probeNow:
		}
	}
}

func (a *App) probeAll() {
	var servers []store.Server
	a.store.Read(func(d *store.Data) {
		for _, s := range d.Servers {
			servers = append(servers, *s)
		}
	})
	result := map[string]ServerStatus{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for _, s := range servers {
		wg.Add(1)
		sem <- struct{}{}
		go func(s store.Server) {
			defer wg.Done()
			defer func() { <-sem }()
			st := ServerStatus{CheckedAt: time.Now().UnixMilli()}
			host, port := s.Host, s.Port
			if s.JumpServerID != "" {
				// 经跳板机访问的服务器，以跳板机可达性作为参考
				a.store.Read(func(d *store.Data) {
					if j := d.Server(s.JumpServerID); j != nil {
						host, port = j.Host, j.Port
					}
				})
			}
			ms, err := sshx.Probe(context.Background(), host, port, 5*time.Second)
			if err != nil {
				st.Status, st.Error = "offline", err.Error()
			} else {
				st.Status, st.Latency = "online", ms
			}
			mu.Lock()
			result[s.ID] = st
			mu.Unlock()
		}(s)
	}
	wg.Wait()
	a.statusMu.Lock()
	a.status = result
	a.statusMu.Unlock()
	a.emit("servers:status", result)
}

func (a *App) statusOf(id string) ServerStatus {
	a.statusMu.RLock()
	defer a.statusMu.RUnlock()
	if st, ok := a.status[id]; ok {
		return st
	}
	return ServerStatus{Status: "unknown"}
}

// ---- 公共工具 ----

func (a *App) serverView(s store.Server) store.Server {
	v := s.Sanitized()
	st := a.statusOf(s.ID)
	v.LastStatus, v.LastLatency, v.LastCheckAt, v.LastError = st.Status, st.Latency, st.CheckedAt, st.Error
	return v
}

func (a *App) serverName(id string) string {
	name := id
	a.store.Read(func(d *store.Data) {
		if s := d.Server(id); s != nil {
			name = s.Name
		}
	})
	return name
}

// parallel 以设置中的并发度对多台服务器执行 fn。
func (a *App) parallel(ids []string, fn func(id string)) {
	sem := make(chan struct{}, a.concurrency())
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()
			fn(id)
		}(id)
	}
	wg.Wait()
}

func uniqueIDs(ids []string) []string {
	var out []string
	for _, id := range ids {
		if id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// ---- 应用信息与仪表盘 ----

type AppInfo struct {
	Version      string `json:"version"`
	DataDir      string `json:"dataDir"`
	InitError    string `json:"initError"`
	SecretErrors int    `json:"secretErrors"`
	Platform     string `json:"platform"`
	Product      string `json:"product"`
	Author       string `json:"author"`
	// Window 为 "files" 时，当前窗口是 WindowServer 这台服务器的文件管理独立窗口。
	Window       string `json:"window"`
	WindowServer string `json:"windowServer"`
	// DataMode：program 表示数据在程序目录下的 data 文件夹；appdata 表示程序目录不可写，数据在 %AppData%；custom 表示由环境变量指定。
	DataMode  string `json:"dataMode"`
	BackupDir string `json:"backupDir"`
	// MigratedFrom 是数据迁移前所在的旧目录；JustMigrated 表示就是这次启动时迁移的。
	MigratedFrom string `json:"migratedFrom"`
	JustMigrated bool   `json:"justMigrated"`
	// PreviousDataDir 不为空表示这次启动新建了空的数据目录，之前的数据还留在这个目录（程序被移动了，data 文件夹没有跟着移动）。
	PreviousDataDir string `json:"previousDataDir"`
}

func (a *App) GetAppInfo() AppInfo {
	info := AppInfo{Version: AppVersion, DataDir: a.dataDir, InitError: a.initErr, SecretErrors: a.store.SecretErrors(),
		Platform: goruntime.GOOS + "/" + goruntime.GOARCH, Product: ProductName, Author: AuthorName}
	if a.filesWindow != "" {
		info.Window, info.WindowServer = "files", a.filesWindow
	}
	loc := a.dataLocation()
	info.DataMode, info.BackupDir = loc.Mode, backupDirectory(loc)
	info.MigratedFrom, info.JustMigrated = migratedFrom(a.dataDir), loc.MigratedFrom != ""
	info.PreviousDataDir = loc.PreviousDir
	return info
}

// dataLocation 返回当前使用的数据目录的位置信息。测试里直接指定了 dataDir、或者启动失败退回临时目录时，按自定义目录处理。
func (a *App) dataLocation() dataLocation {
	if a.dataLoc.Dir != "" && a.dataLoc.Dir == a.dataDir {
		return a.dataLoc
	}
	return dataLocation{Dir: a.dataDir, Mode: dataModeCustom}
}

// OpenDir 在资源管理器里打开数据目录、备份目录或迁移前的旧数据目录。
func (a *App) OpenDir(kind string) error {
	dir := a.dataDir
	switch kind {
	case "backup":
		dir = backupDirectory(a.dataLocation())
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	case "legacy":
		if dir = migratedFrom(a.dataDir); dir == "" {
			return errors.New("没有旧的数据目录")
		}
	}
	if goruntime.GOOS == "windows" {
		return exec.Command("explorer", dir).Start()
	}
	runtime.BrowserOpenURL(a.ctx, "file://"+dir)
	return nil
}

func (a *App) OpenDataDir() error {
	if goruntime.GOOS == "windows" {
		return exec.Command("explorer", a.dataDir).Start()
	}
	runtime.BrowserOpenURL(a.ctx, "file://"+a.dataDir)
	return nil
}

func (a *App) OpenURL(url string) {
	if strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://") {
		runtime.BrowserOpenURL(a.ctx, url)
	}
}

type CertBrief struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Domains   []string `json:"domains"`
	NotAfter  int64    `json:"notAfter"`
	Days      int      `json:"days"`
	Targets   int      `json:"targets"`
	OutOfSync int      `json:"outOfSync"`
	Failed    int      `json:"failed"`
}

type GroupCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type Dashboard struct {
	ServerCount    int                `json:"serverCount"`
	OnlineCount    int                `json:"onlineCount"`
	OfflineCount   int                `json:"offlineCount"`
	KeyCount       int                `json:"keyCount"`
	PublicKeyCount int                `json:"publicKeyCount"`
	BindingCount   int                `json:"bindingCount"`
	CertCount      int                `json:"certCount"`
	CertExpiring   int                `json:"certExpiring"`
	CertExpired    int                `json:"certExpired"`
	ScriptCount    int                `json:"scriptCount"`
	SkillCount     int                `json:"skillCount"`
	SessionCount   int                `json:"sessionCount"`
	Memory         agent.MemoryView   `json:"memory"`
	UserMemory     agent.MemoryView   `json:"userMemory"`
	LongMemory     agent.IndexStatus  `json:"longMemory"`
	Certs          []CertBrief        `json:"certs"`
	Groups         []GroupCount       `json:"groups"`
	RecentRuns     []store.RunRecord  `json:"recentRuns"`
	RecentLearning []agent.LearnEvent `json:"recentLearning"`
	LastReport     *ops.Report        `json:"lastReport"`
	AIConfigured   bool               `json:"aiConfigured"`
	AIModel        string             `json:"aiModel"`
}

func certDays(notAfter int64) int {
	return int(time.Until(time.UnixMilli(notAfter)).Hours() / 24)
}

func (a *App) GetDashboard() Dashboard {
	var d Dashboard
	warn := a.settings().General.CertWarnDays
	groups := map[string]int{}
	a.store.Read(func(data *store.Data) {
		d.ServerCount = len(data.Servers)
		d.KeyCount = len(data.Keys)
		d.PublicKeyCount = len(data.PublicKeys)
		d.BindingCount = len(data.Bindings)
		d.CertCount = len(data.Certs)
		d.ScriptCount = len(data.Scripts)
		for _, s := range data.Servers {
			g := s.Group
			if g == "" {
				g = "未分组"
			}
			groups[g]++
		}
		for _, c := range data.Certs {
			b := CertBrief{ID: c.ID, Name: c.Name, Domains: c.Domains, NotAfter: c.NotAfter, Days: certDays(c.NotAfter)}
			for _, t := range data.Targets {
				if t.CertID != c.ID {
					continue
				}
				b.Targets++
				if t.LastStatus == "failed" {
					b.Failed++
				} else if t.LastFingerprint != c.Fingerprint {
					b.OutOfSync++
				}
			}
			switch {
			case b.Days < 0:
				d.CertExpired++
			case b.Days <= warn:
				d.CertExpiring++
			}
			d.Certs = append(d.Certs, b)
		}
		_, aiErr := agent.NewProvider(data.Settings.AI)
		d.AIConfigured = aiErr == nil
		d.AIModel = data.Settings.AI.Model
	})
	a.statusMu.RLock()
	for _, st := range a.status {
		switch st.Status {
		case "online":
			d.OnlineCount++
		case "offline":
			d.OfflineCount++
		}
	}
	a.statusMu.RUnlock()
	slices.SortFunc(d.Certs, func(x, y CertBrief) int { return int(x.NotAfter/1000 - y.NotAfter/1000) })
	for name, n := range groups {
		d.Groups = append(d.Groups, GroupCount{name, n})
	}
	slices.SortFunc(d.Groups, func(x, y GroupCount) int { return y.Count - x.Count })
	d.RecentRuns = a.store.ListRuns(6)
	d.RecentLearning = a.agent.Learn.Recent(6)
	d.SkillCount = len(a.agent.Skills.List())
	d.SessionCount = len(a.agent.Sessions.List())
	d.Memory = a.agent.Memory.View("memory")
	d.UserMemory = a.agent.Memory.View("user")
	d.LongMemory = a.agent.Long.Status()
	a.reportMu.Lock()
	d.LastReport = a.lastReport
	a.reportMu.Unlock()
	if d.Certs == nil {
		d.Certs = []CertBrief{}
	}
	if d.Groups == nil {
		d.Groups = []GroupCount{}
	}
	return d
}

// envSnapshot 生成给智能体的环境概要（服务器、证书、脚本）。
func (a *App) envSnapshot() string {
	var b strings.Builder
	a.store.Read(func(d *store.Data) {
		fmt.Fprintf(&b, "服务器（共 %d 台）：\n", len(d.Servers))
		if len(d.Servers) == 0 {
			b.WriteString("（尚未添加服务器）\n")
		}
		for i, s := range d.Servers {
			if i >= 80 {
				fmt.Fprintf(&b, "…其余 %d 台请用 list_servers 查看\n", len(d.Servers)-i)
				break
			}
			st := a.statusOf(s.ID)
			state := map[string]string{"online": "在线", "offline": "离线"}[st.Status]
			if state == "" {
				state = "未知"
			}
			fmt.Fprintf(&b, "- %s｜%s@%s:%d", s.Name, s.Username, s.Host, s.Port)
			if s.Group != "" {
				fmt.Fprintf(&b, "｜分组 %s", s.Group)
			}
			if len(s.Tags) > 0 {
				fmt.Fprintf(&b, "｜标签 %s", strings.Join(s.Tags, ","))
			}
			if s.OS != "" {
				fmt.Fprintf(&b, "｜%s", s.OS)
			}
			fmt.Fprintf(&b, "｜%s", state)
			if s.Notes != "" {
				fmt.Fprintf(&b, "｜备注：%s", strings.ReplaceAll(s.Notes, "\n", " "))
			}
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "\n证书（共 %d 张）：\n", len(d.Certs))
		for _, c := range d.Certs {
			n := 0
			for _, t := range d.Targets {
				if t.CertID == c.ID {
					n++
				}
			}
			fmt.Fprintf(&b, "- %s｜到期 %s（剩 %d 天）｜部署目标 %d 个\n", strings.Join(c.Domains, ", "),
				time.UnixMilli(c.NotAfter).Format("2006-01-02"), certDays(c.NotAfter), n)
		}
		fmt.Fprintf(&b, "\n脚本库：%d 个脚本（用 list_scripts 查看）", len(d.Scripts))
	})
	return b.String()
}

func (a *App) loadLastReport() {
	b, err := os.ReadFile(filepath.Join(a.dataDir, "inspection.json"))
	if err != nil {
		return
	}
	var r ops.Report
	if json.Unmarshal(b, &r) == nil {
		a.lastReport = &r
	}
}
