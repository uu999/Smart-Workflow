package mysql

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/smart-workflow/smart-workflow/internal/storage/mysql/gen"
)

// Store 封装 DB 连接与 sqlc 生成的查询。
type Store struct {
	DB *sql.DB
	Q  *gen.Queries
}

// Open 打开 MySQL 连接并配置连接池。
func Open(dsn string) (*Store, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(time.Hour)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}

	return &Store{DB: db, Q: gen.New(db)}, nil
}

// Close 关闭连接。
func (s *Store) Close() error {
	return s.DB.Close()
}

// WithTx 在事务中执行 fn，出错回滚。
func (s *Store) WithTx(fn func(q *gen.Queries) error) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	if err := fn(s.Q.WithTx(tx)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
