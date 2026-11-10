package model

// The model layer on PostgreSQL: a pgx pool, or database/sql (PGX_STDLIB=1).
//   DBTEST=1 DBDRIVER=postgres DBHOST=127.0.0.1 DBPORT=5432 DBUSER=postgres \
//   DBNAME=the_test go test -run x -bench Postgres ./model/

import (
	"context"
	"testing"
)

var (
	pgCategory = Define(&Model{Name: "pg_category", Table: "bench_pg_categories", Columns: []string{"id", "title"}})
	pgItem     = Define(&Model{Name: "pg_item", Table: "bench_pg_items",
		Columns:   []string{"id", "name", "price", "qty", "created", "note", "enable", "category_id"},
		Fillable:  []string{"name", "price", "qty", "note", "enable", "category_id"},
		Relations: map[string]Relation{"category": {Model: "pg_category", Table: "bench_pg_categories", Name: "category_id", Key: "id"}}})
)

func BenchmarkPostgres(b *testing.B) {
	if d := openTestDB(b); d != Postgres {
		b.Skip("set DBDRIVER=postgres")
	}
	ctx := context.Background()
	for _, s := range []string{
		"DROP TABLE IF EXISTS bench_pg_items", "DROP TABLE IF EXISTS bench_pg_categories",
		"CREATE TABLE bench_pg_categories (id bigserial PRIMARY KEY, title text NOT NULL)",
		"CREATE TABLE bench_pg_items (id bigserial PRIMARY KEY, name text NOT NULL, price numeric(10,2), qty int, " +
			"created timestamptz DEFAULT now(), note text, enable smallint NOT NULL DEFAULT 1, category_id bigint)",
		"INSERT INTO bench_pg_categories (title) VALUES ('a'),('b'),('c')",
		"INSERT INTO bench_pg_items (name, price, qty, note, category_id) SELECT 'item ' || g, g * 1.25, g, repeat('lorem ', 10), 1 + g % 3 FROM generate_series(1, 1000) g",
	} {
		if _, err := exec(ctx, s, nil); err != nil {
			b.Fatal(err)
		}
	}
	b.Cleanup(func() {
		exec(ctx, "DROP TABLE IF EXISTS bench_pg_items", nil)
		exec(ctx, "DROP TABLE IF EXISTS bench_pg_categories", nil)
	})
	b.Run("Find", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := pgItem.Find(ctx, 1+i%1000); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Where+WhereHas Get", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := pgItem.Where(W{"id": 1 + i%1000}).Where(W{"enable": 1}).WhereHas("category", W{"title": "b"}).Get(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Get 1000 rows", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			rows, err := pgItem.Get(ctx)
			if err != nil || len(rows) != 1000 {
				b.Fatal(err, len(rows))
			}
		}
	})
	b.Run("Insert", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := pgItem.Insert(ctx, []W{{"name": "new", "price": "1.50", "qty": i, "note": "x", "category_id": 1}}); err != nil {
				b.Fatal(err)
			}
		}
	})
}
