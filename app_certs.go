package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"sshpilot/internal/certs"
	"sshpilot/internal/store"
)

type TargetView struct {
	store.DeployTarget
	ServerName string `json:"serverName"`
	ServerHost string `json:"serverHost"`
	Synced     bool   `json:"synced"`
}

type CertView struct {
	store.Certificate
	Days             int          `json:"days"`
	Targets          []TargetView `json:"targets"`
	SourceServerName string       `json:"sourceServerName"`
}

func (a *App) certView(d *store.Data, c *store.Certificate) CertView {
	v := CertView{Certificate: c.Sanitized(), Days: certDays(c.NotAfter), Targets: []TargetView{}}
	if c.Source != nil {
		if s := d.Server(c.Source.ServerID); s != nil {
			v.SourceServerName = s.Name
		}
	}
	for _, t := range d.Targets {
		if t.CertID != c.ID {
			continue
		}
		tv := TargetView{DeployTarget: *t, Synced: t.LastFingerprint == c.Fingerprint && t.LastStatus == "success"}
		if tv.Services == nil {
			tv.Services = []string{}
		}
		if s := d.Server(t.ServerID); s != nil {
			tv.ServerName, tv.ServerHost = s.Name, s.Host
		}
		v.Targets = append(v.Targets, tv)
	}
	return v
}

// ListCerts 按保存的顺序返回全部证书（顺序可以在界面上拖动调整，见 ReorderCerts）。
func (a *App) ListCerts() []CertView {
	out := []CertView{}
	a.store.Read(func(d *store.Data) {
		for _, c := range d.Certs {
			out = append(out, a.certView(d, c))
		}
	})
	return out
}

// ---- 分组与排序 ----

// maxCertGroupName 是分组名称的长度上限（按字符算）。
const maxCertGroupName = 30

func cleanGroupName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if utf8.RuneCountInString(name) > maxCertGroupName {
		return "", fmt.Errorf("分组名称太长了（最多 %d 个字）", maxCertGroupName)
	}
	return name, nil
}

// ListCertGroups 按顺序返回证书的分组（只包含里面有证书的分组）。
func (a *App) ListCertGroups() []string {
	out := []string{}
	a.store.Read(func(d *store.Data) { out = append(out, d.CertGroups...) })
	return out
}

// SetCertGroup 把证书放进一个分组；group 为空表示移出分组。分组不存在时新建，排在最后。
func (a *App) SetCertGroup(ids []string, group string) error {
	group, err := cleanGroupName(group)
	if err != nil {
		return err
	}
	return a.store.Update(func(d *store.Data) error {
		for _, id := range ids {
			c := d.Cert(id)
			if c == nil {
				return errors.New("证书不存在")
			}
			c.Group = group
		}
		d.TidyCertGroups()
		return nil
	})
}

// SaveCertGroup 新建或修改一个分组：分组叫 name，里面正好是 ids 这些证书。
// old 是修改前的名称（新建时为空）：改名后分组留在原来的位置，原来在这个分组里、这次没有选上的证书移出分组。
// ids 为空表示解散这个分组。
func (a *App) SaveCertGroup(old, name string, ids []string) error {
	name, err := cleanGroupName(name)
	if err != nil {
		return err
	}
	if name == "" && len(ids) > 0 {
		return errors.New("请填写分组名称")
	}
	return a.store.Update(func(d *store.Data) error {
		if old != name && name != "" && slices.Contains(d.CertGroups, name) {
			return fmt.Errorf("已经有一个叫「%s」的分组了", name)
		}
		for _, id := range ids {
			if d.Cert(id) == nil {
				return errors.New("证书不存在")
			}
		}
		for _, c := range d.Certs {
			switch {
			case slices.Contains(ids, c.ID):
				c.Group = name
			case old != "" && c.Group == old:
				c.Group = ""
			}
		}
		// 改名的分组留在原来的位置
		if i := slices.Index(d.CertGroups, old); old != "" && i >= 0 && name != "" {
			d.CertGroups[i] = name
		}
		d.TidyCertGroups()
		return nil
	})
}

// reorderInPlace 把 listed 选中的元素按 order 给出的顺序，重新排进它们现在占着的那几个位置，其余元素不动。
func reorderInPlace[T any](items []T, order []T, listed func(T) bool) {
	next := 0
	for i, it := range items {
		if listed(it) && next < len(order) {
			items[i] = order[next]
			next++
		}
	}
}

// ReorderCerts 调整证书的先后顺序：ids 里的证书按给出的顺序排进它们现在占着的位置，没有列出的不动。
// 在一个分组里拖动，只影响这个分组里的证书之间的顺序。
func (a *App) ReorderCerts(ids []string) error {
	return a.store.Update(func(d *store.Data) error {
		var order []*store.Certificate
		listed := map[string]bool{}
		for _, id := range ids {
			if c := d.Cert(id); c != nil && !listed[id] {
				listed[id] = true
				order = append(order, c)
			}
		}
		reorderInPlace(d.Certs, order, func(c *store.Certificate) bool { return listed[c.ID] })
		return nil
	})
}

// ReorderCertGroups 调整分组的先后顺序，规则同 ReorderCerts。
func (a *App) ReorderCertGroups(names []string) error {
	return a.store.Update(func(d *store.Data) error {
		var order []string
		listed := map[string]bool{}
		for _, n := range names {
			if slices.Contains(d.CertGroups, n) && !listed[n] {
				listed[n] = true
				order = append(order, n)
			}
		}
		reorderInPlace(d.CertGroups, order, func(n string) bool { return listed[n] })
		return nil
	})
}

func (a *App) GetCert(id string) (CertView, error) {
	var v CertView
	var err error
	a.store.Read(func(d *store.Data) {
		c := d.Cert(id)
		if c == nil {
			err = errors.New("证书不存在")
			return
		}
		v = a.certView(d, c)
	})
	return v, err
}

// PreviewCert 解析粘贴/选择的 PEM，返回证书信息与校验结果（不保存）。
func (a *App) PreviewCert(certPEM, keyPEM, chainPEM string) (*certs.Parsed, error) {
	return certs.Parse(certPEM, chainPEM, keyPEM)
}

type ImportCertRequest struct {
	ID       string `json:"id"` // 非空表示替换（续期）已有证书，保留部署目标
	Name     string `json:"name"`
	CertPEM  string `json:"certPem"`
	KeyPEM   string `json:"keyPem"`
	ChainPEM string `json:"chainPem"`
	Notes    string `json:"notes"`
	// Group 是新证书放进的分组（可以不填）；替换已有证书时不改变它的分组
	Group string `json:"group"`
}

func (a *App) saveParsed(id, name, notes string, p *certs.Parsed, src *store.CertSource) (CertView, error) {
	var view CertView
	err := a.store.Update(func(d *store.Data) error {
		if !p.HasKey && id != "" {
			// 续期时若未提供私钥，尝试沿用旧私钥
			if old := d.Cert(id); old != nil && old.KeyPEM != "" {
				if q, err := certs.Parse(p.FullchainPEM, old.KeyPEM); err == nil && q.KeyMatches {
					p = q
				}
			}
		}
		if !p.HasKey {
			return errors.New("缺少私钥：请同时提供证书对应的私钥")
		}
		if !p.KeyMatches {
			return errors.New("私钥与证书不匹配")
		}
		for _, c := range d.Certs {
			if c.Fingerprint == p.Fingerprint && c.ID != id {
				return fmt.Errorf("该证书已存在：%s", c.Name)
			}
		}
		now := store.Now()
		c := d.Cert(id)
		if c == nil {
			if id != "" {
				return errors.New("要替换的证书不存在")
			}
			c = &store.Certificate{ID: store.NewID(), CreatedAt: now, History: []store.CertVersion{}}
			d.Certs = append(d.Certs, c)
		} else if c.Fingerprint != p.Fingerprint {
			c.History = append([]store.CertVersion{{Fingerprint: c.Fingerprint, NotAfter: c.NotAfter, ReplacedAt: now}}, c.History...)
			if len(c.History) > 10 {
				c.History = c.History[:10]
			}
		}
		if name = strings.TrimSpace(name); name != "" {
			c.Name = name
		} else if c.Name == "" && len(p.Domains) > 0 {
			c.Name = p.Domains[0]
		}
		if notes != "" {
			c.Notes = notes
		}
		c.Domains, c.Subject, c.Issuer, c.Serial = p.Domains, p.Subject, p.Issuer, p.Serial
		c.NotBefore, c.NotAfter, c.Fingerprint, c.KeyType, c.ChainCount = p.NotBefore, p.NotAfter, p.Fingerprint, p.KeyType, p.ChainCount
		c.CertPEM, c.KeyPEM = p.FullchainPEM, p.KeyPEM
		if src != nil {
			c.Source = src
		}
		c.UpdatedAt = now
		view = a.certView(d, c)
		return nil
	})
	return view, err
}

func (a *App) ImportCert(req ImportCertRequest) (CertView, error) {
	p, err := certs.Parse(req.CertPEM, req.ChainPEM, req.KeyPEM)
	if err != nil {
		return CertView{}, err
	}
	v, err := a.saveParsed(req.ID, req.Name, req.Notes, p, nil)
	return a.groupNew(v, err, req.ID, req.Group)
}

// groupNew 把刚导入的新证书放进指定的分组（替换已有证书时不动它的分组）。
func (a *App) groupNew(v CertView, err error, replacedID, group string) (CertView, error) {
	if err != nil || replacedID != "" || strings.TrimSpace(group) == "" {
		return v, err
	}
	if err := a.SetCertGroup([]string{v.ID}, group); err != nil {
		return v, err
	}
	return a.GetCert(v.ID)
}

type PullCertRequest struct {
	CertID    string `json:"certId"`
	ServerID  string `json:"serverId"`
	CertPath  string `json:"certPath"`
	KeyPath   string `json:"keyPath"`
	ChainPath string `json:"chainPath"`
	Name      string `json:"name"`
	Group     string `json:"group"`
}

// PullCert 从服务器拉取证书（例如 certbot 所在的服务器），记录来源以便日后一键刷新。
func (a *App) PullCert(req PullCertRequest) (CertView, error) {
	src := &store.CertSource{ServerID: req.ServerID, CertPath: strings.TrimSpace(req.CertPath), KeyPath: strings.TrimSpace(req.KeyPath), ChainPath: strings.TrimSpace(req.ChainPath)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	p, err := certs.Pull(ctx, a.pool, req.ServerID, *src)
	if err != nil {
		return CertView{}, err
	}
	v, err := a.saveParsed(req.CertID, req.Name, "", p, src)
	return a.groupNew(v, err, req.CertID, req.Group)
}

type RefreshResult struct {
	Changed bool     `json:"changed"`
	Cert    CertView `json:"cert"`
	Message string   `json:"message"`
}

// RefreshCert 从来源服务器重新拉取证书，判断是否已续期。
func (a *App) RefreshCert(id string) (RefreshResult, error) {
	var src *store.CertSource
	var oldFP string
	a.store.Read(func(d *store.Data) {
		if c := d.Cert(id); c != nil && c.Source != nil {
			cp := *c.Source
			src, oldFP = &cp, c.Fingerprint
		}
	})
	if src == nil {
		return RefreshResult{}, errors.New("该证书没有记录来源服务器")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	p, err := certs.Pull(ctx, a.pool, src.ServerID, *src)
	if err != nil {
		return RefreshResult{}, err
	}
	if p.Fingerprint == oldFP {
		v, err := a.GetCert(id)
		return RefreshResult{Changed: false, Cert: v, Message: "来源服务器上的证书没有变化"}, err
	}
	v, err := a.saveParsed(id, "", "", p, src)
	if err != nil {
		return RefreshResult{}, err
	}
	return RefreshResult{Changed: true, Cert: v, Message: fmt.Sprintf("已更新为新证书，有效期至 %s", time.UnixMilli(p.NotAfter).Format("2006-01-02"))}, nil
}

// CertHistory 返回证书被替换前的历史版本。
func (a *App) CertHistory(id string) []store.CertVersion {
	out := []store.CertVersion{}
	a.store.Read(func(d *store.Data) {
		if c := d.Cert(id); c != nil {
			out = append(out, c.History...)
		}
	})
	return out
}

// GetCertSource 返回证书的来源服务器配置（没有来源时为 nil）。
func (a *App) GetCertSource(id string) *store.CertSource {
	var src *store.CertSource
	a.store.Read(func(d *store.Data) {
		if c := d.Cert(id); c != nil && c.Source != nil {
			cp := *c.Source
			src = &cp
		}
	})
	return src
}

func (a *App) UpdateCertMeta(id, name, notes string) error {
	return a.store.Update(func(d *store.Data) error {
		c := d.Cert(id)
		if c == nil {
			return errors.New("证书不存在")
		}
		if name = strings.TrimSpace(name); name != "" {
			c.Name = name
		}
		c.Notes = notes
		return nil
	})
}

func (a *App) DeleteCert(id string) error {
	return a.store.Update(func(d *store.Data) error {
		if d.Cert(id) == nil {
			return errors.New("证书不存在")
		}
		d.Certs = slices.DeleteFunc(d.Certs, func(c *store.Certificate) bool { return c.ID == id })
		d.Targets = slices.DeleteFunc(d.Targets, func(t *store.DeployTarget) bool { return t.CertID == id })
		d.TidyCertGroups()
		return nil
	})
}

// ExportCert 把证书文件导出到用户选择的目录。
func (a *App) ExportCert(id string) (string, error) {
	if err := a.guard(); err != nil {
		return "", err
	}
	var c *store.Certificate
	a.store.Read(func(d *store.Data) {
		if x := d.Cert(id); x != nil {
			cp := *x
			c = &cp
		}
	})
	if c == nil {
		return "", errors.New("证书不存在")
	}
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择导出目录"})
	if err != nil || dir == "" {
		return "", err
	}
	p, err := certs.Parse(c.CertPEM, c.KeyPEM)
	if err != nil {
		return "", err
	}
	base := filepath.Join(dir, certs.SafeName(c.Name))
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	files := map[string]string{"fullchain.pem": p.FullchainPEM, "cert.pem": p.LeafPEM, "chain.pem": p.ChainPEM, "privkey.pem": p.KeyPEM}
	for name, content := range files {
		if content == "" {
			continue
		}
		mode := os.FileMode(0o644)
		if name == "privkey.pem" {
			mode = 0o600
		}
		if err := os.WriteFile(filepath.Join(base, name), []byte(content), mode); err != nil {
			return "", err
		}
	}
	return base, nil
}

// ---- 部署目标 ----

func (a *App) SaveTarget(t store.DeployTarget) (store.DeployTarget, error) {
	t.CertPath, t.KeyPath, t.ChainPath = strings.TrimSpace(t.CertPath), strings.TrimSpace(t.KeyPath), strings.TrimSpace(t.ChainPath)
	if t.CertPath == "" || !strings.HasPrefix(t.CertPath, "/") {
		return t, errors.New("证书路径必须是服务器上的绝对路径")
	}
	if t.CertFormat != "combined" && (t.KeyPath == "" || !strings.HasPrefix(t.KeyPath, "/")) {
		return t, errors.New("私钥路径必须是服务器上的绝对路径")
	}
	if t.ChainPath != "" && !strings.HasPrefix(t.ChainPath, "/") {
		return t, errors.New("证书链路径必须是绝对路径")
	}
	var services []string
	for _, s := range t.Services {
		if s = strings.TrimSpace(s); s != "" && !slices.Contains(services, s) {
			services = append(services, s)
		}
	}
	t.Services = services
	if t.Services == nil {
		t.Services = []string{}
	}
	if t.ServiceAction == "" {
		t.ServiceAction = "reload"
	}
	var saved store.DeployTarget
	err := a.store.Update(func(d *store.Data) error {
		if d.Cert(t.CertID) == nil {
			return errors.New("证书不存在")
		}
		if d.Server(t.ServerID) == nil {
			return errors.New("请选择服务器")
		}
		existing := d.Target(t.ID)
		if existing == nil {
			for _, o := range d.Targets {
				if o.CertID == t.CertID && o.ServerID == t.ServerID && o.CertPath == t.CertPath {
					return errors.New("该服务器上已存在相同路径的部署目标")
				}
			}
			t.ID = store.NewID()
			t.LastDeployAt, t.LastStatus, t.LastMessage, t.LastFingerprint = 0, "", "", ""
			cp := t
			d.Targets = append(d.Targets, &cp)
		} else {
			t.LastDeployAt, t.LastStatus, t.LastMessage, t.LastFingerprint = existing.LastDeployAt, existing.LastStatus, existing.LastMessage, existing.LastFingerprint
			*existing = t
		}
		saved = t
		return nil
	})
	return saved, err
}

func (a *App) DeleteTarget(id string) error {
	return a.store.Update(func(d *store.Data) error {
		d.Targets = slices.DeleteFunc(d.Targets, func(t *store.DeployTarget) bool { return t.ID == id })
		return nil
	})
}

// deployTargets 并发部署证书到多个目标；stepFn 接收每一步的进度。
func (a *App) deployTargets(ctx context.Context, certID string, targetIDs []string, stepFn func(certs.Step), doneFn func(certs.DeployResult)) ([]certs.DeployResult, error) {
	var cert store.Certificate
	var targets []store.DeployTarget
	servers := map[string]store.Server{}
	a.store.Read(func(d *store.Data) {
		if c := d.Cert(certID); c != nil {
			cert = *c
		}
		for _, t := range d.Targets {
			if t.CertID == certID && (len(targetIDs) == 0 || slices.Contains(targetIDs, t.ID)) {
				targets = append(targets, *t)
				if s := d.Server(t.ServerID); s != nil {
					servers[s.ID] = *s
				}
			}
		}
	})
	if cert.ID == "" {
		return nil, errors.New("证书不存在")
	}
	if len(targets) == 0 {
		return nil, errors.New("没有可部署的目标，请先添加部署目标")
	}
	results := make([]certs.DeployResult, len(targets))
	ids := make([]string, len(targets))
	pos := map[string]int{}
	for i, t := range targets {
		ids[i] = t.ID
		pos[t.ID] = i
	}
	var mu sync.Mutex
	a.parallel(ids, func(tid string) {
		t := targets[pos[tid]]
		srv, ok := servers[t.ServerID]
		var r certs.DeployResult
		if !ok {
			r = certs.DeployResult{TargetID: t.ID, Message: "服务器不存在"}
		} else {
			r = certs.Deploy(ctx, a.pool, t, srv, cert, stepFn)
		}
		mu.Lock()
		results[pos[tid]] = r
		mu.Unlock()
		_ = a.store.Update(func(d *store.Data) error {
			if x := d.Target(t.ID); x != nil {
				x.LastDeployAt = store.Now()
				x.LastMessage = r.Message
				if r.Success {
					x.LastStatus, x.LastFingerprint = "success", r.Fingerprint
				} else {
					x.LastStatus = "failed"
				}
			}
			return nil
		})
		if doneFn != nil {
			doneFn(r)
		}
	})
	var hist []store.RunResult
	for _, r := range results {
		var lines []string
		for _, s := range r.Steps {
			line := fmt.Sprintf("[%s] %s", s.Status, s.Label)
			if s.Message != "" {
				line += "：" + s.Message
			}
			lines = append(lines, line)
		}
		rr := store.RunResult{ServerID: r.ServerID, ServerName: r.ServerName, Output: strings.Join(lines, "\n"), DurationMs: r.DurationMs}
		if !r.Success {
			rr.ExitCode, rr.Error = 1, r.Message
		}
		hist = append(hist, rr)
	}
	a.recordOp("cert", "部署证书 "+cert.Name, strings.Join(cert.Domains, ", "), hist)
	return results, nil
}

// DeployCert 异步部署证书，进度通过 deploy:* 事件推送，返回本次部署 ID。
func (a *App) DeployCert(certID string, targetIDs []string) (string, error) {
	runID := store.NewID()
	var count int
	a.store.Read(func(d *store.Data) {
		for _, t := range d.Targets {
			if t.CertID == certID && (len(targetIDs) == 0 || slices.Contains(targetIDs, t.ID)) {
				count++
			}
		}
	})
	if count == 0 {
		return "", errors.New("没有可部署的目标，请先添加部署目标")
	}
	go func() {
		results, err := a.deployTargets(context.Background(), certID, targetIDs,
			func(s certs.Step) { a.emit("deploy:step", map[string]any{"runId": runID, "step": s}) },
			func(r certs.DeployResult) { a.emit("deploy:target_done", map[string]any{"runId": runID, "result": r}) })
		payload := map[string]any{"runId": runID, "certId": certID, "results": results}
		if err != nil {
			payload["error"] = err.Error()
		}
		a.emit("deploy:done", payload)
	}()
	return runID, nil
}

type DiscoveredView struct {
	certs.Discovered
	MatchCertID   string `json:"matchCertId"`
	MatchCertName string `json:"matchCertName"`
	SameVersion   bool   `json:"sameVersion"`
	HasTarget     bool   `json:"hasTarget"`
}

// DiscoverCerts 扫描服务器上 Web 服务配置中引用的证书，并与证书库匹配。
func (a *App) DiscoverCerts(serverID string) ([]DiscoveredView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	found, err := certs.Discover(ctx, a.pool, serverID)
	if err != nil {
		return nil, err
	}
	out := []DiscoveredView{}
	a.store.Read(func(d *store.Data) {
		for _, f := range found {
			v := DiscoveredView{Discovered: f}
			if v.Domains == nil {
				v.Domains = []string{}
			}
			for _, c := range d.Certs {
				if c.Fingerprint == f.Fingerprint || (len(f.Domains) > 0 && sameDomains(c.Domains, f.Domains)) {
					v.MatchCertID, v.MatchCertName, v.SameVersion = c.ID, c.Name, c.Fingerprint == f.Fingerprint
					for _, t := range d.Targets {
						if t.CertID == c.ID && t.ServerID == serverID && t.CertPath == f.CertPath {
							v.HasTarget = true
						}
					}
					break
				}
			}
			out = append(out, v)
		}
	})
	return out, nil
}

func sameDomains(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}

func (a *App) CheckTLS(host string, port int, sni string) (certs.TLSInfo, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return certs.TLSInfo{}, errors.New("请填写主机")
	}
	if port <= 0 {
		port = 443
	}
	return certs.CheckTLS(context.Background(), host, port, strings.TrimSpace(sni))
}
