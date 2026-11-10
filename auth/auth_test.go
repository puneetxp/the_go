package auth_test

// Auth, roles and scoped CRUD over real HTTP against a real database:
// PostgreSQL with DBDRIVER=postgres, MySQL/MariaDB otherwise. Creates and drops
// the_a_* tables.
//   DBTEST=1 DBHOST=127.0.0.1 DBPORT=3306 DBUSER=root DBNAME=the_test go test ./auth/
// On PostgreSQL the models run on a pgx pool; PGX_STDLIB=1 uses database/sql.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/puneetxp/the_go/auth"
	"github.com/puneetxp/the_go/model"
	"github.com/puneetxp/the_go/router"
)

var (
	user = model.Define(&model.Model{Name: "user", Table: "the_a_users",
		Columns: []string{"id", "name", "email", "password", "enable"}, Fillable: []string{"name", "email", "password", "enable"}})
	_      = model.Define(&model.Model{Name: "role", Table: "the_a_roles", Columns: []string{"id", "name"}, Fillable: []string{"name"}})
	_      = model.Define(&model.Model{Name: "active_role", Table: "the_a_active_roles", Columns: []string{"id", "user_id", "role_id"}, Fillable: []string{"user_id", "role_id"}})
	book   = model.Define(&model.Model{Name: "book", Table: "the_a_books", Columns: []string{"id", "name", "user_id"}, Fillable: []string{"name", "user_id"}})
	client = model.Define(&model.Model{Name: "client", Table: "the_a_clients",
		Columns: []string{"id", "name", "book_id", "deleted_at"}, Fillable: []string{"name", "book_id", "deleted_at"}})
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func setup(t *testing.T) string {
	if os.Getenv("DBTEST") != "1" {
		t.Skip("set DBTEST=1 to run against a database")
	}
	pg := env("DBDRIVER", "mysql") == "postgres"
	// run executes setup SQL on whichever pool the models use; closeDB ends it.
	var run func(s string) error
	var closeDB func()
	switch {
	case pg && os.Getenv("PGX_STDLIB") != "1":
		// PostgreSQL on a pgx pool, as generated apps use it.
		cfg, err := pgxpool.ParseConfig(fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", env("DBUSER", "postgres"), env("DBPWD", ""), env("DBHOST", "127.0.0.1"), env("DBPORT", "5432"), env("DBNAME", "postgres")))
		if err != nil {
			t.Fatal(err)
		}
		if os.Getenv("PGLITE") == "1" {
			// One connection, one shared session: drop statements left by earlier ones.
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
		model.UsePgx(pool)
		run = func(s string) error { _, err := pool.Exec(context.Background(), s); return err }
		closeDB = pool.Close
	default:
		var db *sql.DB
		var err error
		if pg {
			db, err = sql.Open("pgx", fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", env("DBUSER", "postgres"), env("DBPWD", ""), env("DBHOST", "127.0.0.1"), env("DBPORT", "5432"), env("DBNAME", "postgres")))
			db.SetMaxOpenConns(1)
			model.Use(db, model.Postgres)
		} else {
			db, err = sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?interpolateParams=true&parseTime=true", env("DBUSER", "root"), env("DBPWD", ""), env("DBHOST", "127.0.0.1"), env("DBPORT", "3306"), env("DBNAME", "the_test")))
			model.Use(db, model.MySQL)
		}
		if err != nil {
			t.Fatal(err)
		}
		run = func(s string) error { _, err := db.Exec(s); return err }
		closeDB = func() { db.Close() }
	}
	serial := "bigint unsigned AUTO_INCREMENT"
	if pg {
		serial = "bigserial"
	}
	tables := []string{"the_a_clients", "the_a_books", "the_a_active_roles", "the_a_roles", "the_a_users"}
	drop := func() {
		for _, tb := range tables {
			run("DROP TABLE IF EXISTS " + tb)
		}
	}
	drop()
	for _, s := range []string{
		"CREATE TABLE the_a_users (id " + serial + " PRIMARY KEY, name varchar(100) NOT NULL DEFAULT '', email varchar(100) NOT NULL UNIQUE, password varchar(200) NULL, enable smallint NOT NULL DEFAULT 1)",
		"CREATE TABLE the_a_roles (id " + serial + " PRIMARY KEY, name varchar(50) NOT NULL)",
		"CREATE TABLE the_a_active_roles (id " + serial + " PRIMARY KEY, user_id bigint NOT NULL, role_id bigint NOT NULL)",
		"CREATE TABLE the_a_books (id " + serial + " PRIMARY KEY, name varchar(100) NOT NULL, user_id bigint NOT NULL)",
		"CREATE TABLE the_a_clients (id " + serial + " PRIMARY KEY, name varchar(100) NOT NULL, book_id bigint NOT NULL, deleted_at timestamp NULL)",
	} {
		if err := run(s); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { drop(); closeDB() })

	under := &router.Under{Parent: book, Param: "book_id", Owner: "user_id"}
	rt, err := router.New(append([]router.Route{
		{Islogin: true, Child: []router.Route{
			{Path: "/islogin", Child: []router.Route{
				{Path: "/book", Crud: &router.Crud{Model: book, Can: "acrudwp", Owner: "user_id"}},
				{Path: "/book/:book_id/client", Crud: &router.Crud{Model: client, Can: "acrud", Under: under}},
			}},
			{Path: "/isuper", Roles: []string{"isuper"}, Child: []router.Route{
				{Path: "/book", Crud: &router.Crud{Model: book, Can: "a"}},
				{Path: "/user", Crud: &router.Crud{Model: user, Can: "aw"}},
				{Path: "/client", Crud: &router.Crud{Model: client, Can: "d"}},
			}},
		}},
	}, auth.Routes()...), router.Options{})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(rt)
	t.Cleanup(srv.Close)
	return srv.URL
}

type browser struct {
	t    *testing.T
	base string
	c    *http.Client
}

func newBrowser(t *testing.T, base string) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t, base, &http.Client{Jar: jar}}
}

func (b *browser) call(method, path string, body any) (int, any) {
	b.t.Helper()
	var r *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		r = bytes.NewReader(raw)
	} else {
		r = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, b.base+path, r)
	res, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	var out any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func mustHash(p string) string {
	h, err := auth.Hash(p)
	if err != nil {
		panic(err)
	}
	return h
}

func m(v any) map[string]any { x, _ := v.(map[string]any); return x }
func n(v any) int64 {
	f, _ := v.(float64)
	return int64(f)
}

func TestAuthAndScopedCrud(t *testing.T) {
	base := setup(t)
	ctx := context.Background()
	alice, bob, anon := newBrowser(t, base), newBrowser(t, base), newBrowser(t, base)
	check := func(name string, gotStatus int, gotBody any, wantStatus int, want func(any) bool) {
		t.Helper()
		if gotStatus != wantStatus || (want != nil && !want(gotBody)) {
			t.Errorf("%s: got %d %v, want %d", name, gotStatus, gotBody, wantStatus)
		}
	}
	is := func(v any) func(any) bool { return func(got any) bool { return fmt.Sprint(got) == fmt.Sprint(v) } }

	// User 1 passes every role check (as in the Deno and PHP adapters), so the
	// install's root account comes first, before anyone registers.
	user.Create(ctx, model.W{"name": "Root", "email": "root@x.in", "password": mustHash("r00t")})

	// ---- auth
	s, b := alice.call("POST", "/register", map[string]string{"name": "Alice", "email": "alice@x.in", "password": "pa55word"})
	check("register", s, b, 200, func(v any) bool { return m(v)["email"] == "alice@x.in" })
	aliceID := n(m(b)["id"])
	s, b = anon.call("POST", "/register", map[string]string{"email": "alice@x.in", "password": "another-pw"})
	check("email taken", s, b, 422, func(v any) bool { return m(v)["email"] == "Email Already Taken" })
	s, b = anon.call("POST", "/register", map[string]string{"email": "", "password": ""})
	check("register needs email and password", s, b, 422, nil)
	stored, _ := user.Find(ctx, "alice@x.in", "email")
	if ok, rehash := auth.Verify("pa55word", fmt.Sprint(stored["password"])); !ok || rehash || !strings.HasPrefix(fmt.Sprint(stored["password"]), "$2a$10$") {
		t.Errorf("register should store bcrypt cost 10, got %v", stored["password"])
	}
	hindi := "मेरा पासवर्ड बहुत लंबा और सुरक्षित है" // 99 bytes: over bcrypt's 72
	s, _ = newBrowser(t, base).call("POST", "/register", map[string]string{"email": "hindi@x.in", "password": hindi})
	check("register a long Hindi password", s, nil, 200, nil)
	s, _ = newBrowser(t, base).call("POST", "/login", map[string]string{"email": "hindi@x.in", "password": hindi})
	check("and log in with it", s, nil, 200, nil)
	s, _ = newBrowser(t, base).call("POST", "/login", map[string]string{"email": "hindi@x.in", "password": hindi[:72]})
	check("its first 72 bytes are not enough", s, nil, 401, nil)
	s, b = anon.call("POST", "/register", map[string]string{"email": "long@x.in", "password": strings.Repeat("x", 129)})
	check("register refuses over 128 characters, saying nothing about hashing", s, b, 422, func(v any) bool { return m(v)["password"] == "Password is too long" })
	s, b = anon.call("POST", "/register", map[string]string{"email": "short@x.in", "password": "12345"})
	check("register refuses under 6 characters", s, b, 422, func(v any) bool { return m(v)["password"] == "Password must be at least 6 characters" })

	s, b = anon.call("POST", "/login", map[string]string{"email": "alice@x.in", "password": "wrong"})
	check("wrong password", s, b, 401, is("Password is Incorrect"))
	s, b = anon.call("POST", "/login", map[string]string{"email": "nobody@x.in", "password": "x"})
	check("unknown user", s, b, 404, is("User Not Found"))
	s, b = anon.call("GET", "/login", nil)
	check("status without login", s, b, 401, is("Login First"))
	s, b = alice.call("GET", "/login", nil)
	check("status after register", s, b, 200, func(v any) bool { return m(v)["email"] == "alice@x.in" })
	s, b = alice.call("GET", "/profile", nil)
	check("profile has no password", s, b, 200, func(v any) bool { _, has := m(v)["password"]; return !has && m(v)["name"] == "Alice" })

	// bob is an isuper; carol is disabled
	bobRow, _ := user.Create(ctx, model.W{"name": "Bob", "email": "bob@x.in", "password": mustHash("b0b")})
	role, _ := model.Lookup("role").Create(ctx, model.W{"name": "isuper"})
	model.Lookup("active_role").Create(ctx, model.W{"user_id": bobRow["id"], "role_id": role["id"]})
	user.Create(ctx, model.W{"name": "Carol", "email": "carol@x.in", "password": mustHash("c"), "enable": 0})
	s, b = anon.call("POST", "/login", map[string]string{"email": "carol@x.in", "password": "c"})
	check("disabled user", s, b, 403, is("This account has been disabled"))
	// PHP stores hex: those users log in too
	user.Create(ctx, model.W{"name": "Dave", "email": "dave@x.in", "password": "ab33dcadcb33e257f2a6fd6bd7aaca676ab6e9eaf89daa5dd1a97e744b306e04"})
	s, _ = newBrowser(t, base).call("POST", "/login", map[string]string{"email": "dave@x.in", "password": "s3cret-Pä55"})
	check("a PHP-hashed password logs in", s, nil, 200, nil)
	dave, _ := user.Find(ctx, "dave@x.in", "email")
	check("an old SHA3 hash is upgraded to bcrypt at login", 200, strings.HasPrefix(fmt.Sprint(dave["password"]), "$2a$10$"), 200, is(true))
	user.Create(ctx, model.W{"name": "Erin", "email": "erin@x.in", "password": "qzPcrcsz4lfypv1r16rKZ2q26er4napd0al+dEswbgQ="})
	s, _ = newBrowser(t, base).call("POST", "/login", map[string]string{"email": "erin@x.in", "password": "s3cret-Pä55"})
	check("a the_deno SHA3 (base64) password logs in", s, nil, 200, nil)
	s, _ = newBrowser(t, base).call("POST", "/login", map[string]string{"email": "dave@x.in", "password": "s3cret-Pä55"})
	check("and logs in again after the upgrade", s, nil, 200, nil)
	s, b = bob.call("POST", "/login", map[string]string{"email": "bob@x.in", "password": "b0b"})
	check("login loads roles", s, b, 200, func(v any) bool { return fmt.Sprint(m(v)["roles"]) == "[isuper]" })

	// ---- roles
	s, _ = alice.call("GET", "/isuper/book", nil)
	check("no isuper role", s, nil, 403, nil)
	s, _ = bob.call("GET", "/isuper/book", nil)
	check("isuper role", s, nil, 200, nil)

	// ---- owner scope
	s, b = alice.call("POST", "/islogin/book", map[string]any{"name": "Alice Traders", "user_id": 999})
	check("create forces the owner", s, b, 200, func(v any) bool { return n(m(v)["user_id"]) == aliceID })
	aliceBook := n(m(b)["id"])
	_, b = bob.call("POST", "/islogin/book", map[string]any{"name": "Bob Mart"})
	bobBook := n(m(b)["id"])
	s, b = alice.call("GET", "/islogin/book", nil)
	check("all lists only own rows", s, b, 200, func(v any) bool { l, _ := v.([]any); return len(l) == 1 && m(l[0])["name"] == "Alice Traders" })
	s, _ = alice.call("GET", fmt.Sprintf("/islogin/book/%d", bobBook), nil)
	check("someone else's row is a 404", s, nil, 404, nil)
	s, _ = alice.call("PATCH", fmt.Sprintf("/islogin/book/%d", bobBook), map[string]any{"name": "hijack"})
	check("can't update someone else's row", s, nil, 404, nil)
	s, _ = alice.call("DELETE", fmt.Sprintf("/islogin/book/%d", bobBook), nil)
	check("can't delete someone else's row", s, nil, 404, nil)
	s, b = alice.call("PATCH", "/islogin/book", map[string]any{"data": []map[string]any{{"id": bobBook, "name": "hijack", "user_id": aliceID}}})
	check("scoped upsert can't overwrite someone else's row", s, b, 200, func(v any) bool { return n(m(v)["affected"]) == 0 })
	still, _ := book.Find(ctx, bobBook)
	check("bob's book is untouched", 200, still["name"], 200, is("Bob Mart"))
	s, b = alice.call("PATCH", fmt.Sprintf("/islogin/book/%d", aliceBook), map[string]any{"name": "Alice & Co", "user_id": 999})
	check("update returns the saved row, owner kept", s, b, 200, func(v any) bool { return m(v)["name"] == "Alice & Co" && n(m(v)["user_id"]) == aliceID })
	s, b = alice.call("POST", "/islogin/book/where", map[string]any{"name": "Alice & Co"})
	check("where stays inside the scope", s, b, 200, func(v any) bool { l, _ := v.([]any); return len(l) == 1 })

	// ---- under scope
	s, b = alice.call("POST", fmt.Sprintf("/islogin/book/%d/client", aliceBook), map[string]any{"name": "Ravi", "book_id": bobBook})
	check("create under own book forces book_id", s, b, 200, func(v any) bool { return n(m(v)["book_id"]) == aliceBook })
	clientID := n(m(b)["id"])
	s, _ = alice.call("POST", fmt.Sprintf("/islogin/book/%d/client", bobBook), map[string]any{"name": "x"})
	check("can't add under someone else's book", s, nil, 404, nil)
	s, _ = alice.call("GET", fmt.Sprintf("/islogin/book/%d/client", bobBook), nil)
	check("can't list under someone else's book", s, nil, 404, nil)
	s, _ = bob.call("GET", fmt.Sprintf("/islogin/book/%d/client/%d", bobBook, clientID), nil)
	check("a client is only found under its own book", s, nil, 404, nil)
	s, _ = alice.call("GET", "/islogin/book/abc/client", nil)
	check("a non-numeric parent id is a 404", s, nil, 404, nil)
	s, b = alice.call("DELETE", fmt.Sprintf("/islogin/book/%d/client/%d", aliceBook, clientID), nil)
	check("delete is soft when there is deleted_at", s, b, 200, nil)
	soft, _ := client.Find(ctx, clientID)
	check("row kept with deleted_at", 200, soft != nil && soft["deleted_at"] != nil, 200, is(true))
	s, _ = alice.call("DELETE", fmt.Sprintf("/islogin/book/%d/client/perma_delete/%d", aliceBook, clientID), nil)
	check("perma_delete needs isuper", s, nil, 403, nil)
	s, _ = bob.call("DELETE", fmt.Sprintf("/isuper/client/perma_delete/%d", clientID), nil)
	check("isuper perma_delete", s, nil, 200, nil)
	gone, _ := client.Find(ctx, clientID)
	check("row gone", 200, gone == nil, 200, is(true))

	// ---- column safety
	s, b = bob.call("POST", "/isuper/user/where", map[string]any{"password": mustHash("pa55word"), "nope": 1})
	check("where ignores password and unknown columns, never returns passwords", s, b, 200, func(v any) bool {
		l, _ := v.([]any)
		for _, r := range l {
			if _, has := m(r)["password"]; has {
				return false
			}
		}
		return len(l) == 7 // root, alice, hindi, bob, carol, dave, erin
	})
	s, b = alice.call("POST", "/islogin/book", "not an object")
	check("bad JSON body", s, b, 422, func(v any) bool { return strings.HasPrefix(fmt.Sprint(v), "Invalid JSON body") })

	// ---- logout
	s, _ = alice.call("GET", "/logout", nil)
	check("logout", s, nil, 200, nil)
	s, _ = alice.call("GET", "/login", nil)
	check("logged out", s, nil, 401, nil)
}
