package store

// Server 是一台受管的 SSH 服务器。Password / SudoPassword 只在后端与磁盘（加密）中存在，
// 返回给前端前会通过 Sanitized 清空。
type Server struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Host            string   `json:"host"`
	Port            int      `json:"port"`
	Username        string   `json:"username"`
	AuthType        string   `json:"authType"` // key | password
	KeyID           string   `json:"keyId"`
	Password        string   `json:"password,omitempty"`
	HasPassword     bool     `json:"hasPassword"`
	Group           string   `json:"group"`
	Tags            []string `json:"tags"`
	JumpServerID    string   `json:"jumpServerId"`
	UseSudo         bool     `json:"useSudo"`
	SudoPassword    string   `json:"sudoPassword,omitempty"`
	HasSudoPassword bool     `json:"hasSudoPassword"`
	Notes           string   `json:"notes"`
	HostKey         string   `json:"hostKey"`
	HostKeyFP       string   `json:"hostKeyFingerprint"`
	OS              string   `json:"os"`
	LastStatus      string   `json:"lastStatus"` // online | offline | unknown
	LastLatency     int      `json:"lastLatency"`
	LastCheckAt     int64    `json:"lastCheckAt"`
	LastError       string   `json:"lastError"`
	CreatedAt       int64    `json:"createdAt"`
	UpdatedAt       int64    `json:"updatedAt"`
}

func (s Server) Sanitized() Server {
	s.HasPassword = s.Password != ""
	s.HasSudoPassword = s.SudoPassword != ""
	s.Password = ""
	s.SudoPassword = ""
	if s.Tags == nil {
		s.Tags = []string{}
	}
	return s
}

// PrivateKey 是本地保存的 SSH 私钥。
type PrivateKey struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	PrivatePEM    string `json:"privatePem,omitempty"`
	Passphrase    string `json:"passphrase,omitempty"`
	Encrypted     bool   `json:"encrypted"`
	HasPassphrase bool   `json:"hasPassphrase"`
	PublicKey     string `json:"publicKey"`
	Fingerprint   string `json:"fingerprint"`
	Comment       string `json:"comment"`
	CreatedAt     int64  `json:"createdAt"`
}

func (k PrivateKey) Sanitized() PrivateKey {
	k.HasPassphrase = k.Passphrase != ""
	k.PrivatePEM = ""
	k.Passphrase = ""
	return k
}

// PublicKey 是可以部署到服务器 authorized_keys 的公钥；PrivateKeyID 非空表示由本地私钥派生。
type PublicKey struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Key          string `json:"key"`
	Type         string `json:"type"`
	Fingerprint  string `json:"fingerprint"`
	Comment      string `json:"comment"`
	PrivateKeyID string `json:"privateKeyId"`
	CreatedAt    int64  `json:"createdAt"`
}

// KeyBinding 记录某个公钥已部署（绑定）到某台服务器。
type KeyBinding struct {
	PublicKeyID string `json:"publicKeyId"`
	ServerID    string `json:"serverId"`
	BoundAt     int64  `json:"boundAt"`
}

type CertSource struct {
	ServerID  string `json:"serverId"`
	CertPath  string `json:"certPath"`
	KeyPath   string `json:"keyPath"`
	ChainPath string `json:"chainPath"`
}

type CertVersion struct {
	Fingerprint string `json:"fingerprint"`
	NotAfter    int64  `json:"notAfter"`
	ReplacedAt  int64  `json:"replacedAt"`
}

// Certificate 是一张 TLS 证书（fullchain + 私钥）。
type Certificate struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Domains     []string      `json:"domains"`
	Subject     string        `json:"subject"`
	Issuer      string        `json:"issuer"`
	Serial      string        `json:"serial"`
	NotBefore   int64         `json:"notBefore"`
	NotAfter    int64         `json:"notAfter"`
	Fingerprint string        `json:"fingerprint"`
	KeyType     string        `json:"keyType"`
	ChainCount  int           `json:"chainCount"`
	CertPEM     string        `json:"certPem"`
	KeyPEM      string        `json:"keyPem,omitempty"`
	HasKey      bool          `json:"hasKey"`
	Source      *CertSource   `json:"source,omitempty"`
	Notes       string        `json:"notes"`
	History     []CertVersion `json:"history"`
	// AutoSync：来源服务器上的证书续期后，自动拉取并同步到全部部署目标。
	AutoSync        bool   `json:"autoSync"`
	AutoSyncAt      int64  `json:"autoSyncAt"`
	AutoSyncOK      bool   `json:"autoSyncOk"`
	AutoSyncMessage string `json:"autoSyncMessage"`
	CreatedAt       int64  `json:"createdAt"`
	UpdatedAt       int64  `json:"updatedAt"`
}

func (c Certificate) Sanitized() Certificate {
	c.HasKey = c.KeyPEM != ""
	c.KeyPEM = ""
	if c.Domains == nil {
		c.Domains = []string{}
	}
	if c.History == nil {
		c.History = []CertVersion{}
	}
	return c
}

// DeployTarget 描述证书部署到某台服务器的位置与部署后动作。
type DeployTarget struct {
	ID              string   `json:"id"`
	CertID          string   `json:"certId"`
	ServerID        string   `json:"serverId"`
	Preset          string   `json:"preset"` // nginx | apache | haproxy | custom
	CertPath        string   `json:"certPath"`
	KeyPath         string   `json:"keyPath"`
	ChainPath       string   `json:"chainPath"`
	CertFormat      string   `json:"certFormat"` // fullchain | leaf | combined
	CertMode        string   `json:"certMode"`
	KeyMode         string   `json:"keyMode"`
	Owner           string   `json:"owner"`
	Backup          bool     `json:"backup"`
	TestCommand     string   `json:"testCommand"`
	Services        []string `json:"services"`
	ServiceAction   string   `json:"serviceAction"` // reload | restart | none
	PostCommand     string   `json:"postCommand"`
	VerifyPort      int      `json:"verifyPort"`
	VerifyDomain    string   `json:"verifyDomain"`
	LastDeployAt    int64    `json:"lastDeployAt"`
	LastStatus      string   `json:"lastStatus"`
	LastMessage     string   `json:"lastMessage"`
	LastFingerprint string   `json:"lastFingerprint"`
}

type ScriptParam struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Default  string `json:"default"`
	Required bool   `json:"required"`
}

// Script 是脚本库中的一个脚本。
type Script struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Category    string        `json:"category"`
	Interpreter string        `json:"interpreter"` // bash | sh | python3
	Content     string        `json:"content"`
	Params      []ScriptParam `json:"params"`
	Tags        []string      `json:"tags"`
	ReadOnly    bool          `json:"readOnly"`
	Builtin     bool          `json:"builtin"`
	CreatedBy   string        `json:"createdBy"` // user | agent | builtin
	CreatedAt   int64         `json:"createdAt"`
	UpdatedAt   int64         `json:"updatedAt"`
}

// QuickCommand 是用户自定义的快捷命令：在终端中一键发送，或在多台服务器上批量执行。
// 命令中的 {{名称}} / {{名称:默认值}} 是占位符，使用时再填写。
type QuickCommand struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Command     string `json:"command"`
	Group       string `json:"group"`
	Description string `json:"description"`
	AutoEnter   bool   `json:"autoEnter"` // 发送到终端后自动回车执行
	Confirm     bool   `json:"confirm"`   // 执行前需要确认
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
}

type RunResult struct {
	ServerID   string `json:"serverId"`
	ServerName string `json:"serverName"`
	ExitCode   int    `json:"exitCode"`
	Output     string `json:"output"`
	Error      string `json:"error"`
	DurationMs int64  `json:"durationMs"`
}

// RunRecord 是一次批量执行（脚本、命令、证书部署、服务操作、AI 命令）的记录。
type RunRecord struct {
	ID         string      `json:"id"`
	Kind       string      `json:"kind"` // script | command | cert | service | agent | key
	Title      string      `json:"title"`
	ScriptID   string      `json:"scriptId"`
	Command    string      `json:"command"`
	Status     string      `json:"status"` // running | success | partial | failed | cancelled
	StartedAt  int64       `json:"startedAt"`
	FinishedAt int64       `json:"finishedAt"`
	Results    []RunResult `json:"results"`
}

type AISettings struct {
	Provider        string `json:"provider"` // anthropic | openai
	Preset          string `json:"preset"`
	BaseURL         string `json:"baseUrl"`
	APIKey          string `json:"apiKey,omitempty"`
	HasAPIKey       bool   `json:"hasApiKey"`
	Model           string `json:"model"`
	Effort          string `json:"effort"`
	MaxTokens       int    `json:"maxTokens"`
	ApprovalMode    string `json:"approvalMode"` // safe | always | auto
	AutoLearn       bool   `json:"autoLearn"`
	NudgeInterval   int    `json:"nudgeInterval"`
	ReviewThreshold int    `json:"reviewThreshold"`
	MaxSteps        int    `json:"maxSteps"`
	ContextChars    int    `json:"contextChars"`
}

// EmbedSettings 是长期记忆使用的向量模型：OpenAI 兼容的 /embeddings 接口，与对话模型分开配置。
// 没有填写时，长期记忆退回到程序内置的关键词匹配。
type EmbedSettings struct {
	Preset    string `json:"preset"`
	BaseURL   string `json:"baseUrl"`
	APIKey    string `json:"apiKey,omitempty"`
	HasAPIKey bool   `json:"hasApiKey"`
	Model     string `json:"model"`
	// Dimensions 为 0 时使用模型的默认维度。
	Dimensions int `json:"dimensions"`
	// 自动召回：每次提问前，把相关的长期记忆、往期对话要点和技能附在问题后面。
	RecallOff   bool `json:"recallOff"`
	RecallCount int  `json:"recallCount"`
	// MinScore 是召回的相关度下限，0 表示按模型自动校准。
	MinScore float64 `json:"minScore"`
}

type TerminalSettings struct {
	FontSize     int    `json:"fontSize"`
	FontFamily   string `json:"fontFamily"`
	Theme        string `json:"theme"`
	CursorBlink  bool   `json:"cursorBlink"`
	Scrollback   int    `json:"scrollback"`
	CopyOnSelect bool   `json:"copyOnSelect"`
	// 终端快捷键，写成 "Ctrl+Shift+C" 的形式；留空时使用默认值。
	KeyCopy  string `json:"keyCopy"`
	KeyPaste string `json:"keyPaste"`
	KeyFind  string `json:"keyFind"`
	// PlainKeysOff 为 true 时关闭 Windows 习惯的按键（Ctrl+V 粘贴、选中文字时 Ctrl+C 复制），这两个按键原样发给终端。
	PlainKeysOff bool `json:"plainKeysOff"`
}

type GeneralSettings struct {
	Theme          string `json:"theme"` // dark | light
	StatusInterval int    `json:"statusInterval"`
	Concurrency    int    `json:"concurrency"`
	CommandTimeout int    `json:"commandTimeout"`
	CertWarnDays   int    `json:"certWarnDays"`
	// MonitorInterval 是实时监控的采样间隔（秒）；MonitorOff 为 true 时不自动建立监控会话。
	MonitorInterval int  `json:"monitorInterval"`
	MonitorOff      bool `json:"monitorOff"`
	// InspectInterval 是自动巡检的间隔（分钟），0 表示不自动巡检。
	InspectInterval int `json:"inspectInterval"`
}

type Settings struct {
	AI       AISettings       `json:"ai"`
	Embed    EmbedSettings    `json:"embed"`
	Terminal TerminalSettings `json:"terminal"`
	General  GeneralSettings  `json:"general"`
}

func (s Settings) Sanitized() Settings {
	s.AI.HasAPIKey = s.AI.APIKey != ""
	s.AI.APIKey = ""
	s.Embed.HasAPIKey = s.Embed.APIKey != ""
	s.Embed.APIKey = ""
	return s
}

// 默认的 AI 服务：DeepSeek 官方接口（OpenAI 兼容）。
const (
	DeepSeekBaseURL = "https://api.deepseek.com"
	DeepSeekModel   = "deepseek-v4-pro"
	claudeModel     = "claude-opus-5-5"
)

// 默认的向量模型：阿里云百炼（通义千问）的 OpenAI 兼容接口。只有填写了 API Key 才会启用。
const (
	AliyunEmbedBaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	AliyunEmbedModel   = "text-embedding-v4"
)

func DefaultSettings() Settings {
	return Settings{
		AI: AISettings{
			Provider:        "openai",
			Preset:          "deepseek",
			BaseURL:         DeepSeekBaseURL,
			Model:           DeepSeekModel,
			Effort:          "medium",
			MaxTokens:       64000,
			ApprovalMode:    "safe",
			AutoLearn:       true,
			NudgeInterval:   8,
			ReviewThreshold: 5,
			MaxSteps:        30,
			ContextChars:    120000,
		},
		Embed: EmbedSettings{
			Preset:      "aliyun",
			BaseURL:     AliyunEmbedBaseURL,
			Model:       AliyunEmbedModel,
			RecallCount: 6,
		},
		Terminal: TerminalSettings{
			FontSize:     14,
			FontFamily:   `"Cascadia Mono", "Cascadia Code", Consolas, "Microsoft YaHei UI", monospace`,
			Theme:        "pilot",
			CursorBlink:  true,
			Scrollback:   5000,
			CopyOnSelect: true,
			KeyCopy:      "Ctrl+Shift+C",
			KeyPaste:     "Ctrl+Shift+V",
			KeyFind:      "Ctrl+Shift+F",
		},
		General: GeneralSettings{
			Theme:           "dark",
			StatusInterval:  60,
			Concurrency:     8,
			CommandTimeout:  120,
			CertWarnDays:    30,
			MonitorInterval: 3,
		},
	}
}

// fillDefaults 补齐旧版本数据或用户误清空的设置项。
func (s *Settings) fillDefaults() {
	d := DefaultSettings()
	if s.AI.Provider == "" {
		s.AI.Provider = d.AI.Provider
		s.AI.Preset = d.AI.Preset
		s.AI.BaseURL = d.AI.BaseURL
		s.AI.Model = d.AI.Model
	}
	if s.AI.Model == "" {
		// 只有用户还停留在某个内置服务上时才补默认模型，自定义接口的模型名留给用户填写
		switch {
		case s.AI.Provider == "anthropic":
			s.AI.Model = claudeModel
		case s.AI.Preset == d.AI.Preset:
			s.AI.Model = d.AI.Model
		}
	}
	if s.AI.Effort == "" {
		s.AI.Effort = d.AI.Effort
	}
	if s.AI.MaxTokens <= 0 {
		s.AI.MaxTokens = d.AI.MaxTokens
	}
	if s.AI.ApprovalMode == "" {
		s.AI.ApprovalMode = d.AI.ApprovalMode
	}
	if s.AI.NudgeInterval < 0 {
		s.AI.NudgeInterval = d.AI.NudgeInterval
	}
	if s.AI.ReviewThreshold <= 0 {
		s.AI.ReviewThreshold = d.AI.ReviewThreshold
	}
	if s.AI.MaxSteps <= 0 {
		s.AI.MaxSteps = d.AI.MaxSteps
	}
	if s.AI.ContextChars <= 0 {
		s.AI.ContextChars = d.AI.ContextChars
	}
	if s.Embed.Preset == "" && s.Embed.BaseURL == "" && s.Embed.Model == "" {
		s.Embed.Preset, s.Embed.BaseURL, s.Embed.Model = d.Embed.Preset, d.Embed.BaseURL, d.Embed.Model
	}
	if s.Embed.RecallCount <= 0 {
		s.Embed.RecallCount = d.Embed.RecallCount
	}
	if s.Terminal.FontSize <= 0 {
		s.Terminal.FontSize = d.Terminal.FontSize
	}
	if s.Terminal.FontFamily == "" {
		s.Terminal.FontFamily = d.Terminal.FontFamily
	}
	if s.Terminal.Theme == "" {
		s.Terminal.Theme = d.Terminal.Theme
	}
	if s.Terminal.Scrollback <= 0 {
		s.Terminal.Scrollback = d.Terminal.Scrollback
	}
	if s.Terminal.KeyCopy == "" {
		s.Terminal.KeyCopy = d.Terminal.KeyCopy
	}
	if s.Terminal.KeyPaste == "" {
		s.Terminal.KeyPaste = d.Terminal.KeyPaste
	}
	if s.Terminal.KeyFind == "" {
		s.Terminal.KeyFind = d.Terminal.KeyFind
	}
	if s.General.Theme == "" {
		s.General.Theme = d.General.Theme
	}
	if s.General.StatusInterval <= 0 {
		s.General.StatusInterval = d.General.StatusInterval
	}
	if s.General.Concurrency <= 0 {
		s.General.Concurrency = d.General.Concurrency
	}
	if s.General.CommandTimeout <= 0 {
		s.General.CommandTimeout = d.General.CommandTimeout
	}
	if s.General.CertWarnDays <= 0 {
		s.General.CertWarnDays = d.General.CertWarnDays
	}
	if s.General.MonitorInterval <= 0 {
		s.General.MonitorInterval = d.General.MonitorInterval
	}
}

// Data 是持久化到 store.json 的全部结构化数据。
type Data struct {
	Version    int             `json:"version"`
	Servers    []*Server       `json:"servers"`
	Keys       []*PrivateKey   `json:"keys"`
	PublicKeys []*PublicKey    `json:"publicKeys"`
	Bindings   []*KeyBinding   `json:"bindings"`
	Certs      []*Certificate  `json:"certs"`
	Targets    []*DeployTarget `json:"targets"`
	Scripts    []*Script       `json:"scripts"`
	Settings   Settings        `json:"settings"`
	// QuickSeeded 记录是否已写入过默认快捷命令，用户删除后不会再次出现。
	QuickCommands []*QuickCommand `json:"quickCommands"`
	QuickSeeded   bool            `json:"quickSeeded"`
	// AIDefaultV2 记录是否已经把“从未配置过的 AI 设置”切换到新的默认服务（DeepSeek）。
	AIDefaultV2 bool `json:"aiDefaultV2"`
}

// migrateAIDefault 把还停留在旧默认值（Claude、没有填过 API Key）的 AI 设置换成新的默认服务。
// 用户自己配置过的设置不会被改动；只执行一次。
func migrateAIDefault(d *Data) {
	if d.AIDefaultV2 {
		return
	}
	d.AIDefaultV2 = true
	ai := &d.Settings.AI
	if ai.APIKey != "" || ai.BaseURL != "" || ai.Provider != "anthropic" || (ai.Model != "" && ai.Model != claudeModel) {
		return
	}
	def := DefaultSettings().AI
	ai.Provider, ai.Preset, ai.BaseURL, ai.Model = def.Provider, def.Preset, def.BaseURL, def.Model
}

func (d *Data) Server(id string) *Server {
	for _, s := range d.Servers {
		if s.ID == id {
			return s
		}
	}
	return nil
}

func (d *Data) Key(id string) *PrivateKey {
	for _, k := range d.Keys {
		if k.ID == id {
			return k
		}
	}
	return nil
}

func (d *Data) PublicKey(id string) *PublicKey {
	for _, k := range d.PublicKeys {
		if k.ID == id {
			return k
		}
	}
	return nil
}

func (d *Data) Cert(id string) *Certificate {
	for _, c := range d.Certs {
		if c.ID == id {
			return c
		}
	}
	return nil
}

func (d *Data) Target(id string) *DeployTarget {
	for _, t := range d.Targets {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func (d *Data) Script(id string) *Script {
	for _, s := range d.Scripts {
		if s.ID == id {
			return s
		}
	}
	return nil
}

func (d *Data) QuickCommand(id string) *QuickCommand {
	for _, q := range d.QuickCommands {
		if q.ID == id {
			return q
		}
	}
	return nil
}
