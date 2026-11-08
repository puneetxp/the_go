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

var (
	connMu  sync.RWMutex
	conn    *sql.DB
	dialect Dialect
)

// Use sets the connection pool every model runs on. database/sql pools and
// reuses connections. For MySQL, add interpolateParams=true to the DSN, so a
// query is one round trip; pgx caches prepared statements itself.
func Use(db *sql.DB, d Dialect) {
	connMu.Lock()
	defer connMu.Unlock()
	conn, dialect = db, d
}

func current() (*sql.DB, Dialect, error) {
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

func query(ctx context.Context, sqlText string, params []any) ([]Row, error) {
	db, d, err := current()
	if err != nil {
		return nil, err
	}
	if d == Postgres {
		sqlText = ToPostgres(sqlText)
	}
	rows, err := db.QueryContext(ctx, sqlText, params...)
	if err != nil {
		return nil, fmt.Errorf("%w\nquery: %s", err, sqlText)
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

func exec(ctx context.Context, sqlText string, params []any) (sql.Result, error) {
	db, d, err := current()
	if err != nil {
		return nil, err
	}
	if d == Postgres {
		sqlText = ToPostgres(sqlText)
	}
	res, err := db.ExecContext(ctx, sqlText, params...)
	if err != nil {
		return nil, fmt.Errorf("%w\nquery: %s", err, sqlText)
	}
	return res, nil
}

// ---- reading ----------------------------------------------------------------

// Get runs the chain and returns every row.
func (q *Query) Get(ctx context.Context) ([]Row, error) {
	s, params, err := q.ToSQL()
	if err != nil {
		return nil, err
	}
	return query(ctx, s, params)
}

// First returns the first row, or nil when there is none.
func (q *Query) First(ctx context.Context) (Row, error) {
	rows, err := q.Limit(1, q.offset).Get(ctx)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

// Count returns the number of rows the chain matches.
func (q *Query) Count(ctx context.Context) (int64, error) {
	if q.err != nil {
		return 0, q.err
	}
	w, params := q.whereClause("")
	rows, err := query(ctx, "SELECT count(*) AS "+Ident("count")+" FROM "+q.m.Table+w, params)
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	switch n := rows[0]["count"].(type) {
	case int64:
		return n, nil
	case string:
		var v int64
		_, err := fmt.Sscan(n, &v)
		return v, err
	default:
		return 0, fmt.Errorf("count: unexpected %T", n)
	}
}

// WithJoin runs the chain with a LEFT JOIN per relation and nests each
// relation's columns under row[relation].
func (q *Query) WithJoin(ctx context.Context, relations ...string) ([]Row, error) {
	s, params, err := q.WithJoinSQL(relations...)
	if err != nil {
		return nil, err
	}
	rows, err := query(ctx, s, params)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		for k, v := range r {
			rel, col, ok := strings.Cut(k, "__")
			if !ok || !contains(relations, rel) {
				continue
			}
			nested, _ := r[rel].(Row)
			if nested == nil {
				nested = Row{}
				r[rel] = nested
			}
			nested[col] = v
			delete(r, k)
		}
	}
	return rows, nil
}

// Find returns the row whose key column (default "id") is value, or nil.
func (m *Model) Find(ctx context.Context, value any, key ...string) (Row, error) {
	k := "id"
	if len(key) > 0 {
		k = key[0]
	}
	return m.Where(W{k: value}).First(ctx)
}

// Get runs a query with no conditions: every row.
func (m *Model) Get(ctx context.Context) ([]Row, error) { return m.Query().Get(ctx) }

// ---- writing ----------------------------------------------------------------

func columnsAndValues(data W) ([]string, []any) {
	cols := sortedKeys(data)
	vals := make([]any, len(cols))
	for i, c := range cols {
		vals[i] = data[c]
	}
	return cols, vals
}

func quoteAll(cols []string) string {
	q := make([]string, len(cols))
	for i, c := range cols {
		q[i] = Ident(c)
	}
	return strings.Join(q, ",")
}

// Create inserts one row (only Fillable columns) and returns it as saved.
func (m *Model) Create(ctx context.Context, data W) (Row, error) {
	ids, err := m.insert(ctx, []W{data})
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	return m.Find(ctx, ids[0])
}

// Insert inserts rows (only Fillable columns; every row needs the same
// columns) and returns their ids.
func (m *Model) Insert(ctx context.Context, rows []W) ([]any, error) {
	return m.insert(ctx, rows)
}

func (m *Model) insert(ctx context.Context, rows []W) ([]any, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	cols, _ := columnsAndValues(m.writable(rows[0]))
	var params []any
	tuple := "(" + strings.Repeat("?,", len(cols)-1) + "?)"
	tuples := make([]string, len(rows))
	for i, r := range rows {
		w := m.writable(r)
		for _, c := range cols {
			params = append(params, w[c])
		}
		tuples[i] = tuple
	}
	s := "INSERT INTO " + m.Table + "( " + quoteAll(cols) + " ) VALUES " + strings.Join(tuples, ",")
	_, d, err := current()
	if err != nil {
		return nil, err
	}
	if d == Postgres {
		got, err := query(ctx, s+" RETURNING "+Ident("id"), params)
		if err != nil {
			return nil, err
		}
		ids := make([]any, len(got))
		for i, r := range got {
			ids[i] = r["id"]
		}
		return ids, nil
	}
	res, err := exec(ctx, s, params)
	if err != nil {
		return nil, err
	}
	first, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	// A multi-row INSERT takes consecutive ids from the first.
	ids := make([]any, len(rows))
	for i := range ids {
		ids[i] = first + int64(i)
	}
	return ids, nil
}

// Upsert inserts rows, or updates the row with the same id (MySQL: any
// unique key). Fillable columns plus id are written; every row needs the
// same columns. Returns the driver's affected-row count.
func (m *Model) Upsert(ctx context.Context, rows []W) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	keep := func(r W) W {
		w := m.writable(r)
		if id, ok := r["id"]; ok {
			w["id"] = id
		}
		return w
	}
	cols, _ := columnsAndValues(keep(rows[0]))
	var params []any
	tuple := "(" + strings.Repeat("?,", len(cols)-1) + "?)"
	tuples := make([]string, len(rows))
	for i, r := range rows {
		w := keep(r)
		for _, c := range cols {
			params = append(params, w[c])
		}
		tuples[i] = tuple
	}
	_, d, err := current()
	if err != nil {
		return 0, err
	}
	var set []string
	for _, c := range cols {
		if c == "id" {
			continue
		}
		if d == Postgres {
			set = append(set, Ident(c)+" = EXCLUDED."+Ident(c))
		} else {
			set = append(set, Ident(c)+" = VALUES("+Ident(c)+")")
		}
	}
	s := "INSERT INTO " + m.Table + "( " + quoteAll(cols) + " ) VALUES " + strings.Join(tuples, ",")
	switch {
	case d == Postgres && len(set) > 0:
		s += " ON CONFLICT (" + Ident("id") + ") DO UPDATE SET " + strings.Join(set, ", ")
	case d == Postgres:
		s += " ON CONFLICT (" + Ident("id") + ") DO NOTHING"
	case len(set) > 0:
		s += " ON DUPLICATE KEY UPDATE " + strings.Join(set, ", ")
	}
	return affected(exec(ctx, s, params))
}

// HasColumn reports whether col is one of the model's columns.
func (m *Model) HasColumn(col string) bool { return contains(m.Columns, col) }

// Update sets columns (only Fillable ones) on the rows the chain matches and
// returns how many changed.
func (q *Query) Update(ctx context.Context, data W) (int64, error) {
	if q.err != nil {
		return 0, q.err
	}
	cols, vals := columnsAndValues(q.m.writable(data))
	if len(cols) == 0 {
		return 0, fmt.Errorf("%s: Update has no writable columns", q.m.Name)
	}
	set := make([]string, len(cols))
	for i, c := range cols {
		set[i] = Ident(c) + " = ?"
	}
	w, params := q.whereClause("")
	return affected(exec(ctx, "UPDATE "+q.m.Table+" SET "+strings.Join(set, " , ")+w, append(vals, params...)))
}

// Delete removes the rows the chain matches and returns how many.
func (q *Query) Delete(ctx context.Context) (int64, error) {
	if q.err != nil {
		return 0, q.err
	}
	w, params := q.whereClause("")
	return affected(exec(ctx, "DELETE FROM "+q.m.Table+" "+w, params))
}

// Toggle flips a 0/1 flag column (default "enable") on the rows the chain matches.
func (q *Query) Toggle(ctx context.Context, column ...string) (int64, error) {
	if q.err != nil {
		return 0, q.err
	}
	f := "enable"
	if len(column) > 0 {
		f = column[0]
	}
	c := Ident(f)
	w, params := q.whereClause("")
	return affected(exec(ctx, "UPDATE "+q.m.Table+" SET "+c+" = CASE WHEN "+c+" = 0 THEN 1 ELSE 0 END"+w, params))
}

func affected(res sql.Result, err error) (int64, error) {
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
