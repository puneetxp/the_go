package model

import (
	"context"
	"database/sql/driver"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UsePgx sets a pgx pool (PostgreSQL) for every model. It is faster than
// database/sql with pgx's stdlib, and gives the same values: each column is
// decoded with the scan plan stdlib would use, then the same converter runs.
// pgx caches prepared statements per connection.
func UsePgx(pool *pgxpool.Pool) {
	connMu.Lock()
	defer connMu.Unlock()
	conn, dialect = pgxRunner{pool}, Postgres
}

type pgxRunner struct{ pool *pgxpool.Pool }

// valueFunc turns one raw column value into what database/sql would return.
type valueFunc func(src []byte) (driver.Value, error)

// valueFor mirrors pgx's stdlib (Rows.Next): bool, bytea, integers as int64,
// floats as float64, dates and timestamps as time.Time, JSON as bytes, and
// everything else as text.
func valueFor(m *pgtype.Map, fd pgconn.FieldDescription) valueFunc {
	oid, format := fd.DataTypeOID, fd.Format
	switch oid {
	case pgtype.BoolOID:
		var d bool
		plan := m.PlanScan(oid, format, &d)
		return func(src []byte) (driver.Value, error) { return d, plan.Scan(src, &d) }
	case pgtype.ByteaOID, pgtype.JSONOID, pgtype.JSONBOID, pgtype.XMLOID:
		var d []byte
		plan := m.PlanScan(oid, format, &d)
		return func(src []byte) (driver.Value, error) {
			if err := plan.Scan(src, &d); err != nil {
				return nil, err
			}
			return d, nil
		}
	case pgtype.CIDOID, pgtype.OIDOID, pgtype.XIDOID:
		var d pgtype.Uint32
		plan := m.PlanScan(oid, format, &d)
		return func(src []byte) (driver.Value, error) {
			if err := plan.Scan(src, &d); err != nil {
				return nil, err
			}
			return d.Value()
		}
	case pgtype.DateOID:
		var d pgtype.Date
		plan := m.PlanScan(oid, format, &d)
		return func(src []byte) (driver.Value, error) {
			if err := plan.Scan(src, &d); err != nil {
				return nil, err
			}
			return d.Value()
		}
	case pgtype.Float4OID:
		var d float32
		plan := m.PlanScan(oid, format, &d)
		return func(src []byte) (driver.Value, error) { err := plan.Scan(src, &d); return float64(d), err }
	case pgtype.Float8OID:
		var d float64
		plan := m.PlanScan(oid, format, &d)
		return func(src []byte) (driver.Value, error) { return d, plan.Scan(src, &d) }
	case pgtype.Int2OID:
		var d int16
		plan := m.PlanScan(oid, format, &d)
		return func(src []byte) (driver.Value, error) { err := plan.Scan(src, &d); return int64(d), err }
	case pgtype.Int4OID:
		var d int32
		plan := m.PlanScan(oid, format, &d)
		return func(src []byte) (driver.Value, error) { err := plan.Scan(src, &d); return int64(d), err }
	case pgtype.Int8OID:
		var d int64
		plan := m.PlanScan(oid, format, &d)
		return func(src []byte) (driver.Value, error) { return d, plan.Scan(src, &d) }
	case pgtype.TimestampOID:
		var d pgtype.Timestamp
		plan := m.PlanScan(oid, format, &d)
		return func(src []byte) (driver.Value, error) {
			if err := plan.Scan(src, &d); err != nil {
				return nil, err
			}
			return d.Value()
		}
	case pgtype.TimestamptzOID:
		var d pgtype.Timestamptz
		plan := m.PlanScan(oid, format, &d)
		return func(src []byte) (driver.Value, error) {
			if err := plan.Scan(src, &d); err != nil {
				return nil, err
			}
			return d.Value()
		}
	}
	var d string
	plan := m.PlanScan(oid, format, &d)
	return func(src []byte) (driver.Value, error) { return d, plan.Scan(src, &d) }
}

// typeName is the column type's name as stdlib reports it (e.g. "INT8").
func typeName(m *pgtype.Map, oid uint32) string {
	if t, ok := m.TypeForOID(oid); ok {
		return strings.ToUpper(t.Name)
	}
	return strconv.FormatUint(uint64(oid), 10)
}

func (p pgxRunner) query(ctx context.Context, sqlText string, params []any) ([]Row, error) {
	rows, err := p.pool.Query(ctx, sqlText, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := rows.Conn().TypeMap()
	fds := rows.FieldDescriptions()
	cols := make([]string, len(fds))
	values := make([]valueFunc, len(fds))
	conv := make([]func(any) any, len(fds))
	for i, fd := range fds {
		cols[i] = fd.Name
		values[i] = valueFor(m, fd)
		conv[i] = converter(typeName(m, fd.DataTypeOID))
	}
	out := []Row{}
	for rows.Next() {
		raw := rows.RawValues()
		r := make(Row, len(cols))
		for i, c := range cols {
			if raw[i] == nil {
				r[c] = nil
				continue
			}
			v, err := values[i](raw[i])
			if err != nil {
				return nil, err
			}
			r[c] = conv[i](v)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p pgxRunner) exec(ctx context.Context, sqlText string, params []any) (Result, error) {
	tag, err := p.pool.Exec(ctx, sqlText, params...)
	if err != nil {
		return nil, err
	}
	return pgxResult(tag), nil
}

// pgxResult is a command tag as a Result. PostgreSQL has no last insert id
// (inserts use RETURNING), as with pgx's stdlib.
type pgxResult pgconn.CommandTag

func (r pgxResult) RowsAffected() (int64, error) {
	return pgconn.CommandTag(r).RowsAffected(), nil
}

func (r pgxResult) LastInsertId() (int64, error) {
	return 0, errors.New("LastInsertId is not supported by PostgreSQL")
}
