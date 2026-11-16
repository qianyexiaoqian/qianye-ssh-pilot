package store

// 内置脚本：每次启动时按 ID 刷新内容，用户可以复制后修改。
var builtinScripts = []Script{
	{
		ID: "builtin-sysinfo", Name: "系统概览", Category: "系统", ReadOnly: true,
		Description: "主机、内核、负载、内存、磁盘与网络地址一览",
		Content: `echo "== 主机 =="
hostname
(. /etc/os-release 2>/dev/null && echo "系统: $PRETTY_NAME")
echo "内核: $(uname -r)  架构: $(uname -m)"
echo "运行: $(uptime -p 2>/dev/null || uptime)"
echo
echo "== CPU / 负载 =="
echo "核心数: $(nproc 2>/dev/null)  负载: $(cut -d' ' -f1-3 /proc/loadavg)"
echo
echo "== 内存 =="
free -h
echo
echo "== 磁盘 =="
df -hT -x tmpfs -x devtmpfs -x overlay -x squashfs 2>/dev/null || df -h
echo
echo "== 网络地址 =="
ip -brief addr 2>/dev/null || hostname -I`,
	},
	{
		ID: "builtin-top", Name: "资源占用 Top 进程", Category: "系统", ReadOnly: true,
		Description: "按 CPU 和内存排序列出占用最高的进程",
		Content: `echo "== CPU Top 10 =="
ps -eo pid,user,%cpu,%mem,etime,comm --sort=-%cpu | head -n 11
echo
echo "== 内存 Top 10 =="
ps -eo pid,user,%mem,rss,etime,comm --sort=-%mem | head -n 11`,
	},
	{
		ID: "builtin-disk-usage", Name: "磁盘占用分析", Category: "磁盘", ReadOnly: true,
		Description: "找出指定目录下占用最大的子目录和大文件",
		Params: []ScriptParam{
			{Name: "DIR", Label: "分析目录", Default: "/", Required: true},
			{Name: "DEPTH", Label: "目录深度", Default: "2"},
		},
		Content: `TARGET="${DIR:-/}"
echo "== $TARGET 下占用最大的目录 =="
du -xh --max-depth="${DEPTH:-2}" "$TARGET" 2>/dev/null | sort -rh | head -n 25
echo
echo "== 大于 100M 的文件 =="
find "$TARGET" -xdev -type f -size +100M -printf '%s\t%p\n' 2>/dev/null | sort -rn | head -n 15 \
  | awk -F'\t' '{printf "%9.1f MB  %s\n", $1/1048576, $2}'`,
	},
	{
		ID: "builtin-cleanup", Name: "清理日志与缓存", Category: "磁盘",
		Description: "压缩 journal、清理包管理器缓存、删除过期的轮转日志",
		Params: []ScriptParam{{Name: "KEEP_DAYS", Label: "保留天数", Default: "7", Required: true}},
		Content: `echo "清理前: $(df -h / | tail -1)"
if command -v journalctl >/dev/null 2>&1; then
  journalctl --vacuum-time="${KEEP_DAYS:-7}d" 2>&1 | tail -n 1
fi
if command -v apt-get >/dev/null 2>&1; then apt-get clean && echo "已清理 apt 缓存"; fi
if command -v dnf >/dev/null 2>&1; then dnf clean all -q && echo "已清理 dnf 缓存"
elif command -v yum >/dev/null 2>&1; then yum clean all -q && echo "已清理 yum 缓存"; fi
n=$(find /var/log -type f \( -name "*.gz" -o -name "*.[0-9]" -o -name "*.old" \) -mtime +"${KEEP_DAYS:-7}" -print -delete 2>/dev/null | wc -l)
echo "删除过期日志文件: $n 个"
echo "清理后: $(df -h / | tail -1)"`,
	},
	{
		ID: "builtin-cert-scan", Name: "扫描证书到期时间", Category: "证书", ReadOnly: true,
		Description: "在常见目录中查找证书文件并按剩余天数排序（需要 openssl）",
		Params: []ScriptParam{{Name: "DIRS", Label: "扫描目录（空格分隔）", Default: "/etc/nginx /etc/letsencrypt/live /etc/apache2 /etc/httpd /etc/haproxy /usr/local/nginx/conf"}},
		Content: `command -v openssl >/dev/null 2>&1 || { echo "未安装 openssl"; exit 1; }
now=$(date +%s)
for d in $DIRS; do
  [ -d "$d" ] || continue
  find -L "$d" -type f \( -name "*.pem" -o -name "*.crt" -o -name "*.cer" \) 2>/dev/null
done | sort -u | while read -r f; do
  end=$(openssl x509 -enddate -noout -in "$f" 2>/dev/null | cut -d= -f2)
  [ -n "$end" ] || continue
  exp=$(date -d "$end" +%s 2>/dev/null) || continue
  cn=$(openssl x509 -subject -noout -in "$f" 2>/dev/null | sed -n 's/.*CN *= *\([^,/]*\).*/\1/p')
  printf "%6s 天  %-32s %s\n" "$(( (exp - now) / 86400 ))" "$cn" "$f"
done | sort -n`,
	},
	{
		ID: "builtin-nginx-reload", Name: "Nginx 检测并重载", Category: "服务",
		Description: "先执行 nginx -t 校验配置，通过后平滑重载",
		Content: `nginx -t || exit 1
if command -v systemctl >/dev/null 2>&1; then systemctl reload nginx; else nginx -s reload; fi && echo "Nginx 已重载"`,
	},
	{
		ID: "builtin-failed-services", Name: "失败服务排查", Category: "服务", ReadOnly: true,
		Description: "列出 systemd 中失败的服务并输出最近日志",
		Content: `systemctl --failed --no-pager
for u in $(systemctl --failed --no-legend --plain 2>/dev/null | awk '{print $1}'); do
  echo
  echo "== $u 最近日志 =="
  journalctl -u "$u" -n 20 --no-pager 2>/dev/null
done`,
	},
	{
		ID: "builtin-ports", Name: "监听端口", Category: "网络", ReadOnly: true,
		Description: "列出正在监听的 TCP/UDP 端口及对应进程",
		Content:     `if command -v ss >/dev/null 2>&1; then ss -tulnp; else netstat -tulnp; fi`,
	},
	{
		ID: "builtin-ssh-audit", Name: "SSH 安全检查", Category: "安全", ReadOnly: true,
		Description: "检查 sshd 关键配置、24 小时内的暴力破解来源和当前登录",
		Content: `echo "== sshd 关键配置 =="
sshd -T 2>/dev/null | grep -Ei '^(port|permitrootlogin|passwordauthentication|pubkeyauthentication|maxauthtries) ' \
  || grep -Ei '^\s*(Port|PermitRootLogin|PasswordAuthentication|PubkeyAuthentication)' /etc/ssh/sshd_config
echo
echo "== 24 小时内登录失败最多的 IP =="
{ journalctl -u ssh -u sshd --since "24 hours ago" --no-pager 2>/dev/null; cat /var/log/auth.log /var/log/secure 2>/dev/null; } \
  | grep -Ei "failed password|invalid user" | grep -oE "from [0-9a-f.:]+" | sort | uniq -c | sort -rn | head
echo
echo "== 当前登录 =="
who`,
	},
	{
		ID: "builtin-update", Name: "更新系统软件包", Category: "安全",
		Description: "使用 apt / dnf / yum 升级全部软件包",
		Content: `if command -v apt-get >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -q && apt-get upgrade -y -q
elif command -v dnf >/dev/null 2>&1; then dnf upgrade -y -q
elif command -v yum >/dev/null 2>&1; then yum update -y -q
else echo "未识别的包管理器"; exit 1; fi
[ -f /var/run/reboot-required ] && echo "需要重启以完成更新"
true`,
	},
	{
		ID: "builtin-docker", Name: "Docker 状态", Category: "容器", ReadOnly: true,
		Description: "容器列表与磁盘占用",
		Content: `command -v docker >/dev/null 2>&1 || { echo "未安装 Docker"; exit 0; }
docker ps -a --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}'
echo
docker system df`,
	},
	{
		ID: "builtin-swap", Name: "创建 Swap", Category: "系统",
		Description: "没有 swap 时创建 /swapfile 并写入 fstab",
		Params:      []ScriptParam{{Name: "SIZE", Label: "大小", Default: "2G", Required: true}},
		Content: `if swapon --show 2>/dev/null | grep -q .; then echo "已存在 swap:"; swapon --show; exit 0; fi
fallocate -l "${SIZE:-2G}" /swapfile || exit 1
chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile || exit 1
grep -q '^/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
swapon --show
free -h`,
	},
	{
		ID: "builtin-time", Name: "时间同步检查", Category: "系统", ReadOnly: true,
		Description: "检查时区、NTP 同步状态",
		Content: `timedatectl 2>/dev/null || date
command -v chronyc >/dev/null 2>&1 && chronyc tracking 2>/dev/null | head -n 6
true`,
	},
}

func ensureBuiltinScripts(d *Data) {
	now := Now()
	for _, b := range builtinScripts {
		b.Builtin = true
		b.CreatedBy = "builtin"
		if b.Interpreter == "" {
			b.Interpreter = "bash"
		}
		if b.Params == nil {
			b.Params = []ScriptParam{}
		}
		if b.Tags == nil {
			b.Tags = []string{}
		}
		if existing := d.Script(b.ID); existing != nil {
			b.CreatedAt = existing.CreatedAt
			b.UpdatedAt = existing.UpdatedAt
			*existing = b
			continue
		}
		b.CreatedAt, b.UpdatedAt = now, now
		cp := b
		d.Scripts = append(d.Scripts, &cp)
	}
}
