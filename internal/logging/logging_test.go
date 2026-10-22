package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestHandlerBoundsValues(t *testing.T) {
	var b bytes.Buffer
	slog.New(NewHandler(&b)).Error("failure", "path", strings.Repeat("x", 10000), "error", errors.New(strings.Repeat("y", 10000)))
	var fields map[string]any
	if err := json.Unmarshal(b.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"path", "error"} {
		if len(fields[key].(string)) > MaxValueBytes+len("[truncated]") {
			t.Fatalf("unbounded %s", key)
		}
	}
}
