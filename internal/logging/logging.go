// Package logging bounds structured log values before they reach the output.
package logging

import (
	"fmt"
	"io"
	"log/slog"
)

const MaxValueBytes = 1024

func NewHandler(w io.Writer) slog.Handler {
	return slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo, ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
		var value string
		switch a.Value.Kind() {
		case slog.KindString:
			value = a.Value.String()
		case slog.KindAny:
			value = fmt.Sprint(a.Value.Any())
		default:
			return a
		}
		if len(value) > MaxValueBytes {
			value = value[:MaxValueBytes] + "[truncated]"
		}
		a.Value = slog.StringValue(value)
		return a
	}})
}
