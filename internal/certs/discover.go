package certs

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"path"
	"strconv"
	"strings"
	"time"

	"sshpilot/internal/sshx"
)

// Discovered 是在服务器上发现的证书配置。
type Discovered struct {
	Source      string   `json:"source"` // nginx | apache | haproxy | letsencrypt
	CertPath    string   `json:"certPath"`
	KeyPath     string   `json:"keyPath"`
	Domains     []string `json:"domains"`
	Issuer      string   `json:"issuer"`
	NotAfter    int64    `json:"notAfter"`
	Fingerprint string   `json:"fingerprint"`
	Error       string   `json:"error"`
}

const discoverScript = `if command -v nginx >/dev/null 2>&1; then
  nginx -T 2>/dev/null | awk '$1=="ssl_certificate" || $1=="ssl_certificate_key" { v=$2; sub(/;.*/, "", v); print "nginx", $1, v }'
fi
for d in /etc/apache2 /etc/httpd; do
  [ -d "$d" ] && grep -rhiE '^[[:space:]]*SSLCertificate(Key)?File[[:space:]]' "$d" 2>/dev/null | awk '{print "apache", tolower($1), $2}'
done
if [ -f /etc/haproxy/haproxy.cfg ]; then
  grep -oE 'crt[[:space:]]+[^[:space:]]+' /etc/haproxy/haproxy.cfg | awk '{print "haproxy", "crt", $2}'
fi
for d in /etc/letsencrypt/live/*/; do
  [ -f "${d}fullchain.pem" ] && echo "letsencrypt cert ${d}fullchain.pem" && echo "letsencrypt key ${d}privkey.pem"
done
true`

func cleanPath(source, p string) string {
	p = strings.Trim(p, `"'`)
	if p == "" || strings.Contains(p, "$") {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		switch source {
		case "nginx":
			p = path.Join("/etc/nginx", p)
		case "apache":
			p = path.Join("/etc/httpd", p)
		default:
			return ""
		}
	}
	return p
}

// Discover 从 Nginx / Apache / HAProxy / Let's Encrypt 配置中查找证书，并读取每个证书的信息。
func Discover(ctx context.Context, pool *sshx.Pool, serverID string) ([]Discovered, error) {
	res, err := pool.RunScript(ctx, serverID, sshx.Script{Content: discoverScript, Interpreter: "sh"}, sshx.ExecOptions{Privileged: true, Timeout: time.Minute})
	if err != nil {
		return nil, err
	}
	var found []Discovered
	index := map[string]int{}
	pending := -1 // 等待配对私钥的证书下标
	for _, line := range strings.Split(res.Stdout, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		source, kind := f[0], f[1]
		p := cleanPath(source, f[2])
		if p == "" || strings.HasSuffix(p, "/") {
			continue
		}
		isKey := kind == "ssl_certificate_key" || kind == "sslcertificatekeyfile" || kind == "key"
		if isKey {
			if pending >= 0 && found[pending].KeyPath == "" {
				found[pending].KeyPath = p
			}
			continue
		}
		if i, ok := index[p]; ok {
			pending = i
			continue
		}
		d := Discovered{Source: source, CertPath: p}
		if source == "haproxy" {
			d.KeyPath = p // HAProxy 使用证书+私钥合并文件
		}
		found = append(found, d)
		pending = len(found) - 1
		index[p] = pending
	}
	if len(found) == 0 {
		return []Discovered{}, nil
	}

	// 一次性读取所有证书文件
	var b strings.Builder
	for _, d := range found {
		fmt.Fprintf(&b, "f=%s; echo \"===SSHPILOT_FILE $f\"; head -c 131072 \"$f\" 2>/dev/null || echo SSHPILOT_UNREADABLE\n", sshx.ShellQuote(d.CertPath))
	}
	out, err := pool.RunScript(ctx, serverID, sshx.Script{Content: b.String(), Interpreter: "sh"}, sshx.ExecOptions{Privileged: true, Timeout: time.Minute})
	if err != nil {
		return found, nil
	}
	contents := map[string]string{}
	for _, part := range strings.Split(out.Stdout, "===SSHPILOT_FILE ")[1:] {
		name, body, _ := strings.Cut(part, "\n")
		contents[strings.TrimSpace(name)] = body
	}
	for i := range found {
		d := &found[i]
		body, ok := contents[d.CertPath]
		if !ok || strings.Contains(body, "SSHPILOT_UNREADABLE") {
			d.Error = "无法读取文件"
			continue
		}
		p, err := Parse(body)
		if err != nil {
			d.Error = err.Error()
			continue
		}
		d.Domains = p.Domains
		d.Issuer = p.Issuer
		d.NotAfter = p.NotAfter
		d.Fingerprint = p.Fingerprint
	}
	return found, nil
}

// TLSInfo 是一次 TLS 握手得到的线上证书信息。
type TLSInfo struct {
	Address     string   `json:"address"`
	SNI         string   `json:"sni"`
	Domains     []string `json:"domains"`
	Subject     string   `json:"subject"`
	Issuer      string   `json:"issuer"`
	NotBefore   int64    `json:"notBefore"`
	NotAfter    int64    `json:"notAfter"`
	Fingerprint string   `json:"fingerprint"`
	Protocol    string   `json:"protocol"`
	ChainCount  int      `json:"chainCount"`
	Trusted     bool     `json:"trusted"`
	VerifyError string   `json:"verifyError"`
	LatencyMs   int64    `json:"latencyMs"`
}

// CheckTLS 连接 host:port 并读取服务器实际返回的证书。
func CheckTLS(ctx context.Context, host string, port int, sni string) (TLSInfo, error) {
	if sni == "" && net.ParseIP(host) == nil {
		sni = host
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	d := tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 8 * time.Second},
		Config:    &tls.Config{ServerName: sni, InsecureSkipVerify: true},
	}
	start := time.Now()
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := d.DialContext(cctx, "tcp", addr)
	if err != nil {
		return TLSInfo{}, err
	}
	defer conn.Close()
	state := conn.(*tls.Conn).ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return TLSInfo{}, fmt.Errorf("服务器没有返回证书")
	}
	leaf := state.PeerCertificates[0]
	info := TLSInfo{
		Address:     addr,
		SNI:         sni,
		Domains:     leaf.DNSNames,
		Subject:     leaf.Subject.String(),
		Issuer:      issuerName(leaf),
		NotBefore:   leaf.NotBefore.UnixMilli(),
		NotAfter:    leaf.NotAfter.UnixMilli(),
		Fingerprint: Fingerprint(leaf.Raw),
		Protocol:    tls.VersionName(state.Version),
		ChainCount:  len(state.PeerCertificates) - 1,
		LatencyMs:   time.Since(start).Milliseconds(),
	}
	inter := x509.NewCertPool()
	for _, c := range state.PeerCertificates[1:] {
		inter.AddCert(c)
	}
	_, verr := leaf.Verify(x509.VerifyOptions{DNSName: sni, Intermediates: inter})
	if verr != nil {
		info.VerifyError = verr.Error()
	} else {
		info.Trusted = true
	}
	return info, nil
}
