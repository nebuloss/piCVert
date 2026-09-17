package server

import (
	"encoding/json"
	"os"
)

// writeJSON drops a small note beside a directory. Never a blocker: the note is
// a courtesy — it says when something was deleted — and losing it costs a date,
// where failing the deletion over it would cost the deletion.
func writeJSON(file string, value any) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(file, append(raw, '\n'), 0o600)
}

func readJSON(file string, into any) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return
	}
	_ = json.Unmarshal(raw, into)
}

func str(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}
