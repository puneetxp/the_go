// Package model is the framework's model layer for Go: queries are built as a
// chain on a model, relations are used by name, and the SQL is the same as the
// Deno and PHP adapters build for the same chain (see spec/README.md).
//
//	rows, err := BookStock.Where(model.W{"publish": 1}).
//		WhereHas("book", model.W{"public_listing": 1}).
//		WhereHas("product", func(p *model.Query) *model.Query { return p.Where(model.W{"enable": 1}) }).
//		OrderBy("id", "ASC").Get(ctx)
package model

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// W is a set of column conditions: a value means col IN (?), a slice means
// col IN (?, ?, ...). Columns are emitted in name order, so the SQL never
// depends on map order.
type W map[string]any

// Relation is one entry of a model's relations map: rows of Model (table
// Table) whose column Key equals this model's column Name.
type Relation struct {
	Model string // related model's name, as given to Define
	Table string
	Name  string // column on this model
	Key   string // column on the related model
}

// Model is a table definition. Define it once (compile-php generates these)
// and start queries from it.
type Model struct {
	Name      string
	Table     string
	Columns   []string
	Fillable  []string
	Relations map[string]Relation
}

var (
	registryMu sync.RWMutex
	registry   = map[string]*Model{}
)

// Define registers a model by name so relations can refer to it, and returns it.
func Define(m *Model) *Model {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[m.Name] = m
	return m
}

// Lookup returns the model registered under name, or nil.
func Lookup(name string) *Model {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registry[name]
}

// Models returns every defined model, in name order.
func Models() []*Model {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]*Model, 0, len(registry))
	for _, name := range sortedKeys(registry) {
		out = append(out, registry[name])
	}
	return out
}

// Query starts an empty query on the model.
func (m *Model) Query() *Query { return &Query{m: m} }

// The chain can start on the model itself: Item.Where(...).Get(ctx).

func (m *Model) Where(w W) *Query                    { return m.Query().Where(w) }
func (m *Model) WhereC(col, op string, v any) *Query { return m.Query().WhereC(col, op, v) }
func (m *Model) WhereHas(rel string, where ...any) *Query {
	return m.Query().WhereHas(rel, where...)
}
func (m *Model) WhereNotHas(rel string, where ...any) *Query {
	return m.Query().WhereNotHas(rel, where...)
}
func (m *Model) OrderBy(col, dir string) *Query { return m.Query().OrderBy(col, dir) }
func (m *Model) Limit(count, offset int) *Query { return m.Query().Limit(count, offset) }

func (m *Model) relation(name string) (Relation, *Model, error) {
	rel, ok := m.Relations[name]
	if !ok {
		return rel, nil, fmt.Errorf("%s: no relation %q (has: %s)", m.Name, name, strings.Join(sortedKeys(m.Relations), ", "))
	}
	related := Lookup(rel.Model)
	if related == nil {
		return rel, nil, fmt.Errorf("%s: relation %q names model %q, which is not defined", m.Name, name, rel.Model)
	}
	return rel, related, nil
}

// writable keeps the columns that may be written: Fillable, else Columns.
func (m *Model) writable(data W) W {
	allowed := m.Fillable
	if len(allowed) == 0 {
		allowed = m.Columns
	}
	out := W{}
	for _, c := range allowed {
		if v, ok := data[c]; ok {
			out[c] = v
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
