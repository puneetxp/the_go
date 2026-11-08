// Package router serves the framework's route tree over the standard
// library's http.ServeMux, the same tree shape as the_deno's _Routes: paths,
// islogin, roles and guards inherited by children, method groups, and CRUD
// routes generated from a model (see crud.go).
//
//	r, err := router.New([]router.Route{
//		{Path: "/", Handler: home},
//		{Islogin: true, Child: []router.Route{
//			{Path: "/islogin/book", Crud: &router.Crud{Model: models.Book, Can: "acrudw", Owner: "user_id"}},
//			{Path: "/isuper", Roles: []string{"isuper"}, Child: isuperRoutes},
//		}},
//		auth.Routes()...,
//	}, router.Options{})
//	http.ListenAndServe(":9000", r)
package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/puneetxp/the_go/session"
)

// Handler serves one route. Return an error for a 500; write the response
// with c.JSON.
type Handler func(c *Ctx) error

// Guard runs before a handler; a non-empty result refuses the request with
// that message (like the_deno's guards).
type Guard func(c *Ctx) string

// Route is one node of the route tree.
type Route struct {
	Path    string
	Method  string  // for Handler; default GET
	Handler Handler // a leaf
	Islogin bool    // needs a logged-in user (inherited)
	Roles   []string
	Guard   []Guard
	Child   []Route            // paths are appended to this one's
	Group   map[string][]Route // method -> routes, like the_deno's group
	Crud    *Crud              // CRUD routes for a model
}

// Options configure a Router.
type Options struct {
	Sessions session.Store // default: in-memory
	Cookie   session.CookieOptions
	TTL      time.Duration // session lifetime, default session.DefaultTTL
	// Bearer, when set, checks "Authorization: Bearer <token>" (API keys) on
	// islogin routes. Return nil to refuse the token.
	Bearer func(c *Ctx, token string) (*session.Login, error)
	Log    *log.Logger
	// CORS, the same switch in every adapter (spec/auth_cases.json): off by
	// default, so no other site can call the API with a user's cookies.
	// With CORS on, Origins limits it to those origins; empty means any
	// origin (development). CORSFromEnv reads both from CORS / CORS_ORIGINS.
	CORS    bool
	Origins []string
}

// CORSFromEnv reads the CORS switch (CORS=on|true|1|yes) and the allowed
// origins (CORS_ORIGINS, comma separated), as every adapter does.
func CORSFromEnv() (bool, []string) {
	return corsSetting(os.Getenv("CORS"), os.Getenv("CORS_ORIGINS"))
}

func corsSetting(on, origins string) (bool, []string) {
	enabled := contains([]string{"on", "true", "1", "yes"}, strings.ToLower(strings.TrimSpace(on)))
	var list []string
	for _, o := range strings.Split(origins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			list = append(list, o)
		}
	}
	return enabled, list
}

// Router is an http.Handler.
type Router struct {
	mux  *http.ServeMux
	opts Options
}

type leaf struct {
	method, path string
	handler      Handler
	islogin      bool
	roles        []string
	guard        []Guard
}

// New compiles the route tree. Two routes with the same method and path, or
// patterns ServeMux can't order, are an error instead of a panic.
func New(routes []Route, opts Options) (*Router, error) {
	if opts.Sessions == nil {
		opts.Sessions = session.NewMemoryStore()
	}
	if opts.TTL <= 0 {
		opts.TTL = session.DefaultTTL
	}
	if opts.Log == nil {
		opts.Log = log.Default()
	}
	var leaves []leaf
	for _, r := range routes {
		if err := flatten(r, "", false, nil, nil, &leaves); err != nil {
			return nil, err
		}
	}
	rt := &Router{mux: http.NewServeMux(), opts: opts}
	seen := map[string]bool{}
	for _, l := range leaves {
		pattern := l.method + " " + muxPath(l.path)
		if seen[pattern] {
			return nil, fmt.Errorf("router: %s %s is defined twice", l.method, l.path)
		}
		seen[pattern] = true
		if err := rt.handle(pattern, l); err != nil {
			return nil, err
		}
	}
	return rt, nil
}

// Routes lists every compiled route as "METHOD /path" (for tests and docs).
func Routes(routes []Route) ([]string, error) {
	var leaves []leaf
	for _, r := range routes {
		if err := flatten(r, "", false, nil, nil, &leaves); err != nil {
			return nil, err
		}
	}
	out := make([]string, len(leaves))
	for i, l := range leaves {
		out[i] = l.method + " " + cleanPath(l.path)
	}
	return out, nil
}

// Describe lists every compiled route with its access rule, as
// "METHOD /path login=<bool> roles=<a,b>" (to compare with other adapters).
func Describe(routes []Route) ([]string, error) {
	var leaves []leaf
	for _, r := range routes {
		if err := flatten(r, "", false, nil, nil, &leaves); err != nil {
			return nil, err
		}
	}
	out := make([]string, len(leaves))
	for i, l := range leaves {
		out[i] = fmt.Sprintf("%s %s login=%t roles=%s", l.method, cleanPath(l.path), l.islogin, strings.Join(l.roles, ","))
	}
	return out, nil
}

func (rt *Router) handle(pattern string, l leaf) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("router: %v", p)
		}
	}()
	rt.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) { rt.serve(w, r, l) })
	return nil
}

func flatten(r Route, prefix string, islogin bool, roles []string, guard []Guard, out *[]leaf) error {
	path := prefix + r.Path
	islogin = islogin || r.Islogin
	roles = append(append([]string{}, roles...), r.Roles...)
	guard = append(append([]Guard{}, guard...), r.Guard...)
	if r.Handler != nil {
		m := strings.ToUpper(r.Method)
		if m == "" {
			m = http.MethodGet
		}
		*out = append(*out, leaf{method: m, path: path, handler: r.Handler, islogin: islogin, roles: roles, guard: guard})
	}
	for _, method := range sortedMethods(r.Group) {
		for _, g := range r.Group[method] {
			g.Method = method
			if err := flatten(g, path, islogin, roles, guard, out); err != nil {
				return err
			}
		}
	}
	for _, c := range r.Child {
		if err := flatten(c, path, islogin, roles, guard, out); err != nil {
			return err
		}
	}
	if r.Crud != nil {
		crudRoutes, err := r.Crud.routes()
		if err != nil {
			return fmt.Errorf("router: %s: %w", cleanPath(path), err)
		}
		for _, c := range crudRoutes {
			if err := flatten(c, path, islogin, roles, guard, out); err != nil {
				return err
			}
		}
	}
	return nil
}

func sortedMethods(g map[string][]Route) []string {
	order := []string{"GET", "POST", "PATCH", "PUT", "DELETE"}
	var out []string
	for _, m := range order {
		if _, ok := g[m]; ok {
			out = append(out, m)
		}
	}
	for m := range g {
		if !contains(order, m) {
			out = append(out, m)
		}
	}
	return out
}

var slashes = regexp.MustCompile(`/+`)

func cleanPath(p string) string {
	p = slashes.ReplaceAllString("/"+p, "/")
	if len(p) > 1 {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

var colonParam = regexp.MustCompile(`:([A-Za-z_][A-Za-z0-9_]*)`)

// muxPath turns /book/:book_id into the ServeMux pattern /book/{book_id};
// "/" matches only the root, as in the_deno.
func muxPath(p string) string {
	p = colonParam.ReplaceAllString(cleanPath(p), "{$1}")
	if p == "/" {
		return "/{$}"
	}
	return p
}

// ServeHTTP answers CORS preflights, then routes; anything unmatched is a
// JSON 404, as in the_deno.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		cors(w, r, rt.opts.CORS, rt.opts.Origins)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if _, pattern := rt.mux.Handler(r); pattern == "" {
		c := &Ctx{W: w, R: r, rt: rt}
		c.JSON(http.StatusNotFound, "Not Found")
		return
	}
	rt.mux.ServeHTTP(w, r)
}

func (rt *Router) serve(w http.ResponseWriter, r *http.Request, l leaf) {
	c := &Ctx{W: w, R: r, rt: rt}
	defer func() {
		if p := recover(); p != nil {
			rt.opts.Log.Printf("[HANDLER PANIC] %s %s: %v", r.Method, r.URL.Path, p)
			c.JSON(http.StatusInternalServerError, "An internal server error occurred. Please try again later.")
		}
	}()
	if l.islogin {
		if msg, status := rt.login(c); msg != "" {
			c.JSON(status, msg)
			return
		}
	} else if s, _ := session.FromRequest(r.Context(), rt.opts.Sessions, r); s != nil {
		// Public routes still know who is logged in (e.g. GET /login status).
		c.Session, c.Login = s, &s.Login
	}
	for _, g := range l.guard {
		if msg := g(c); msg != "" {
			c.JSON(http.StatusUnauthorized, msg)
			return
		}
	}
	if len(l.roles) > 0 && !c.Login.HasRole(l.roles...) {
		c.JSON(http.StatusForbidden, "Your role is limited")
		return
	}
	if err := l.handler(c); err != nil {
		rt.opts.Log.Printf("[HANDLER ERROR] %s %s: %v", r.Method, r.URL.Path, err)
		if !c.wrote {
			c.JSON(http.StatusInternalServerError, "An internal server error occurred. Please try again later.")
		}
	}
}

// login finds the user from a bearer token (when Options.Bearer is set) or
// the session cookie. It returns a refusal message and status, or "".
func (rt *Router) login(c *Ctx) (string, int) {
	if h := c.R.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") && rt.opts.Bearer != nil {
		l, err := rt.opts.Bearer(c, strings.TrimPrefix(h, "Bearer "))
		if err != nil {
			rt.opts.Log.Printf("[BEARER ERROR] %v", err)
		}
		if l == nil {
			return "Invalid or expired API Key", http.StatusUnauthorized
		}
		c.Login = l
		return "", 0
	}
	s, err := session.FromRequest(c.R.Context(), rt.opts.Sessions, c.R)
	if err != nil {
		rt.opts.Log.Printf("[SESSION ERROR] %v", err)
	}
	if s == nil {
		return "Login First", http.StatusUnauthorized
	}
	c.Session, c.Login = s, &s.Login
	return "", 0
}

// Ctx is one request.
type Ctx struct {
	W       http.ResponseWriter
	R       *http.Request
	Session *session.Session // nil when nobody is logged in (or on a bearer token)
	Login   *session.Login   // who is logged in, nil when nobody
	rt      *Router
	wrote   bool
}

// Context is the request's context, for model calls.
func (c *Ctx) Context() context.Context { return c.R.Context() }

// Param is a path value, e.g. Param("id") for /book/:id.
func (c *Ctx) Param(name string) string { return c.R.PathValue(name) }

// Bind decodes the JSON body into v.
func (c *Ctx) Bind(v any) error {
	if c.R.Body == nil {
		return errors.New("empty body")
	}
	dec := json.NewDecoder(http.MaxBytesReader(c.W, c.R.Body, 8<<20))
	dec.UseNumber()
	return dec.Decode(v)
}

// JSON writes body as JSON with CORS headers. While a session is active it
// also extends it and refreshes the cookie, like the_deno's response.JSON.
func (c *Ctx) JSON(status int, body any) error {
	if c.wrote {
		return errors.New("router: response already written")
	}
	c.wrote = true
	cors(c.W, c.R, c.rt.opts.CORS, c.rt.opts.Origins)
	if c.Session != nil {
		c.Session.Expire = time.Now().Add(c.rt.opts.TTL)
		if err := c.rt.opts.Sessions.Set(c.Context(), c.Session); err == nil {
			http.SetCookie(c.W, c.rt.opts.Cookie.Cookie(c.Session))
		}
	}
	c.W.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.W.WriteHeader(status)
	return json.NewEncoder(c.W).Encode(body)
}

// StartSession logs login in: a new session, its cookie, any old session
// on this request ended.
func (c *Ctx) StartSession(login session.Login) error {
	if c.Session != nil {
		c.rt.opts.Sessions.Delete(c.Context(), c.Session.ID)
	}
	s := session.New(c.R, login, c.rt.opts.TTL)
	if err := c.rt.opts.Sessions.Set(c.Context(), s); err != nil {
		return err
	}
	c.Session, c.Login = s, &s.Login
	return nil
}

// EndSession logs out: the session is deleted and the cookie expired.
func (c *Ctx) EndSession() error {
	if c.Session != nil {
		if err := c.rt.opts.Sessions.Delete(c.Context(), c.Session.ID); err != nil {
			return err
		}
	}
	c.Session, c.Login = nil, nil
	http.SetCookie(c.W, c.rt.opts.Cookie.Cookie(nil))
	return nil
}

// cors sets the CORS headers when CORS is on and the origin is allowed;
// otherwise none, and the browser keeps other sites out.
func cors(w http.ResponseWriter, r *http.Request, on bool, allowed []string) {
	origin := r.Header.Get("Origin")
	h := w.Header()
	h.Add("Vary", "Origin")
	if !corsAllows(on, allowed, origin) {
		return
	}
	h.Set("Access-Control-Allow-Origin", origin)
	h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Cookie")
	h.Set("Access-Control-Allow-Credentials", "true")
	h.Set("Access-Control-Max-Age", "86400")
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func corsAllows(on bool, allowed []string, origin string) bool {
	return on && origin != "" && (len(allowed) == 0 || contains(allowed, origin))
}
