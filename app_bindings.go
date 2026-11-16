//go:build bindings

package main

// wails 生成前端绑定时会用 bindings 标签编译并运行一次程序：这时不要创建数据目录，更不要迁移数据。
const isBindingsBuild = true
