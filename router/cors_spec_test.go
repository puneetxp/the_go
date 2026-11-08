package router

// The CORS cases of spec/auth_cases.json (same file in every adapter).

import (
	"encoding/json"
	"os"
	"testing"
)

func TestCORSSpec(t *testing.T) {
	raw, err := os.ReadFile("../spec/auth_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		CORS []struct {
			Name, Cors, Origins, Origin string
			Allow                       *string
		} `json:"cors"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	for _, c := range spec.CORS {
		on, list := corsSetting(c.Cors, c.Origins)
		r, err := New([]Route{{Path: "/p", Handler: ok("p")}}, Options{CORS: on, Origins: list})
		if err != nil {
			t.Fatal(err)
		}
		hdr := map[string]string{}
		if c.Origin != "" {
			hdr["Origin"] = c.Origin
		}
		for _, method := range []string{"GET", "OPTIONS"} {
			got := do(t, r, method, "/p", nil, hdr).header
			want := ""
			if c.Allow != nil {
				want = *c.Allow
			}
			if got.Get("Access-Control-Allow-Origin") != want {
				t.Errorf("%s (%s): Allow-Origin %q, want %q", c.Name, method, got.Get("Access-Control-Allow-Origin"), want)
			}
			if want != "" && got.Get("Access-Control-Allow-Credentials") != "true" {
				t.Errorf("%s: no Allow-Credentials", c.Name)
			}
		}
	}
}
