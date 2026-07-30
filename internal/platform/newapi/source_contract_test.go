package newapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

func TestSourceAcceptsOnlyOrdinaryUserTokenMode(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"auto", "channel"} {
		if _, err := NewSource(Config{
			SourceID: "source-a", BaseURL: "https://newapi.example.com",
			AccessToken: "user-management-token", DiscoveryMode: mode,
		}, nil); err == nil {
			t.Fatalf("NewSource(discovery_mode=%q) error = nil", mode)
		}
	}

	for _, mode := range []string{"", "token"} {
		mode := mode
		t.Run("mode "+mode, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/token/" {
					t.Fatalf("ordinary-user source called %s", r.URL.Path)
				}
				_, _ = w.Write([]byte(`{"success":true,"data":{"items":[],"total":0,"page":1,"page_size":100}}`))
			}))
			t.Cleanup(server.Close)
			source, err := NewSource(Config{
				SourceID: "source-a", BaseURL: server.URL,
				AccessToken: "user-management-token", DiscoveryMode: mode,
			}, server.Client())
			if err != nil {
				t.Fatalf("NewSource() error = %v", err)
			}
			capabilities, err := source.Capabilities(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !capabilities.SecretResolution || !capabilities.GroupCatalog || !reflect.DeepEqual(capabilities.AssetKinds, []platform.AssetKind{platform.AssetProxyKey}) {
				t.Fatalf("capabilities = %#v", capabilities)
			}
			if _, err := source.ListAssets(context.Background(), platform.PageCursor{}); err != nil {
				t.Fatalf("ListAssets() error = %v", err)
			}
			if got := source.DiscoveryModeStatus(); got != (platform.DiscoveryModeStatus{EffectiveMode: "token", Status: "ready"}) {
				t.Fatalf("DiscoveryModeStatus() = %#v", got)
			}
		})
	}
}

func setNewAPIUserIDForTest(t *testing.T, config any, userID int) {
	t.Helper()
	field := reflect.ValueOf(config).Elem().FieldByName("UserID")
	if !field.IsValid() || !field.CanSet() || field.Kind() != reflect.Int {
		t.Fatal("New API adapter configuration is missing an integer UserID field")
	}
	field.SetInt(int64(userID))
}
