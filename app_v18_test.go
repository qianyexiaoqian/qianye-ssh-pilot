package main

import (
	"strings"
	"testing"

	"sshpilot/internal/agent"
	"sshpilot/internal/store"
)

func certOrder(a *App) string {
	var names []string
	for _, c := range a.ListCerts() {
		names = append(names, c.Name)
	}
	return strings.Join(names, " ")
}

// groupsOf 把证书按分组写成一行，例如 "生产[a b] 测试[c] -[d]"（- 表示没有分组）。
func groupsOf(a *App) string {
	var parts []string
	for _, g := range append(a.ListCertGroups(), "") {
		var names []string
		for _, c := range a.ListCerts() {
			if c.Group == g {
				names = append(names, c.Name)
			}
		}
		label := g
		if g == "" {
			label = "-"
		}
		parts = append(parts, label+"["+strings.Join(names, " ")+"]")
	}
	return strings.Join(parts, " ")
}

func TestCertGroupsAndOrder(t *testing.T) {
	a := newTestApp(t)
	id := map[string]string{}
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		certPEM, keyPEM := selfSigned(t, name+".example.com")
		v, err := a.ImportCert(ImportCertRequest{Name: name, CertPEM: certPEM, KeyPEM: keyPEM})
		if err != nil {
			t.Fatal(err)
		}
		id[name] = v.ID
	}
	// 让第一张证书最晚到期：只要哪里又按到期时间排了一次，它就会跑到后面去
	if err := a.store.Update(func(d *store.Data) error {
		d.Cert(id["a"]).NotAfter += 300 * 24 * 3600 * 1000
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// 按保存的顺序显示，新导入的排在最后；还没有分组
	if got := certOrder(a); got != "a b c d e" {
		t.Fatalf("initial order: %q", got)
	}
	if g := a.ListCertGroups(); len(g) != 0 {
		t.Fatalf("no groups yet: %v", g)
	}

	// 分组：按建立的先后排；没有证书的分组自动消失
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(a.SetCertGroup([]string{id["c"], id["a"]}, "  生产  "))
	must(a.SetCertGroup([]string{id["d"]}, "测试"))
	if got := groupsOf(a); got != "生产[a c] 测试[d] -[b e]" {
		t.Fatalf("after grouping: %q", got)
	}
	must(a.SetCertGroup([]string{id["d"]}, ""))
	if got := groupsOf(a); got != "生产[a c] -[b d e]" {
		t.Fatalf("emptied group should disappear: %q", got)
	}
	must(a.SetCertGroup([]string{id["d"], id["e"]}, "测试"))
	if err := a.SetCertGroup([]string{"nope"}, "测试"); err == nil {
		t.Error("unknown certificate should be refused")
	}
	if err := a.SetCertGroup([]string{id["a"]}, strings.Repeat("长", maxCertGroupName+1)); err == nil {
		t.Error("an over-long group name should be refused")
	}

	// 在一个分组里拖动：只调整这个分组里的证书之间的顺序
	must(a.ReorderCerts([]string{id["e"], id["d"]}))
	if got, all := groupsOf(a), certOrder(a); got != "生产[a c] 测试[e d] -[b]" || all != "a b c e d" {
		t.Fatalf("reorder inside a group: %q / %q", got, all)
	}
	must(a.ReorderCerts([]string{id["c"], "nope", id["a"], id["c"]}))
	if got := groupsOf(a); got != "生产[c a] 测试[e d] -[b]" {
		t.Fatalf("reorder ignores unknown and repeated ids: %q", got)
	}
	// 调整分组的先后
	must(a.ReorderCertGroups([]string{"测试", "生产"}))
	if got := groupsOf(a); got != "测试[e d] 生产[c a] -[b]" {
		t.Fatalf("reorder groups: %q", got)
	}

	// 修改分组：改名后留在原来的位置；没有选上的证书移出分组，新选上的加进来
	must(a.SaveCertGroup("测试", "预发", []string{id["d"], id["b"]}))
	if got := groupsOf(a); got != "预发[b d] 生产[c a] -[e]" {
		t.Fatalf("edit a group: %q", got)
	}
	if err := a.SaveCertGroup("预发", "生产", []string{id["d"]}); err == nil || !strings.Contains(err.Error(), "已经有一个叫「生产」的分组") {
		t.Errorf("renaming onto an existing group: %v", err)
	}
	if err := a.SaveCertGroup("", "", []string{id["e"]}); err == nil {
		t.Error("a new group needs a name")
	}
	// 新建分组排在最后；解散分组
	must(a.SaveCertGroup("", "备用", []string{id["e"]}))
	if got := groupsOf(a); got != "预发[b d] 生产[c a] 备用[e] -[]" {
		t.Fatalf("new group: %q", got)
	}
	must(a.SaveCertGroup("预发", "预发", nil))
	if got := groupsOf(a); got != "生产[c a] 备用[e] -[b d]" {
		t.Fatalf("dissolve a group: %q", got)
	}

	// 导入时可以直接放进分组；替换（续期）已有证书时分组和位置都不变
	certPEM, keyPEM := selfSigned(t, "f.example.com")
	f, err := a.ImportCert(ImportCertRequest{Name: "f", CertPEM: certPEM, KeyPEM: keyPEM, Group: "生产"})
	if err != nil || f.Group != "生产" {
		t.Fatalf("import into a group: %+v %v", f.Group, err)
	}
	certPEM, keyPEM = selfSigned(t, "c.example.com")
	if v, err := a.ImportCert(ImportCertRequest{ID: id["c"], CertPEM: certPEM, KeyPEM: keyPEM, Group: "备用"}); err != nil || v.Group != "生产" {
		t.Fatalf("renewal keeps the group: %+v %v", v.Group, err)
	}
	if got := groupsOf(a); got != "生产[c a f] 备用[e] -[b d]" {
		t.Fatalf("after import and renewal: %q", got)
	}

	// 顺序和分组保存在数据里，重新打开还在
	b := reopenApp(t, a.dataDir)
	if got := groupsOf(b); got != "生产[c a f] 备用[e] -[b d]" {
		t.Fatalf("after reopen: %q", got)
	}
	// 删除证书：空了的分组跟着消失
	must(b.DeleteCert(id["e"]))
	if got := groupsOf(b); got != "生产[c a f] -[b d]" {
		t.Fatalf("after deleting the only certificate of a group: %q", got)
	}

	// AI 助手可以换分组，也能在证书列表里看到分组
	tc := &agent.ToolContext{Agent: b.agent, Session: &agent.Session{ID: "test"}}
	r, err, asked := askTool(t, b, tc, "import_certificate", map[string]any{"certificate": "b", "group": "测试", "name": "备用站"}, false)
	if err != nil || len(asked) != 0 || !strings.Contains(r.Content, "改名为「备用站」") || !strings.Contains(r.Content, "放进分组「测试」") {
		t.Fatalf("AI moves a certificate into a group: %v %+v", err, r)
	}
	if got := groupsOf(b); got != "生产[c a f] 测试[备用站] -[d]" {
		t.Fatalf("after the AI regrouped: %q", got)
	}
	if r, _, _ = askTool(t, b, tc, "list_certificates", map[string]any{}, false); !strings.Contains(r.Content, "分组：测试") || !strings.Contains(r.Content, "分组：生产") {
		t.Errorf("list should show groups: %s", r.Content)
	}
	if r, err, _ = askTool(t, b, tc, "import_certificate", map[string]any{"certificate": "备用站", "group": ""}, false); err != nil || !strings.Contains(r.Content, "移出分组") {
		t.Fatalf("AI removes a certificate from its group: %v %+v", err, r)
	}
	if r, err, _ = askTool(t, b, tc, "import_certificate", map[string]any{"certificate": "备用站", "group": ""}, false); err != nil || !strings.Contains(r.Content, "没有需要修改") {
		t.Fatalf("no-op regroup: %v %+v", err, r)
	}
}

// 以前证书列表总是按到期时间显示。升级后第一次打开保持原来看到的顺序，之后就按用户拖出来的顺序。
func TestCertOrderKeepsExpiryOrderOnUpgrade(t *testing.T) {
	a := newTestApp(t)
	for _, name := range []string{"late", "soon", "mid"} {
		certPEM, keyPEM := selfSigned(t, name+".example.com")
		if _, err := a.ImportCert(ImportCertRequest{Name: name, CertPEM: certPEM, KeyPEM: keyPEM}); err != nil {
			t.Fatal(err)
		}
	}
	// 模拟旧版本留下的数据：没有“已经改成按保存顺序排”的标记，到期时间各不相同
	day := int64(24 * 3600 * 1000)
	if err := a.store.Update(func(d *store.Data) error {
		d.CertOrderV1 = false
		for _, c := range d.Certs {
			c.NotAfter += map[string]int64{"late": 90 * day, "soon": 5 * day, "mid": 40 * day}[c.Name]
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	b := reopenApp(t, a.dataDir)
	if got := certOrder(b); got != "soon mid late" {
		t.Fatalf("first open after upgrade should keep the expiry order: %q", got)
	}
	ids := map[string]string{}
	for _, c := range b.ListCerts() {
		ids[c.Name] = c.ID
	}
	if err := b.ReorderCerts([]string{ids["late"], ids["soon"], ids["mid"]}); err != nil {
		t.Fatal(err)
	}
	if got := certOrder(reopenApp(t, a.dataDir)); got != "late soon mid" {
		t.Fatalf("the user's own order must survive a restart: %q", got)
	}
}
