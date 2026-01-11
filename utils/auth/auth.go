package auth

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/puneetxp/the_go/utils/response"
)

func Login(email, password string) interface{} {
	// DB logic placeholder
	return response.NotFound("User Not Found")
}

func Register(name, email, password string) interface{} {
	// DB logic placeholder
	return response.JSON("Register logic placeholder")
}

func Hash(password string) string {
	h := sha256.Sum256([]byte(password))
	return hex.EncodeToString(h[:])
}
