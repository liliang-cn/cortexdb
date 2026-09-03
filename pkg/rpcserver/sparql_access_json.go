package rpcserver

import "encoding/json"

// jsonStringField pulls one top-level string out of a tool's argument JSON.
// A body that will not decode yields no query, which leaves the static Write
// classification in place — the call is about to fail on the same JSON anyway.
func jsonStringField(body, field string) (string, bool) {
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return "", false
	}
	v, ok := m[field].(string)
	return v, ok
}
