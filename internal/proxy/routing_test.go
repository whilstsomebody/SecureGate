package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/whilstsomebody/securegate/internal/config"
	"github.com/whilstsomebody/securegate/internal/metrics"
)

func TestEncodedPathsOnUpstreamWire(t *testing.T) {
	upstream := setUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.RequestURI)
	}))
	proxy := proxyServer(t)
	client := &http.Client{Timeout: 2 * time.Second}
	cases := []struct{ path, base, want string }{
		{"/users/a%2Fb", "", "/a%2Fb"},
		{"/users/a%2fb", "", "/a%2fb"},
		{"/%75sers/a%2Fb", "", "/a%2Fb"},
		{"/users%2Fa%2Fb", "", "/a%2Fb"},
		{"/users%2f", "", "/"},
		{"/users", "", "/"},
		{"/users/", "", "/"},
		{"/users/a%252Fb", "", "/a%252Fb"},
		{"/users/a%20b/%E2%9C%93", "", "/a%20b/%E2%9C%93"},
		{"/users//a/", "", "//a/"},
		{"/users/a%2Fb?q=a%2Fb&x=1&x=2", "", "/a%2Fb?q=a%2Fb&x=1&x=2"},
		{"/users/a%2Fb", "/base", "/base/a%2Fb"},
		{"/users/a%2Fb", "/base/", "/base/a%2Fb"},
		{"/users/a%2Fb?q=1", "/base%2Fv1?fixed=a%2Fb", "/base%2Fv1/a%2Fb?fixed=a%2Fb&q=1"},
	}
	for _, tt := range cases {
		t.Run(tt.path+tt.base, func(t *testing.T) {
			config.Cfg.UserServiceAddr = upstream.URL + tt.base
			response, err := client.Get(proxy.URL + tt.path)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != 200 || string(body) != tt.want {
				t.Fatalf("upstream URI=%q status=%d err=%v, want %q", body, response.StatusCode, err, tt.want)
			}
		})
	}
}

func TestLookalikeRoutesDoNotReachTransport(t *testing.T) {
	handler := newProxyHandler(roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unmatched route reached upstream")
		return nil, nil
	}))
	for _, path := range []string{"/usersanything", "/users%61nything", "/administrator", "/payments-extra/x"} {
		req := httptest.NewRequest("GET", path, nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != 404 {
			t.Errorf("%s: status=%d", path, rr.Code)
		}
		if label := metrics.RouteLabel(req.URL.Path); label != "unmatched" {
			t.Errorf("%s: label=%s", path, label)
		}
	}
}

func TestRewritePreservesIncomingEncodedURL(t *testing.T) {
	req := httptest.NewRequest("GET", "/%75sers/a%2Fb?q=a%2Fb", nil)
	before := *req.URL
	handler := newProxyHandler(roundTripFunc(func(out *http.Request) (*http.Response, error) {
		if out.URL.EscapedPath() != "/a%2Fb" {
			t.Errorf("outbound path=%s", out.URL.EscapedPath())
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: http.NoBody}, nil
	}))
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if *req.URL != before {
		t.Fatalf("incoming URL changed: before=%v after=%v", before, *req.URL)
	}
}

func FuzzRewriteURLPath(f *testing.F) {
	for _, raw := range []string{"/users/a%2Fb", "/%75sers%2Fa%252Fb", "/admin/%E2%9C%93", "/payments", "/users//a", "/usersanything"} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		u, err := url.ParseRequestURI(raw)
		if err != nil {
			return
		}
		path := rewritePath(u.Path)
		originalQuery := u.RawQuery
		rewriteURLPath(u)
		decoded, err := url.PathUnescape(u.EscapedPath())
		if err != nil || decoded != path || u.Path != path {
			t.Fatalf("inconsistent rewrite for %q: %+v", raw, u)
		}
		if u.RawQuery != originalQuery {
			t.Fatal("query changed")
		}
	})
}
