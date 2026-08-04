package api

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestV2ConnectionKeyAndModelRoutes(t *testing.T) {
	router := newTestEnvironment().router(t)
	routes := make(map[string]struct{})
	for _, route := range router.(*gin.Engine).Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}

	want := []string{
		"POST /api/v1/targets/:target_id/connection-tests",
		"POST /api/v1/upstreams/:upstream_id/connection-tests",
		"GET /api/v1/upstreams/:upstream_id/keys",
		"POST /api/v1/upstreams/:upstream_id/keys",
		"PATCH /api/v1/upstreams/:upstream_id/keys/:key_id",
		"DELETE /api/v1/upstreams/:upstream_id/keys/:key_id",
		"POST /api/v1/upstreams/:upstream_id/model-discoveries",
		"GET /api/v1/upstreams/:upstream_id/keys/:key_id/models",
		"POST /api/v1/upstreams/:upstream_id/keys/:key_id/model-probes",
	}
	for _, route := range want {
		if _, ok := routes[route]; !ok {
			t.Errorf("route %q is not registered", route)
		}
	}
}
