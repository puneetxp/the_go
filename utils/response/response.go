package response

type ErrorResponse struct {
	Error   string      `json:"error"`
	Details interface{} `json:"details,omitempty"`
}

func JSON(data interface{}) interface{} {
	return data
}

func NotFound(details interface{}) ErrorResponse {
	return ErrorResponse{Error: "Not Found", Details: details}
}

func Unprocessable(details interface{}) ErrorResponse {
	return ErrorResponse{Error: "Unprocessable Entity", Details: details}
}

func Unauthorized() ErrorResponse {
	return ErrorResponse{Error: "Unauthorized"}
}
