package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// 技能库的整理：AI 自己沉淀的技能如果长期没有被用到，会让技能索引越来越长、每次对话都白白占用提示词。
// 这里按“最近一次使用或修改”的时间给技能分状态：超过 skillStaleAfter 标记为久未使用（只是提示），
// 超过 skillArchiveAfter 的 AI 沉淀技能自动归档（移到 .archive 目录，随时可以恢复）。
// 内置技能、用户手写的技能和被固定的技能不会被自动归档。
const (
	skillStaleAfter   = 30 * 24 * time.Hour
	skillArchiveAfter = 90 * 24 * time.Hour
)

func (s *SkillStore) archivePath(name string) string { return filepath.Join(s.dir, ".archive", name) }

// lastActivity 是技能最近一次被读取或被修改的时间。
func (s *SkillStore) lastActivity(name string, st skillStats) time.Time {
	t := time.UnixMilli(st.LastUsed)
	if fi, err := os.Stat(s.path(name)); err == nil && fi.ModTime().After(t) {
		t = fi.ModTime()
	}
	return t
}

// SetPinned 固定或取消固定一个技能：固定后不会被自动归档，AI 也不能删除它（仍然可以改进它）。
func (s *SkillStore) SetPinned(name string, pinned bool) error {
	if !skillNameRe.MatchString(name) {
		return fmt.Errorf("技能名无效: %s", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.path(name)); err != nil {
		return fmt.Errorf("技能 %s 不存在", name)
	}
	stats := s.loadStats()
	st := stats[name]
	st.Pinned = pinned
	stats[name] = st
	s.saveStats(stats)
	return nil
}

func (s *SkillStore) IsPinned(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadStats()[name].Pinned
}

// Archive 把技能移出技能库（不再出现在索引里），保留在归档目录中。
func (s *SkillStore) Archive(name string) error {
	if !skillNameRe.MatchString(name) {
		return fmt.Errorf("技能名无效: %s", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.archiveLocked(name)
}

func (s *SkillStore) archiveLocked(name string) error {
	if _, err := os.Stat(s.path(name)); err != nil {
		return fmt.Errorf("技能 %s 不存在", name)
	}
	dst := s.archivePath(name)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	_ = os.RemoveAll(dst)
	if err := os.Rename(filepath.Join(s.dir, name), dst); err != nil {
		return err
	}
	now := time.Now()
	_ = os.Chtimes(dst, now, now) // 目录的修改时间就是归档时间
	return nil
}

// Restore 把归档的技能放回技能库。
func (s *SkillStore) Restore(name string) error {
	if !skillNameRe.MatchString(name) {
		return fmt.Errorf("技能名无效: %s", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.archivePath(name)
	if _, err := os.Stat(filepath.Join(src, "SKILL.md")); err != nil {
		return fmt.Errorf("归档里没有技能 %s", name)
	}
	if _, err := os.Stat(s.path(name)); err == nil {
		return fmt.Errorf("技能库里已经有同名的技能 %s", name)
	}
	if err := os.Rename(src, filepath.Join(s.dir, name)); err != nil {
		return err
	}
	// 恢复后重新计时，避免马上又被归档
	now := time.Now()
	_ = os.Chtimes(s.path(name), now, now)
	return nil
}

// DeleteArchived 彻底删除一个已归档的技能。
func (s *SkillStore) DeleteArchived(name string) error {
	if !skillNameRe.MatchString(name) {
		return fmt.Errorf("技能名无效: %s", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.archivePath(name)); err != nil {
		return errors.New("归档里没有这个技能")
	}
	return os.RemoveAll(s.archivePath(name))
}

// ListArchived 返回已归档的技能，最近归档的在前。
func (s *SkillStore) ListArchived() []SkillMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, _ := os.ReadDir(filepath.Join(s.dir, ".archive"))
	stats := s.loadStats()
	out := []SkillMeta{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.archivePath(e.Name()), "SKILL.md"))
		if err != nil {
			continue
		}
		meta, _, _ := splitFrontmatter(string(b))
		meta.Name = e.Name()
		meta.Uses = stats[e.Name()].Uses
		meta.LastUsed = stats[e.Name()].LastUsed
		if fi, err := e.Info(); err == nil {
			meta.ArchivedAt = fi.ModTime().UnixMilli()
		}
		out = append(out, meta)
	}
	slices.SortFunc(out, func(a, b SkillMeta) int { return int(b.ArchivedAt/1000 - a.ArchivedAt/1000) })
	return out
}

// Curate 归档长期没有用到的 AI 沉淀技能，返回被归档的技能名。
func (s *SkillStore) Curate(now time.Time) []string {
	list := s.List()
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := s.loadStats()
	var archived []string
	for _, m := range list {
		if m.Pinned || !(m.CreatedBy == "agent" || m.CreatedBy == "review") {
			continue
		}
		if now.Sub(s.lastActivity(m.Name, stats[m.Name])) < skillArchiveAfter {
			continue
		}
		if s.archiveLocked(m.Name) == nil {
			archived = append(archived, m.Name)
		}
	}
	return archived
}

// CurateSkills 整理技能库并把结果记入学习日志。
func (a *Agent) CurateSkills() []string {
	archived := a.Skills.Curate(time.Now())
	if len(archived) > 0 {
		a.RecordLearning("", LearnEvent{Source: "curator", Kind: "skill", Action: "archive", Target: strings.Join(archived, "、"),
			Summary: fmt.Sprintf("超过 %d 天没有用到，已自动归档（可以在「记忆与技能」里恢复）", int(skillArchiveAfter.Hours()/24))})
	}
	return archived
}
