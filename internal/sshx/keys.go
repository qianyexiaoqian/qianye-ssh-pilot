package sshx

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

type KeyInfo struct {
	Type        string `json:"type"` // 展示用：ED25519 / RSA 4096 / ECDSA P-256
	Algorithm   string `json:"algorithm"`
	PublicKey   string `json:"publicKey"` // authorized_keys 行（含注释）
	Fingerprint string `json:"fingerprint"`
	Comment     string `json:"comment"`
	Encrypted   bool   `json:"encrypted"`
}

func describeKey(pk ssh.PublicKey) string {
	switch pk.Type() {
	case ssh.KeyAlgoED25519:
		return "ED25519"
	case ssh.KeyAlgoRSA:
		if cpk, ok := pk.(ssh.CryptoPublicKey); ok {
			if r, ok := cpk.CryptoPublicKey().(*rsa.PublicKey); ok {
				return fmt.Sprintf("RSA %d", r.N.BitLen())
			}
		}
		return "RSA"
	case ssh.KeyAlgoECDSA256:
		return "ECDSA P-256"
	case ssh.KeyAlgoECDSA384:
		return "ECDSA P-384"
	case ssh.KeyAlgoECDSA521:
		return "ECDSA P-521"
	default:
		return strings.ToUpper(pk.Type())
	}
}

func authorizedLine(pk ssh.PublicKey, comment string) string {
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pk)))
	if comment = strings.TrimSpace(comment); comment != "" {
		line += " " + comment
	}
	return line
}

func infoFor(pk ssh.PublicKey, comment string, encrypted bool) KeyInfo {
	return KeyInfo{
		Type:        describeKey(pk),
		Algorithm:   pk.Type(),
		PublicKey:   authorizedLine(pk, comment),
		Fingerprint: ssh.FingerprintSHA256(pk),
		Comment:     comment,
		Encrypted:   encrypted,
	}
}

// GenerateKey 生成新的密钥对，私钥为 OpenSSH 格式。
func GenerateKey(kind string, bits int, comment, passphrase string) (string, KeyInfo, error) {
	var priv crypto.PrivateKey
	var err error
	switch kind {
	case "rsa":
		if bits != 2048 && bits != 3072 && bits != 4096 {
			bits = 4096
		}
		priv, err = rsa.GenerateKey(rand.Reader, bits)
	case "ecdsa":
		priv, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	default:
		_, priv, err = ed25519.GenerateKey(rand.Reader)
	}
	if err != nil {
		return "", KeyInfo{}, err
	}
	var block *pem.Block
	if passphrase != "" {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, comment, []byte(passphrase))
	} else {
		block, err = ssh.MarshalPrivateKey(priv, comment)
	}
	if err != nil {
		return "", KeyInfo{}, err
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return "", KeyInfo{}, err
	}
	return string(pem.EncodeToMemory(block)), infoFor(signer.PublicKey(), comment, passphrase != ""), nil
}

func normalizePEM(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.TrimSpace(s) + "\n"
}

// ParseSigner 解析私钥（支持 OpenSSH / PKCS#1 / PKCS#8 / EC，可带口令）。
func ParseSigner(pemStr, passphrase string) (ssh.Signer, error) {
	data := []byte(normalizePEM(pemStr))
	if strings.HasPrefix(strings.TrimSpace(pemStr), "PuTTY-User-Key-File") {
		return nil, errors.New("暂不支持 PuTTY .ppk 格式，请用 PuTTYgen 导出为 OpenSSH 格式")
	}
	var signer ssh.Signer
	var err error
	if passphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(data, []byte(passphrase))
		if err != nil && strings.Contains(err.Error(), "not encrypted") {
			signer, err = ssh.ParsePrivateKey(data)
		}
	} else {
		signer, err = ssh.ParsePrivateKey(data)
	}
	if err != nil {
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) {
			return nil, errors.New("私钥已加密，需要提供密码短语")
		}
		if strings.Contains(err.Error(), "decryption password incorrect") || strings.Contains(err.Error(), "bcrypt_pbkdf") {
			return nil, errors.New("私钥密码短语不正确")
		}
		return nil, fmt.Errorf("无法解析私钥: %w", err)
	}
	return signer, nil
}

// InspectPrivateKey 校验私钥并推导出公钥信息。
func InspectPrivateKey(pemStr, passphrase, comment string) (KeyInfo, error) {
	signer, err := ParseSigner(pemStr, passphrase)
	if err != nil {
		return KeyInfo{}, err
	}
	return infoFor(signer.PublicKey(), comment, isEncryptedKey(pemStr)), nil
}

// isEncryptedKey 判断私钥是否受口令保护（OpenSSH 与传统 PEM 两种格式）。
func isEncryptedKey(pemStr string) bool {
	_, err := ssh.ParseRawPrivateKey([]byte(normalizePEM(pemStr)))
	var missing *ssh.PassphraseMissingError
	return errors.As(err, &missing)
}

// ParsePublicKey 解析 authorized_keys 格式的公钥行。
func ParsePublicKey(line string) (KeyInfo, error) {
	line = strings.TrimSpace(line)
	pk, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return KeyInfo{}, fmt.Errorf("无法解析公钥: %w", err)
	}
	return infoFor(pk, comment, false), nil
}

// KeyBlob 返回公钥行中的 base64 主体，用于在 authorized_keys 中查找。
func KeyBlob(line string) string {
	pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(line)))
	if err != nil {
		return ""
	}
	full := string(ssh.MarshalAuthorizedKey(pk))
	parts := strings.Fields(full)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}
