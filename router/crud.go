package router

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/puneetxp/the_go/model"
)

// Crud serves a model's CRUD routes, the same set the_deno compiles from a
// crud entry. Can holds the letters:
//
//	a  GET    ""                 all rows (?latest=<time>: only rows updated since)
//	r  GET    "/:id"             one row, 404 when missing
//	c  POST   ""                 create, returns the row
//	w  POST   "/where"           rows matching the JSON body's columns
//	u  PATCH  "/:id" (and PUT)   update, returns the row
//	p  PATCH  "" (and PUT)       upsert {"data": [rows]}
//	d  DELETE "/:id"             delete (soft when the model has deleted_at),
//	   DELETE "/perma_delete/:id"  hard delete, isuper only
//
// Owner or Under scope every route to the logged-in user, like the Deno
// generator's scoped controllers: someone else's row is a 404, never a 403,
// so ids can't be probed. A column named "password" is never sent.
type Crud struct {
	Model *model.Model
	Can   string
	Owner string // rows whose Owner column is the user's id
	Under *Under // rows of a parent (in the URL) that the user owns
}

// Under: the URL names a parent row, e.g. /book/:book_id/client, which must
// belong to the user (Parent's Owner column is the user's id). Param is the
// URL parameter and this model's column, e.g. "book_id".
type Under struct {
	Parent *model.Model
	Param  string
	Owner  string
}

func (cr *Crud) routes() ([]Route, error) {
	if cr.Model == nil {
		return nil, fmt.Errorf("crud has no model")
	}
	if cr.Under != nil && (cr.Under.Parent == nil || cr.Under.Param == "" || cr.Under.Owner == "") {
		return nil, fmt.Errorf("%s: Under needs Parent, Param and Owner", cr.Model.Name)
	}
	for _, l := range cr.Can {
		if !strings.ContainsRune("acrwupd", l) {
			return nil, fmt.Errorf("%s: unknown crud letter %q", cr.Model.Name, l)
		}
	}
	has := func(l string) bool { return strings.Contains(cr.Can, l) }
	var rs []Route
	add := func(method, path string, h Handler, roles ...string) {
		rs = append(rs, Route{Method: method, Path: path, Handler: h, Roles: roles})
	}
	if has("a") {
		add("GET", "", cr.all)
	}
	if has("r") {
		add("GET", "/:id", cr.show)
	}
	if has("c") {
		add("POST", "", cr.store)
	}
	if has("w") {
		add("POST", "/where", cr.where)
	}
	if has("p") {
		add("PATCH", "", cr.upsert)
		add("PUT", "", cr.upsert)
	}
	if has("u") {
		add("PATCH", "/:id", cr.update)
		add("PUT", "/:id", cr.update)
	}
	if has("d") {
		add("DELETE", "/:id", cr.delete)
		add("DELETE", "/perma_delete/:id", cr.permaDelete, "isuper")
	}
	return rs, nil
}

// scope is the condition every query of this route carries; ok is false
// when a response (401/404) was already sent.
func (cr *Crud) scope(c *Ctx) (model.W, bool) {
	if cr.Owner == "" && cr.Under == nil {
		return model.W{}, true
	}
	if c.Login == nil {
		c.JSON(http.StatusUnauthorized, "Login First")
		return nil, false
	}
	if cr.Owner != "" {
		return model.W{cr.Owner: c.Login.ID}, true
	}
	id, err := strconv.ParseInt(c.Param(cr.Under.Param), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusNotFound, "Not Found")
		return nil, false
	}
	parent, err := cr.Under.Parent.Where(model.W{"id": id, cr.Under.Owner: c.Login.ID}).First(c.Context())
	if err != nil || parent == nil {
		if err != nil {
			c.rt.opts.Log.Printf("[SCOPE ERROR] %v", err)
		}
		c.JSON(http.StatusNotFound, "Not Found")
		return nil, false
	}
	return model.W{cr.Under.Param: id}, true
}

func merge(a, b model.W) model.W {
	out := model.W{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func hidden(rows []model.Row) []model.Row {
	for _, r := range rows {
		delete(r, "password")
	}
	return rows
}

// body reads a JSON object; JSON numbers become int64 when whole, else
// stay as their decimal text, so DECIMAL values keep every digit.
func body(c *Ctx) (model.W, error) {
	var raw map[string]any
	if err := c.Bind(&raw); err != nil {
		return nil, err
	}
	return model.W(numbers(raw).(map[string]any)), nil
}

func numbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n
		}
		return x.String()
	case map[string]any:
		for k, e := range x {
			x[k] = numbers(e)
		}
	case []any:
		for i, e := range x {
			x[i] = numbers(e)
		}
	}
	return v
}

func (cr *Crud) bad(c *Ctx, err error) error {
	return c.JSON(http.StatusUnprocessableEntity, "Invalid JSON body: "+err.Error())
}

func (cr *Crud) all(c *Ctx) error {
	scope, ok := cr.scope(c)
	if !ok {
		return nil
	}
	q := cr.Model.Where(scope)
	if latest := c.R.URL.Query().Get("latest"); latest != "" && cr.Model.HasColumn("updated_at") {
		if t, err := time.Parse(time.RFC3339, latest); err == nil {
			q.WhereC("updated_at", ">", t)
		}
	}
	rows, err := q.Get(c.Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, hidden(rows))
}

func (cr *Crud) where(c *Ctx) error {
	scope, ok := cr.scope(c)
	if !ok {
		return nil
	}
	b, err := body(c)
	if err != nil {
		return cr.bad(c, err)
	}
	// Only the model's own columns: a filter can't probe columns the model
	// doesn't list.
	filters := model.W{}
	for k, v := range b {
		if cr.Model.HasColumn(k) && k != "password" {
			filters[k] = v
		}
	}
	rows, err := cr.Model.Where(merge(filters, scope)).Get(c.Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, hidden(rows))
}

func (cr *Crud) find(c *Ctx, scope model.W) (model.Row, error) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return nil, nil
	}
	return cr.Model.Where(merge(model.W{"id": id}, scope)).First(c.Context())
}

func (cr *Crud) show(c *Ctx) error {
	scope, ok := cr.scope(c)
	if !ok {
		return nil
	}
	row, err := cr.find(c, scope)
	if err != nil {
		return err
	}
	if row == nil {
		return c.JSON(http.StatusNotFound, "Not Found")
	}
	return c.JSON(http.StatusOK, hidden([]model.Row{row})[0])
}

func (cr *Crud) store(c *Ctx) error {
	scope, ok := cr.scope(c)
	if !ok {
		return nil
	}
	b, err := body(c)
	if err != nil {
		return cr.bad(c, err)
	}
	row, err := cr.Model.Create(c.Context(), merge(b, scope))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, hidden([]model.Row{row})[0])
}

func (cr *Crud) update(c *Ctx) error {
	scope, ok := cr.scope(c)
	if !ok {
		return nil
	}
	row, err := cr.find(c, scope)
	if err != nil {
		return err
	}
	if row == nil {
		return c.JSON(http.StatusNotFound, "Not Found")
	}
	b, err := body(c)
	if err != nil {
		return cr.bad(c, err)
	}
	delete(b, "id")
	if _, err := cr.Model.Where(merge(model.W{"id": row["id"]}, scope)).Update(c.Context(), merge(b, scope)); err != nil {
		return err
	}
	saved, err := cr.Model.Find(c.Context(), row["id"])
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, hidden([]model.Row{saved})[0])
}

func (cr *Crud) upsert(c *Ctx) error {
	scope, ok := cr.scope(c)
	if !ok {
		return nil
	}
	var in struct {
		Data []map[string]any `json:"data"`
	}
	if err := c.Bind(&in); err != nil {
		return cr.bad(c, err)
	}
	rows := make([]model.W, len(in.Data))
	for i, r := range in.Data {
		rows[i] = merge(model.W(numbers(r).(map[string]any)), scope)
	}
	if len(scope) == 0 {
		n, err := cr.Model.Upsert(c.Context(), rows)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, map[string]int64{"affected": n})
	}
	// Scoped: a row with an id may only update the user's own row; an upsert
	// keyed on id alone would overwrite anyone's.
	var n int64
	for _, r := range rows {
		id, hasID := r["id"]
		if !hasID {
			if _, err := cr.Model.Create(c.Context(), r); err != nil {
				return err
			}
			n++
			continue
		}
		delete(r, "id")
		got, err := cr.Model.Where(merge(model.W{"id": id}, scope)).Update(c.Context(), r)
		if err != nil {
			return err
		}
		n += got
	}
	return c.JSON(http.StatusOK, map[string]int64{"affected": n})
}

func (cr *Crud) delete(c *Ctx) error {
	scope, ok := cr.scope(c)
	if !ok {
		return nil
	}
	row, err := cr.find(c, scope)
	if err != nil {
		return err
	}
	if row == nil {
		return c.JSON(http.StatusNotFound, "Not Found")
	}
	q := cr.Model.Where(merge(model.W{"id": row["id"]}, scope))
	var n int64
	if cr.Model.HasColumn("deleted_at") {
		n, err = q.Update(c.Context(), model.W{"deleted_at": time.Now()})
	} else {
		n, err = q.Delete(c.Context())
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]int64{"affected": n})
}

func (cr *Crud) permaDelete(c *Ctx) error {
	scope, ok := cr.scope(c)
	if !ok {
		return nil
	}
	row, err := cr.find(c, scope)
	if err != nil {
		return err
	}
	if row == nil {
		return c.JSON(http.StatusNotFound, "Not Found")
	}
	n, err := cr.Model.Where(merge(model.W{"id": row["id"]}, scope)).Delete(c.Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]int64{"affected": n})
}
