package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/AkkunYo/SyncHub/internal/platform"
	"gopkg.in/yaml.v3"
)

func TestDeepCopyClonesUpstreamGroupPointerAndModels(t *testing.T) {
	t.Parallel()

	original := Config{Upstreams: []UpstreamConfig{{
		ID: "source-a",
		SyncMappings: []SyncMapping{{
			UpstreamAssetID: "source-a:token:42",
			UpstreamGroup: &platform.UpstreamGroupSnapshot{
				Group: "vip", Ratio: 1.5, RatioKnown: true,
				Models: []string{"gpt-4o"}, ModelsVerified: true,
			},
		}},
	}}}

	cloned := deepCopy(original)
	cloned.Upstreams[0].SyncMappings[0].UpstreamGroup.Group = "mutated"
	cloned.Upstreams[0].SyncMappings[0].UpstreamGroup.Models[0] = "mutated-model"
	if got := original.Upstreams[0].SyncMappings[0].UpstreamGroup; got.Group != "vip" || !reflect.DeepEqual(got.Models, []string{"gpt-4o"}) {
		t.Fatalf("deepCopy shared upstream group storage: %#v", got)
	}
	if cloned.Upstreams[0].SyncMappings[0].UpstreamGroup == original.Upstreams[0].SyncMappings[0].UpstreamGroup {
		t.Fatal("deepCopy retained upstream group pointer")
	}
}

func TestUpstreamGroupModelsVerifiedRoundTripsThroughYAML(t *testing.T) {
	t.Parallel()

	want := Config{Upstreams: []UpstreamConfig{{
		ID: "source-a",
		SyncMappings: []SyncMapping{{
			UpstreamAssetID: "source-a:token:42",
			UpstreamGroup: &platform.UpstreamGroupSnapshot{
				Group: "vip", Models: []string{"gpt-4o"}, ModelsVerified: true,
			},
		}},
	}}}
	encoded, err := yaml.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "models_verified: true") {
		t.Fatalf("YAML omitted models_verified:\n%s", encoded)
	}
	var got Config
	if err := yaml.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if group := got.Upstreams[0].SyncMappings[0].UpstreamGroup; group == nil || !group.ModelsVerified {
		t.Fatalf("YAML round trip group = %#v", group)
	}
}
