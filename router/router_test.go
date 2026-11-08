package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/puneetxp/the_go/model"
	"github.com/puneetxp/the_go/session"
)

var tBook = model.Define(&model.Model{Name: "rt_book", Table: "books", Columns: []string{"id", "name", "user_id"}})

func ok(msg string) Handler { return func(c *Ctx) error { return c.JSON(200, msg) } }

func TestRoutesFlattenLikeDeno(t *testing.T) {
	got, err := Routes([]Route{
		{Handler: ok("home")},
		{Islogin: true, Child: []Route{
			{Path: "islogin", Child: []Route{
				{Path: "/book", Crud: &Crud{Model: tBook, Can: "ar"}},
				{Path: "/book/:book_id/listing", Group: map[string][]Route{"GET": {{Handler: ok("l")}}, "POST": {{Handler: ok("l")}}}},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"GET /", "GET /islogin/book", "GET /islogin/book/:id", "GET /islogin/book/:book_id/listing", "POST /islogin/book/:book_id/listing"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}

func TestCrudLettersGiveDenoRoutes(t *testing.T) {
	got, err := Routes([]Route{{Path: "/isuper/book", Crud: &Crud{Model: tBook, Can: "acrwupd"}}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"GET /isuper/book", "GET /isuper/book/:id", "POST /isuper/book", "POST /isuper/book/where",
		"PATCH /isuper/book", "PUT /isuper/book", "PATCH /isuper/book/:id", "PUT /isuper/book/:id",
		"DELETE /isuper/book/:id", "DELETE /isuper/book/perma_delete/:id",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
	if _, err := Routes([]Route{{Path: "/x", Crud: &Crud{Model: tBook, Can: "az"}}}); err == nil {
		t.Error("unknown crud letter should be an error")
	}
}

func TestDuplicateRouteIsAnError(t *testing.T) {
	_, err := New([]Route{{Path: "/a", Handler: ok("1")}, {Path: "/a/", Handler: ok("2")}}, Options{})
	if err == nil || !strings.Contains(err.Error(), "defined twice") {
		t.Errorf("got %v", err)
	}
}

type res struct {
	status int
	body   any
	header http.Header
}

func do(t *testing.T, h http.Handler, method, path string, cookie *http.Cookie, hdr map[string]string) res {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("User-Agent", "test-agent")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var b any
	json.Unmarshal(w.Body.Bytes(), &b)
	return res{w.Code, b, w.Header()}
}

func loginCookie(t *testing.T, store session.Store, login session.Login) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "test-agent")
	s := session.New(req, login, 0)
	if err := store.Set(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: session.CookieName, Value: s.ID}
}

func TestAccess(t *testing.T) {
	store := session.NewMemoryStore()
	denyFriday := func(c *Ctx) string {
		if c.R.Header.Get("X-Day") == "friday" {
			return "closed on fridays"
		}
		return ""
	}
	r, err := New([]Route{
		{Handler: ok("home")},
		{Path: "/public", Handler: ok("public")},
		{Islogin: true, Child: []Route{
			{Path: "/islogin/me", Handler: func(c *Ctx) error { return c.JSON(200, c.Login.Email) }},
			{Path: "/isuper", Roles: []string{"isuper"}, Child: []Route{{Path: "/panel", Handler: ok("panel")}}},
			{Path: "/guarded", Guard: []Guard{denyFriday}, Handler: ok("open")},
		}},
		{Path: "/boom", Handler: func(c *Ctx) error { panic("boom") }},
	}, Options{Sessions: store})
	if err != nil {
		t.Fatal(err)
	}
	alice := loginCookie(t, store, session.Login{ID: 7, Email: "a@x", Roles: []string{"manager"}})
	admin := loginCookie(t, store, session.Login{ID: 8, Email: "b@x", Roles: []string{"isuper"}})
	first := loginCookie(t, store, session.Login{ID: 1, Email: "root@x"})

	cases := []struct {
		name, method, path string
		cookie             *http.Cookie
		hdr                map[string]string
		status             int
		body               any
	}{
		{"root is exact", "GET", "/", nil, nil, 200, "home"},
		{"unknown path is a JSON 404", "GET", "/nope", nil, nil, 404, "Not Found"},
		{"wrong method is a JSON 404", "POST", "/public", nil, nil, 404, "Not Found"},
		{"islogin without a cookie", "GET", "/islogin/me", nil, nil, 401, "Login First"},
		{"islogin with a session", "GET", "/islogin/me", alice, nil, 200, "a@x"},
		{"session bound to its user agent", "GET", "/islogin/me", alice, map[string]string{"User-Agent": "other"}, 401, "Login First"},
		{"unknown session id", "GET", "/islogin/me", &http.Cookie{Name: session.CookieName, Value: "forged"}, nil, 401, "Login First"},
		{"role missing", "GET", "/isuper/panel", alice, nil, 403, "Your role is limited"},
		{"role present", "GET", "/isuper/panel", admin, nil, 200, "panel"},
		{"user 1 passes role checks", "GET", "/isuper/panel", first, nil, 200, "panel"},
		{"guard refuses", "GET", "/guarded", alice, map[string]string{"X-Day": "friday"}, 401, "closed on fridays"},
		{"guard passes", "GET", "/guarded", alice, nil, 200, "open"},
		{"a panic is a 500, not a crash", "GET", "/boom", nil, nil, 500, "An internal server error occurred. Please try again later."},
	}
	for _, c := range cases {
		got := do(t, r, c.method, c.path, c.cookie, c.hdr)
		if got.status != c.status || got.body != c.body {
			t.Errorf("%s: got %d %v, want %d %v", c.name, got.status, got.body, c.status, c.body)
		}
	}

	got := do(t, r, "OPTIONS", "/islogin/me", nil, map[string]string{"Origin": "https://app.example"})
	if got.status != 204 || got.header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("preflight with CORS off (the default): %d %v", got.status, got.header)
	}
	got = do(t, r, "GET", "/islogin/me", alice, nil)
	if !strings.Contains(strings.Join(got.header.Values("Set-Cookie"), ";"), session.CookieName+"=") {
		t.Errorf("an authenticated response refreshes the cookie: %v", got.header)
	}
}

func TestOriginsAllowList(t *testing.T) {
	r, _ := New([]Route{{Path: "/p", Handler: ok("p")}}, Options{CORS: true, Origins: []string{"https://intax.in"}})
	if h := do(t, r, "GET", "/p", nil, map[string]string{"Origin": "https://evil.example"}).header; h.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("other origin got CORS: %v", h)
	}
	if h := do(t, r, "GET", "/p", nil, map[string]string{"Origin": "https://intax.in"}).header; h.Get("Access-Control-Allow-Origin") != "https://intax.in" {
		t.Errorf("allowed origin: %v", h)
	}
}

func TestBearerHook(t *testing.T) {
	r, _ := New([]Route{{Path: "/api", Islogin: true, Handler: func(c *Ctx) error { return c.JSON(200, c.Login.ID) }}}, Options{
		Bearer: func(c *Ctx, token string) (*session.Login, error) {
			if token == "good" {
				return &session.Login{ID: 42}, nil
			}
			return nil, nil
		},
	})
	if got := do(t, r, "GET", "/api", nil, map[string]string{"Authorization": "Bearer good"}); got.status != 200 || got.body != float64(42) {
		t.Errorf("good token: %v", got)
	}
	if got := do(t, r, "GET", "/api", nil, map[string]string{"Authorization": "Bearer bad"}); got.status != 401 {
		t.Errorf("bad token: %v", got)
	}
}
