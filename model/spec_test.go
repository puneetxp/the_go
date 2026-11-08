package model

// Runs spec/query_cases.json, the query builder cases every adapter of the
// framework must pass (same file as the_deno's and the_lib's).

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

type specFile struct {
	Models map[string]struct {
		Table     string   `json:"table"`
		Columns   []string `json:"columns"`
		Relations map[string]struct {
			Model, Table, Name, Key string
		} `json:"relations"`
	} `json:"models"`
	Cases []struct {
		Name   string  `json:"name"`
		Model  string  `json:"model"`
		Chain  [][]any `json:"chain"`
		SQL    string  `json:"sql"`
		Params []any   `json:"params"`
	} `json:"cases"`
	Errors []struct {
		Name    string  `json:"name"`
		Model   string  `json:"model"`
		Chain   [][]any `json:"chain"`
		Message string  `json:"message"`
	} `json:"errors"`
}

func loadSpec(t *testing.T) specFile {
	t.Helper()
	raw, err := os.ReadFile("../spec/query_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var s specFile
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	for name, m := range s.Models {
		rels := map[string]Relation{}
		for rn, r := range m.Relations {
			rels[rn] = Relation{Model: "spec_" + r.Model, Table: r.Table, Name: r.Name, Key: r.Key}
		}
		Define(&Model{Name: "spec_" + name, Table: m.Table, Columns: m.Columns, Relations: rels})
	}
	return s
}

func replay(q *Query, chain [][]any) *Query {
	for _, step := range chain {
		switch step[0] {
		case "where":
			if len(step) > 2 && step[2] == true {
				q.Reset()
			}
			q.Where(W(step[1].(map[string]any)))
		case "whereHas", "whereNotHas":
			var cond any
			if len(step) > 2 {
				switch c := step[2].(type) {
				case []any: // a nested chain
					steps := make([][]any, len(c))
					for i, s := range c {
						steps[i] = s.([]any)
					}
					cond = func(r *Query) *Query { return replay(r, steps) }
				case map[string]any:
					cond = W(c)
				}
			}
			if step[0] == "whereHas" {
				q.WhereHas(step[1].(string), cond)
			} else {
				q.WhereNotHas(step[1].(string), cond)
			}
		case "orderBy":
			dir := "ASC"
			if len(step) > 2 {
				dir = step[2].(string)
			}
			q.OrderBy(step[1].(string), dir)
		case "limit":
			offset := 0
			if len(step) > 2 {
				offset = int(step[2].(float64))
			}
			q.Limit(int(step[1].(float64)), offset)
		case "withJoin":
			// last step; sqlOf handles it
		default:
			panic("spec uses unknown step " + step[0].(string))
		}
	}
	return q
}

func sqlOf(model string, chain [][]any) (string, []any, error) {
	q := replay(Lookup("spec_"+model).Query(), chain)
	last := chain[len(chain)-1]
	if last[0] == "withJoin" {
		var rels []string
		for _, r := range last[1].([]any) {
			rels = append(rels, r.(string))
		}
		return q.WithJoinSQL(rels...)
	}
	return q.ToSQL()
}

var spaces = regexp.MustCompile(`\s+`)

func TestSpec(t *testing.T) {
	s := loadSpec(t)
	for _, c := range s.Cases {
		t.Run(c.Name, func(t *testing.T) {
			sql, params, err := sqlOf(c.Model, c.Chain)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(spaces.ReplaceAllString(sql, " ")); got != c.SQL {
				t.Errorf("sql\n got: %s\nwant: %s", got, c.SQL)
			}
			want := c.Params
			if want == nil {
				want = []any{}
			}
			if params == nil {
				params = []any{}
			}
			if !reflect.DeepEqual(params, want) {
				t.Errorf("params got %#v want %#v", params, want)
			}
			again, _, _ := sqlOf(c.Model, c.Chain)
			if again != sql {
				t.Errorf("second run gave different SQL")
			}
		})
	}
	for _, e := range s.Errors {
		t.Run("error: "+e.Name, func(t *testing.T) {
			_, _, err := sqlOf(e.Model, e.Chain)
			if err == nil || !strings.Contains(err.Error(), e.Message) {
				t.Errorf("got error %v, want one containing %q", err, e.Message)
			}
		})
	}
}

func TestToPostgres(t *testing.T) {
	cases := map[string]string{
		"SELECT * FROM t WHERE `a` IN (?, ?) AND `b` = ?":           `SELECT * FROM t WHERE "a" IN ($1, $2) AND "b" = $3`,
		"SELECT 'it''s ? `x`', \"a?b\" FROM t -- why?\nWHERE x = ?": "SELECT 'it''s ? `x`', \"a?b\" FROM t -- why?\nWHERE x = $1",
		"SELECT /* ? */ $tag$ ? `y` $tag$, $$?$$, ?":                "SELECT /* ? */ $tag$ ? `y` $tag$, $$?$$, $1",
		`SELECT * FROM t WHERE "id" = $1`:                           `SELECT * FROM t WHERE "id" = $1`,
		"`we``ird`":                                                 `"we` + "`" + `ird"`,
	}
	for in, want := range cases {
		if got := ToPostgres(in); got != want {
			t.Errorf("ToPostgres(%q)\n got: %s\nwant: %s", in, got, want)
		}
	}
}

func TestMultiColumnWhereIsStable(t *testing.T) {
	m := &Model{Name: "stable", Table: "t"}
	first, _, _ := m.Where(W{"b": 1, "a": 2, "c": 3}).ToSQL()
	for i := 0; i < 50; i++ {
		if s, _, _ := m.Where(W{"b": 1, "a": 2, "c": 3}).ToSQL(); s != first {
			t.Fatalf("map order leaked into SQL: %s vs %s", s, first)
		}
	}
}
