package main

import (
	"net/http"
)

type UpstreamErrorInput struct {
	UpstreamHttpStatus *int
	UpstreamCode       *int
	UpstreamMessage    string
	FailureReason      string
	UpstreamError      string
	UpstreamJSON       any
	UpstreamBodyText   string
	Note               string
}

func openAIError(w http.ResponseWriter, status int, code string, message string, detail any) {
	payload := map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"type":    "banana_error",
			"param":   nil,
		},
	}

	if detail != nil {
		payload["error"].(map[string]any)["detail"] = detail
	}

	writeJSON(w, status, payload)
}

func openAIUpstreamError(w http.ResponseWriter, status int, input UpstreamErrorInput) {
	var code string
	if input.UpstreamCode != nil {
		code = "upstream_code_" + itoa(*input.UpstreamCode)
	} else if input.FailureReason != "" {
		code = "upstream_failure_reason_" + input.FailureReason
	} else if input.UpstreamHttpStatus != nil && *input.UpstreamHttpStatus >= 400 {
		code = "upstream_http_" + itoa(*input.UpstreamHttpStatus)
	} else {
		code = "upstream_error"
	}

	message := "Upstream error"
	if stringsTrim(input.UpstreamMessage) != "" {
		message = stringsTrim(input.UpstreamMessage)
	} else if stringsTrim(input.UpstreamError) != "" {
		message = stringsTrim(input.UpstreamError)
	} else if stringsTrim(input.Note) != "" {
		message = stringsTrim(input.Note)
	}

	detail := map[string]any{
		"upstream_http_status":    toNullableInt(input.UpstreamHttpStatus),
		"upstream_code":           toNullableInt(input.UpstreamCode),
		"upstream_message":        nullableString(input.UpstreamMessage),
		"upstream_failure_reason": nullableString(input.FailureReason),
		"upstream_error":          nullableString(input.UpstreamError),
		"note":                    nullableString(input.Note),
	}

	if input.UpstreamBodyText != "" {
		detail["upstream_body"] = truncateText(input.UpstreamBodyText, 2000)
	}

	if input.UpstreamJSON != nil {
		detail["upstream_json"] = input.UpstreamJSON
	}

	openAIError(w, status, code, message, detail)
}

func geminiError(w http.ResponseWriter, status int, message string, details any) {
	httpStatus := status
	if httpStatus == 0 {
		httpStatus = http.StatusInternalServerError
	}

	msg := message
	if stringsTrim(msg) == "" {
		msg = "Error"
	}

	payload := map[string]any{
		"error": map[string]any{
			"code":    httpStatus,
			"message": msg,
			"status":  googleStatusFromHttpStatus(httpStatus),
		},
	}

	if details != nil {
		payload["error"].(map[string]any)["details"] = details
	}

	writeJSON(w, httpStatus, payload)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	writeJSONWithHeaders(w, status, payload, nil)
}

func writeJSONWithHeaders(w http.ResponseWriter, status int, payload any, extraHeaders map[string]string) {
	if extraHeaders != nil {
		for key, value := range extraHeaders {
			w.Header().Set(key, value)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = jsonNewEncoder(w).Encode(payload)
}
