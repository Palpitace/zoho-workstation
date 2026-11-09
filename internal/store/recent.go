// input: 已完整验证的最新工单 ID 集合。
// output: 原子替换活动范围，保留范围外快照和 AI 历史。
// pos: 滚动测试范围持久化。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package store

import (
	"context"
	"errors"
	"time"
)

func (s *Store) EnableRecentScope() { s.recentOnly.Store(true) }

// ReplaceRecent preserves the last successful selection if validation or persistence fails.
func (s *Store) ReplaceRecent(ctx context.Context, ids []string) error {
	if len(ids) > 50 {
		return errors.New("recent scope exceeds 50 tickets")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			return errors.New("invalid recent ticket selection")
		}
		seen[id] = true
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM recent_tickets"); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO tickets(id,selected_at) VALUES(?,?)", id, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO recent_tickets(id) VALUES(?)", id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
