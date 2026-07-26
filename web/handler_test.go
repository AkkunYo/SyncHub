package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

const testIndex = `<!doctype html><html><body><div id="app"></div><script type="module" src="/assets/app-a1b2c3d4.js"></script></body></html>`

func testAssets() fs.FS {
	return fstest.MapFS{
		"index.html":             {Data: []byte(testIndex)},
		"assets/app-a1b2c3d4.js": {Data: []byte("console.log('SyncHub')\n")},
		"assets/plain.js":        {Data: []byte("console.log('plain')\n")},
	}
}

func request(t *testing.T, handler http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, target, nil))
	return recorder
}

func assertSPAHeaders(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()

	csp := response.Header().Get("Content-Security-Policy")
	for _, directive := range []string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self'",
		"connect-src 'self'",
		"object-src 'none'",
		"base-uri 'none'",
		"frame-ancestors 'none'",
	} {
		if !strings.Contains(csp, directive) {
			t.Errorf("CSP %q does not contain %q", csp, directive)
		}
	}
	if strings.Contains(csp, "'unsafe-") {
		t.Errorf("CSP contains an unsafe source: %q", csp)
	}
	if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := response.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", got)
	}
}

func TestHandlerDelegatesAPIWithoutOverridingHeaders(t *testing.T) {
	var called []string
	api := http.HandlerFunc(func(response http.ResponseWriter, req *http.Request) {
		called = append(called, req.Method+" "+req.URL.RequestURI())
		response.Header().Set("Content-Security-Policy", "default-src 'none'")
		response.Header().Set("Cache-Control", "private, no-store")
		response.WriteHeader(http.StatusCreated)
		_, _ = response.Write([]byte(`{"source":"api"}`))
	})
	handler := newHandler(api, testAssets())

	for _, target := range []string{"http://synchub.test/api", "http://synchub.test/api/v1/config?fresh=true"} {
		response := request(t, handler, http.MethodPost, target)
		if response.Code != http.StatusCreated {
			t.Errorf("POST %s status = %d, want %d", target, response.Code, http.StatusCreated)
		}
		if got := response.Header().Get("Content-Security-Policy"); got != "default-src 'none'" {
			t.Errorf("POST %s CSP = %q, want API CSP", target, got)
		}
		if got := response.Header().Get("Cache-Control"); got != "private, no-store" {
			t.Errorf("POST %s Cache-Control = %q, want API value", target, got)
		}
		if got := response.Body.String(); got != `{"source":"api"}` {
			t.Errorf("POST %s body = %q", target, got)
		}
	}

	want := []string{"POST /api", "POST /api/v1/config?fresh=true"}
	if strings.Join(called, "|") != strings.Join(want, "|") {
		t.Fatalf("API calls = %v, want %v", called, want)
	}
}

func TestHandlerServesIndexAndClientRoutes(t *testing.T) {
	handler := newHandler(http.NotFoundHandler(), testAssets())

	for _, target := range []string{"http://synchub.test/", "http://synchub.test/matrix", "http://synchub.test/settings/", "http://synchub.test/apix"} {
		t.Run(target, func(t *testing.T) {
			response := request(t, handler, http.MethodGet, target)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
			}
			if got := response.Body.String(); got != testIndex {
				t.Fatalf("body = %q, want embedded index", got)
			}
			if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
				t.Errorf("Content-Type = %q, want text/html", got)
			}
			if got := response.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
			assertSPAHeaders(t, response)
		})
	}
}

func TestHandlerServesAssetsWithStableDimensionsAndCaching(t *testing.T) {
	handler := newHandler(http.NotFoundHandler(), testAssets())

	hashed := request(t, handler, http.MethodGet, "http://synchub.test/assets/app-a1b2c3d4.js")
	if hashed.Code != http.StatusOK {
		t.Fatalf("hashed asset status = %d, want %d", hashed.Code, http.StatusOK)
	}
	if got := hashed.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("hashed asset Cache-Control = %q", got)
	}
	if got := hashed.Header().Get("Content-Type"); !strings.Contains(got, "javascript") {
		t.Errorf("hashed asset Content-Type = %q, want JavaScript", got)
	}
	assertSPAHeaders(t, hashed)

	plain := request(t, handler, http.MethodGet, "http://synchub.test/assets/plain.js")
	if got := plain.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("plain asset Cache-Control = %q, want no-store", got)
	}

	head := request(t, handler, http.MethodHead, "http://synchub.test/assets/app-a1b2c3d4.js")
	if head.Code != http.StatusOK {
		t.Fatalf("HEAD status = %d, want %d", head.Code, http.StatusOK)
	}
	if head.Body.Len() != 0 {
		t.Fatalf("HEAD body length = %d, want 0", head.Body.Len())
	}
	if got := head.Header().Get("Content-Length"); got != strconv.Itoa(len("console.log('SyncHub')\n")) {
		t.Errorf("HEAD Content-Length = %q", got)
	}
}

func TestHandlerRejectsMissingResourcesDirectoriesAndWrites(t *testing.T) {
	handler := newHandler(http.NotFoundHandler(), testAssets())

	for _, target := range []string{
		"http://synchub.test/assets/missing.js",
		"http://synchub.test/assets/",
		"http://synchub.test/missing.css",
	} {
		response := request(t, handler, http.MethodGet, target)
		if response.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want %d", target, response.Code, http.StatusNotFound)
		}
		if strings.Contains(response.Body.String(), `<div id="app">`) {
			t.Errorf("GET %s unexpectedly returned the SPA index", target)
		}
	}

	response := request(t, handler, http.MethodPost, "http://synchub.test/settings")
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST SPA status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
	if got := response.Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q, want GET, HEAD", got)
	}
	assertSPAHeaders(t, response)
}

func TestHandlerRejectsTraversal(t *testing.T) {
	handler := newHandler(http.NotFoundHandler(), testAssets())

	for _, target := range []string{
		"http://synchub.test/../index.html",
		"http://synchub.test/%2e%2e/index.html",
		"http://synchub.test/assets%2f..%2findex.html",
		"http://synchub.test/assets\\..\\index.html",
	} {
		response := request(t, handler, http.MethodGet, target)
		if response.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want %d", target, response.Code, http.StatusNotFound)
		}
		if response.Body.String() == testIndex {
			t.Errorf("GET %s returned index through a traversal path", target)
		}
	}
}

func TestNewHandlerUsesEmbeddedViteBuild(t *testing.T) {
	handler := NewHandler(http.NotFoundHandler())
	index := request(t, handler, http.MethodGet, "http://synchub.test/")
	if index.Code != http.StatusOK {
		t.Fatalf("embedded index status = %d, want %d", index.Code, http.StatusOK)
	}
	if !strings.Contains(index.Body.String(), `<div id="app"></div>`) {
		t.Fatal("embedded index is not the Vite application entry point")
	}
	for _, forbidden := range []string{
		`http-equiv="Content-Security-Policy"`,
		"'unsafe-inline'",
		"localhost",
		"ws://",
	} {
		if strings.Contains(index.Body.String(), forbidden) {
			t.Errorf("embedded index contains forbidden CSP content %q", forbidden)
		}
	}

	assetPattern := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`)
	matches := assetPattern.FindAllStringSubmatch(index.Body.String(), -1)
	if len(matches) == 0 {
		t.Fatal("embedded Vite index references no production assets")
	}
	for _, match := range matches {
		asset := request(t, handler, http.MethodGet, "http://synchub.test"+match[1])
		if asset.Code != http.StatusOK {
			t.Errorf("embedded asset %s status = %d, want %d", match[1], asset.Code, http.StatusOK)
		}
	}
}
