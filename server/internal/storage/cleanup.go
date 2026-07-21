// Package storage: cleanup_tasks 数据访问 + 清理执行。
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CleanupTask 是 cleanup_tasks 行的 Go 表示。
type CleanupTask struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	Strategy         string `json:"strategy"` // "lru" / "capacity"
	ThresholdSeconds int    `json:"thresholdSeconds"`
	ThresholdBytes   int64  `json:"thresholdBytes"`
	Enabled          bool   `json:"enabled"`
	CronIntervalSec  int    `json:"cronIntervalSec"`
	LastRunAt        int64  `json:"lastRunAt"`
	LastStatus       string `json:"lastStatus"`
	LastFreedBytes   int64  `json:"lastFreedBytes"`
	LastFreedCount   int    `json:"lastFreedCount"`
	CreatedAt        int64  `json:"createdAt"`
}

// ListCleanupTasks 列出所有任务。
func (d *DB) ListCleanupTasks(ctx context.Context) ([]CleanupTask, error) {
	rows, err := d.SQLDB.QueryContext(ctx, `
		SELECT id, task_name, strategy, threshold_seconds, threshold_bytes, enabled,
		       cron_interval_sec, last_run_at, last_status, last_freed_bytes,
		       last_freed_count, created_at
		FROM cleanup_tasks
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("storage: list cleanup tasks: %w", err)
	}
	defer rows.Close()
	var out []CleanupTask
	for rows.Next() {
		var t CleanupTask
		var enabled int
		if err := rows.Scan(&t.ID, &t.Name, &t.Strategy, &t.ThresholdSeconds, &t.ThresholdBytes,
			&enabled, &t.CronIntervalSec, &t.LastRunAt, &t.LastStatus, &t.LastFreedBytes,
			&t.LastFreedCount, &t.CreatedAt); err != nil {
			return nil, err
		}
		t.Enabled = enabled == 1
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateCleanupTaskLastRun 更新任务 last_run / last_status / last_freed_*。
func (d *DB) UpdateCleanupTaskLastRun(ctx context.Context, id int64, status string, freedBytes int64, freedCount int) error {
	now := time.Now().Unix()
	_, err := d.SQLDB.ExecContext(ctx, `
		UPDATE cleanup_tasks
		SET last_run_at = ?, last_status = ?, last_freed_bytes = ?, last_freed_count = ?
		WHERE id = ?
	`, now, status, freedBytes, freedCount, id)
	return err
}

// UpdateCleanupTaskConfig 更新 task 配置（threshold_* / enabled / interval）。
func (d *DB) UpdateCleanupTaskConfig(ctx context.Context, id int64, thresholdSeconds int, thresholdBytes int64, enabled bool, intervalSec int) error {
	en := 0
	if enabled {
		en = 1
	}
	_, err := d.SQLDB.ExecContext(ctx, `
		UPDATE cleanup_tasks
		SET threshold_seconds = ?, threshold_bytes = ?, enabled = ?, cron_interval_sec = ?
		WHERE id = ?
	`, thresholdSeconds, thresholdBytes, en, intervalSec, id)
	return err
}

// CleanupReport 清理结果。
type CleanupReport struct {
	TaskID       int64
	Strategy     string
	FreedCount   int
	FreedBytes   int64
	BeforeCount  int
	BeforeBytes  int64
	AfterCount   int
	AfterBytes   int64
	DurationMs   int64
}

// RunLRU 删 last_access_at 距今超过 thresholdSeconds 秒的条目（批量，N=200 上限避免长事务）。
//
// 删除顺序：先按 last_access_at ASC（最旧）选最多 batchSize 条；循环直到没更多。
//
// 文件删除由调用方负责（本函数只删 DB 行；Proxy 层可以 batch 删文件）。
//
// dryRun=true 时只算 freed_count/freed_bytes，不真删；返回与实际跑一样的 report。
func (d *DB) RunLRU(ctx context.Context, taskID int64, thresholdSeconds int, batchSize int, dryRun bool) (CleanupReport, error) {
	if batchSize <= 0 {
		batchSize = 200
	}
	report := CleanupReport{TaskID: taskID, Strategy: "lru"}
	cutoff := time.Now().Unix() - int64(thresholdSeconds)

	// 起始状态
	if err := d.SQLDB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(size_bytes), 0) FROM cache_entries`).Scan(&report.BeforeCount, &report.BeforeBytes); err != nil {
		return report, err
	}

	start := time.Now()
	// dry-run 只跑一次（不删行，循环没意义，会无限选同样行）。
	maxIters := 1
	if !dryRun {
		maxIters = 1000 // 兜底，防止异常情况下死循环
	}
	for iter := 0; iter < maxIters; iter++ {
		// 用子事务避免长 lock
		rows, err := d.SQLDB.QueryContext(ctx, `
			SELECT id, registry, repository, digest, size_bytes
			FROM cache_entries
			WHERE last_access_at < ? AND digest != ''
			ORDER BY last_access_at ASC
			LIMIT ?
		`, cutoff, batchSize)
		if err != nil {
			return report, err
		}

		var batch []struct {
			id        int64
			reg, repo, dig string
			size      int64
		}
		for rows.Next() {
			var b struct {
				id        int64
				reg, repo, dig string
				size      int64
			}
			if err := rows.Scan(&b.id, &b.reg, &b.repo, &b.dig, &b.size); err != nil {
				rows.Close()
				return report, err
			}
			batch = append(batch, b)
		}
		rows.Close()

		if len(batch) == 0 {
			break
		}

		for _, b := range batch {
			if !dryRun {
				if _, err := d.SQLDB.ExecContext(ctx, `DELETE FROM cache_entries WHERE id = ?`, b.id); err != nil {
					return report, err
				}
			}
			report.FreedCount++
			report.FreedBytes += b.size
		}
	}

	// 结束状态（dry-run 时等于 before）
	report.AfterCount = report.BeforeCount - report.FreedCount
	report.AfterBytes = report.BeforeBytes - report.FreedBytes
	if !dryRun {
		if err := d.SQLDB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(size_bytes), 0) FROM cache_entries`).Scan(&report.AfterCount, &report.AfterBytes); err != nil {
			return report, err
		}
	}
	report.DurationMs = time.Since(start).Milliseconds()
	return report, nil
}

// RunCapacity 删到 cache 总量 ≤ thresholdBytes（按 last_access_at ASC 先删最旧）。
func (d *DB) RunCapacity(ctx context.Context, taskID int64, thresholdBytes int64, batchSize int, dryRun bool) (CleanupReport, error) {
	if batchSize <= 0 {
		batchSize = 200
	}
	report := CleanupReport{TaskID: taskID, Strategy: "capacity"}

	if err := d.SQLDB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(size_bytes), 0) FROM cache_entries`).Scan(&report.BeforeCount, &report.BeforeBytes); err != nil {
		return report, err
	}

	start := time.Now()
	maxIters := 1
	if !dryRun {
		maxIters = 1000
	}
	for iter := 0; iter < maxIters; iter++ {
		// 查当前总量
		var total int64
		if err := d.SQLDB.QueryRowContext(ctx, `SELECT COALESCE(SUM(size_bytes), 0) FROM cache_entries`).Scan(&total); err != nil {
			return report, err
		}
		if total <= thresholdBytes {
			break
		}

		// 取最旧的 batchSize 条
		rows, err := d.SQLDB.QueryContext(ctx, `
			SELECT id, registry, repository, digest, size_bytes
			FROM cache_entries
			WHERE digest != ''
			ORDER BY last_access_at ASC
			LIMIT ?
		`, batchSize)
		if err != nil {
			return report, err
		}
		var batch []struct {
			id        int64
			reg, repo, dig string
			size      int64
		}
		for rows.Next() {
			var b struct {
				id        int64
				reg, repo, dig string
				size      int64
			}
			if err := rows.Scan(&b.id, &b.reg, &b.repo, &b.dig, &b.size); err != nil {
				rows.Close()
				return report, err
			}
			batch = append(batch, b)
		}
		rows.Close()
		if len(batch) == 0 {
			break
		}
		for _, b := range batch {
			if !dryRun {
				if _, err := d.SQLDB.ExecContext(ctx, `DELETE FROM cache_entries WHERE id = ?`, b.id); err != nil {
					return report, err
				}
			}
			report.FreedCount++
			report.FreedBytes += b.size
		}
	}

	// 结束状态
	report.AfterCount = report.BeforeCount - report.FreedCount
	report.AfterBytes = report.BeforeBytes - report.FreedBytes
	if !dryRun {
		if err := d.SQLDB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(size_bytes), 0) FROM cache_entries`).Scan(&report.AfterCount, &report.AfterBytes); err != nil {
			return report, err
		}
	}
	report.DurationMs = time.Since(start).Milliseconds()
	return report, nil
}

// SetTaskThresholdSeconds / SetTaskThresholdBytes 提供类型安全的 helper。
func (d *DB) GetCleanupTaskByID(ctx context.Context, id int64) (*CleanupTask, error) {
	row := d.SQLDB.QueryRowContext(ctx, `
		SELECT id, task_name, strategy, threshold_seconds, threshold_bytes, enabled,
		       cron_interval_sec, last_run_at, last_status, last_freed_bytes,
		       last_freed_count, created_at
		FROM cleanup_tasks WHERE id = ?
	`, id)
	var t CleanupTask
	var enabled int
	if err := row.Scan(&t.ID, &t.Name, &t.Strategy, &t.ThresholdSeconds, &t.ThresholdBytes,
		&enabled, &t.CronIntervalSec, &t.LastRunAt, &t.LastStatus, &t.LastFreedBytes,
		&t.LastFreedCount, &t.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	t.Enabled = enabled == 1
	return &t, nil
}
