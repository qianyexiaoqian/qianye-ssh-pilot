package main

import (
	"errors"
	"slices"
	"strings"

	"sshpilot/internal/store"
)

// ---- 快捷命令 ----

func (a *App) ListQuickCommands() []store.QuickCommand {
	out := []store.QuickCommand{}
	a.store.Read(func(d *store.Data) {
		for _, q := range d.QuickCommands {
			out = append(out, *q)
		}
	})
	return out
}

// SaveQuickCommand 新建或更新一条快捷命令；名称留空时取命令的第一行。
func (a *App) SaveQuickCommand(in store.QuickCommand) (store.QuickCommand, error) {
	in.Command = strings.TrimSpace(strings.ReplaceAll(in.Command, "\r\n", "\n"))
	in.Name = strings.TrimSpace(in.Name)
	in.Group = strings.TrimSpace(in.Group)
	in.Description = strings.TrimSpace(in.Description)
	if in.Command == "" {
		return store.QuickCommand{}, errors.New("请填写命令内容")
	}
	if len(in.Command) > 8000 {
		return store.QuickCommand{}, errors.New("命令过长，较长的脚本请保存到脚本库")
	}
	if in.Name == "" {
		in.Name = clipText(strings.SplitN(in.Command, "\n", 2)[0], 24)
	}
	var saved store.QuickCommand
	err := a.store.Update(func(d *store.Data) error {
		now := store.Now()
		q := d.QuickCommand(in.ID)
		if q == nil {
			if in.ID != "" {
				return errors.New("快捷命令不存在或已删除")
			}
			q = &store.QuickCommand{ID: store.NewID(), CreatedAt: now}
			d.QuickCommands = append(d.QuickCommands, q)
		}
		q.Name, q.Command, q.Group, q.Description = in.Name, in.Command, in.Group, in.Description
		q.AutoEnter, q.Confirm, q.UpdatedAt = in.AutoEnter, in.Confirm, now
		saved = *q
		return nil
	})
	return saved, err
}

func (a *App) DeleteQuickCommand(id string) error {
	return a.store.Update(func(d *store.Data) error {
		if d.QuickCommand(id) == nil {
			return errors.New("快捷命令不存在或已删除")
		}
		d.QuickCommands = slices.DeleteFunc(d.QuickCommands, func(q *store.QuickCommand) bool { return q.ID == id })
		return nil
	})
}

// ReorderQuickCommands 按给定的 ID 顺序重新排列；未出现在列表中的命令保持原有相对顺序排在后面。
func (a *App) ReorderQuickCommands(ids []string) error {
	return a.store.Update(func(d *store.Data) error {
		rank := map[string]int{}
		for i, id := range ids {
			if _, dup := rank[id]; !dup {
				rank[id] = i
			}
		}
		slices.SortStableFunc(d.QuickCommands, func(x, y *store.QuickCommand) int {
			rx, okx := rank[x.ID]
			ry, oky := rank[y.ID]
			switch {
			case okx && oky:
				return rx - ry
			case okx:
				return -1
			case oky:
				return 1
			}
			return 0
		})
		return nil
	})
}
