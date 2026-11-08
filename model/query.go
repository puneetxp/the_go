package model

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

type cond struct {
	raw    bool // ORM-built SQL (whereHas): sql + params
	sql    string
	params []any
	col    string
	op     string
	val    any
}

// Query is a chain of conditions, order and limit on one model. Methods return
// the same Query; a mistake (unknown relation, bad direction) is kept and
// returned by ToSQL or by the method that runs the query.
type Query struct {
	m      *Model
	and    []cond
	or     []cond
	order  [][2]string
	limit  int
	offset int
	alias  string // name of this table in the SQL: an alias inside whereHas
	depth  int    // nesting level inside whereHas (0 = the query itself)
	err    error
}

var allowedOps = map[string]bool{
	"=": true, "!=": true, "<>": true, "<": true, "<=": true, ">": true, ">=": true,
	"IN": true, "NOT IN": true, "LIKE": true, "NOT LIKE": true,
}

// Where adds col IN (...) conditions; calls chain with AND.
func (q *Query) Where(w W) *Query {
	for _, k := range sortedKeys(w) {
		q.and = append(q.and, cond{col: k, op: "IN", val: w[k]})
	}
	return q
}

// OrWhere adds conditions joined with OR after the AND ones.
func (q *Query) OrWhere(w W) *Query {
	for _, k := range sortedKeys(w) {
		q.or = append(q.or, cond{col: k, op: "IN", val: w[k]})
	}
	return q
}

// WhereC adds one condition with an operator: WhereC("order", ">", 2).
func (q *Query) WhereC(col, op string, v any) *Query {
	op = strings.ToUpper(strings.TrimSpace(op))
	if !allowedOps[op] {
		q.fail(fmt.Errorf("%s: operator %q is not allowed", q.m.Name, op))
		return q
	}
	q.and = append(q.and, cond{col: col, op: op, val: v})
	return q
}

// Reset drops everything added to the chain so far.
func (q *Query) Reset() *Query {
	*q = Query{m: q.m, alias: q.alias, depth: q.depth, err: q.err}
	return q
}

// WhereHas keeps rows with at least one related row, by relation name. The
// optional argument is a W of conditions on the related model, or a
// func(*Query) *Query that builds on it (including more WhereHas).
func (q *Query) WhereHas(rel string, where ...any) *Query {
	return q.relationExists(rel, where, "EXISTS")
}

// WhereNotHas keeps rows with no such related row.
func (q *Query) WhereNotHas(rel string, where ...any) *Query {
	return q.relationExists(rel, where, "NOT EXISTS")
}

func (q *Query) relationExists(name string, where []any, op string) *Query {
	rel, relatedModel, err := q.m.relation(name)
	if err != nil {
		return q.fail(err)
	}
	related := relatedModel.Query()
	related.depth = q.depth + 1
	related.alias = rel.Table + "__" + strconv.Itoa(related.depth)
	if len(where) > 0 && where[0] != nil {
		switch w := where[0].(type) {
		case W:
			related.Where(w)
		case map[string]any:
			related.Where(W(w))
		case func(*Query) *Query:
			related = w(related)
		default:
			return q.fail(fmt.Errorf("%s: WhereHas(%q) takes model.W or func(*model.Query) *model.Query, got %T", q.m.Name, name, where[0]))
		}
	}
	if related.err != nil {
		return q.fail(related.err)
	}
	inner, params := related.whereSQL("")
	outer := q.alias
	if outer == "" {
		outer = q.m.Table
	}
	sql := op + " (SELECT 1 FROM " + rel.Table + " AS " + Ident(related.alias) +
		" WHERE " + Ident(related.alias) + "." + Ident(rel.Key) + " = " + Ident(outer) + "." + Ident(rel.Name)
	if inner != "" {
		sql += " AND (" + inner + ")"
	}
	q.and = append(q.and, cond{raw: true, sql: sql + ")", params: params})
	return q
}

// OrderBy adds a sort column; direction is "ASC" or "DESC".
func (q *Query) OrderBy(col, dir string) *Query {
	d := strings.ToUpper(dir)
	if d == "" {
		d = "ASC"
	}
	if d != "ASC" && d != "DESC" {
		return q.fail(fmt.Errorf("orderBy direction must be ASC or DESC, got %q", dir))
	}
	q.order = append(q.order, [2]string{col, d})
	return q
}

// Limit sets LIMIT and OFFSET (0 means none).
func (q *Query) Limit(count, offset int) *Query {
	if count < 0 || offset < 0 {
		return q.fail(fmt.Errorf("limit needs whole numbers >= 0, got %d, %d", count, offset))
	}
	q.limit, q.offset = count, offset
	return q
}

func (q *Query) fail(err error) *Query {
	if q.err == nil {
		q.err = err
	}
	return q
}

// ---- SQL --------------------------------------------------------------------

func colRef(qualifier, col string) string {
	if qualifier != "" {
		return Ident(qualifier) + "." + Ident(col)
	}
	return Ident(col)
}

func (q *Query) bind(b *strings.Builder, params []any, conds []cond, join, qualifier string) []any {
	for i, c := range conds {
		if i > 0 {
			b.WriteString(" " + join + " ")
		}
		if c.raw {
			b.WriteString(" " + c.sql + " ")
			params = append(params, c.params...)
			continue
		}
		if list, ok := asList(c.val); ok {
			if len(list) == 0 {
				// "IN ()" is invalid SQL: nothing is in an empty list.
				if c.op == "NOT IN" {
					b.WriteString(" 1 = 1 ")
				} else {
					b.WriteString(" 1 = 0 ")
				}
				continue
			}
			b.WriteString(" " + colRef(qualifier, c.col) + " " + c.op + " (" + strings.Repeat("?, ", len(list)-1) + "?)")
			params = append(params, list...)
		} else if c.op == "IN" {
			b.WriteString(" " + colRef(qualifier, c.col) + " IN (?) ")
			params = append(params, c.val)
		} else {
			b.WriteString(" " + colRef(qualifier, c.col) + " " + c.op + " ? ")
			params = append(params, c.val)
		}
	}
	return params
}

// whereSQL: the conditions without the WHERE keyword.
func (q *Query) whereSQL(qualifier string) (string, []any) {
	if len(q.and) == 0 {
		return "", nil
	}
	var b strings.Builder
	params := q.bind(&b, nil, q.and, "AND", qualifier)
	if len(q.or) > 0 {
		b.WriteString(" OR ")
		params = q.bind(&b, params, q.or, "OR", qualifier)
	}
	return strings.TrimSpace(b.String()), params
}

func (q *Query) tail(qualifier string) string {
	var b strings.Builder
	for i, o := range q.order {
		if i == 0 {
			b.WriteString(" ORDER BY ")
		} else {
			b.WriteString(", ")
		}
		b.WriteString(colRef(qualifier, o[0]) + " " + o[1])
	}
	if q.limit > 0 {
		b.WriteString(" LIMIT " + strconv.Itoa(q.limit))
	}
	if q.offset > 0 {
		b.WriteString(" OFFSET " + strconv.Itoa(q.offset))
	}
	return b.String()
}

func (q *Query) whereClause(qualifier string) (string, []any) {
	w, params := q.whereSQL(qualifier)
	if w == "" {
		return "", nil
	}
	return " WHERE " + w, params
}

// ToSQL is the SELECT the chain would run (MySQL form), without running it.
func (q *Query) ToSQL() (string, []any, error) {
	if q.err != nil {
		return "", nil, q.err
	}
	w, params := q.whereClause("")
	return "SELECT * FROM " + q.m.Table + w + q.tail(""), params, nil
}

// WithJoinSQL is the SELECT WithJoin(relations) would run: a LEFT JOIN per
// relation, related columns selected as <rel>__<col>.
func (q *Query) WithJoinSQL(relations ...string) (string, []any, error) {
	if q.err != nil {
		return "", nil, q.err
	}
	base := q.m.Table
	sel := selectList(base, q.m.Columns, "")
	var joins strings.Builder
	for _, name := range relations {
		rel, related, err := q.m.relation(name)
		if err != nil {
			return "", nil, err
		}
		alias := "r_" + name
		sel = append(sel, selectList(alias, related.Columns, name)...)
		joins.WriteString(" LEFT JOIN " + rel.Table + " " + Ident(alias) + " ON " +
			Ident(base) + "." + Ident(rel.Name) + " = " + Ident(alias) + "." + Ident(rel.Key))
	}
	w, params := q.whereClause(base)
	return "SELECT " + strings.Join(sel, ", ") + " FROM " + q.m.Table + " " + Ident(base) +
		joins.String() + w + q.tail(base), params, nil
}

func selectList(alias string, cols []string, prefix string) []string {
	if len(cols) == 0 {
		return []string{Ident(alias) + ".*"}
	}
	out := make([]string, len(cols))
	for i, c := range cols {
		as := c
		if prefix != "" {
			as = prefix + "__" + c
		}
		out[i] = Ident(alias) + "." + Ident(c) + " AS " + Ident(as)
	}
	return out
}

// asList reports whether v is a list of values (any slice except []byte).
func asList(v any) ([]any, bool) {
	switch l := v.(type) {
	case []any:
		return l, true
	case []byte:
		return nil, false
	case nil:
		return nil, false
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, false
	}
	out := make([]any, rv.Len())
	for i := range out {
		out[i] = rv.Index(i).Interface()
	}
	return out, true
}

var errNoDB = errors.New("model: no database; call model.Use(db, model.MySQL or model.Postgres) first")
