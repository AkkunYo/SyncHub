// Package web serves the embedded SyncHub console and delegates management API
// requests to the application handler.
package web

import (
	"embed"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const spaContentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"

var fingerprintedAsset = regexp.MustCompile(`(?:^|/)[^/]+-[A-Za-z0-9_-]{8,}\.[^/]+$`)

//go:embed all:dist
var embeddedFiles embed.FS

type handler struct {
	api    http.Handler
	assets fs.FS
}

// NewHandler combines the management API with the embedded production console.
// The returned handler does not need Node.js or files on disk at runtime.
func NewHandler(api http.Handler) http.Handler {
	assets, err := fs.Sub(embeddedFiles, "dist")
	if err != nil {
		panic("web: embedded dist directory is unavailable")
	}
	return newHandler(api, assets)
}

func newHandler(api http.Handler, assets fs.FS) http.Handler {
	if api == nil {
		api = http.NotFoundHandler()
	}
	return &handler{api: api, assets: assets}
}

func (h *handler) ServeHTTP(response http.ResponseWriter, req *http.Request) {
	if isAPIPath(req.URL.Path) {
		h.api.ServeHTTP(response, req)
		return
	}

	setSPAHeaders(response.Header())
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		response.Header().Set("Allow", "GET, HEAD")
		writeStatus(response, req.Method, http.StatusMethodNotAllowed)
		return
	}

	name, ok := requestedFile(req.URL)
	if !ok {
		writeStatus(response, req.Method, http.StatusNotFound)
		return
	}

	data, servedName, ok := h.resolve(name)
	if !ok {
		writeStatus(response, req.Method, http.StatusNotFound)
		return
	}

	if fingerprintedAsset.MatchString(servedName) {
		response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	contentType := mime.TypeByExtension(path.Ext(servedName))
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	response.Header().Set("Content-Type", contentType)
	response.Header().Set("Content-Length", strconv.Itoa(len(data)))
	response.WriteHeader(http.StatusOK)
	if req.Method == http.MethodGet {
		_, _ = response.Write(data)
	}
}

func (h *handler) resolve(name string) ([]byte, string, bool) {
	if name == "" {
		data, err := fs.ReadFile(h.assets, "index.html")
		return data, "index.html", err == nil
	}

	hadTrailingSlash := strings.HasSuffix(name, "/")
	lookup := strings.TrimSuffix(name, "/")
	info, err := fs.Stat(h.assets, lookup)
	if err == nil {
		if info.IsDir() || hadTrailingSlash {
			return nil, "", false
		}
		data, readErr := fs.ReadFile(h.assets, lookup)
		return data, lookup, readErr == nil
	}
	if !errors.Is(err, fs.ErrNotExist) || path.Ext(lookup) != "" {
		return nil, "", false
	}

	data, indexErr := fs.ReadFile(h.assets, "index.html")
	return data, "index.html", indexErr == nil
}

func isAPIPath(requestPath string) bool {
	return requestPath == "/api" || strings.HasPrefix(requestPath, "/api/")
}

func requestedFile(requestURL *url.URL) (string, bool) {
	escaped := requestURL.EscapedPath()
	decoded, err := url.PathUnescape(escaped)
	if err != nil || decoded != requestURL.Path || !strings.HasPrefix(decoded, "/") {
		return "", false
	}
	if strings.ContainsRune(decoded, '\x00') || strings.Contains(decoded, `\`) {
		return "", false
	}

	segments := strings.Split(decoded, "/")
	for index, segment := range segments {
		if segment == "." || segment == ".." {
			return "", false
		}
		if segment == "" && index != 0 && index != len(segments)-1 {
			return "", false
		}
	}

	name := strings.TrimPrefix(decoded, "/")
	validName := strings.TrimSuffix(name, "/")
	if validName != "" && !fs.ValidPath(validName) {
		return "", false
	}
	return name, true
}

func setSPAHeaders(header http.Header) {
	header.Set("Content-Security-Policy", spaContentSecurityPolicy)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Frame-Options", "DENY")
	header.Set("Cache-Control", "no-store")
}

func writeStatus(response http.ResponseWriter, method string, status int) {
	body := http.StatusText(status) + "\n"
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.Header().Set("Content-Length", strconv.Itoa(len(body)))
	response.WriteHeader(status)
	if method != http.MethodHead {
		_, _ = response.Write([]byte(body))
	}
}
