package model

import (
	"regexp"
	"strconv"
	"strings"
)

// Dialect is the database the SQL is sent to. Queries are always built
// MySQL-style (? placeholders, backtick identifiers), the same in every
// adapter of the framework, and rewritten for PostgreSQL right before they run.
type Dialect int

const (
	MySQL Dialect = iota
	Postgres
)

// Ident quotes one identifier, MySQL-style.
func Ident(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

var dollarTag = regexp.MustCompile(`^\$([A-Za-z_][A-Za-z0-9_]*)?\$`)

// ToPostgres rewrites MySQL-style SQL for PostgreSQL: ? becomes $1, $2, ... and
// backtick identifiers become double-quoted. Strings, quoted identifiers,
// dollar-quoted strings and comments are left alone, so SQL already written
// with $n is unchanged.
func ToPostgres(sql string) string {
	var out strings.Builder
	out.Grow(len(sql) + 8)
	n := 0
	for i := 0; i < len(sql); {
		c := sql[i]
		switch {
		case c == '\'' || c == '"':
			j := i + 1
			for j < len(sql) {
				if sql[j] == c {
					if j+1 < len(sql) && sql[j+1] == c {
						j += 2
						continue
					}
					break
				}
				if c == '\'' && sql[j] == '\\' {
					j++
				}
				j++
			}
			end := min(j+1, len(sql))
			out.WriteString(sql[i:end])
			i = end
		case c == '`':
			j := i + 1
			var id strings.Builder
			for j < len(sql) {
				if sql[j] == '`' {
					if j+1 < len(sql) && sql[j+1] == '`' {
						id.WriteByte('`')
						j += 2
						continue
					}
					break
				}
				id.WriteByte(sql[j])
				j++
			}
			out.WriteString(`"` + strings.ReplaceAll(id.String(), `"`, `""`) + `"`)
			i = j + 1
		case c == '-' && i+1 < len(sql) && sql[i+1] == '-':
			end := strings.IndexByte(sql[i:], '\n')
			if end < 0 {
				end = len(sql)
			} else {
				end += i
			}
			out.WriteString(sql[i:end])
			i = end
		case c == '/' && i+1 < len(sql) && sql[i+1] == '*':
			end := strings.Index(sql[i+2:], "*/")
			if end < 0 {
				end = len(sql)
			} else {
				end += i + 4
			}
			out.WriteString(sql[i:end])
			i = end
		case c == '$':
			if tag := dollarTag.FindString(sql[i:]); tag != "" {
				end := strings.Index(sql[i+len(tag):], tag)
				if end < 0 {
					end = len(sql)
				} else {
					end += i + 2*len(tag)
				}
				out.WriteString(sql[i:end])
				i = end
			} else {
				out.WriteByte(c)
				i++
			}
		case c == '?':
			n++
			out.WriteString("$" + strconv.Itoa(n))
			i++
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String()
}
