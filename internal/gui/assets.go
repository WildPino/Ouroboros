package gui

import (
	"embed"
	"io/fs"
	"mime"
	"net/url"
	"path"
	"strings"
)

// Origin is the one origin the window loads. The .invalid top-level domain
// never resolves, so nothing on the network can answer for it.
const Origin = "https://app.ouroboros.invalid"

// StartPage is the page the window opens.
const StartPage = Origin + "/index.html"

//go:embed web
var embedded embed.FS

// WebFS returns the embedded page files.
func WebFS() fs.FS {
	sub, err := fs.Sub(embedded, "web")
	if err != nil {
		// Not reachable: "web" is a valid, embedded folder.
		return embedded
	}
	return sub
}

// SameOrigin reports whether uri is a document or file of Origin: the
// https scheme, exactly that host, no port and no user information.
func SameOrigin(uri string) bool {
	u, err := url.Parse(uri)
	if err != nil {
		return false
	}
	return u.Scheme == "https" && u.Host == "app.ouroboros.invalid" && u.User == nil && u.Opaque == ""
}

// Response is the answer to a request of the page.
type Response struct {
	Status  int
	Reason  string
	Headers string
	Body    []byte
}

// securityHeaders go with every response. The policy allows only files of
// the origin itself: no inline scripts or styles, no frames, no other
// connections.
const securityHeaders = "Content-Security-Policy: default-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; object-src 'none'\r\n" +
	"X-Content-Type-Options: nosniff\r\n" +
	"Referrer-Policy: no-referrer\r\n" +
	"Cache-Control: no-store"

// Serve answers a request for a file of the page from files. Only GET of
// an existing file of Origin succeeds; everything else is refused.
func Serve(files fs.FS, method, uri string) Response {
	deny := func(status int, reason string) Response {
		return Response{Status: status, Reason: reason, Headers: "Content-Type: text/plain\r\n" + securityHeaders}
	}
	if !SameOrigin(uri) {
		return deny(403, "Forbidden")
	}
	if method != "GET" {
		return deny(405, "Method Not Allowed")
	}
	u, _ := url.Parse(uri)
	p := strings.TrimPrefix(path.Clean("/"+u.Path), "/")
	if p == "" {
		p = "index.html"
	}
	if !fs.ValidPath(p) {
		return deny(404, "Not Found")
	}
	body, err := fs.ReadFile(files, p)
	if err != nil {
		return deny(404, "Not Found")
	}
	ctype := mime.TypeByExtension(path.Ext(p))
	switch path.Ext(p) {
	case ".html":
		ctype = "text/html; charset=utf-8"
	case ".js":
		ctype = "text/javascript; charset=utf-8"
	case ".css":
		ctype = "text/css; charset=utf-8"
	}
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	return Response{Status: 200, Reason: "OK", Headers: "Content-Type: " + ctype + "\r\n" + securityHeaders, Body: body}
}
