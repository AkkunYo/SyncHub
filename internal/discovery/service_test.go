package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AkkunYo/SyncHub/internal/platform"
)

func TestRefreshReadsEveryPageDeduplicatesAndReturnsDeepCopies(t *testing.T) {
	t.Parallel()

	first := testAsset("asset-1", "first")
	duplicate := testAsset("asset-2", "duplicate")
	last := testAsset("asset-3", "last")
	next := platform.PageCursor{Page: 2, PageSize: 2, Token: "page-2"}
	adapter := newScriptedAdapter(t,
		listStep{
			want: platform.PageCursor{},
			page: platform.AssetPage{
				Assets:  []platform.UpstreamAsset{first, duplicate},
				Next:    next,
				HasMore: true,
			},
		},
		listStep{
			want: next,
			page: platform.AssetPage{
				Assets: []platform.UpstreamAsset{duplicate, last},
			},
		},
	)
	service := NewService()

	got, err := service.Refresh(context.Background(), "source-a", adapter)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	want := Snapshot{SourceID: "source-a", Assets: []platform.UpstreamAsset{first, duplicate, last}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Refresh() = %#v, want %#v", got, want)
	}
	if cursors := adapter.gotCursors(); !reflect.DeepEqual(cursors, []platform.PageCursor{{}, next}) {
		t.Fatalf("ListAssets cursors = %#v", cursors)
	}
	if calls := adapter.resolveCallCount(); calls != 0 {
		t.Fatalf("ResolveSecret calls = %d, want 0", calls)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), adapter.resolvedSecret) {
		t.Fatal("snapshot contains resolved secret")
	}

	got.Assets[0].Models[0] = "caller-mutated-model"
	got.Assets[0].Metadata["region"] = "caller-mutated-region"
	first.Models[0] = "adapter-mutated-model"
	first.Metadata["region"] = "adapter-mutated-region"

	stored, ok := service.Snapshot("source-a")
	if !ok {
		t.Fatal("Snapshot(source-a) was not published")
	}
	assertAssetUnchanged(t, stored.Assets[0])
	stored.Assets[0].Models[0] = "snapshot-caller-mutated-model"
	stored.Assets[0].Metadata["region"] = "snapshot-caller-mutated-region"

	again, ok := service.Snapshot("source-a")
	if !ok {
		t.Fatal("second Snapshot(source-a) was not published")
	}
	assertAssetUnchanged(t, again.Assets[0])
	if _, ok := service.Snapshot("missing-source"); ok {
		t.Fatal("Snapshot(missing-source) unexpectedly exists")
	}
}

func TestRefreshPublishesOnlyAfterTheFinalPageSucceeds(t *testing.T) {
	t.Parallel()

	service, old := seedSnapshot(t)
	pageTwoStarted := make(chan struct{}, 1)
	releasePageTwo := make(chan struct{})
	next := platform.PageCursor{Token: "page-2"}
	adapter := newScriptedAdapter(t,
		listStep{
			want: platform.PageCursor{},
			page: platform.AssetPage{
				Assets:  []platform.UpstreamAsset{testAsset("new-1", "new first")},
				Next:    next,
				HasMore: true,
			},
		},
		listStep{
			want:    next,
			page:    platform.AssetPage{Assets: []platform.UpstreamAsset{testAsset("new-2", "new last")}},
			started: pageTwoStarted,
			wait:    releasePageTwo,
		},
	)
	type refreshResult struct {
		snapshot Snapshot
		err      error
	}
	result := make(chan refreshResult, 1)
	go func() {
		snapshot, err := service.Refresh(context.Background(), "source-a", adapter)
		result <- refreshResult{snapshot: snapshot, err: err}
	}()

	select {
	case <-pageTwoStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("Refresh did not request the final page")
	}
	during, ok := service.Snapshot("source-a")
	if !ok || !reflect.DeepEqual(during, old) {
		t.Fatalf("snapshot changed before final page completed: %#v", during)
	}
	close(releasePageTwo)

	select {
	case completed := <-result:
		if completed.err != nil {
			t.Fatalf("Refresh() error = %v", completed.err)
		}
		if ids := assetIDs(completed.snapshot); !reflect.DeepEqual(ids, []string{"new-1", "new-2"}) {
			t.Fatalf("published asset IDs = %v", ids)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Refresh did not finish after the final page was released")
	}
}

func TestRefreshPageFailurePreservesPreviousSnapshot(t *testing.T) {
	t.Parallel()

	service, old := seedSnapshot(t)
	pageFailure := errors.New("page failed")
	next := platform.PageCursor{Page: 2}
	adapter := newScriptedAdapter(t,
		listStep{
			want: platform.PageCursor{},
			page: platform.AssetPage{
				Assets:  []platform.UpstreamAsset{testAsset("partial", "partial")},
				Next:    next,
				HasMore: true,
			},
		},
		listStep{want: next, err: pageFailure},
	)

	if _, err := service.Refresh(context.Background(), "source-a", adapter); !errors.Is(err, pageFailure) {
		t.Fatalf("Refresh() error = %v, want page failure", err)
	}
	assertSnapshotEqual(t, service, old)
}

func TestRefreshCursorLoopPreservesPreviousSnapshot(t *testing.T) {
	t.Parallel()

	service, old := seedSnapshot(t)
	next := platform.PageCursor{Token: "next"}
	adapter := newScriptedAdapter(t,
		listStep{
			want: platform.PageCursor{},
			page: platform.AssetPage{
				Assets:  []platform.UpstreamAsset{testAsset("loop-1", "loop one")},
				Next:    next,
				HasMore: true,
			},
		},
		listStep{
			want: next,
			page: platform.AssetPage{
				Assets:  []platform.UpstreamAsset{testAsset("loop-2", "loop two")},
				Next:    platform.PageCursor{},
				HasMore: true,
			},
		},
	)

	if _, err := service.Refresh(context.Background(), "source-a", adapter); !errors.Is(err, ErrCursorLoop) {
		t.Fatalf("Refresh() error = %v, want ErrCursorLoop", err)
	}
	if calls := len(adapter.gotCursors()); calls != 2 {
		t.Fatalf("ListAssets calls = %d, want 2 before loop detection", calls)
	}
	assertSnapshotEqual(t, service, old)
}

func TestRefreshCancellationPreservesPreviousSnapshot(t *testing.T) {
	t.Parallel()

	service, old := seedSnapshot(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	adapter := newScriptedAdapter(t, listStep{
		want:         platform.PageCursor{},
		page:         platform.AssetPage{Assets: []platform.UpstreamAsset{testAsset("cancelled", "cancelled")}},
		beforeReturn: cancel,
	})

	if _, err := service.Refresh(ctx, "source-a", adapter); !errors.Is(err, context.Canceled) {
		t.Fatalf("Refresh() error = %v, want context.Canceled", err)
	}
	assertSnapshotEqual(t, service, old)
}

func TestRefreshConflictingDuplicatePreservesPreviousSnapshot(t *testing.T) {
	t.Parallel()

	service, old := seedSnapshot(t)
	first := testAsset("duplicate", "first metadata")
	conflict := testAsset("duplicate", "conflicting metadata")
	next := platform.PageCursor{Token: "next"}
	adapter := newScriptedAdapter(t,
		listStep{
			want: platform.PageCursor{},
			page: platform.AssetPage{Assets: []platform.UpstreamAsset{first}, Next: next, HasMore: true},
		},
		listStep{
			want: next,
			page: platform.AssetPage{Assets: []platform.UpstreamAsset{conflict}},
		},
	)

	if _, err := service.Refresh(context.Background(), "source-a", adapter); !errors.Is(err, ErrAssetConflict) {
		t.Fatalf("Refresh() error = %v, want ErrAssetConflict", err)
	}
	assertSnapshotEqual(t, service, old)
}

func TestRefreshPublishesAnEmptySnapshot(t *testing.T) {
	t.Parallel()

	service := NewService()
	adapter := newScriptedAdapter(t, listStep{want: platform.PageCursor{}, page: platform.AssetPage{}})

	got, err := service.Refresh(context.Background(), "empty-source", adapter)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if got.SourceID != "empty-source" || len(got.Assets) != 0 {
		t.Fatalf("Refresh() = %#v", got)
	}
	stored, ok := service.Snapshot("empty-source")
	if !ok || stored.SourceID != "empty-source" || len(stored.Assets) != 0 {
		t.Fatalf("Snapshot(empty-source) = %#v, %v", stored, ok)
	}
}

type listStep struct {
	want         platform.PageCursor
	page         platform.AssetPage
	err          error
	beforeReturn func()
	started      chan<- struct{}
	wait         <-chan struct{}
}

type scriptedAdapter struct {
	t              *testing.T
	mu             sync.Mutex
	steps          []listStep
	cursors        []platform.PageCursor
	resolveCalls   int
	resolvedSecret string
}

func newScriptedAdapter(t *testing.T, steps ...listStep) *scriptedAdapter {
	t.Helper()
	return &scriptedAdapter{t: t, steps: steps, resolvedSecret: "resolved-secret-must-not-appear"}
}

func (a *scriptedAdapter) Capabilities(context.Context) (platform.SourceCapabilities, error) {
	return platform.SourceCapabilities{}, nil
}

func (a *scriptedAdapter) ListAssets(ctx context.Context, cursor platform.PageCursor) (platform.AssetPage, error) {
	a.mu.Lock()
	index := len(a.cursors)
	a.cursors = append(a.cursors, cursor)
	if index >= len(a.steps) {
		a.mu.Unlock()
		return platform.AssetPage{}, fmt.Errorf("unexpected ListAssets call %d with cursor %#v", index+1, cursor)
	}
	step := a.steps[index]
	a.mu.Unlock()

	if cursor != step.want {
		a.t.Errorf("ListAssets call %d cursor = %#v, want %#v", index+1, cursor, step.want)
	}
	if step.started != nil {
		step.started <- struct{}{}
	}
	if step.wait != nil {
		select {
		case <-step.wait:
		case <-ctx.Done():
			return platform.AssetPage{}, ctx.Err()
		}
	}
	if step.beforeReturn != nil {
		step.beforeReturn()
	}
	return step.page, step.err
}

func (a *scriptedAdapter) ResolveSecret(context.Context, string, platform.SecretGrant) (platform.ResolvedSecret, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.resolveCalls++
	return platform.ResolvedSecret{Bytes: []byte(a.resolvedSecret)}, nil
}

func (a *scriptedAdapter) gotCursors() []platform.PageCursor {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]platform.PageCursor(nil), a.cursors...)
}

func (a *scriptedAdapter) resolveCallCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.resolveCalls
}

func seedSnapshot(t *testing.T) (*Service, Snapshot) {
	t.Helper()
	service := NewService()
	adapter := newScriptedAdapter(t, listStep{
		want: platform.PageCursor{},
		page: platform.AssetPage{Assets: []platform.UpstreamAsset{testAsset("old", "old snapshot")}},
	})
	snapshot, err := service.Refresh(context.Background(), "source-a", adapter)
	if err != nil {
		t.Fatalf("seed Refresh() error = %v", err)
	}
	return service, snapshot
}

func testAsset(id, name string) platform.UpstreamAsset {
	return platform.UpstreamAsset{
		ID:             id,
		SourceID:       "source-a",
		SourceType:     "newapi",
		Provider:       platform.ProviderOpenAI,
		RawType:        "1",
		Kind:           platform.AssetStaticAPIKey,
		Name:           name,
		BaseURL:        "https://api.example.com",
		Models:         []string{"model-a", "model-b"},
		Enabled:        true,
		SecretReadable: true,
		Metadata:       map[string]string{"region": "east"},
	}
}

func assertAssetUnchanged(t *testing.T, got platform.UpstreamAsset) {
	t.Helper()
	if !reflect.DeepEqual(got.Models, []string{"model-a", "model-b"}) {
		t.Fatalf("stored models were mutated: %v", got.Models)
	}
	if !reflect.DeepEqual(got.Metadata, map[string]string{"region": "east"}) {
		t.Fatalf("stored metadata was mutated: %v", got.Metadata)
	}
}

func assertSnapshotEqual(t *testing.T, service *Service, want Snapshot) {
	t.Helper()
	got, ok := service.Snapshot(want.SourceID)
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("stored snapshot = %#v, %v; want %#v", got, ok, want)
	}
}

func assetIDs(snapshot Snapshot) []string {
	ids := make([]string, 0, len(snapshot.Assets))
	for _, asset := range snapshot.Assets {
		ids = append(ids, asset.ID)
	}
	return ids
}
