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

### 3. Routes and auth

The same route tree as the_deno, on the standard library's `http.ServeMux`
(no router dependency):

```go
r, err := router.New(append([]router.Route{
    {Islogin: true, Child: []router.Route{
        {Path: "/islogin/book", Crud: &router.Crud{Model: models.Book, Can: "acrudw", Owner: "user_id"}},
        {Path: "/islogin/book/:book_id/client", Crud: &router.Crud{Model: models.Client, Can: "acrud",
            Under: &router.Under{Parent: models.Book, Param: "book_id", Owner: "user_id"}}},
        {Path: "/isuper", Roles: []string{"isuper"}, Child: isuperRoutes},
    }},
}, auth.Routes()...), router.Options{CORS: true, Origins: []string{"https://app.example.com"}})
http.ListenAndServe(":9000", r)
```

**Routes:**
- `Islogin`, `Roles` and `Guard` pass down to child routes, and `Group` maps a method to its routes.
- A wrong path or method gets a JSON 404, and a handler panic gets a JSON 500.

**`Crud`:** the letters `a r c w u p d` give the same URLs as the_deno, including `/where` and `/perma_delete/:id` (isuper only).
- `Owner` limits rows to the logged-in user, and `Under` to a parent row in the URL that the user owns.
- Someone else's row is a 404.
- Passwords are never sent, and `/where` filters only on the model's columns.

**Sessions:** server-side, in the `PHPSESSID` cookie (as in the Deno and PHP adapters), bound to the user agent.
- `MemoryStore` is the default; plug in a shared `session.Store` when several processes serve one app.
- API keys can use `Options.Bearer`.

**Auth:** `auth.Routes()` gives `GET`/`POST /login`, `POST /register`, `GET /logout` and `GET /profile`, using the app's `user`, `active_role` and `role` models.
- Passwords use bcrypt; old SHA3 hashes are upgraded at login (see below).
- User 1 passes every role check, as in the other adapters.

**Generated apps:** compile-php writes `go/routes/generated.go` from the models' `crud` entries, with the same 503 routes and access rules as its Deno output for the_billing. `main.go`, `routes/routes.go` and `routes/custom.go` are the app's own and are never overwritten.

## Passwords and CORS (the same in every adapter)

`spec/auth_cases.json` holds the cases for both. The file is the same in the_deno, the_lib and the_go, and each adapter's tests read it.

**Passwords:** bcrypt, cost 10. A hash made by any adapter logs in through the others.
- Old unsalted SHA3-256 hashes (the_deno base64, the_lib hex) still log in, and are replaced by bcrypt at that login.
- Passwords over 72 bytes are refused at register, because bcrypt reads only the first 72 bytes.

**CORS:** a switch, off by default.

| Setting | Effect |
| --- | --- |
| `CORS` unset or `off` | no other site may call the API with cookies; same-origin setups need nothing |
| `CORS=on` | other sites may: any origin, for development |
| `CORS=on` with `CORS_ORIGINS=https://a.com,https://b.com` | only those origins, for production |
