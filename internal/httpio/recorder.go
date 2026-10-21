// Package httpio preserves HTTP response capabilities while tracking commitment.
package httpio

import (
	"bufio"
	"net"
	"net/http"
)

type Recorder struct {
	http.ResponseWriter
	Status    int
	Committed bool
}

func NewRecorder(w http.ResponseWriter) *Recorder {
	return &Recorder{ResponseWriter: w, Status: http.StatusOK}
}
func (w *Recorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *Recorder) WriteHeader(status int) {
	if w.Committed {
		return
	}
	if status == http.StatusSwitchingProtocols || status >= 200 {
		w.Status = status
		w.Committed = true
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *Recorder) Write(b []byte) (int, error) {
	if !w.Committed {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}
func (w *Recorder) FlushError() error {
	if !w.Committed {
		w.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *Recorder) Flush() { _ = w.FlushError() }
func (w *Recorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.Committed = true
		w.Status = http.StatusSwitchingProtocols
	}
	return conn, rw, err
}
