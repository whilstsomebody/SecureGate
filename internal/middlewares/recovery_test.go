package middlewares

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecoveryMiddlewareCatchesPanic(t *testing.T) {
	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("intentional test panic")
	})

	handler := RecoveryMiddleware(panicking)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 after panic, got %d", rr.Code)
	}
}

func TestRecoveryMiddlewarePassesThroughOnNoError(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := RecoveryMiddleware(ok)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestRecoveryPropagatesAbortAndNeverAppendsToPartialResponse(t *testing.T) {
	for _, sentinel := range []bool{false, true} {
		func() {
			rr := httptest.NewRecorder()
			defer func() {
				if got := recover(); got != http.ErrAbortHandler {
					t.Errorf("panic=%v", got)
				}
				if rr.Body.String() != "partial" {
					t.Errorf("body corrupted: %q", rr.Body.String())
				}
			}()
			RecoveryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("partial"))
				if sentinel {
					panic(http.ErrAbortHandler)
				}
				panic("unexpected failure after commitment")
			})).ServeHTTP(rr, httptest.NewRequest("GET", "/users/x", nil))
		}()
	}
}
func TestRecoveryAfterFlushAborts(t *testing.T) {
	defer func() {
		if got := recover(); got != http.ErrAbortHandler {
			t.Errorf("panic=%v", got)
		}
	}()
	RecoveryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Fatal(err)
		}
		panic("after flush")
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
}
