package model

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
)

// Result is what a write gives back: the rows it touched and, on MySQL, the
// first id it inserted. sql.Result satisfies it.
type Result interface {
	RowsAffected() (int64, error)
	LastInsertId() (int64, error)
}

// runner runs SQL for the models. There are two: database/sql (MySQL, or
// PostgreSQL through pgx's stdlib), and pgx's own pool (pgx.go), which skips
// the database/sql layer.
type runner interface {
	query(ctx context.Context, sqlText string, params []any) ([]Row, error)
	exec(ctx context.Context, sqlText string, params []any) (Result, error)
}

var (
	connMu  sync.RWMutex
	conn    runner
	dialect Dialect
)

// Use sets a database/sql pool for every model. database/sql pools and
// reuses connections. For MySQL, add interpolateParams=true to the DSN, so a
// query is one round trip. For PostgreSQL, UsePgx is faster.
func Use(db *sql.DB, d Dialect) {
	connMu.Lock()
	defer connMu.Unlock()
	conn, dialect = sqlRunner{db}, d
}

func current() (runner, Dialect, error) {
	connMu.RLock()
	defer connMu.RUnlock()
	if conn == nil {
		return nil, dialect, errNoDB
	}
	return conn, dialect, nil
}

// Row is one result row: column name to value. Text and DECIMAL columns come
// back as string, integers as int64, timestamps as time.Time.
type Row = map[string]any

// query runs SQL written MySQL-style (? and backticks) and returns its rows.
func query(ctx context.Context, sqlText string, params []any) ([]Row, error) {
	r, d, err := current()
	if err != nil {
		return nil, err
	}
	if d == Postgres {
		sqlText = ToPostgres(sqlText)
	}
	rows, err := r.query(ctx, sqlText, params)
	if err != nil {
		return nil, fmt.Errorf("%w\nquery: %s", err, sqlText)
	}
	return rows, nil
}

// exec runs SQL written MySQL-style that returns no rows.
func exec(ctx context.Context, sqlText string, params []any) (Result, error) {
	r, d, err := current()
	if err != nil {
		return nil, err
	}
	if d == Postgres {
		sqlText = ToPostgres(sqlText)
	}
	res, err := r.exec(ctx, sqlText, params)
	if err != nil {
		return nil, fmt.Errorf("%w\nquery: %s", err, sqlText)
	}
	return res, nil
}

// ---- database/sql ---------------------------------------------------------

type sqlRunner struct{ db *sql.DB }

func (s sqlRunner) query(ctx context.Context, sqlText string, params []any) ([]Row, error) {
	rows, err := s.db.QueryContext(ctx, sqlText, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	cols := make([]string, len(types))
	conv := make([]func(any) any, len(types))
	for i, t := range types {
		cols[i] = t.Name()
		conv[i] = converter(t.DatabaseTypeName())
	}
	out := []Row{}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		r := make(Row, len(cols))
		for i, c := range cols {
			r[c] = conv[i](vals[i])
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s sqlRunner) exec(ctx context.Context, sqlText string, params []any) (Result, error) {
	return s.db.ExecContext(ctx, sqlText, params...)
}

// converter gives every driver the same value types: integers int64, FLOAT
// and DOUBLE float64, everything else (DECIMAL included) string, timestamps
// time.Time. The MySQL driver sends text-protocol results (interpolateParams)
// as bytes, so the column type decides.
func converter(dbType string) func(any) any {
	t := strings.ToUpper(dbType)
	switch {
	case strings.Contains(t, "INT") || t == "YEAR" || t == "SERIAL":
		return func(v any) any {
			switch x := v.(type) {
			case []byte:
				if n, err := strconv.ParseInt(string(x), 10, 64); err == nil {
					return n
				}
				return string(x) // unsigned beyond int64
			case uint64:
				if x <= math.MaxInt64 {
					return int64(x)
				}
				return x
			case int32:
				return int64(x)
			case int16:
				return int64(x)
			}
			return v
		}
	case t == "FLOAT" || t == "DOUBLE" || t == "REAL" || t == "FLOAT4" || t == "FLOAT8":
		return func(v any) any {
			switch x := v.(type) {
			case []byte:
				if f, err := strconv.ParseFloat(string(x), 64); err == nil {
					return f
				}
				return string(x)
			case float32:
				return float64(x)
			}
			return v
		}
	}
	return func(v any) any {
		if b, ok := v.([]byte); ok {
			return string(b)
		}
		return v
	}
}
