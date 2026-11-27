package gui

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSameOrigin(t *testing.T) {
	tests := []struct {
		uri  string
		want bool
	}{
		{Origin + "/", true},
		{Origin + "/index.html?x=1#top", true},
		{Origin, true},
		{"http://app.ouroboros.invalid/index.html", false},
		{"https://app.ouroboros.invalid:8443/index.html", false},
		{"https://user@app.ouroboros.invalid/index.html", false},
		{"https://app.ouroboros.invalid.example.com/", false},
		{"https://APP.OUROBOROS.INVALID/", false},
		{"https://example.com/?u=https://app.ouroboros.invalid/", false},
		{"about:blank", false},
		{"data:text/html,hi", false},
		{"blob:https://app.ouroboros.invalid/0f1e", false},
		{"file:///C:/Windows/win.ini", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := SameOrigin(tt.uri); got != tt.want {
			t.Errorf("SameOrigin(%q) = %v, want %v", tt.uri, got, tt.want)
		}
	}
}

func TestServe(t *testing.T) {
	files := fstest.MapFS{
		"index.html":   {Data: []byte("<!doctype html>")},
		"app.js":       {Data: []byte("1")},
		"app.css":      {Data: []byte("a{}")},
		"img/logo.png": {Data: []byte("png")},
	}
	tests := []struct {
		method, uri string
		status      int
		ctype, body string
	}{
		{"GET", Origin + "/index.html", 200, "text/html; charset=utf-8", "<!doctype html>"},
		{"GET", Origin + "/", 200, "text/html; charset=utf-8", "<!doctype html>"},
		{"GET", Origin + "/app.js?v=2", 200, "text/javascript; charset=utf-8", "1"},
		{"GET", Origin + "/app.css", 200, "text/css; charset=utf-8", "a{}"},
		{"GET", Origin + "/img/logo.png", 200, "image/png", "png"},
		{"GET", Origin + "/img/../index.html", 200, "text/html; charset=utf-8", "<!doctype html>"},
		{"GET", Origin + "/../../Windows/win.ini", 404, "", ""},
		{"GET", Origin + "/%2e%2e/secret", 404, "", ""},
		{"GET", Origin + "/img", 404, "", ""},
		{"GET", Origin + "/missing.js", 404, "", ""},
		{"POST", Origin + "/index.html", 405, "", ""},
		{"GET", "https://example.com/index.html", 403, "", ""},
		{"GET", "http://app.ouroboros.invalid/index.html", 403, "", ""},
	}
	for _, tt := range tests {
		r := Serve(files, tt.method, tt.uri)
		if r.Status != tt.status {
			t.Errorf("%s %s: status %d, want %d", tt.method, tt.uri, r.Status, tt.status)
			continue
		}
		if !strings.Contains(r.Headers, "Content-Security-Policy: default-src 'self';") ||
			!strings.Contains(r.Headers, "frame-ancestors 'none'") {
			t.Errorf("%s %s: no content security policy in %q", tt.method, tt.uri, r.Headers)
		}
		if tt.status != 200 {
			if len(r.Body) != 0 {
				t.Errorf("%s %s: body on an error", tt.method, tt.uri)
			}
			continue
		}
		if !strings.HasPrefix(r.Headers, "Content-Type: "+tt.ctype+"\r\n") || string(r.Body) != tt.body {
			t.Errorf("%s %s: %q %q", tt.method, tt.uri, r.Headers, r.Body)
		}
	}
}

func TestEmbeddedPage(t *testing.T) {
	for _, name := range []string{"index.html", "app.js", "bridge.js", "strings.js", "app.css"} {
		if _, err := fs.Stat(WebFS(), name); err != nil {
			t.Errorf("%s is not embedded: %v", name, err)
		}
	}
	page, err := fs.ReadFile(WebFS(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(page), "<script>") || strings.Contains(string(page), "style=") {
		t.Error("index.html has inline script or style, which the policy blocks")
	}
}
