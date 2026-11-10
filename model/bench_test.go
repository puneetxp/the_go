package model

// The model layer against bare database/sql on the same query.
//   DBTEST=1 DBHOST=127.0.0.1 DBPORT=3306 DBUSER=root DBNAME=the_test go test -run x -bench . ./model/

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

var (
	bCategory = Define(&Model{Name: "b_category", Table: "bench_categories", Columns: []string{"id", "title"}})
	bItem     = Define(&Model{Name: "b_item", Table: "bench_items", Columns: []string{"id", "name", "enable", "category_id"},
		Relations: map[string]Relation{"category": {Model: "b_category", Table: "bench_categories", Name: "category_id", Key: "id"}}})
)

func benchSetup(b *testing.B) {
	if d := openTestDB(b); d == Postgres {
		b.Skip("benchmark uses MySQL DDL")
	}
	ctx := context.Background()
	for _, s := range []string{
		"DROP TABLE IF EXISTS bench_items", "DROP TABLE IF EXISTS bench_categories",
		"CREATE TABLE bench_categories (id bigint unsigned AUTO_INCREMENT PRIMARY KEY, title varchar(50) NOT NULL)",
		"CREATE TABLE bench_items (id bigint unsigned AUTO_INCREMENT PRIMARY KEY, name varchar(50) NOT NULL, enable tinyint(1) NOT NULL DEFAULT 1, category_id bigint unsigned NULL, KEY (category_id))",
		"INSERT INTO bench_categories (title) VALUES ('a'),('b'),('c')",
	} {
		if _, err := exec(ctx, s, nil); err != nil {
			b.Fatal(err)
		}
	}
	vals := make([]string, 1000)
	for i := range vals {
		vals[i] = fmt.Sprintf("('item %d', 1, %d)", i+1, 1+(i+1)%3)
	}
	if _, err := exec(ctx, "INSERT INTO bench_items (name, enable, category_id) VALUES "+strings.Join(vals, ","), nil); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkFind(b *testing.B) {
	benchSetup(b)
	ctx := context.Background()
	b.Run("bare database/sql", func(b *testing.B) {
		db := testDB
		for i := 0; i < b.N; i++ {
			rows, err := db.QueryContext(ctx, "SELECT * FROM bench_items WHERE `id` IN (?) LIMIT 1", 1+i%1000)
			if err != nil {
				b.Fatal(err)
			}
			var id, enable, cat int64
			var name string
			for rows.Next() {
				rows.Scan(&id, &name, &enable, &cat)
			}
			rows.Close()
		}
	})
	b.Run("model Find", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := bItem.Find(ctx, 1+i%1000); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("bare database/sql, whereHas query", func(b *testing.B) {
		db := testDB
		q := "SELECT * FROM bench_items WHERE `id` IN (?) AND `enable` IN (?) AND EXISTS (SELECT 1 FROM bench_categories AS `bench_categories__1` WHERE `bench_categories__1`.`id` = `bench_items`.`category_id` AND (`title` IN (?)))"
		for i := 0; i < b.N; i++ {
			rows, err := db.QueryContext(ctx, q, 1+i%1000, 1, "b")
			if err != nil {
				b.Fatal(err)
			}
			var id, enable, cat int64
			var name string
			for rows.Next() {
				rows.Scan(&id, &name, &enable, &cat)
			}
			rows.Close()
		}
	})
	b.Run("model Where+Where+WhereHas Get", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := bItem.Where(W{"id": 1 + i%1000}).Where(W{"enable": 1}).WhereHas("category", W{"title": "b"}).Get(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("model build SQL only", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			bItem.Where(W{"id": 1 + i%1000}).Where(W{"enable": 1}).WhereHas("category", W{"title": "b"}).ToSQL()
		}
	})
}
