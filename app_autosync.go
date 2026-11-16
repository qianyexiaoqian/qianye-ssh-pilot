package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"sshpilot/internal/store"
)

const (
	autoSyncEvery = 6 * time.Hour    // 每张证书的检查周期
	autoSyncTick  = 30 * time.Minute // 后台轮询间隔
)

// AutoSyncResult 是一次「检查来源 → 拉取新证书 → 同步到部署目标」的结果。
type AutoSyncResult struct {
	CertID   string `json:"certId"`
	CertName string `json:"certName"`
	OK       bool   `json:"ok"`
	Changed  bool   `json:"changed"`  // 来源服务器上的证书已续期，已拉取新版本
	Deployed int    `json:"deployed"` // 本次同步成功的部署目标数
	Failed   int    `json:"failed"`   // 本次同步失败的部署目标数
	Message  string `json:"message"`
	Time     int64  `json:"time"`
}

// SetCertAutoSync 开关证书的自动同步；只有记录了来源服务器的证书才能开启。
func (a *App) SetCertAutoSync(id string, on bool) error {
	return a.store.Update(func(d *store.Data) error {
		c := d.Cert(id)
		if c == nil {
			return errors.New("证书不存在")
		}
		if on && c.Source == nil {
			return errors.New("这张证书没有记录来源服务器。请先通过「从服务器拉取」导入，才能自动获取续期后的新证书")
		}
		c.AutoSync = on
		return nil
	})
}

// SyncCertNow 立即检查一张证书的来源并同步（与自动同步执行相同的流程）。
func (a *App) SyncCertNow(id string) (AutoSyncResult, error) {
	var has bool
	a.store.Read(func(d *store.Data) {
		c := d.Cert(id)
		has = c != nil && c.Source != nil
	})
	if !has {
		return AutoSyncResult{}, errors.New("这张证书没有记录来源服务器")
	}
	return a.syncCert(id), nil
}

// syncCert 从来源服务器检查证书是否续期；有新版本就拉取，然后把证书同步到还没有更新的部署目标。
// 某个目标在当前版本上已经部署失败过时不会反复重试（部署失败会回滚，需要人工处理后手动同步）。
func (a *App) syncCert(id string) AutoSyncResult {
	a.autoSyncMu.Lock()
	defer a.autoSyncMu.Unlock()
	res := AutoSyncResult{CertID: id, Time: store.Now()}
	defer func() {
		_ = a.store.Update(func(d *store.Data) error {
			if c := d.Cert(id); c != nil {
				c.AutoSyncAt, c.AutoSyncOK, c.AutoSyncMessage = res.Time, res.OK, res.Message
			}
			return nil
		})
	}()

	r, err := a.RefreshCert(id)
	if err != nil {
		a.store.Read(func(d *store.Data) {
			if c := d.Cert(id); c != nil {
				res.CertName = c.Name
			}
		})
		res.Message = "检查来源服务器失败：" + err.Error()
		return res
	}
	res.Changed, res.CertName = r.Changed, r.Cert.Name

	var pending []string
	total, stuck := 0, 0
	a.store.Read(func(d *store.Data) {
		c := d.Cert(id)
		if c == nil {
			return
		}
		for _, t := range d.Targets {
			if t.CertID != id {
				continue
			}
			total++
			switch {
			case t.LastFingerprint == c.Fingerprint && t.LastStatus == "success":
			case t.LastStatus == "failed" && t.LastDeployAt >= c.UpdatedAt:
				stuck++
			default:
				pending = append(pending, t.ID)
			}
		}
	})
	if len(pending) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		results, err := a.deployTargets(ctx, id, pending, nil, nil)
		cancel()
		if err != nil {
			res.Message = "同步失败：" + err.Error()
			return res
		}
		for _, x := range results {
			if x.Success {
				res.Deployed++
			} else {
				res.Failed++
			}
		}
	}

	head := "来源服务器上的证书没有变化"
	if res.Changed {
		head = fmt.Sprintf("证书已续期（有效期至 %s）", time.UnixMilli(r.Cert.NotAfter).Format("2006-01-02"))
	}
	switch {
	case total == 0:
		res.Message = head + "，还没有配置部署目标"
	case res.Failed > 0:
		res.Message = fmt.Sprintf("%s，%d 个部署目标同步成功，%d 个失败（已自动回滚）", head, res.Deployed, res.Failed)
	case res.Deployed > 0:
		res.Message = fmt.Sprintf("%s，已同步到 %d 个部署目标", head, res.Deployed)
	case stuck > 0:
		res.Message = fmt.Sprintf("%s，有 %d 个部署目标此前同步失败，需要处理后手动同步", head, stuck)
	default:
		res.Message = head + "，全部部署目标都是最新版本"
	}
	res.OK = res.Failed == 0 && stuck == 0
	return res
}

// autoSyncDue 返回开启了自动同步且到了检查时间的证书。
func (a *App) autoSyncDue(now time.Time) []string {
	var ids []string
	a.store.Read(func(d *store.Data) {
		for _, c := range d.Certs {
			if c.AutoSync && c.Source != nil && now.Sub(time.UnixMilli(c.AutoSyncAt)) >= autoSyncEvery {
				ids = append(ids, c.ID)
			}
		}
	})
	return ids
}

// autoSyncRun 检查所有到期的证书；有变化或出现问题时通知前端。
func (a *App) autoSyncRun(now time.Time) []AutoSyncResult {
	var out []AutoSyncResult
	for _, id := range a.autoSyncDue(now) {
		res := a.syncCert(id)
		out = append(out, res)
		if res.Changed || res.Deployed+res.Failed > 0 || !res.OK {
			a.emit("cert:autosync", res)
		}
	}
	return out
}

func (a *App) autoSyncLoop() {
	time.Sleep(2 * time.Minute)
	for {
		a.autoSyncRun(time.Now())
		time.Sleep(autoSyncTick)
	}
}
