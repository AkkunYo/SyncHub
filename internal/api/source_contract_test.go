package api

import (
	"net/http"
	"testing"
)

func TestUpstreamCreateAllowsOnlyNewAPIUserTokenAndGenericTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		body   string
		status int
	}{
		{
			name:   "New API user token",
			body:   `{"id":"source-newapi","name":"New API User","type":"newapi","base_url":"https://newapi.example.com","access_token":"user-management-token"}`,
			status: http.StatusCreated,
		},
		{
			name:   "New API explicit token compatibility value",
			body:   `{"id":"source-newapi","name":"New API User","type":"newapi","base_url":"https://newapi.example.com","access_token":"user-management-token","discovery_mode":"token"}`,
			status: http.StatusCreated,
		},
		{
			name:   "New API auto mode",
			body:   `{"id":"source-newapi","name":"New API User","type":"newapi","base_url":"https://newapi.example.com","access_token":"user-management-token","discovery_mode":"auto"}`,
			status: http.StatusBadRequest,
		},
		{
			name:   "New API channel mode",
			body:   `{"id":"source-newapi","name":"New API User","type":"newapi","base_url":"https://newapi.example.com","access_token":"user-management-token","discovery_mode":"channel"}`,
			status: http.StatusBadRequest,
		},
		{
			name:   "New API API key alias",
			body:   `{"id":"source-newapi","name":"New API User","type":"newapi","base_url":"https://newapi.example.com","api_key":"ambiguous-key"}`,
			status: http.StatusBadRequest,
		},
		{
			name:   "CPA management source",
			body:   `{"id":"source-cpa","name":"CPA","type":"cliproxyapi","base_url":"https://cpa.example.com","management_key":"management-key"}`,
			status: http.StatusBadRequest,
		},
		{
			name:   "Sub2Api management source",
			body:   `{"id":"source-sub2api","name":"Sub2Api","type":"sub2api","base_url":"https://sub2api.example.com","api_key":"admin-key"}`,
			status: http.StatusBadRequest,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			env := newTestEnvironment()
			recorder, envelope := request(t, env.router(t), http.MethodPost, "/api/v1/upstreams", test.body, "application/json")
			if recorder.Code != test.status {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if test.status == http.StatusBadRequest && errorCode(t, envelope) != "invalid_request" {
				t.Fatalf("error = %#v", envelope)
			}
			if test.status == http.StatusCreated {
				created := dataObject(t, envelope)
				if created["discovery_mode"] != "token" {
					t.Fatalf("created upstream = %#v", created)
				}
			}
		})
	}
}
