package model

// Models against a real database: PostgreSQL with DBDRIVER=postgres,
// MySQL/MariaDB otherwise. Creates and drops the_t_items / the_t_categories.
//   DBTEST=1 DBHOST=127.0.0.1 DBPORT=3306 DBUSER=root DBNAME=the_test go test ./model/
//   DBTEST=1 DBDRIVER=postgres DBHOST=127.0.0.1 DBPORT=5432 DBUSER=postgres DBNAME=the_test go test ./model/
// On PostgreSQL the models run on a pgx pool; PGX_STDLIB=1 runs them on
// database/sql instead.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// testDB is the database/sql pool of the last openTestDB, or nil with pgxpool.
var testDB *sql.DB

// openTestDB connects every model to the test database. PostgreSQL uses a
// pgx pool, or database/sql with PGX_STDLIB=1. PGLITE=1: PGlite serves one
// connection, and all connections share one session, so each one drops the
// prepared statements an earlier one left (pgx names them the same).
func openTestDB(t testing.TB) Dialect {
	t.Helper()
	if os.Getenv("DBTEST") != "1" {
		t.Skip("set DBTEST=1 to run against a database")
	}
	env := func(k, d string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return d
	}
	testDB = nil
	if env("DBDRIVER", "mysql") == "postgres" {
		url := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
			env("DBUSER", "postgres"), env("DBPWD", ""), env("DBHOST", "127.0.0.1"), env("DBPORT", "5432"), env("DBNAME", "postgres"))
		pglite := os.Getenv("PGLITE") == "1"
		if os.Getenv("PGX_STDLIB") == "1" {
			db, err := sql.Open("pgx", url)
			if err != nil {
				t.Fatal(err)
			}
			if pglite {
				db.SetMaxOpenConns(1)
				db.Exec("DEALLOCATE ALL")
			}
			t.Cleanup(func() { db.Close() })
			testDB = db
			Use(db, Postgres)
			return Postgres
		}
		cfg, err := pgxpool.ParseConfig(url)
		if err != nil {
			t.Fatal(err)
		}
		if pglite {
			cfg.MaxConns = 1
			cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
				_, err := c.Exec(ctx, "DEALLOCATE ALL", pgx.QueryExecModeSimpleProtocol)
				return err
			}
		}
		pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		UsePgx(pool)
		return Postgres
	}
	db, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?interpolateParams=true&parseTime=true",
		env("DBUSER", "root"), env("DBPWD", ""), env("DBHOST", "127.0.0.1"), env("DBPORT", "3306"), env("DBNAME", "the_test")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	testDB = db
	Use(db, MySQL)
	return MySQL
}

var (
	tCategory = Define(&Model{Name: "t_category", Table: "the_t_categories", Columns: []string{"id", "title"}, Fillable: []string{"title"},
		Relations: map[string]Relation{"item": {Model: "t_item", Table: "the_t_items", Name: "id", Key: "category_id"}}})
	tItem = Define(&Model{Name: "t_item", Table: "the_t_items", Columns: []string{"id", "name", "order", "enable", "price", "category_id"},
		Fillable:  []string{"name", "order", "enable", "price", "category_id"},
		Relations: map[string]Relation{"category": {Model: "t_category", Table: "the_t_categories", Name: "category_id", Key: "id"}}})
)

func names(rows []Row) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r["name"].(string))
	}
	return out
}

func TestDB(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	run := func(s string) {
		if _, err := exec(ctx, s, nil); err != nil {
			t.Fatal(err)
		}
	}
	serial, fk := "bigint unsigned AUTO_INCREMENT", "bigint unsigned"
	if d == Postgres {
		serial, fk = "bigserial", "bigint"
	}
	run("DROP TABLE IF EXISTS the_t_items")
	run("DROP TABLE IF EXISTS the_t_categories")
	run("CREATE TABLE the_t_categories (id " + serial + " PRIMARY KEY, title varchar(100) NOT NULL)")
	run("CREATE TABLE the_t_items (id " + serial + " PRIMARY KEY, name varchar(100) NOT NULL, `order` int NOT NULL DEFAULT 0, " +
		"enable smallint NOT NULL DEFAULT 1, price numeric(10,2) NOT NULL DEFAULT 0, category_id " + fk + " NULL REFERENCES the_t_categories(id))")
	t.Cleanup(func() {
		exec(ctx, "DROP TABLE IF EXISTS the_t_items", nil)
		exec(ctx, "DROP TABLE IF EXISTS the_t_categories", nil)
	})
	must := func(v any, err error) any {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	eq := func(name string, got, want any) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %#v, want %#v", name, got, want)
		}
	}

	tea := must(tCategory.Create(ctx, W{"title": "Tea", "id": 999})).(Row)
	must(tCategory.Create(ctx, W{"title": "Empty"}))
	_, isInt := tea["id"].(int64)
	eq("create returns the saved row with an int64 id", []any{isInt, tea["title"], tea["id"] != int64(999)}, []any{true, "Tea", true})

	a := must(tItem.Create(ctx, W{"name": "Assam", "order": 2, "price": "120.50", "category_id": tea["id"]})).(Row)
	eq("reserved column `order`, DECIMAL as string", []any{a["order"], a["price"], a["enable"]}, []any{int64(2), "120.50", int64(1)})

	ids := must(tItem.Insert(ctx, []W{{"name": "Darjeeling", "order": 1}, {"name": "Nilgiri", "order": 3}, {"name": "Kangra", "order": 4}})).([]any)
	eq("insert many returns every id", len(ids), 3)
	eq("ids read back in order", names(must(tItem.Where(W{"id": ids}).OrderBy("id", "ASC").Get(ctx)).([]Row)), []string{"Darjeeling", "Nilgiri", "Kangra"})

	eq("chained where", names(must(tItem.Where(W{"name": []string{"Assam", "Kangra"}}).Where(W{"order": 4}).Get(ctx)).([]Row)), []string{"Kangra"})
	eq("WhereC operator", len(must(tItem.WhereC("order", ">", 2).Get(ctx)).([]Row)), 2)
	eq("find by column", must(tItem.Find(ctx, "Nilgiri", "name")).(Row)["order"], int64(3))
	eq("find missing is nil", must(tItem.Find(ctx, -1)).(Row) == nil, true)

	eq("update", must(tItem.Where(W{"name": "Nilgiri"}).Update(ctx, W{"name": "Nilgiri FTGFOP", "id": 5000})), int64(1))
	eq("update ignored non-fillable id", must(tItem.Find(ctx, "Nilgiri FTGFOP", "name")).(Row)["order"], int64(3))

	eq("whereHas", names(must(tItem.WhereHas("category", W{"title": "Tea"}).Get(ctx)).([]Row)), []string{"Assam"})
	withAssam := must(tCategory.WhereHas("item", func(i *Query) *Query { return i.Where(W{"name": "Assam"}) }).Get(ctx)).([]Row)
	eq("whereHas with a chain", withAssam[0]["title"], "Tea")
	empty := must(tCategory.WhereNotHas("item").Get(ctx)).([]Row)
	eq("whereNotHas", []any{len(empty), empty[0]["title"]}, []any{1, "Empty"})
	eq("orderBy + limit", names(must(tItem.OrderBy("order", "DESC").Limit(2, 0).Get(ctx)).([]Row)), []string{"Kangra", "Nilgiri FTGFOP"})
	eq("count", must(tItem.WhereC("order", ">=", 2).Count(ctx)), int64(3))

	j := must(tItem.WhereHas("category", W{"title": "Tea"}).OrderBy("id", "ASC").WithJoin(ctx, "category")).([]Row)
	eq("withJoin nests the relation", []any{len(j), j[0]["name"], j[0]["category"].(Row)["title"]}, []any{1, "Assam", "Tea"})

	eq("toggle", must(tItem.Where(W{"name": "Assam"}).Toggle(ctx)), int64(1))
	eq("toggled to 0", must(tItem.Find(ctx, "Assam", "name")).(Row)["enable"], int64(0))

	eq("delete", must(tItem.Where(W{"name": "Darjeeling"}).Delete(ctx)), int64(1))
	eq("empty list matches nothing", len(must(tItem.Where(W{"id": []int{}}).Get(ctx)).([]Row)), 0)
	_, err := tItem.WhereHas("nope").Get(ctx)
	eq("unknown relation is an error", err != nil, true)
}
