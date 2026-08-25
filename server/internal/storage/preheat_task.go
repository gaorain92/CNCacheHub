package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// PreheatTaskKind 是任务 kind 字面量。
const (
	PreheatKindDocker           = "docker"
	PreheatKindSteam            = "steam"
	PreheatKindResource         = "resource"
	PreheatKindHuggingFaceModel = "huggingface_model" // HF 全量预热（按 model id 拉全部文件）
)

// PreheatTaskStatus 是任务状态字面量。
const (
	PreheatStatusPending  = "pending"
	PreheatStatusRunning  = "running"
	PreheatStatusDone     = "done"
	PreheatStatusError    = "error"
	PreheatStatusCanceled = "canceled"
)

// PreheatItemStatus 是单条 target 状态。
const (
	PreheatItemPending = "pending"
	PreheatItemRunning = "running"
	PreheatItemDone    = "done"
	PreheatItemError   = "error"
	PreheatItemSkipped = "skipped"
)

// PreheatTask 是 preheat_tasks 行的 Go 表示（PRD §9.2.3 / §9.3.3）。
type PreheatTask struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	Kind            string   `json:"kind"`
	Targets         []string `json:"targets"`
	Status          string   `json:"status"`
	ProgressTotal   int      `json:"progressTotal"`
	ProgressDone    int      `json:"progressDone"`
	ProgressBytes   int64    `json:"progressBytes"`
	ErrorMessage    string   `json:"errorMessage"`
	CronExpression  string   `json:"cronExpression"`
	Enabled         bool     `json:"enabled"`
	NextRunAt       int64    `json:"nextRunAt"`
	LastRunAt       int64    `json:"lastRunAt"`
	LastDurationMs  int64    `json:"lastDurationMs"`
	RetryCount      int      `json:"retryCount"`
	MaxRetries      int      `json:"maxRetries"`
	CreatedAt       int64    `json:"createdAt"`
	UpdatedAt       int64    `json:"updatedAt"`
}

// PreheatItem 是 preheat_items 行的 Go 表示。
type PreheatItem struct {
	ID           int64  `json:"id"`
	TaskID       int64  `json:"taskId"`
	Target       string `json:"target"`
	Status       string `json:"status"`
	ErrorMessage string `json:"errorMessage"`
	BytesAdded   int64  `json:"bytesAdded"`
	StartedAt    int64  `json:"startedAt"`
	FinishedAt   int64  `json:"finishedAt"`
}

// ListPreheatTasks 列出全部任务（按 id 倒序）。
func (d *DB) ListPreheatTasks(ctx context.Context) ([]PreheatTask, error) {
	rows, err := d.SQLDB.QueryContext(ctx, `
		SELECT id, name, kind, targets_json, status, progress_total, progress_done, progress_bytes,
		       error_message, cron_expression, enabled, next_run_at, last_run_at, last_duration_ms,
		       retry_count, max_retries, created_at, updated_at
		FROM preheat_tasks
		ORDER BY id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("storage: list preheat tasks: %w", err)
	}
	defer rows.Close()
	out := make([]PreheatTask, 0, 8)
	for rows.Next() {
		t, err := scanPreheatTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetPreheatTask 拿单条。
func (d *DB) GetPreheatTask(ctx context.Context, id int64) (PreheatTask, error) {
	row := d.SQLDB.QueryRowContext(ctx, `
		SELECT id, name, kind, targets_json, status, progress_total, progress_done, progress_bytes,
		       error_message, cron_expression, enabled, next_run_at, last_run_at, last_duration_ms,
		       retry_count, max_retries, created_at, updated_at
		FROM preheat_tasks WHERE id = ?
	`, id)
	var enabledI int
	var targetsJSON string
	t := PreheatTask{}
	err := row.Scan(&t.ID, &t.Name, &t.Kind, &targetsJSON, &t.Status, &t.ProgressTotal, &t.ProgressDone,
		&t.ProgressBytes, &t.ErrorMessage, &t.CronExpression, &enabledI, &t.NextRunAt, &t.LastRunAt,
		&t.LastDurationMs, &t.RetryCount, &t.MaxRetries, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PreheatTask{}, ErrNotFound
	}
	if err != nil {
		return PreheatTask{}, fmt.Errorf("storage: get preheat task: %w", err)
	}
	t.Enabled = enabledI == 1
	if err := json.Unmarshal([]byte(targetsJSON), &t.Targets); err != nil {
		return PreheatTask{}, fmt.Errorf("storage: parse targets: %w", err)
	}
	if t.Targets == nil {
		t.Targets = []string{}
	}
	return t, nil
}

func scanPreheatTask(rows *sql.Rows) (PreheatTask, error) {
	var enabledI int
	var targetsJSON string
	t := PreheatTask{}
	err := rows.Scan(&t.ID, &t.Name, &t.Kind, &targetsJSON, &t.Status, &t.ProgressTotal, &t.ProgressDone,
		&t.ProgressBytes, &t.ErrorMessage, &t.CronExpression, &enabledI, &t.NextRunAt, &t.LastRunAt,
		&t.LastDurationMs, &t.RetryCount, &t.MaxRetries, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return PreheatTask{}, fmt.Errorf("storage: scan preheat task: %w", err)
	}
	t.Enabled = enabledI == 1
	if err := json.Unmarshal([]byte(targetsJSON), &t.Targets); err != nil {
		return PreheatTask{}, fmt.Errorf("storage: parse targets: %w", err)
	}
	if t.Targets == nil {
		t.Targets = []string{}
	}
	return t, nil
}

// CreatePreheatTask 创建任务 + 同步创建所有 item 行（默认 pending）。
func (d *DB) CreatePreheatTask(ctx context.Context, in PreheatTask) (PreheatTask, error) {
	now := time.Now().Unix()
	in.CreatedAt = now
	in.UpdatedAt = now
	if in.Status == "" {
		in.Status = PreheatStatusPending
	}
	if in.MaxRetries == 0 {
		in.MaxRetries = 2
	}
	if len(in.Targets) == 0 {
		return PreheatTask{}, errors.New("storage: preheat task requires at least 1 target")
	}
	in.ProgressTotal = len(in.Targets)
	enabledI := 0
	if in.Enabled {
		enabledI = 1
	}
	targetsJSON, err := json.Marshal(in.Targets)
	if err != nil {
		return PreheatTask{}, fmt.Errorf("storage: marshal targets: %w", err)
	}
	res, err := d.SQLDB.ExecContext(ctx, `
		INSERT INTO preheat_tasks
		  (name, kind, targets_json, status, progress_total, progress_done, progress_bytes,
		   error_message, cron_expression, enabled, next_run_at, last_run_at, last_duration_ms,
		   retry_count, max_retries, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, 0, '', ?, ?, 0, 0, 0, 0, ?, ?, ?)
	`, in.Name, in.Kind, string(targetsJSON), in.Status, in.ProgressTotal, in.CronExpression,
		enabledI, in.MaxRetries, in.CreatedAt, in.UpdatedAt)
	if err != nil {
		return PreheatTask{}, fmt.Errorf("storage: insert preheat task: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return PreheatTask{}, fmt.Errorf("storage: last insert id: %w", err)
	}
	in.ID = id
	// 同步建 items
	for _, target := range in.Targets {
		if _, err := d.SQLDB.ExecContext(ctx, `
			INSERT INTO preheat_items (task_id, target, status) VALUES (?, ?, 'pending')
		`, id, target); err != nil {
			return PreheatTask{}, fmt.Errorf("storage: insert preheat item: %w", err)
		}
	}
	in.Enabled = enabledI == 1
	return in, nil
}

// DeletePreheatTask 删任务 + items（cascade）。
func (d *DB) DeletePreheatTask(ctx context.Context, id int64) error {
	_, err := d.SQLDB.ExecContext(ctx, `DELETE FROM preheat_tasks WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("storage: delete preheat task: %w", err)
	}
	return nil
}

// UpdatePreheatTaskStatus 任务级状态机（pending → running → done/error/canceled）。
func (d *DB) UpdatePreheatTaskStatus(ctx context.Context, id int64, status, errMsg string, durationMs int64) error {
	now := time.Now().Unix()
	_, err := d.SQLDB.ExecContext(ctx, `
		UPDATE preheat_tasks
		SET status=?, error_message=?, last_run_at=?, last_duration_ms=?, updated_at=?
		WHERE id=?
	`, status, errMsg, now, durationMs, now, id)
	if err != nil {
		return fmt.Errorf("storage: update preheat task status: %w", err)
	}
	return nil
}

// UpdatePreheatTaskProgress 累加 progress_done / progress_bytes。
func (d *DB) UpdatePreheatTaskProgress(ctx context.Context, id int64, deltaDone int, deltaBytes int64) error {
	_, err := d.SQLDB.ExecContext(ctx, `
		UPDATE preheat_tasks
		SET progress_done = progress_done + ?, progress_bytes = progress_bytes + ?, updated_at = ?
		WHERE id = ?
	`, deltaDone, deltaBytes, time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("storage: update preheat task progress: %w", err)
	}
	return nil
}

// ListPreheatItems 拿任务下所有 item。
func (d *DB) ListPreheatItems(ctx context.Context, taskID int64) ([]PreheatItem, error) {
	rows, err := d.SQLDB.QueryContext(ctx, `
		SELECT id, task_id, target, status, error_message, bytes_added, started_at, finished_at
		FROM preheat_items
		WHERE task_id = ?
		ORDER BY id ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("storage: list preheat items: %w", err)
	}
	defer rows.Close()
	out := make([]PreheatItem, 0, 8)
	for rows.Next() {
		var it PreheatItem
		if err := rows.Scan(&it.ID, &it.TaskID, &it.Target, &it.Status, &it.ErrorMessage, &it.BytesAdded, &it.StartedAt, &it.FinishedAt); err != nil {
			return nil, fmt.Errorf("storage: scan preheat item: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// UpdatePreheatItem 写单条 item 状态。
func (d *DB) UpdatePreheatItem(ctx context.Context, id int64, status, errMsg string, bytesAdded int64, startedAt, finishedAt int64) error {
	_, err := d.SQLDB.ExecContext(ctx, `
		UPDATE preheat_items
		SET status=?, error_message=?, bytes_added=?, started_at=?, finished_at=?
		WHERE id=?
	`, status, errMsg, bytesAdded, startedAt, finishedAt, id)
	if err != nil {
		return fmt.Errorf("storage: update preheat item: %w", err)
	}
	return nil
}
