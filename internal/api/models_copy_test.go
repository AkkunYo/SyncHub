package api

import (
	"reflect"
	"testing"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

func TestCloneMappingsForResponseDeepCopiesUpstreamGroup(t *testing.T) {
	t.Parallel()

	input := []platform.SyncMapping{{
		UpstreamAssetID: "source-a:token:42",
		Snapshot:        platform.ChannelSnapshot{Models: []string{"gpt-4o"}},
		UpstreamGroup: &platform.UpstreamGroupSnapshot{
			Group: "vip", Ratio: 1.5, RatioKnown: true,
			Models: []string{"gpt-4o"}, ModelsVerified: true,
		},
	}}
	cloned := cloneMappingsForResponse(input)
	cloned[0].UpstreamGroup.Group = "mutated"
	cloned[0].UpstreamGroup.Models[0] = "mutated-model"
	if group := input[0].UpstreamGroup; group.Group != "vip" || !reflect.DeepEqual(group.Models, []string{"gpt-4o"}) {
		t.Fatalf("API clone shared upstream group storage: %#v", group)
	}
	if cloned[0].UpstreamGroup == input[0].UpstreamGroup {
		t.Fatal("API clone retained upstream group pointer")
	}
}
