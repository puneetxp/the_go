# The Go Library

A standard library for Golang applications, ported from `the_lib` (PHP) and `the_deno`. Designed to speed up backend development with standard utilities.

## Features

- **model**: models and queries as a chain (`Where`, `WhereHas`, `OrderBy`, `Limit`, `WithJoin`), on MySQL/MariaDB or PostgreSQL; the same SQL as the Deno and PHP adapters build.
- **auth**: Authentication helpers (Login, Register, Hashing).
- **file**: File upload and directory management (`FileAct`).
- **mail**: Email sending utilities.
- **response**: Standard JSON response formatting.
- **session**: Session management helpers.

## Installation

```bash
go get github.com/puneetxp/the_go
```

## Usage

### 1. Models and queries

```go
import (
    "database/sql"

    _ "github.com/go-sql-driver/mysql" // or _ "github.com/jackc/pgx/v5/stdlib"
    "github.com/puneetxp/the_go/model"
)

var Book = model.Define(&model.Model{Name: "book", Table: "books", Columns: []string{"id", "public_listing"},
    Relations: map[string]model.Relation{"book_stock": {Model: "book_stock", Table: "book_stocks", Name: "id", Key: "book_id"}}})

// interpolateParams=true: one round trip per query.
db, _ := sql.Open("mysql", "user:pwd(127.0.0.1:3306)/app?interpolateParams=true&parseTime=true")
model.Use(db, model.MySQL) // or model.Postgres with the pgx driver

rows, err := Book.Where(model.W{"public_listing": 1}).
    WhereHas("book_stock", func(s *model.Query) *model.Query {
        return s.Where(model.W{"publish": 1}).WhereHas("product", model.W{"enable": 1})
    }).
    OrderBy("id", "DESC").Limit(20, 0).Get(ctx)
```

Also `Find`, `First`, `Count`, `WithJoin(ctx, "rel")`, `Create`, `Insert`, `Update`, `Delete`, `Toggle` and `ToSQL()`.

Rows are `map[string]any`. Integers are `int64` and DECIMAL is `string` on both databases.

`spec/query_cases.json` is the contract every adapter (Deno, PHP, Go) passes, the same file in each. Tests:
- `go test ./model/` replays it.
- With `DBTEST=1` and the DB* variables, it also runs the models against a real database.

### 2. File Upload

```go
import "github.com/puneetxp/the_go/utils/file"

f := file.Init("../storage")
result := f.Upload(multipartFile, header, "custom_name")
```
