package agent

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

// 命令风险分级：safe（只读，可自动执行）、change（有修改，需要批准）、danger（高危，总是需要批准）。
const (
	RiskSafe   = "safe"
	RiskChange = "change"
	RiskDanger = "danger"
)

var dangerPatterns = []struct {
	re     *regexp.Regexp
	reason string
}{
	{regexp.MustCompile(`\brm\s+(-\S+\s+)*(/|/\*|~|~/|\$HOME|/(etc|usr|var|boot|bin|lib|root|home))(\s|$|/\*?(\s|$))`), "删除系统关键目录"},
	{regexp.MustCompile(`\bmkfs(\.\w+)?\b`), "格式化文件系统"},
	{regexp.MustCompile(`\bdd\b[^|;&]*\bof=/dev/`), "直接写入块设备"},
	{regexp.MustCompile(`>\s*/dev/(sd|nvme|vd|xvd|hd)`), "直接写入块设备"},
	{regexp.MustCompile(`:\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}`), "fork 炸弹"},
	{regexp.MustCompile(`\b(shutdown|poweroff|halt|reboot)\b`), "关机或重启服务器"},
	{regexp.MustCompile(`\binit\s+[06]\b`), "关机或重启服务器"},
	{regexp.MustCompile(`\bchmod\s+(-R\s+)?0?777\s+/(\s|$)`), "修改根目录权限"},
	{regexp.MustCompile(`\bchown\s+-R\s+\S+\s+/(\s|$)`), "修改根目录属主"},
	{regexp.MustCompile(`\b(iptables|ip6tables)\s+(-F|--flush)\b`), "清空防火墙规则"},
	{regexp.MustCompile(`\bufw\s+(disable|reset)\b`), "关闭或重置防火墙"},
	{regexp.MustCompile(`\bcrontab\s+-r\b`), "删除全部定时任务"},
	{regexp.MustCompile(`(?i)\bdrop\s+(database|schema|table)\b`), "删除数据库对象"},
	{regexp.MustCompile(`(?i)\bflushall\b|\bflushdb\b`), "清空 Redis 数据"},
	{regexp.MustCompile(`\buserdel\b|\bpasswd\b|\bchpasswd\b`), "修改系统账户"},
	{regexp.MustCompile(`\bsystemctl\s+(stop|disable|mask)\s+(ssh|sshd|networking|NetworkManager|systemd-networkd)(\.service)?\b`), "停止 SSH 或网络服务，可能导致失联"},
	{regexp.MustCompile(`(^|[^>])>\s*(~|/root|/home/\w+)?/?\.ssh/authorized_keys`), "覆盖 authorized_keys，可能导致无法登录"},
	{regexp.MustCompile(`\brm\s+.*\.ssh/authorized_keys`), "删除 authorized_keys，可能导致无法登录"},
	{regexp.MustCompile(`\bip\s+link\s+set\s+\S+\s+down\b|\bifdown\b`), "关闭网卡，可能导致失联"},
	{regexp.MustCompile(`\bwipefs\b|\bfdisk\b|\bparted\b|\bsfdisk\b`), "修改磁盘分区"},
	{regexp.MustCompile(`\b(curl|wget)\b[^|]*\|\s*(sudo\s+)?(ba|z|da)?sh\b`), "下载并直接执行远程脚本"},
}

var sensitiveFileRe = regexp.MustCompile(`(?i)(/etc/shadow|/etc/gshadow|id_rsa|id_ed25519|id_ecdsa|id_dsa|privkey|private[_-]?key|\.key\b|/etc/ssl/private|\.p12\b|\.pfx\b|\.env\b|credentials)`)

// splitShell 按 ; && || | & 换行拆分命令，忽略引号内的分隔符。
func splitShell(cmd string) []string {
	var parts []string
	var cur strings.Builder
	var quote rune
	escaped := false
	rs := []rune(cmd)
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			parts = append(parts, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if escaped {
			cur.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			cur.WriteRune(r)
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			cur.WriteRune(r)
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
			cur.WriteRune(r)
		case ';', '\n', '|', '&':
			// 跳过 2>&1、&> 这类重定向中的 &
			if r == '&' && i > 0 && (rs[i-1] == '>' || (i+1 < len(rs) && rs[i+1] == '>')) {
				cur.WriteRune(r)
				continue
			}
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return parts
}

// fields 按空白拆分并去掉引号。
func fields(seg string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	has := false
	for _, r := range seg {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			has = true
		case r == ' ' || r == '\t':
			if cur.Len() > 0 || has {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 || has {
		out = append(out, cur.String())
	}
	return out
}

var redirectRe = regexp.MustCompile(`(^|[^0-9&<>])(\d?)>{1,2}\s*([^\s&|;]+)`)

// hasWriteRedirect 判断引号外是否存在写文件的重定向（/dev/null 除外）。
func hasWriteRedirect(seg string) bool {
	var outside strings.Builder
	var quote rune
	for _, r := range seg {
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			outside.WriteRune(' ')
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			outside.WriteRune(' ')
			continue
		}
		outside.WriteRune(r)
	}
	for _, m := range redirectRe.FindAllStringSubmatch(outside.String(), -1) {
		target := m[3]
		if target == "/dev/null" || strings.HasPrefix(target, "&") {
			continue
		}
		return true
	}
	return false
}

func hasAny(args []string, opts ...string) bool {
	for _, a := range args {
		for _, o := range opts {
			if a == o || (strings.HasPrefix(o, "--") && strings.HasPrefix(a, o+"=")) {
				return true
			}
		}
	}
	return false
}

func firstArg(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

var safeAlways = []string{
	"cat", "head", "tail", "grep", "egrep", "fgrep", "zgrep", "zcat", "ls", "ll", "stat", "file", "wc", "sort",
	"uniq", "cut", "tr", "column", "df", "du", "free", "uptime", "w", "who", "whoami", "id", "hostname", "uname",
	"date", "ps", "pgrep", "pidof", "lsof", "ss", "netstat", "ifconfig", "dig", "nslookup", "host", "env", "printenv",
	"which", "whereis", "type", "echo", "printf", "true", "false", "test", "[", "vmstat", "iostat", "mpstat", "sar",
	"lsblk", "blkid", "findmnt", "getenforce", "sestatus", "lscpu", "lsmem", "lsmod", "lspci", "lsusb", "md5sum",
	"sha1sum", "sha256sum", "sha512sum", "base64", "xxd", "od", "strings", "tree", "realpath", "readlink",
	"basename", "dirname", "last", "lastlog", "getent", "nproc", "arch", "jq", "cd", "sleep", "ping", "traceroute",
	"tracepath", "nc", "telnet", "diff", "cmp", "seq", "expr", "nl", "rev", "tac", "fold",
}

var subcommandSafe = map[string][]string{
	"systemctl":      {"status", "is-active", "is-enabled", "is-failed", "list-units", "list-unit-files", "list-timers", "list-sockets", "list-dependencies", "show", "cat", "--failed", "get-default"},
	"service":        {"status", "--status-all"},
	"docker":         {"ps", "images", "logs", "inspect", "version", "info", "top", "port", "stats", "network", "volume", "system", "compose", "image", "container"},
	"docker-compose": {"ps", "logs", "config", "images", "top", "version"},
	"podman":         {"ps", "images", "logs", "inspect", "version", "info", "top", "port"},
	"kubectl":        {"get", "describe", "logs", "top", "version", "explain", "api-resources", "cluster-info", "config"},
	"crictl":         {"ps", "images", "logs", "inspect", "info", "version", "pods", "stats"},
	"openssl":        {"x509", "s_client", "version", "verify", "dgst", "req", "crl", "ciphers", "rsa", "ec", "pkey", "asn1parse"},
	"apt":            {"list", "show", "policy", "search"},
	"apt-cache":      {"policy", "show", "search", "depends", "rdepends", "madison"},
	"yum":            {"list", "info", "search", "check-update", "history", "repolist", "provides"},
	"dnf":            {"list", "info", "search", "check-update", "history", "repolist", "provides"},
	"git":            {"status", "log", "diff", "show", "branch", "remote", "rev-parse", "describe", "tag"},
	"ip":             {"a", "addr", "address", "r", "route", "link", "neigh", "-br", "-brief", "-s", "-4", "-6", "rule", "maddr"},
	"nft":            {"list"},
	"ufw":            {"status"},
	"chronyc":        {"tracking", "sources", "sourcestats", "activity"},
	"timedatectl":    {"status", "show", "list-timezones"},
	"hostnamectl":    {"status"},
	"zpool":          {"list", "status", "iostat"},
	"zfs":            {"list", "get"},
	"pm2":            {"list", "ls", "status", "logs", "show", "describe"},
	"supervisorctl":  {"status", "avail", "tail"},
}

func safeSegment(words []string) bool {
	// 去掉前缀：环境变量赋值、sudo、time 等
prefix:
	for len(words) > 0 {
		w := words[0]
		switch {
		case strings.Contains(w, "=") && !strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "="):
			words = words[1:]
		case w == "sudo" || w == "time" || w == "command" || w == "exec" || w == "nohup" || w == "nice" || w == "ionice" || w == "timeout":
			words = words[1:]
			for len(words) > 0 && strings.HasPrefix(words[0], "-") {
				words = words[1:]
			}
			// timeout 的时长参数、nice -n 的数值
			if len(words) > 0 && (w == "timeout" || w == "nice" || w == "ionice") && strings.TrimLeft(words[0], "0123456789.smhd") == "" {
				words = words[1:]
			}
		default:
			break prefix
		}
	}
	if len(words) == 0 {
		return true
	}
	name := path.Base(words[0])
	args := words[1:]
	switch name {
	case "sed":
		return !hasAny(args, "-i", "--in-place") && !slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, "-i") && !strings.HasPrefix(a, "--") })
	case "find":
		return !hasAny(args, "-delete", "-exec", "-execdir", "-ok", "-okdir", "-fprint", "-fprintf", "-fls")
	case "awk", "gawk", "mawk":
		prog := strings.Join(args, " ")
		return !strings.Contains(prog, "system(") && !strings.Contains(prog, "print >") && !strings.Contains(prog, "printf >") && !strings.Contains(prog, "| \"")
	case "curl":
		return !hasAny(args, "-o", "-O", "--output", "-T", "--upload-file", "-d", "--data", "--data-raw", "--data-binary", "-F", "--form", "-X", "--request")
	case "wget":
		return hasAny(args, "-O-", "--spider") || (hasAny(args, "-O", "-qO") && slices.Contains(args, "-"))
	case "journalctl":
		return !slices.ContainsFunc(args, func(a string) bool {
			return strings.HasPrefix(a, "--vacuum") || a == "--rotate" || a == "--flush" || a == "--sync"
		})
	case "nginx":
		return hasAny(args, "-t", "-T", "-v", "-V")
	case "apachectl", "apache2ctl", "httpd", "apache2":
		return hasAny(args, "-t", "-S", "-M", "-v", "-V", "configtest", "-l")
	case "haproxy":
		return hasAny(args, "-c", "-v", "-vv")
	case "dmesg":
		return !hasAny(args, "-c", "-C", "-D", "--clear", "--read-clear")
	case "sysctl":
		return !hasAny(args, "-w", "--write", "-p", "--load") && !slices.ContainsFunc(args, func(a string) bool { return strings.Contains(a, "=") })
	case "iptables", "ip6tables":
		return hasAny(args, "-L", "-S", "--list", "-nL", "-nvL", "-vnL", "--list-rules") &&
			!hasAny(args, "-A", "-D", "-I", "-F", "-X", "-P", "-N", "-R", "-Z", "--append", "--delete", "--insert", "--flush")
	case "firewall-cmd":
		return len(args) > 0 && !slices.ContainsFunc(args, func(a string) bool {
			return !(strings.HasPrefix(a, "--list") || strings.HasPrefix(a, "--get") || strings.HasPrefix(a, "--query") || a == "--state" || strings.HasPrefix(a, "--zone"))
		})
	case "rpm":
		return slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, "-q") }) || hasAny(args, "--query", "--verify", "-V")
	case "dpkg":
		return hasAny(args, "-l", "-L", "-s", "-S", "--list", "--status", "--listfiles", "--search", "--get-selections", "--print-architecture")
	case "crontab":
		return hasAny(args, "-l")
	case "mount":
		return len(args) == 0 || hasAny(args, "-l")
	case "xargs":
		for i, a := range args {
			if !strings.HasPrefix(a, "-") {
				return safeSegment(args[i:])
			}
		}
		return true
	case "timedatectl", "hostnamectl":
		if len(args) == 0 {
			return true
		}
	case "ntpq":
		return hasAny(args, "-p", "-pn")
	case "tee":
		return len(args) == 0 || slices.Equal(args, []string{"/dev/null"})
	}
	if slices.Contains(safeAlways, name) {
		return true
	}
	if subs, ok := subcommandSafe[name]; ok {
		sub := firstArg(args)
		if name == "ip" {
			if len(args) == 0 {
				return false
			}
			for _, a := range args {
				if slices.Contains([]string{"add", "del", "delete", "set", "flush", "change", "replace", "append"}, a) {
					return false
				}
			}
			return slices.Contains(subs, args[0]) || slices.Contains(subs, sub)
		}
		if name == "docker" && slices.Contains([]string{"network", "volume", "system", "compose", "image", "container"}, sub) {
			return hasAny(args, "ls", "list", "inspect", "df", "ps", "logs", "info", "version", "config", "history")
		}
		if name == "docker" && sub == "stats" {
			return hasAny(args, "--no-stream")
		}
		return slices.Contains(subs, sub)
	}
	return false
}

// ClassifyCommand 返回命令的风险等级与原因。
func ClassifyCommand(cmd string) (string, string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return RiskSafe, ""
	}
	for _, p := range dangerPatterns {
		if p.re.MatchString(cmd) {
			return RiskDanger, p.reason
		}
	}
	if strings.Contains(cmd, "$(") || strings.Contains(cmd, "`") || strings.Contains(cmd, "<(") {
		return RiskChange, "包含命令替换"
	}
	if sensitiveFileRe.MatchString(cmd) {
		return RiskChange, "涉及私钥、密码等敏感文件，输出会发送给 AI 模型"
	}
	for _, seg := range splitShell(cmd) {
		if hasWriteRedirect(seg) {
			return RiskChange, "写入文件"
		}
		words := fields(seg)
		if len(words) > 0 && (words[0] == "sh" || words[0] == "bash" || words[0] == "python" || words[0] == "python3" || words[0] == "perl") {
			return RiskChange, "执行脚本"
		}
		if !safeSegment(words) {
			return RiskChange, "可能修改系统状态"
		}
	}
	return RiskSafe, "只读命令"
}
