package store

// 默认快捷命令：只在首次启动时写入一次，之后完全由用户维护（可修改、删除）。
var defaultQuickCommands = []QuickCommand{
	{Group: "系统", Name: "系统负载", Command: "uptime", AutoEnter: true, Description: "运行时长与 1/5/15 分钟平均负载"},
	{Group: "系统", Name: "内存使用", Command: "free -h", AutoEnter: true},
	{Group: "系统", Name: "磁盘空间", Command: "df -h", AutoEnter: true},
	{Group: "系统", Name: "占用最高的进程", Command: "ps aux --sort=-%cpu | head -n 15", AutoEnter: true},
	{Group: "系统", Name: "最近登录记录", Command: "last -n 10", AutoEnter: true},
	{Group: "网络", Name: "监听端口", Command: "ss -tlnp", AutoEnter: true, Description: "查看哪些进程在监听端口"},
	{Group: "网络", Name: "网卡与地址", Command: "ip -brief addr", AutoEnter: true},
	{Group: "服务", Name: "查看服务状态", Command: "systemctl status {{服务名:nginx}} --no-pager", AutoEnter: true},
	{Group: "服务", Name: "重启服务", Command: "systemctl restart {{服务名:nginx}}", AutoEnter: true, Confirm: true, Description: "重启会短暂中断服务，执行前会再次确认"},
	{Group: "服务", Name: "失败的服务", Command: "systemctl --failed --no-pager", AutoEnter: true},
	{Group: "服务", Name: "检测并重载 Nginx", Command: "nginx -t && systemctl reload nginx", AutoEnter: true, Description: "配置检测通过后才会重载"},
	{Group: "日志", Name: "服务日志", Command: "journalctl -u {{服务名:nginx}} -n {{行数:100}} --no-pager", AutoEnter: true},
	{Group: "日志", Name: "实时跟踪日志", Command: "tail -f {{日志文件:/var/log/nginx/error.log}}", AutoEnter: true, Description: "按 Ctrl+C 结束跟踪"},
	{Group: "Docker", Name: "容器列表", Command: "docker ps", AutoEnter: true},
	{Group: "Docker", Name: "容器日志", Command: "docker logs --tail 100 -f {{容器名}}", AutoEnter: true},
}

func ensureQuickCommands(d *Data) {
	if d.QuickCommands == nil {
		d.QuickCommands = []*QuickCommand{}
	}
	if d.QuickSeeded {
		return
	}
	d.QuickSeeded = true
	now := Now()
	for _, q := range defaultQuickCommands {
		q.ID, q.CreatedAt, q.UpdatedAt = NewID(), now, now
		d.QuickCommands = append(d.QuickCommands, &q)
	}
}
