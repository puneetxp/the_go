package session

import (
	"github.com/puneetxp/the_go/utils/response"
)

var Current = make(map[string]interface{})

func Set(key string, value interface{}) {
	Current[key] = value
}

func Get(key string) interface{} {
	if val, ok := Current[key]; ok {
		return val
	}
	return nil
}

func Create(auth map[string]interface{}) interface{} {
	Set("user_id", auth["id"])
	return response.JSON(auth)
}
