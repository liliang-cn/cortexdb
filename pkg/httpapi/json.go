package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// maxRequestBytes caps a request body.
//
// A knowledge document is the largest thing anyone posts here and 4 MiB is far
// past every one of them; the number is not the point. The point is that an
// unbounded body is a way to spend the server's memory from the outside, and
// the only place to say so is before the decoder starts allocating.
const maxRequestBytes = 4 << 20

// errorResponse is the shape of every failure this package returns.
type errorResponse struct {
	Error string `json:"error"`
}

// decodeBody reads the request body into req, reporting failures itself.
// It returns false when it has already written a response.
func decodeBody(w http.ResponseWriter, r *http.Request, req any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	dec := json.NewDecoder(r.Body)
	// A misspelled field is a request that does not mean what its author
	// thinks it means. Accepted silently, "topK" instead of "top_k" produces a
	// default-sized result set and a 200, and the caller debugs their data
	// instead of their typo.
	dec.DisallowUnknownFields()

	if err := dec.Decode(req); err != nil {
		var tooLarge *http.MaxBytesError
		switch {
		case errors.As(err, &tooLarge):
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("request body exceeds the %d byte limit", maxRequestBytes))
		case errors.Is(err, io.EOF):
			// An empty body is the zero request. Whether that is legal is the
			// facade's call, and it says so in its own words — which is a
			// better error than "EOF" for a curl with no -d.
			return true
		default:
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return false
	}
	if dec.More() {
		writeError(w, http.StatusBadRequest, "unexpected content after the JSON body")
		return false
	}
	return true
}

// writeJSON marshals first and writes second.
//
// Encoding straight into the ResponseWriter would commit a 200 and the status
// line before discovering a value that cannot be marshalled, leaving a
// truncated body behind a success code.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encode response: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeError reports a failure as {"error":"..."} with an HTTP status.
func writeError(w http.ResponseWriter, status int, message string) {
	// Marshalled by hand rather than through writeJSON: a string always
	// encodes, and the two functions calling each other on failure would not.
	body, _ := json.Marshal(errorResponse{Error: message})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
