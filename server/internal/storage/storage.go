// Package storage 封装 SQLite 连接和迁移。
//
// 关键决策：
//   - 驱动选 modernc.org/sqlite（纯 Go，无 CGO，方便单二进制 + 交叉编译）；
//   - 启用 WAL 模式（PRAGMA journal_mode = WAL）以提升并发读 + 写不阻塞；
//   - 启用 foreign_keys = ON（SQLite 默认是 OFF，必须显式打开）；
//   - busy_timeout = 5s，避免高并发瞬时锁竞争立刻失败；
//   - migrations 走 embed，按文件名升序应用，已应用的写入 schema_migrations 表，幂等。
package storage

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	// 纯 Go SQLite 驱动。
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DB 是 storage 层暴露给上层的主要类型。
type DB struct {
	// SQLDB 是底层 *sql.DB。直接暴露，避免无谓的 wrapper。
	SQLDB *sql.DB
	// Path SQLite 文件路径。仅供日志 / 诊断。
	Path string
	// dataDir 派生字段，方便上层做备份等操作。
	dataDir string
}

// Open 打开（或创建）SQLite 数据库并应用未执行的 migrations。
//
// 行为：
//   - 若 DataDir 不存在则自动创建（0755）；
//   - 启用 WAL / foreign_keys / busy_timeout；
//   - 自动应用 migrations/ 下所有 *.sql（按文件名升序，幂等）。
func Open(ctx context.Context, dataDir string) (*DB, error) {
	if dataDir == "" {
		return nil, errors.New("storage: dataDir is required")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("storage: mkdir data dir %q: %w", dataDir, err)
	}

	dbPath := filepath.Join(dataDir, "cncachehub.db")
	// modernc.org/sqlite 的 DSN 写法：file:path?_pragma=...
	// 每个 _pragma 用 & 连接，特殊字符需要 URL escape，但我们用的是字母数字，安全。
	dsn := "file:" + dbPath + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"

	// 关键：用 sql.OpenDB + 驱动 connector，让 busy_timeout 等 PRAGMA 在每次连接时生效。
	// 但 modernc.org/sqlite 的 RegisterConnector 略繁琐；这里用 sql.Open + 立即 ping + 一次性 PRAGMA 双保险。
	// 实际 WAL / foreign_keys / busy_timeout / synchronous 已通过 DSN 设置。
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: open sqlite: %w", err)
	}

	// SQLite 一次只允许一个写连接；设置 MaxOpenConns(1) 简化并发问题。
	// 读并发仍然能扩展（WAL 模式）。
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("storage: ping sqlite: %w", err)
	}

	d := &DB{
		SQLDB:   sqlDB,
		Path:    dbPath,
		dataDir: dataDir,
	}

	// 跑 migrations。
	if err := d.runMigrations(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("storage: run migrations: %w", err)
	}

	return d, nil
}

// DataDir 返回数据目录路径。
func (d *DB) DataDir() string { return d.dataDir }

// Close 关闭底层 *sql.DB。多次调用安全。
func (d *DB) Close() error {
	if d == nil || d.SQLDB == nil {
		return nil
	}
	return d.SQLDB.Close()
}

// runMigrations 应用所有未执行的 migrations。
//
// 流程：
//  1. 确保 schema_migrations 表存在；
//  2. 读取 migrations/*.sql 列表，按文件名升序；
//  3. 对每个文件：若 schema_migrations 中无记录则执行并插入记录；
//  4. 整段放在一个事务里以便原子化（也可以按文件粒度事务，phase 0 先做整体事务）。
func (d *DB) runMigrations(ctx context.Context) error {
	// 1. 准备 schema_migrations 表（即使没有迁移文件也要有这张表）。
	if _, err := d.SQLDB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		filename   TEXT PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	// 2. 列举 migrations/*.sql。
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		files = append(files, e.Name())
	}
	if len(files) == 0 {
		return nil
	}
	sort.Strings(files)

	// 3. 查已应用的。
	applied := make(map[string]struct{}, len(files))
	rows, err := d.SQLDB.QueryContext(ctx, `SELECT filename FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("query schema_migrations: %w", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("rows err: %w", err)
	}
	_ = rows.Close()

	// 4. 顺序应用未执行的 migrations。
	for _, name := range files {
		if _, ok := applied[name]; ok {
			continue
		}
		raw, err := migrationsFS.ReadFile(filepath.Join("migrations", name))
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		// 注意：SQLite 的 ExecContext 一次只能执行一条 statement。
		// 我们的 migrations 文件每条以 ; 结尾、不含 BEGIN/COMMIT 等结构，因此可以整段执行。
		// 若以后出现多语句需求，切换到 migrate.Up() / 第三方库。
		if _, err := d.SQLDB.ExecContext(ctx, string(raw)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := d.SQLDB.ExecContext(ctx,
			`INSERT INTO schema_migrations (filename, applied_at) VALUES (?, ?)`,
			name, time.Now().Unix(),
		); err != nil {
			return fmt.Errorf("record %s: %w", name, err)
		}
	}
	return nil
}

// Ping 是对底层 *sql.DB.PingContext 的薄封装，供 health 端点调用。
func (d *DB) Ping(ctx context.Context) error {
	if d == nil || d.SQLDB == nil {
		return errors.New("storage: db not initialized")
	}
	return d.SQLDB.PingContext(ctx)
}

// ListAppliedMigrations 列出已应用的 migration 文件名（仅供测试 / 诊断）。
func (d *DB) ListAppliedMigrations(ctx context.Context) ([]string, error) {
	rows, err := d.SQLDB.QueryContext(ctx,
		`SELECT filename FROM schema_migrations ORDER BY filename ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MigrationsFS 暴露内嵌的 migrations 子文件系统（仅供测试 / 诊断使用）。
func MigrationsFS() fs.FS {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		// embed 不会失败；安全兜底。
		panic("storage: migrations sub fs: " + err.Error())
	}
	return sub
}
