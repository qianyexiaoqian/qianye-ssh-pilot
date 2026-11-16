package certs

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Parsed 是解析后的证书材料。
type Parsed struct {
	Domains     []string `json:"domains"`
	Subject     string   `json:"subject"`
	Issuer      string   `json:"issuer"`
	Serial      string   `json:"serial"`
	NotBefore   int64    `json:"notBefore"`
	NotAfter    int64    `json:"notAfter"`
	Fingerprint string   `json:"fingerprint"`
	KeyType     string   `json:"keyType"`
	ChainCount  int      `json:"chainCount"`
	SelfSigned  bool     `json:"selfSigned"`
	HasKey      bool     `json:"hasKey"`
	KeyMatches  bool     `json:"keyMatches"`
	Warnings    []string `json:"warnings"`

	LeafPEM      string            `json:"-"`
	ChainPEM     string            `json:"-"`
	FullchainPEM string            `json:"-"`
	KeyPEM       string            `json:"-"`

}

func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	h := strings.ToUpper(hex.EncodeToString(sum[:]))
	var b strings.Builder
	for i := 0; i < len(h); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(h[i : i+2])
	}
	return b.String()
}

func encodeCert(c *x509.Certificate) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw}))
}

func parseKey(block *pem.Block) (crypto.PrivateKey, error) {
	switch block.Type {
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(block.Bytes)
	case "PRIVATE KEY":
		return x509.ParsePKCS8PrivateKey(block.Bytes)
	case "ENCRYPTED PRIVATE KEY":
		return nil, errors.New("不支持加密的私钥，请先解密（openssl pkey -in key.pem -out key-plain.pem）")
	}
	return nil, fmt.Errorf("不支持的私钥类型: %s", block.Type)
}

func keyMatches(cert *x509.Certificate, key crypto.PrivateKey) bool {
	signer, ok := key.(crypto.Signer)
	if !ok {
		return false
	}
	pub, ok := cert.PublicKey.(interface{ Equal(crypto.PublicKey) bool })
	return ok && pub.Equal(signer.Public())
}

func keyType(c *x509.Certificate) string {
	switch pk := c.PublicKey.(type) {
	case *rsa.PublicKey:
		return fmt.Sprintf("RSA %d", pk.N.BitLen())
	case *ecdsa.PublicKey:
		return "ECDSA " + pk.Curve.Params().Name
	case ed25519.PublicKey:
		return "Ed25519"
	}
	return c.PublicKeyAlgorithm.String()
}

func selfSigned(c *x509.Certificate) bool {
	return bytes.Equal(c.RawIssuer, c.RawSubject) && c.CheckSignatureFrom(c) == nil
}

// Parse 从任意组合的 PEM 文本中提取证书链与私钥：证书、链、私钥可以分开也可以粘贴在一起。
func Parse(texts ...string) (*Parsed, error) {
	var certs []*x509.Certificate
	seen := map[string]bool{}
	var keyBlock *pem.Block
	var keyErr error
	for _, text := range texts {
		rest := []byte(strings.ReplaceAll(text, "\r\n", "\n"))
		for {
			var block *pem.Block
			block, rest = pem.Decode(rest)
			if block == nil {
				break
			}
			switch {
			case block.Type == "CERTIFICATE":
				c, err := x509.ParseCertificate(block.Bytes)
				if err != nil {
					return nil, fmt.Errorf("证书解析失败: %w", err)
				}
				fp := Fingerprint(c.Raw)
				if !seen[fp] {
					seen[fp] = true
					certs = append(certs, c)
				}
			case strings.HasSuffix(block.Type, "PRIVATE KEY"):
				if keyBlock == nil {
					keyBlock = block
				}
			}
		}
	}
	if len(certs) == 0 {
		return nil, errors.New("没有找到证书（需要 -----BEGIN CERTIFICATE----- 格式的 PEM）")
	}

	p := &Parsed{}
	var key crypto.PrivateKey
	if keyBlock != nil {
		key, keyErr = parseKey(keyBlock)
		if keyErr != nil {
			return nil, keyErr
		}
		p.HasKey = true
		p.KeyPEM = string(pem.EncodeToMemory(keyBlock))
	}

	// 选出叶子证书：优先与私钥匹配的，其次第一张非 CA 证书。
	leafIdx := -1
	if key != nil {
		leafIdx = slices.IndexFunc(certs, func(c *x509.Certificate) bool { return keyMatches(c, key) })
		p.KeyMatches = leafIdx >= 0
		if leafIdx < 0 {
			p.Warnings = append(p.Warnings, "私钥与证书不匹配")
		}
	}
	if leafIdx < 0 {
		leafIdx = slices.IndexFunc(certs, func(c *x509.Certificate) bool { return !c.IsCA })
	}
	if leafIdx < 0 {
		leafIdx = 0
	}
	leaf := certs[leafIdx]
	others := slices.Delete(slices.Clone(certs), leafIdx, leafIdx+1)

	// 按签发关系从叶子向上排序中间证书，剩余的放在后面；自签名根证书不放入链。
	var chain []*x509.Certificate
	cur := leaf
	for len(others) > 0 {
		i := slices.IndexFunc(others, func(c *x509.Certificate) bool { return bytes.Equal(cur.RawIssuer, c.RawSubject) })
		if i < 0 {
			break
		}
		cur = others[i]
		chain = append(chain, cur)
		others = slices.Delete(others, i, i+1)
	}
	chain = append(chain, others...)
	chain = slices.DeleteFunc(chain, selfSigned)


	p.LeafPEM = encodeCert(leaf)
	var chainPEM strings.Builder
	for _, c := range chain {
		chainPEM.WriteString(encodeCert(c))
	}
	p.ChainPEM = chainPEM.String()
	p.FullchainPEM = p.LeafPEM + p.ChainPEM
	p.ChainCount = len(chain)

	p.Domains = slices.Clone(leaf.DNSNames)
	for _, ip := range leaf.IPAddresses {
		p.Domains = append(p.Domains, ip.String())
	}
	if len(p.Domains) == 0 && leaf.Subject.CommonName != "" {
		p.Domains = []string{leaf.Subject.CommonName}
	}
	p.Subject = leaf.Subject.String()
	p.Issuer = issuerName(leaf)
	p.Serial = strings.ToUpper(leaf.SerialNumber.Text(16))
	p.NotBefore = leaf.NotBefore.UnixMilli()
	p.NotAfter = leaf.NotAfter.UnixMilli()
	p.Fingerprint = Fingerprint(leaf.Raw)
	p.KeyType = keyType(leaf)
	p.SelfSigned = selfSigned(leaf)
	if p.SelfSigned {
		p.Warnings = append(p.Warnings, "自签名证书，浏览器不会信任")
	} else if len(chain) == 0 {
		p.Warnings = append(p.Warnings, "没有包含中间证书，部分客户端可能无法验证（建议使用 fullchain）")
	}
	return p, nil
}

func issuerName(c *x509.Certificate) string {
	if c.Issuer.CommonName != "" {
		if len(c.Issuer.Organization) > 0 {
			return c.Issuer.Organization[0] + " · " + c.Issuer.CommonName
		}
		return c.Issuer.CommonName
	}
	return c.Issuer.String()
}

// PrimaryDomain 返回用于 SNI 验证的域名（通配符替换为 www）。
func PrimaryDomain(domains []string) string {
	for _, d := range domains {
		if !strings.HasPrefix(d, "*.") {
			return d
		}
	}
	if len(domains) > 0 {
		return "www." + strings.TrimPrefix(domains[0], "*.")
	}
	return ""
}

// SafeName 把域名转换为适合作为文件名的形式。
func SafeName(domain string) string {
	domain = strings.ReplaceAll(domain, "*.", "wildcard.")
	var b strings.Builder
	for _, r := range domain {
		if r == '.' || r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "cert"
	}
	return b.String()
}
