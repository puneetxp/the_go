package auth

// The password cases of spec/auth_cases.json (same file in every adapter).

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
	"unicode/utf8"
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
		BcryptInput []struct {
			Name, Password, Input string
			Bytes, Chars          int
		} `json:"bcrypt_input"`
		VerifyLong []struct {
			Name, Password, Stored string
			Ok                     bool
		} `json:"verify_long"`
		NewPassword struct {
			TooShort string `json:"too_short"`
			TooLong  string `json:"too_long"`
			Cases    []struct {
				Password string
				Error    *string
			} `json:"cases"`
		} `json:"new_password"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	for _, c := range spec.Verify {
		if ok, rehash := Verify(spec.Password, c.Stored); ok != c.Ok || rehash != c.Rehash {
			t.Errorf("%s: got ok=%v rehash=%v, want %v %v", c.Name, ok, rehash, c.Ok, c.Rehash)
		}
	}
	for _, c := range spec.BcryptInput {
		if len(c.Password) != c.Bytes || utf8.RuneCountInString(c.Password) != c.Chars || string(BcryptInput(c.Password)) != c.Input {
			t.Errorf("bcrypt input %s: %d bytes %d chars -> %q, want %d %d %q", c.Name, len(c.Password), utf8.RuneCountInString(c.Password), BcryptInput(c.Password), c.Bytes, c.Chars, c.Input)
		}
	}
	for _, c := range spec.VerifyLong {
		if ok, _ := Verify(c.Password, c.Stored); ok != c.Ok {
			t.Errorf("long %s: got %v, want %v", c.Name, ok, c.Ok)
		}
	}
	for _, c := range spec.NewPassword.Cases {
		want := ""
		if c.Error != nil {
			want = *c.Error
		}
		if got := NewPasswordError(c.Password); got != want {
			t.Errorf("new password (%d chars): got %q, want %q", utf8.RuneCountInString(c.Password), got, want)
		}
	}
	for _, pw := range []string{spec.Password, spec.BcryptInput[4].Password} {
		h, err := Hash(pw)
		if err != nil || h[:7] != "$2a$10$" {
			t.Fatalf("hash: %q %v", h, err)
		}
		if ok, rehash := Verify(pw, h); !ok || rehash {
			t.Errorf("a new hash: ok=%v rehash=%v", ok, rehash)
		}
		if h2, _ := Hash(pw); h2 == h {
			t.Error("two hashes of one password should differ (salt)")
		}
	}
	if _, err := Hash(string(make([]rune, 129))); err != ErrTooLong || err.Error() != spec.NewPassword.TooLong {
		t.Errorf("over 128 characters: %v", err)
	}
	leak := regexp.MustCompile(`(?i)72|byte|bcrypt|sha|hash`)
	for _, m := range []string{spec.NewPassword.TooLong, spec.NewPassword.TooShort} {
		if leak.MatchString(m) {
			t.Errorf("message mentions hashing: %q", m)
		}
	}
}
