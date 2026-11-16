//go:build !qa || !windows

package main

// startQA 只在验收构建（-tags qa）里有内容，正式发布的程序里什么都不做。
func (a *App) startQA() {}
