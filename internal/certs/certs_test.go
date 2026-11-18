package certs_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"sshpilot/internal/certs"
	"sshpilot/internal/sshx"
	"sshpilot/internal/store"
	"sshpilot/internal/testsshd"
)

type chain struct {
	leaf, inter, root string
	key               string
	keyObj            *ecdsa.PrivateKey
	leafDER           []byte
}

func issue(t *testing.T, cn string, dns []string, days int, parent *x509.Certificate, parentKey *ecdsa.PrivateKey, isCA bool) (*x509.Certificate, *ecdsa.PrivateKey, string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn, Organization: []string{"SSH Pilot Test"}},
		DNSNames:              dns,
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Duration(days) * 24 * time.Hour),
		IsCA:                  isCA,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if parent == nil {
		parent, parentKey = tmpl, key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return c, key, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func makeChain(t *testing.T, days int) chain {
	root, rootKey, rootPEM := issue(t, "Test Root", nil, 3650, nil, nil, true)
	inter, interKey, interPEM := issue(t, "Test Intermediate", nil, 1825, root, rootKey, true)
	leaf, leafKey, leafPEM := issue(t, "app.example.com", []string{"app.example.com", "www.example.com"}, days, inter, interKey, false)
	der, _ := x509.MarshalPKCS8PrivateKey(leafKey)
	return chain{leaf: leafPEM, inter: interPEM, root: rootPEM, key: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), keyObj: leafKey, leafDER: leaf.Raw}
}

func TestParseOrdersChainAndMatchesKey(t *testing.T) {
	c := makeChain(t, 90)
	// 故意打乱顺序、把私钥混在一起
	p, err := certs.Parse(c.root+c.inter, c.key+c.leaf)
	if err != nil {
		t.Fatal(err)
	}
	if !p.HasKey || !p.KeyMatches {
		t.Fatalf("key: has=%v match=%v", p.HasKey, p.KeyMatches)
	}
	if p.ChainCount != 1 || !strings.HasPrefix(p.FullchainPEM, c.leaf) || !strings.Contains(p.FullchainPEM, c.inter) || strings.Contains(p.FullchainPEM, c.root) {
		t.Fatalf("fullchain order wrong, chain=%d", p.ChainCount)
	}
	if len(p.Domains) != 2 || p.Domains[0] != "app.example.com" {
		t.Fatalf("domains %v", p.Domains)
	}
	if p.Fingerprint != certs.Fingerprint(c.leafDER) || !strings.HasPrefix(p.KeyType, "ECDSA") {
		t.Fatalf("fingerprint/keytype %s %s", p.Fingerprint, p.KeyType)
	}
	other := makeChain(t, 30)
	q, err := certs.Parse(c.leaf, other.key)
	if err != nil || q.KeyMatches || len(q.Warnings) == 0 {
		t.Fatalf("mismatched key should warn: %+v %v", q, err)
	}
	if _, err := certs.Parse("not a cert"); err == nil {
		t.Fatal("garbage should fail")
	}
	if certs.PrimaryDomain([]string{"*.example.com"}) != "www.example.com" {
		t.Fatal("PrimaryDomain wildcard")
	}
}

func deployEnv(t *testing.T) (*sshx.Pool, store.Server) {
	srv := testsshd.Start(t)
	ep := &sshx.Endpoint{ServerID: "s1", Name: "web", Host: srv.Host, Port: srv.Port, User: srv.User, Password: srv.Password}
	pool := sshx.NewPool(func(string) (*sshx.Endpoint, error) { cp := *ep; return &cp, nil }, nil)
	t.Cleanup(pool.Close)
	return pool, store.Server{ID: "s1", Name: "web", Host: srv.Host, Port: srv.Port}
}

func cert(c chain) store.Certificate {
	return store.Certificate{ID: "c1", Name: "app", Domains: []string{"app.example.com"}, CertPEM: c.leaf + c.inter, KeyPEM: c.key}
}

func TestDeployBackupVerifyAndRollback(t *testing.T) {
	pool, srv := deployEnv(t)
	ctx := context.Background()
	dir := "/tmp/sshpilot-deploy-" + time.Now().Format("150405.000000")
	target := store.DeployTarget{ID: "t1", CertID: "c1", ServerID: "s1", CertPath: dir + "/ssl/app.crt", KeyPath: dir + "/ssl/app.key",
		CertFormat: "fullchain", Backup: true, TestCommand: "true", ServiceAction: "none"}

	v1 := makeChain(t, 30)
	var steps []certs.Step
	r := certs.Deploy(ctx, pool, target, srv, cert(v1), func(s certs.Step) { steps = append(steps, s) })
	if !r.Success {
		t.Fatalf("first deploy failed: %s %+v", r.Message, r.Steps)
	}
	got, err := pool.ReadFile(ctx, "s1", target.CertPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	p1, _ := certs.Parse(cert(v1).CertPEM)
	if got != p1.FullchainPEM {
		t.Fatal("remote fullchain differs")
	}
	if key, _ := pool.ReadFile(ctx, "s1", target.KeyPath, 0); !strings.Contains(key, "PRIVATE KEY") {
		t.Fatal("key not written")
	}
	if len(steps) < 3 {
		t.Fatalf("expected progress steps, got %+v", steps)
	}

	// 第二次部署新证书：旧文件被备份
	v2 := makeChain(t, 90)
	r = certs.Deploy(ctx, pool, target, srv, cert(v2), nil)
	if !r.Success || !strings.Contains(stepMsg(r, "upload"), "备份") {
		t.Fatalf("second deploy: %s %+v", r.Message, r.Steps)
	}
	res, _ := pool.Exec(ctx, "s1", "ls "+dir+"/ssl/ | grep -c 'app.crt.bak-'", sshx.ExecOptions{})
	if strings.TrimSpace(res.Stdout) != "1" {
		t.Fatalf("expected one backup, got %q", res.Stdout)
	}

	// 配置检测失败：自动回滚到 v2
	v3 := makeChain(t, 120)
	target.TestCommand = "echo 'nginx: [emerg] bad config' >&2; exit 1"
	r = certs.Deploy(ctx, pool, target, srv, cert(v3), nil)
	if r.Success || !r.RolledBack {
		t.Fatalf("expected rollback: %+v", r)
	}
	got, _ = pool.ReadFile(ctx, "s1", target.CertPath, 0)
	p2, _ := certs.Parse(cert(v2).CertPEM)
	if got != p2.FullchainPEM {
		t.Fatal("rollback did not restore previous certificate")
	}
	if !strings.Contains(stepMsg(r, "test"), "bad config") {
		t.Fatalf("test output not reported: %+v", r.Steps)
	}

	// 私钥不匹配时阻止部署
	bad := cert(v3)
	bad.KeyPEM = v1.key
	r = certs.Deploy(ctx, pool, target, srv, bad, nil)
	if r.Success || !strings.Contains(r.Message, "不匹配") {
		t.Fatalf("mismatched key should be blocked: %+v", r)
	}

	// HAProxy 合并格式
	combined := store.DeployTarget{ID: "t2", CertPath: dir + "/haproxy/app.pem", CertFormat: "combined", Backup: true, ServiceAction: "none"}
	r = certs.Deploy(ctx, pool, combined, srv, cert(v2), nil)
	if !r.Success {
		t.Fatalf("combined deploy: %+v", r)
	}
	got, _ = pool.ReadFile(ctx, "s1", combined.CertPath, 0)
	if !strings.Contains(got, "BEGIN CERTIFICATE") || !strings.Contains(got, "PRIVATE KEY") {
		t.Fatal("combined pem should contain cert and key")
	}

	// 从服务器拉取
	pulled, err := certs.Pull(ctx, pool, "s1", store.CertSource{CertPath: target.CertPath, KeyPath: target.KeyPath})
	if err != nil || pulled.Fingerprint != p2.Fingerprint || !pulled.KeyMatches {
		t.Fatalf("pull: %+v %v", pulled, err)
	}

	// 没有 Web 服务配置时，扫描结果为空
	found, err := certs.Discover(ctx, pool, "s1")
	if err != nil || len(found) != 0 {
		t.Fatalf("discover: %+v %v", found, err)
	}

	// XBoard 节点：从 config.yml 的 cert_file / key_file 里读出证书路径（带引号、带注释、写在列表项里都认识）
	cfg := dir + "/xboard-node/config.yml"
	yml := "panel:\n  url: \"https://panel.example.com\"\n  node_id: 1\ncert:\n  cert_mode: file\n" +
		"  cert_file: \"" + target.CertPath + "\"   # 站点证书\n  key_file: '" + target.KeyPath + "'\n" +
		"instances:\n  - cert_file: " + combined.CertPath + "\n    key_file: " + combined.CertPath + "\n" +
		"  - cert_file: certs/missing.pem\n# cert_file: /commented/out.pem\n"
	if err := pool.WriteFile(ctx, "s1", cfg, []byte(yml), "600", ""); err != nil {
		t.Fatal(err)
	}
	old := certs.XboardConfigs
	certs.XboardConfigs = []string{dir + "/xboard-node/absent.yml", cfg}
	defer func() { certs.XboardConfigs = old }()
	found, err = certs.Discover(ctx, pool, "s1")
	if err != nil || len(found) != 3 {
		t.Fatalf("discover xboard: %+v %v", found, err)
	}
	if d := found[0]; d.Source != "xboard" || d.CertPath != target.CertPath || d.KeyPath != target.KeyPath || d.Fingerprint != p2.Fingerprint || d.Error != "" {
		t.Errorf("xboard cert: %+v", d)
	}
	if d := found[1]; d.Source != "xboard" || d.CertPath != combined.CertPath || d.KeyPath != combined.CertPath {
		t.Errorf("xboard instance cert: %+v", d)
	}
	// 相对路径按配置文件所在的目录解析；文件不存在时说明读不到，而不是丢掉这一条
	if d := found[2]; d.CertPath != "/etc/xboard-node/certs/missing.pem" || d.KeyPath != "" || d.Error == "" {
		t.Errorf("relative path: %+v", d)
	}

	// 1Panel：网站证书放在面板自己的目录里（OpenResty 跑在容器里，宿主机上没有 nginx 命令可问）
	site := dir + "/1panel/sites/shop.example.com/ssl"
	for p, text := range map[string]string{
		site + "/fullchain.pem": cert(v2).CertPEM,
		site + "/privkey.pem":   cert(v2).KeyPEM,
		// 只有证书没有私钥的目录不算
		dir + "/1panel/sites/half.example.com/ssl/fullchain.pem": cert(v2).CertPEM,
	} {
		if err := pool.WriteFile(ctx, "s1", p, []byte(text), "600", ""); err != nil {
			t.Fatal(err)
		}
	}
	oldDirs := certs.OnePanelSiteDirs
	certs.OnePanelSiteDirs = []string{dir + "/1panel/absent/*/ssl", dir + "/1panel/sites/*/ssl"}
	defer func() { certs.OnePanelSiteDirs = oldDirs }()
	found, err = certs.Discover(ctx, pool, "s1")
	if err != nil || len(found) != 4 {
		t.Fatalf("discover 1panel: %+v %v", found, err)
	}
	if d := found[3]; d.Source != "1panel" || d.CertPath != site+"/fullchain.pem" || d.KeyPath != site+"/privkey.pem" || d.Fingerprint != p2.Fingerprint || d.Error != "" {
		t.Errorf("1panel site cert: %+v", d)
	}
}

func stepMsg(r certs.DeployResult, step string) string {
	var parts []string
	for _, s := range r.Steps {
		if s.Step == step {
			parts = append(parts, s.Message)
		}
	}
	return strings.Join(parts, "\n")
}

func TestCheckTLS(t *testing.T) {
	c := makeChain(t, 60)
	pair, err := tls.X509KeyPair([]byte(c.leaf+c.inter), []byte(c.key))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
	ts.StartTLS()
	defer ts.Close()
	host, port, _ := net.SplitHostPort(ts.Listener.Addr().String())
	p, _ := strconv.Atoi(port)
	info, err := certs.CheckTLS(context.Background(), host, p, "app.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if info.Fingerprint != certs.Fingerprint(c.leafDER) || info.ChainCount != 1 || info.Trusted || info.VerifyError == "" {
		t.Fatalf("tls info: %+v", info)
	}
	if _, err := certs.CheckTLS(context.Background(), "127.0.0.1", 1, ""); err == nil {
		t.Fatal("closed port should fail")
	}
}
