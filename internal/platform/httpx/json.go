package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ErrOperatorKey is returned when a client key starts with "$" or contains ".".
var ErrOperatorKey = errors.New("operator key")

// ErrorEnvelope is the body of every error response.
type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
} //	@name	Error

// ErrorBody is the error inside ErrorEnvelope.
type ErrorBody struct {
	Code    string `json:"code" example:"not_found"`
	Message string `json:"message" example:"user not found"`
	Details any    `json:"details,omitempty" swaggertype:"object"`
} //	@name	ErrorBody

// JSON writes a JSON body.
func JSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		return
	}
}

// Error writes the shared error envelope.
func Error(w http.ResponseWriter, status int, code, message string, details any) {
	JSON(w, status, ErrorEnvelope{Error: ErrorBody{Code: code, Message: message, Details: details}})
}

// ReadJSON decodes one JSON value. On failure it writes the error response and returns false.
func ReadJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeDecodeError(w, err)
		return false
	}
	return true
}

func writeDecodeError(w http.ResponseWriter, err error) {
	var maxBytes *http.MaxBytesError
	if errors.As(err, &maxBytes) {
		Error(w, http.StatusBadRequest, "bad_request", "request body is too large", nil)
		return
	}
	if errors.Is(err, io.EOF) {
		Error(w, http.StatusBadRequest, "bad_request", "request body is required", nil)
		return
	}
	Error(w, http.StatusBadRequest, "bad_request", "request body must be valid JSON", nil)
}

// RejectOperators reports client keys that must never reach a MongoDB filter or update.
func RejectOperators(v any) error {
	switch value := v.(type) {
	case map[string]any:
		for key, child := range value {
			if strings.HasPrefix(key, "$") || strings.Contains(key, ".") {
				return fmt.Errorf("%w: %s", ErrOperatorKey, key)
			}
			if err := RejectOperators(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range value {
			if err := RejectOperators(child); err != nil {
				return err
			}
		}
	}
	return nil
}
