package httpio

import (
	"encoding/json"
	"net/http"
)

// ErrorResponse represents a standardized JSON error envelope.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail contains machine-readable and human-readable error information.
type ErrorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Status    int    `json:"status"`
	RequestID string `json:"request_id,omitempty"`
}

// WriteError writes a consistent JSON error response with proper headers.
func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	if r != nil && r.Context().Err() != nil {
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	var requestID string
	if r != nil {
		requestID = r.Header.Get("X-Request-ID")
		if requestID != "" {
			w.Header().Set("X-Request-ID", requestID)
		}
	}

	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorResponse{
		Error: ErrorDetail{
			Code:      code,
			Message:   message,
			Status:    status,
			RequestID: requestID,
		},
	})
}
