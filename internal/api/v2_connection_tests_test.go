package api

import (
	"net/http"
	"testing"
)

func TestV2TargetConnectionTestIsReadOnly(t *testing.T) {
	env := newTestEnvironment()
	target := env.resolver.targets["target-a"].adapter.(*fakeTarget)

	recorder, envelope := request(
		t,
		env.router(t),
		http.MethodPost,
		"/api/v1/targets/target-a/connection-tests",
		"",
		"",
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	data := dataObject(t, envelope)
	if data["reachable"] != true || data["authenticated"] != true || data["authorized"] != true {
		t.Fatalf("connection result = %#v", data)
	}
	if data["resource_count"] != float64(0) {
		t.Fatalf("resource_count = %#v", data["resource_count"])
	}
	if target.listCalls != 1 || target.updateCalls != 0 || target.deleteCalls != 0 {
		t.Fatalf("target calls: list=%d update=%d delete=%d", target.listCalls, target.updateCalls, target.deleteCalls)
	}
}

func TestV2UpstreamConnectionTestRefreshesWithoutResolvingSecrets(t *testing.T) {
	env := newTestEnvironment()
	upstream := env.resolver.upstreams["source-a"].(*fakeUpstream)

	recorder, envelope := request(
		t,
		env.router(t),
		http.MethodPost,
		"/api/v1/upstreams/source-a/connection-tests",
		"",
		"",
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	data := dataObject(t, envelope)
	if data["reachable"] != true || data["authenticated"] != true || data["authorized"] != true {
		t.Fatalf("connection result = %#v", data)
	}
	if env.fakeDisc.refreshCalls != 1 || upstream.resolveCalls != 0 {
		t.Fatalf("refresh calls=%d secret calls=%d", env.fakeDisc.refreshCalls, upstream.resolveCalls)
	}
}
