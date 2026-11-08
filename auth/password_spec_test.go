package auth

// The password cases of spec/auth_cases.json (same file in every adapter).

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestPasswordSpec(t *testing.T) {
	raw, err := os.ReadFile("../spec/auth_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Password string `json:"password"`
		Verify   []struct {
			Name, Stored string
			Ok, Rehash   bool
		} `json:"verify"`
		TooLong string `json:"too_long"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	for _, c := range spec.Verify {
		if ok, rehash := Verify(spec.Password, c.Stored); ok != c.Ok || rehash != c.Rehash {
			t.Errorf("%s: got ok=%v rehash=%v, want %v %v", c.Name, ok, rehash, c.Ok, c.Rehash)
		}
	}
	h, err := Hash(spec.Password)
	if err != nil || !strings.HasPrefix(h, "$2a$10$") {
		t.Fatalf("hash: %q %v", h, err)
	}
	if ok, rehash := Verify(spec.Password, h); !ok || rehash {
		t.Errorf("a new hash: ok=%v rehash=%v", ok, rehash)
	}
	if h2, _ := Hash(spec.Password); h2 == h {
		t.Error("two hashes of one password should differ (salt)")
	}
	if _, err := Hash(spec.TooLong); err != ErrTooLong {
		t.Errorf("over 72 bytes: %v", err)
	}
}
