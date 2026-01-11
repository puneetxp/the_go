# The Go Library

A standard library for Golang applications, ported from `the_lib` (PHP) and `the_deno`. Designed to speed up backend development with standard utilities.

## Features

- **sqlbuilder**: Dynamic SQL query builder with `Left Join` support.
- **model**: Base Model struct for ORM-like database interactions.
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

### 1. Models & Joins

```go
import (
    "github.com/puneetxp/the_go/utils/model"
    "github.com/puneetxp/the_go/utils/sqlbuilder"
)

// Init model
m := &model.Model{
    Table: "users",
    DB:    dbConnection,
}

// Perform Join
m.Join(map[string]interface{}{
    "roles": map[string]interface{}{
        "table": "roles",
        "key":   "id",
        "name":  "role_id",
    },
}, nil)
```

### 2. File Upload

```go
import "github.com/puneetxp/the_go/utils/file"

f := file.Init("../storage")
result := f.Upload(multipartFile, header, "custom_name")
```
