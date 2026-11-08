// Package session keeps logged-in users on the server, like the_deno's
// Session: the browser holds only a random id in the PHPSESSID cookie (the
// same cookie the Deno and PHP adapters use, so frontends work unchanged).
// A session is bound to the user agent it was created with.
package session

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sync"
	"time"
)

// CookieName is shared by every adapter of the framework.
const CookieName = "PHPSESSID"

// DefaultTTL matches the_deno (3000 minutes).
const DefaultTTL = 3000 * time.Minute

// Login is who is logged in, as handlers and the frontend see it.
type Login struct {
	ID    int64          `json:"id"`
	Name  string         `json:"name"`
	Email string         `json:"email"`
	Roles []string       `json:"roles"`
	Extra map[string]any `json:"extra,omitempty"` // app data, e.g. the active book
}

// HasRole reports whether the user has any of roles. User 1 has them all,
// as in the Deno and PHP adapters.
func (l *Login) HasRole(roles ...string) bool {
	if l == nil {
		return false
	}
	if l.ID == 1 {
		return true
	}
	for _, want := range roles {
		for _, have := range l.Roles {
			if want == have {
				return true
			}
		}
	}
	return false
}

// Session is one login on the server.
type Session struct {
	ID     string    `json:"id"`
	Login  Login     `json:"login"`
	Expire time.Time `json:"expire"`
	IP     string    `json:"ip"`
	Agent  string    `json:"agent"`
}

// Store keeps sessions. MemoryStore is the default; a shared store (database,
// Redis) is needed when several processes serve the same users.
type Store interface {
	Get(ctx context.Context, id string) (*Session, error) // nil, nil when missing or expired
	Set(ctx context.Context, s *Session) error
	Delete(ctx context.Context, id string) error
}

// New starts a session for login from request r.
func New(r *http.Request, login Login, ttl time.Duration) *Session {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("session: no randomness: " + err.Error())
	}
	return &Session{
		ID:     base64.RawURLEncoding.EncodeToString(b),
		Login:  login,
		Expire: time.Now().Add(ttl),
		IP:     r.Header.Get("X-Forwarded-For"),
		Agent:  r.UserAgent(),
	}
}

// FromRequest returns the session named by the request's cookie, if it is
// valid and was created with the same user agent.
func FromRequest(ctx context.Context, store Store, r *http.Request) (*Session, error) {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return nil, nil
	}
	s, err := store.Get(ctx, c.Value)
	if err != nil || s == nil {
		return nil, err
	}
	if s.Agent != r.UserAgent() {
		return nil, nil
	}
	return s, nil
}

// CookieOptions are the session cookie's attributes.
type CookieOptions struct {
	Domain   string
	Secure   bool
	SameSite http.SameSite // default Lax
}

// Cookie is the Set-Cookie for s; expired when s is nil (logout).
func (o CookieOptions) Cookie(s *Session) *http.Cookie {
	same := o.SameSite
	if same == 0 {
		same = http.SameSiteLaxMode
	}
	c := &http.Cookie{Name: CookieName, Path: "/", Domain: o.Domain, Secure: o.Secure, HttpOnly: true, SameSite: same}
	if s == nil {
		c.MaxAge = -1
		c.Expires = time.Unix(0, 0)
		return c
	}
	c.Value = s.ID
	c.Expires = s.Expire
	return c
}

// MemoryStore keeps sessions in this process.
type MemoryStore struct {
	mu   sync.RWMutex
	all  map[string]*Session
	sets int
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{all: map[string]*Session{}} }

func (m *MemoryStore) Get(_ context.Context, id string) (*Session, error) {
	m.mu.RLock()
	s := m.all[id]
	m.mu.RUnlock()
	if s == nil || time.Now().After(s.Expire) {
		return nil, nil
	}
	cp := *s
	return &cp, nil
}

func (m *MemoryStore) Set(_ context.Context, s *Session) error {
	cp := *s
	m.mu.Lock()
	defer m.mu.Unlock()
	m.all[s.ID] = &cp
	if m.sets++; m.sets%1000 == 0 { // drop expired sessions now and then
		now := time.Now()
		for id, x := range m.all {
			if now.After(x.Expire) {
				delete(m.all, id)
			}
		}
	}
	return nil
}

func (m *MemoryStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.all, id)
	m.mu.Unlock()
	return nil
}
