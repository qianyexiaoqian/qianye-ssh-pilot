package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"sshpilot/internal/ops"
	"sshpilot/internal/sshx"
	"sshpilot/internal/store"
)

type ServerInput struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Host              string   `json:"host"`
	Port              int      `json:"port"`
	Username          string   `json:"username"`
	AuthType          string   `json:"authType"`
	KeyID             string   `json:"keyId"`
	Password          string   `json:"password"`
	ClearPassword     bool     `json:"clearPassword"`
	Group             string   `json:"group"`
	Tags              []string `json:"tags"`
	JumpServerID      string   `json:"jumpServerId"`
	UseSudo           bool     `json:"useSudo"`
	SudoPassword      string   `json:"sudoPassword"`
	ClearSudoPassword bool     `json:"clearSudoPassword"`
	Notes             string   `json:"notes"`
}

func (in *ServerInput) normalize() error {
	in.Host = strings.TrimSpace(in.Host)
	in.Name = strings.TrimSpace(in.Name)
	in.Username = strings.TrimSpace(in.Username)
	in.Group = strings.TrimSpace(in.Group)
	if in.Host == "" {
		return errors.New("请填写主机地址")
	}
	if strings.ContainsAny(in.Host, " \t/") {
		return errors.New("主机地址格式不正确")
	}
	if in.Port == 0 {
		in.Port = 22
	}
	if in.Port < 1 || in.Port > 65535 {
		return errors.New("端口范围应为 1-65535")
	}
	if in.Username == "" {
		in.Username = "root"
	}
	if in.Name == "" {
		in.Name = in.Host
	}
	if in.AuthType != "password" {
		in.AuthType = "key"
	}
	var tags []string
	for _, t := range in.Tags {
		if t = strings.TrimSpace(t); t != "" && !slices.Contains(tags, t) {
			tags = append(tags, t)
		}
	}
	in.Tags = tags
	if in.JumpServerID == in.ID && in.ID != "" {
		in.JumpServerID = ""
	}
	return nil
}

func (a *App) ListServers() []store.Server {
	out := []store.Server{}
	a.store.Read(func(d *store.Data) {
		for _, s := range d.Servers {
			out = append(out, a.serverView(*s))
		}
	})
	return out
}

func (a *App) SaveServer(in ServerInput) (store.Server, error) {
	if err := in.normalize(); err != nil {
		return store.Server{}, err
	}
	var saved store.Server
	err := a.store.Update(func(d *store.Data) error {
		if in.AuthType == "key" {
			if d.Key(in.KeyID) == nil {
				return errors.New("请选择用于登录的私钥（可在「密钥」页导入或生成）")
			}
		}
		if in.JumpServerID != "" && d.Server(in.JumpServerID) == nil {
			return errors.New("跳板机不存在")
		}
		now := store.Now()
		s := d.Server(in.ID)
		if s == nil {
			if in.AuthType == "password" && in.Password == "" {
				return errors.New("请填写登录密码")
			}
			s = &store.Server{ID: store.NewID(), CreatedAt: now}
			d.Servers = append(d.Servers, s)
		} else if s.Host != in.Host || s.Port != in.Port {
			s.HostKey, s.HostKeyFP = "", "" // 地址变化后重新学习主机密钥
		}
		s.Name, s.Host, s.Port, s.Username = in.Name, in.Host, in.Port, in.Username
		s.AuthType, s.KeyID, s.Group, s.Tags = in.AuthType, in.KeyID, in.Group, in.Tags
		s.JumpServerID, s.UseSudo, s.Notes = in.JumpServerID, in.UseSudo, in.Notes
		if in.AuthType != "key" {
			s.KeyID = ""
		}
		switch {
		case in.Password != "":
			s.Password = in.Password
		case in.ClearPassword:
			s.Password = ""
		}
		if in.AuthType == "password" && s.Password == "" {
			return errors.New("请填写登录密码")
		}
		switch {
		case in.SudoPassword != "":
			s.SudoPassword = in.SudoPassword
		case in.ClearSudoPassword:
			s.SudoPassword = ""
		}
		s.UpdatedAt = now
		saved = *s
		return nil
	})
	if err != nil {
		return store.Server{}, err
	}
	a.pool.Invalidate(saved.ID)
	a.forgetOwners(saved.ID)
	a.monitor.unblock(saved.ID)
	a.RefreshStatus()
	return a.serverView(saved), nil
}

func (a *App) DeleteServer(id string) error {
	err := a.store.Update(func(d *store.Data) error {
		if d.Server(id) == nil {
			return errors.New("服务器不存在")
		}
		d.Servers = slices.DeleteFunc(d.Servers, func(s *store.Server) bool { return s.ID == id })
		d.Bindings = slices.DeleteFunc(d.Bindings, func(b *store.KeyBinding) bool { return b.ServerID == id })
		d.Targets = slices.DeleteFunc(d.Targets, func(t *store.DeployTarget) bool { return t.ServerID == id })
		for _, s := range d.Servers {
			if s.JumpServerID == id {
				s.JumpServerID = ""
			}
		}
		return nil
	})
	a.pool.Invalidate(id)
	a.forgetOwners(id)
	a.monitor.forget(id)
	return err
}

func (a *App) ListGroups() []string {
	out := []string{}
	a.store.Read(func(d *store.Data) {
		for _, s := range d.Servers {
			if s.Group != "" && !slices.Contains(out, s.Group) {
				out = append(out, s.Group)
			}
		}
	})
	slices.Sort(out)
	return out
}

// RefreshStatus 立即触发一次在线状态探测。
func (a *App) RefreshStatus() {
	select {
	case a.probeNow <- struct{}{}:
	default:
	}
}

type TestResult struct {
	OK          bool   `json:"ok"`
	LatencyMs   int64  `json:"latencyMs"`
	Message     string `json:"message"`
	OS          string `json:"os"`
	HostKeyFP   string `json:"hostKeyFingerprint"`
	HostKeyNew  bool   `json:"hostKeyNew"`
	Mismatch    bool   `json:"mismatch"`
	SudoOK      bool   `json:"sudoOk"`
	SudoChecked bool   `json:"sudoChecked"`
}

const osProbe = `. /etc/os-release 2>/dev/null && echo "${PRETTY_NAME:-$NAME}" || uname -sr`

func (a *App) runTest(ep *sshx.Endpoint, persist bool) TestResult {
	var learned string
	learn := func(serverID, key, fp string) {
		learned = fp
		if persist {
			a.learnHostKey(serverID, key, fp)
		}
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cl, err := sshx.Dial(ctx, ep, learn, 12*time.Second)
	if err != nil {
		var mm *sshx.HostKeyMismatchError
		return TestResult{Message: err.Error(), Mismatch: errors.As(err, &mm)}
	}
	defer cl.Close()
	res := TestResult{OK: true, LatencyMs: time.Since(start).Milliseconds(), HostKeyFP: learned, HostKeyNew: learned != ""}
	if s, err := cl.NewSession(); err == nil {
		if out, err := s.Output(osProbe); err == nil {
			res.OS = strings.TrimSpace(string(out))
		}
		s.Close()
	}
	if ep.UseSudo && !ep.IsRoot() {
		res.SudoChecked = true
		if s, err := cl.NewSession(); err == nil {
			pw := ep.SudoPassword
			if pw == "" {
				pw = ep.Password
			}
			cmd := "sudo -n true"
			if pw != "" {
				cmd = "sudo -S -p '' true"
				s.Stdin = strings.NewReader(pw + "\n")
			}
			res.SudoOK = s.Run(cmd) == nil
			s.Close()
		}
	}
	res.Message = fmt.Sprintf("连接成功（%d ms）", res.LatencyMs)
	if res.SudoChecked && !res.SudoOK {
		res.Message += "，但 sudo 验证失败：请检查 sudo 密码或 sudoers 配置"
	}
	return res
}

// TestServer 测试已保存服务器的连接，并更新系统信息。
func (a *App) TestServer(id string) TestResult {
	ep, err := a.resolveEndpoint(id)
	if err != nil {
		return TestResult{Message: err.Error()}
	}
	res := a.runTest(ep, true)
	if res.OK && res.OS != "" {
		_ = a.store.Update(func(d *store.Data) error {
			if s := d.Server(id); s != nil {
				s.OS = res.OS
			}
			return nil
		})
	}
	a.pool.Invalidate(id)
	if res.OK {
		a.monitor.unblock(id)
	}
	return res
}

// TestServerInput 在保存前用表单中的参数测试连接。
func (a *App) TestServerInput(in ServerInput) TestResult {
	if err := in.normalize(); err != nil {
		return TestResult{Message: err.Error()}
	}
	ep := &sshx.Endpoint{Name: in.Name, Host: in.Host, Port: in.Port, User: in.Username, UseSudo: in.UseSudo, SudoPassword: in.SudoPassword}
	var err error
	a.store.Read(func(d *store.Data) {
		if old := d.Server(in.ID); old != nil {
			if old.Host == in.Host && old.Port == in.Port {
				ep.HostKey = old.HostKey
			}
			if in.Password == "" && !in.ClearPassword {
				in.Password = old.Password
			}
			if ep.SudoPassword == "" && !in.ClearSudoPassword {
				ep.SudoPassword = old.SudoPassword
			}
		}
		if in.AuthType == "password" {
			ep.Password = in.Password
		} else {
			k := d.Key(in.KeyID)
			if k == nil {
				err = errors.New("请选择私钥")
				return
			}
			ep.PrivateKey, ep.Passphrase = k.PrivatePEM, k.Passphrase
		}
	})
	if err != nil {
		return TestResult{Message: err.Error()}
	}
	if in.AuthType == "password" && ep.Password == "" {
		return TestResult{Message: "请填写登录密码"}
	}
	if in.JumpServerID != "" {
		j, err := a.resolveEndpoint(in.JumpServerID)
		if err != nil {
			return TestResult{Message: err.Error()}
		}
		ep.Jump = j
	}
	return a.runTest(ep, false)
}

// TrustHostKey 清除记录的主机密钥，下次连接时重新学习。
func (a *App) TrustHostKey(id string) error {
	err := a.store.Update(func(d *store.Data) error {
		s := d.Server(id)
		if s == nil {
			return errors.New("服务器不存在")
		}
		s.HostKey, s.HostKeyFP = "", ""
		return nil
	})
	a.pool.Invalidate(id)
	return err
}

func (a *App) GetServerInfo(id string) (*ops.ServerInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	info, err := ops.CollectInfo(ctx, a.pool, id)
	if err != nil {
		return nil, err
	}
	if info.OS != "" {
		_ = a.store.Update(func(d *store.Data) error {
			if s := d.Server(id); s != nil && s.OS != info.OS {
				s.OS = info.OS
			}
			return nil
		})
	}
	return info, nil
}

// ---- 终端 ----

func (a *App) OpenTerminal(serverID string, cols, rows int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return a.terms.Open(ctx, serverID, cols, rows)
}

// TerminalReady 在前端订阅输出事件后调用，此后缓存的输出才会推送。
func (a *App) TerminalReady(id string) { a.terms.Ready(id) }

func (a *App) TerminalWrite(id, data string) error { return a.terms.Write(id, data) }

func (a *App) TerminalResize(id string, cols, rows int) error { return a.terms.Resize(id, cols, rows) }

func (a *App) CloseTerminal(id string) { a.terms.Close(id) }
