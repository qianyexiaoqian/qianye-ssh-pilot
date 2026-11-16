package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"sshpilot/internal/ops"
	"sshpilot/internal/sshx"
	"sshpilot/internal/store"
)

func (a *App) ListServices(serverID string) ([]ops.ServiceInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	list, err := ops.ListServices(ctx, a.pool, serverID)
	if list == nil {
		list = []ops.ServiceInfo{}
	}
	return list, err
}

// ServiceAction 在多台服务器上对同一服务执行 start / stop / restart / reload / enable / disable。
func (a *App) ServiceAction(serverIDs []string, service, action string) []OpResult {
	service = strings.TrimSuffix(strings.TrimSpace(service), ".service")
	if err := ops.ValidateService(service, action); err != nil {
		return []OpResult{{OK: false, Message: err.Error()}}
	}
	results := a.eachServer(serverIDs, func(ctx context.Context, id string) (string, error) {
		res, err := a.pool.Exec(ctx, id, ops.ServiceCommand(service, action), sshx.ExecOptions{Privileged: true, Timeout: 2 * time.Minute})
		if err != nil {
			return "", err
		}
		out := strings.TrimSpace(res.Combined())
		if res.ExitCode != 0 {
			if out == "" {
				out = fmt.Sprintf("退出码 %d", res.ExitCode)
			}
			return "", errors.New(out)
		}
		if out == "" {
			out = "完成"
		}
		return out, nil
	})
	var hist []store.RunResult
	for _, r := range results {
		rr := store.RunResult{ServerID: r.ServerID, ServerName: r.ServerName, Output: r.Message}
		if !r.OK {
			rr.ExitCode, rr.Error = 1, r.Message
		}
		hist = append(hist, rr)
	}
	a.recordOp("service", fmt.Sprintf("%s %s", action, service), "systemctl "+action+" "+service, hist)
	return results
}

func (a *App) ServiceStatus(serverID, service string) (string, error) {
	return a.serviceText(serverID, service, ops.ServiceCommand(service, "status"))
}

func (a *App) ServiceLogs(serverID, service string, lines int) (string, error) {
	return a.serviceText(serverID, service, ops.ServiceLogsCommand(service, lines))
}

func (a *App) serviceText(serverID, service, cmd string) (string, error) {
	service = strings.TrimSuffix(strings.TrimSpace(service), ".service")
	if err := ops.ValidateService(service, ""); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	res, err := a.pool.Exec(ctx, serverID, cmd, sshx.ExecOptions{Privileged: true, Timeout: 50 * time.Second})
	if err != nil {
		return "", err
	}
	return res.Combined(), nil
}

// ---- 智能巡检 ----

// RunInspection 采集服务器指标并结合证书状态生成巡检报告；serverIDs 为空表示全部服务器。
func (a *App) RunInspection(serverIDs []string) (*ops.Report, error) {
	a.inspectMu.Lock()
	defer a.inspectMu.Unlock()
	return a.runInspection(serverIDs)
}

func (a *App) runInspection(serverIDs []string) (*ops.Report, error) {
	var servers []store.Server
	var certList []store.Certificate
	var targets []store.DeployTarget
	a.store.Read(func(d *store.Data) {
		for _, s := range d.Servers {
			if len(serverIDs) == 0 || containsID(serverIDs, s.ID) {
				servers = append(servers, *s)
			}
		}
		for _, c := range d.Certs {
			certList = append(certList, *c)
		}
		for _, t := range d.Targets {
			targets = append(targets, *t)
		}
	})
	if len(servers) == 0 && len(certList) == 0 {
		return nil, errors.New("还没有可巡检的服务器或证书")
	}
	r := &ops.Report{ID: store.NewID(), StartedAt: store.Now(), Servers: make([]ops.ServerReport, len(servers))}
	ids := make([]string, len(servers))
	pos := map[string]int{}
	for i, s := range servers {
		ids[i], pos[s.ID] = s.ID, i
	}
	var mu sync.Mutex
	done := 0
	a.parallel(ids, func(id string) {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		s := servers[pos[id]]
		sr := ops.ServerReport{ServerID: id, ServerName: s.Name}
		info, err := ops.CollectInfo(ctx, a.pool, id)
		if err != nil {
			sr.Error = err.Error()
		} else {
			sr.Online, sr.Info = true, info
		}
		mu.Lock()
		r.Servers[pos[id]] = sr
		done++
		n := done
		mu.Unlock()
		a.emit("inspect:progress", map[string]any{"done": n, "total": len(ids), "server": s.Name})
	})
	ops.Analyze(r, certList, targets, servers, a.settings().General.CertWarnDays)
	r.FinishedAt = store.Now()
	if r.Findings == nil {
		r.Findings = []ops.Finding{}
	}
	a.reportMu.Lock()
	a.lastReport = r
	a.reportMu.Unlock()
	if b, err := json.Marshal(r); err == nil {
		_ = store.WriteFileAtomic(filepath.Join(a.dataDir, "inspection.json"), b, 0o600)
	}
	return r, nil
}

func (a *App) GetLastInspection() *ops.Report {
	a.reportMu.Lock()
	defer a.reportMu.Unlock()
	return a.lastReport
}

// InspectionPrompt 返回把巡检报告交给 AI 分析时使用的提示词。
func (a *App) InspectionPrompt() (string, error) {
	r := a.GetLastInspection()
	if r == nil {
		return "", errors.New("还没有巡检报告")
	}
	return ops.ReportPrompt(r), nil
}

func containsID(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// ---- 自动巡检 ----

func clampInspectInterval(mins int) int {
	if mins <= 0 {
		return 0
	}
	return min(max(mins, 10), 1440)
}

// SetInspectInterval 设置自动巡检的间隔（分钟），0 表示关闭。
func (a *App) SetInspectInterval(minutes int) (store.Settings, error) {
	err := a.store.Update(func(d *store.Data) error {
		d.Settings.General.InspectInterval = clampInspectInterval(minutes)
		return nil
	})
	return a.GetSettings(), err
}

var digitsRe = regexp.MustCompile(`[0-9.]+`)

// findingKey 标识「同一个问题」：标题里的数值（使用率、天数）每次巡检都会变化，比较时去掉。
func findingKey(f ops.Finding) string {
	return f.ServerID + "|" + f.Category + "|" + digitsRe.ReplaceAllString(f.Title, "")
}

// newCritical 返回上一份报告里没有、这次新出现的严重问题标题。
func newCritical(prev, cur *ops.Report) []string {
	seen := map[string]bool{}
	if prev != nil {
		for _, f := range prev.Findings {
			if f.Severity == "critical" {
				seen[findingKey(f)] = true
			}
		}
	}
	var out []string
	for _, f := range cur.Findings {
		if f.Severity != "critical" || seen[findingKey(f)] {
			continue
		}
		title := f.Title
		if f.ServerName != "" {
			title = "[" + f.ServerName + "] " + title
		}
		out = append(out, title)
	}
	return out
}

// autoInspectTick 在到达设定的间隔时自动巡检全部服务器；出现新的严重问题时通知前端。
func (a *App) autoInspectTick(now time.Time) *ops.Report {
	mins := a.settings().General.InspectInterval
	if mins <= 0 {
		return nil
	}
	prev := a.GetLastInspection()
	if prev != nil && now.Sub(time.UnixMilli(prev.FinishedAt)) < time.Duration(mins)*time.Minute {
		return nil
	}
	if !a.inspectMu.TryLock() {
		return nil // 正在手动巡检
	}
	defer a.inspectMu.Unlock()
	r, err := a.runInspection(nil)
	if err != nil {
		return nil
	}
	a.emit("inspect:done", r)
	if fresh := newCritical(prev, r); len(fresh) > 0 {
		a.emit("inspect:alert", map[string]any{"score": r.Score, "count": len(fresh), "titles": fresh[:min(len(fresh), 3)]})
	}
	return r
}

func (a *App) autoInspectLoop() {
	for {
		time.Sleep(time.Minute)
		a.autoInspectTick(time.Now())
	}
}

// ClearInspection 删除巡检报告。
func (a *App) ClearInspection() {
	a.reportMu.Lock()
	a.lastReport = nil
	a.reportMu.Unlock()
	_ = os.Remove(filepath.Join(a.dataDir, "inspection.json"))
}
