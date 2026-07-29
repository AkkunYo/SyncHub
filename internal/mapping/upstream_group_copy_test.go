package mapping_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/AkkunYo/SyncHub/internal/mapping"
	"github.com/AkkunYo/SyncHub/internal/platform"
)

func TestRepositoryDeepCopiesUpstreamGroupOnSaveAndRead(t *testing.T) {
	t.Parallel()

	store := openStore(t, nil)
	repository := mapping.NewRepository(store)
	source := repository.ForSource("source-a")
	want := testMapping("source-a:token:42", "target-a", "channel-1", "gpt-4o")
	want.UpstreamGroup = &platform.UpstreamGroupSnapshot{
		Group: "vip", Ratio: 1.5, RatioKnown: true,
		Models: []string{"gpt-4o"}, ModelsVerified: true,
	}
	if err := source.SaveMappings(context.Background(), []platform.SyncMapping{want}); err != nil {
		t.Fatalf("SaveMappings() error = %v", err)
	}

	want.UpstreamGroup.Group = "caller-mutated"
	want.UpstreamGroup.Models[0] = "caller-mutated-model"
	first, err := repository.ListMappings(context.Background(), "target-a")
	if err != nil {
		t.Fatal(err)
	}
	assertStoredGroup(t, first)

	first[0].UpstreamGroup.Group = "reader-mutated"
	first[0].UpstreamGroup.Models[0] = "reader-mutated-model"
	second, err := repository.ListMappings(context.Background(), "target-a")
	if err != nil {
		t.Fatal(err)
	}
	assertStoredGroup(t, second)
}

func assertStoredGroup(t *testing.T, mappings []platform.SyncMapping) {
	t.Helper()
	if len(mappings) != 1 || mappings[0].UpstreamGroup == nil {
		t.Fatalf("mappings = %#v", mappings)
	}
	group := mappings[0].UpstreamGroup
	if group.Group != "vip" || group.Ratio != 1.5 || !group.RatioKnown || !group.ModelsVerified || !reflect.DeepEqual(group.Models, []string{"gpt-4o"}) {
		t.Fatalf("upstream group = %#v", group)
	}
}
