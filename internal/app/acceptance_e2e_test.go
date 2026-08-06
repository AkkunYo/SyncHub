package app_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/AkkunYo/SyncHub/internal/api"
	"github.com/AkkunYo/SyncHub/internal/app"
	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/platform"
)

const (
	acceptanceUpstreamToken = "REPLACE_WITH_UPSTREAM_MANAGEMENT_TOKEN"
	acceptanceAssetKey      = "sk-REPLACE_WITH_UPSTREAM_ASSET_KEY"
	acceptanceProof         = "REPLACE_WITH_SECURITY_PROOF"
	acceptanceTargetAToken  = "REPLACE_WITH_TARGET_A_ACCESS_TOKEN"
	acceptanceTargetBToken  = "REPLACE_WITH_TARGET_B_ACCESS_TOKEN"
)

type acceptanceChannel struct {
	ID       int    `json:"id"`
	Type     int    `json:"type"`
	Name     string `json:"name"`
	Status   int    `json:"status"`
	BaseURL  string `json:"base_url"`
	Models   string `json:"models"`
	Group    string `json:"group"`
	Priority int    `json:"priority"`
	Weight   int    `json:"weight"`
}

type acceptanceTarget struct {
	token string

	mu             sync.Mutex
	nextID         int
	channels       map[int]acceptanceChannel
	failSecondPage bool
	reportedTotal  int
	listPages      []int
	server         *httptest.Server
}

func newAcceptanceTarget(t *testing.T, token string, nextID int) *acceptanceTarget {
	t.Helper()
	target := &acceptanceTarget{
		token: token, nextID: nextID, channels: make(map[int]acceptanceChannel),
	}
	target.server = httptest.NewServer(http.HandlerFunc(target.serveHTTP))
	t.Cleanup(target.server.Close)
	return target
}

func (t *acceptanceTarget) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer "+t.token {
		http.Error(w, `{"success":false}`, http.StatusUnauthorized)
		return
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/channel/":
		t.listChannels(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/channel/":
		t.createChannel(w, r)
	case r.Method == http.MethodPut && r.URL.Path == "/api/channel/":
		t.updateChannel(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/status"):
		t.updateStatus(w, r)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/channel/"):
		t.deleteChannel(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/channel/"):
		t.getChannel(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (t *acceptanceTarget) listChannels(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("p"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if page < 1 || pageSize < 1 {
		http.Error(w, `{"success":false}`, http.StatusBadRequest)
		return
	}

	t.mu.Lock()
	t.listPages = append(t.listPages, page)
	if t.failSecondPage && page == 2 {
		t.mu.Unlock()
		http.Error(w, `{"success":false}`, http.StatusBadGateway)
		return
	}
	items := make([]acceptanceChannel, 0, len(t.channels))
	for _, channel := range t.channels {
		items = append(items, channel)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	total := len(items)
	if t.reportedTotal > total {
		total = t.reportedTotal
	}
	start := (page - 1) * pageSize
	if start > len(items) {
		start = len(items)
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	pageItems := append([]acceptanceChannel(nil), items[start:end]...)
	t.mu.Unlock()

	writeAcceptanceJSON(w, map[string]any{
		"success": true,
		"data": map[string]any{
			"items": pageItems, "total": total, "page": page, "page_size": pageSize,
		},
	})
}

func (t *acceptanceTarget) createChannel(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Mode    string `json:"mode"`
		Channel struct {
			Type     int    `json:"type"`
			Key      string `json:"key"`
			Status   int    `json:"status"`
			Name     string `json:"name"`
			Weight   int    `json:"weight"`
			BaseURL  string `json:"base_url"`
			Models   string `json:"models"`
			Group    string `json:"group"`
			Priority int    `json:"priority"`
		} `json:"channel"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil || request.Mode != "single" || request.Channel.Key != acceptanceAssetKey {
		http.Error(w, `{"success":false}`, http.StatusBadRequest)
		return
	}
	t.mu.Lock()
	id := t.nextID
	t.nextID++
	t.channels[id] = acceptanceChannel{
		ID: id, Type: request.Channel.Type, Name: request.Channel.Name, Status: request.Channel.Status,
		BaseURL: request.Channel.BaseURL, Models: request.Channel.Models, Group: request.Channel.Group,
		Priority: request.Channel.Priority, Weight: request.Channel.Weight,
	}
	t.mu.Unlock()
	writeAcceptanceJSON(w, map[string]any{"success": true, "data": map[string]any{"id": id}})
}

func (t *acceptanceTarget) updateChannel(w http.ResponseWriter, r *http.Request) {
	var request acceptanceChannel
	if json.NewDecoder(r.Body).Decode(&request) != nil || request.ID <= 0 {
		http.Error(w, `{"success":false}`, http.StatusBadRequest)
		return
	}
	t.mu.Lock()
	current, ok := t.channels[request.ID]
	if ok {
		request.Type = current.Type
		request.Status = current.Status
		t.channels[request.ID] = request
	}
	t.mu.Unlock()
	if !ok {
		http.Error(w, `{"success":false}`, http.StatusNotFound)
		return
	}
	writeAcceptanceJSON(w, map[string]any{"success": true, "data": request})
}

func (t *acceptanceTarget) updateStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := channelIDFromPath(r.URL.Path, "/status")
	var request struct {
		Status int `json:"status"`
	}
	if !ok || json.NewDecoder(r.Body).Decode(&request) != nil {
		http.Error(w, `{"success":false}`, http.StatusBadRequest)
		return
	}
	t.mu.Lock()
	channel, exists := t.channels[id]
	if exists {
		channel.Status = request.Status
		t.channels[id] = channel
	}
	t.mu.Unlock()
	if !exists {
		http.Error(w, `{"success":false}`, http.StatusNotFound)
		return
	}
	writeAcceptanceJSON(w, map[string]any{"success": true, "data": true})
}

func (t *acceptanceTarget) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := channelIDFromPath(r.URL.Path, "")
	if !ok {
		http.Error(w, `{"success":false}`, http.StatusBadRequest)
		return
	}
	t.mu.Lock()
	_, exists := t.channels[id]
	delete(t.channels, id)
	t.mu.Unlock()
	if !exists {
		http.Error(w, `{"success":false}`, http.StatusNotFound)
		return
	}
	writeAcceptanceJSON(w, map[string]any{"success": true, "data": true})
}

func (t *acceptanceTarget) getChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := channelIDFromPath(r.URL.Path, "")
	t.mu.Lock()
	channel, exists := t.channels[id]
	t.mu.Unlock()
	if !ok || !exists {
		http.Error(w, `{"success":false}`, http.StatusNotFound)
		return
	}
	writeAcceptanceJSON(w, map[string]any{"success": true, "data": channel})
}

func (t *acceptanceTarget) mutate(id int, mutate func(*acceptanceChannel)) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	channel, ok := t.channels[id]
	if !ok {
		return false
	}
	mutate(&channel)
	t.channels[id] = channel
	return true
}

func (t *acceptanceTarget) remove(id int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.channels[id]
	delete(t.channels, id)
	return ok
}

func (t *acceptanceTarget) channelCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.channels)
}

func channelIDFromPath(path, suffix string) (int, bool) {
	value := strings.TrimPrefix(path, "/api/channel/")
	value = strings.TrimSuffix(value, suffix)
	id, err := strconv.Atoi(value)
	return id, err == nil && id > 0
}

func writeAcceptanceJSON(w http.ResponseWriter, value any) {
	_ = json.NewEncoder(w).Encode(value)
}

func newAcceptanceUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer "+acceptanceUpstreamToken {
			http.Error(w, `{"success":false}`, http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/user/self":
			writeAcceptanceJSON(w, map[string]any{"success": true, "data": map[string]any{"role": 1, "group": "default"}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/user/self/groups":
			writeAcceptanceJSON(w, map[string]any{"success": true, "data": map[string]any{
				"default": map[string]any{"ratio": 1, "description": "Default group"},
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/token/" && r.URL.Query().Get("p") == "1":
			writeAcceptanceJSON(w, map[string]any{
				"success": true,
				"data": map[string]any{
					"items": []map[string]any{{
						"id": 7, "name": "primary", "status": 1, "group": "default",
						"remain_quota": 1000, "unlimited_quota": false, "expired_time": -1,
					}},
					"total": 1, "page": 1, "page_size": 100,
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/user/models" && r.URL.Query().Get("group") == "default":
			writeAcceptanceJSON(w, map[string]any{"success": true, "data": []string{"gpt-4.1"}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/token/batch/keys":
			writeAcceptanceJSON(w, map[string]any{"success": true, "data": map[string]any{"keys": map[string]string{"7": acceptanceAssetKey}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func createAcceptanceConfig(t *testing.T, path string, targets []config.TargetConfig, upstreams []config.UpstreamConfig) {
	t.Helper()
	store, err := config.Open(path)
	if err != nil {
		t.Fatalf("config.Open() error = %v", err)
	}
	if err := store.Update(t.Context(), func(cfg *config.Config) error {
		cfg.Targets = targets
		cfg.Upstreams = upstreams
		return nil
	}); err != nil {
		_ = store.Close()
		t.Fatalf("Store.Update() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Store.Close() error = %v", err)
	}
}

func newAcceptanceRouter(t *testing.T, path string) http.Handler {
	t.Helper()
	application, err := app.New(app.Options{ConfigPath: path, Version: "acceptance", HTTPClient: &http.Client{}})
	if err != nil {
		t.Fatalf("app.New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := application.Close(); err != nil {
			t.Errorf("Application.Close() error = %v", err)
		}
	})
	router, err := api.NewRouterWithRuntime(application.Dependencies(), application.Runtime())
	if err != nil {
		t.Fatalf("api.NewRouterWithRuntime() error = %v", err)
	}
	return router
}

func acceptanceRequest(t *testing.T, handler http.Handler, method, path string, body any, wantStatus int) map[string]any {
	t.Helper()
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		requestBody = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, requestBody)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != wantStatus {
		t.Fatalf("%s %s status = %d, want %d; body = %s", method, path, recorder.Code, wantStatus, recorder.Body.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("%s %s invalid response: %v; body = %s", method, path, err, recorder.Body.String())
	}
	return envelope
}

func acceptanceData(t *testing.T, envelope map[string]any) map[string]any {
	t.Helper()
	data, ok := envelope["data"].(map[string]any)
	if !ok || envelope["success"] != true {
		t.Fatalf("response envelope = %#v", envelope)
	}
	return data
}

func acceptanceMappingsByTarget(t *testing.T, path string) map[string]platform.SyncMapping {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	if len(cfg.Upstreams) != 1 {
		t.Fatalf("upstreams = %#v", cfg.Upstreams)
	}
	result := make(map[string]platform.SyncMapping, len(cfg.Upstreams[0].SyncMappings))
	for _, mapping := range cfg.Upstreams[0].SyncMappings {
		result[mapping.TargetID] = mapping
	}
	return result
}

func acceptanceMatrixCells(t *testing.T, handler http.Handler) map[string]map[string]any {
	t.Helper()
	envelope := acceptanceRequest(t, handler, http.MethodGet, "/api/v1/matrix?upstream_id=source-a", nil, http.StatusOK)
	rows := acceptanceData(t, envelope)["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("matrix rows = %#v", rows)
	}
	cells := rows[0].(map[string]any)["cells"].([]any)
	result := make(map[string]map[string]any, len(cells))
	for _, raw := range cells {
		cell := raw.(map[string]any)
		result[cell["target_id"].(string)] = cell
	}
	return result
}

func TestSystemAcceptanceSyncReconcileAndChannelLifecycle(t *testing.T) {
	upstream := newAcceptanceUpstream(t)
	targetA := newAcceptanceTarget(t, acceptanceTargetAToken, 101)
	targetB := newAcceptanceTarget(t, acceptanceTargetBToken, 201)
	configPath := t.TempDir() + "/synchub.yaml"
	createAcceptanceConfig(t, configPath, []config.TargetConfig{
		{ID: "target-a", Name: "Target A", Type: "newapi", BaseURL: targetA.server.URL, AccessToken: acceptanceTargetAToken},
		{ID: "target-b", Name: "Target B", Type: "newapi", BaseURL: targetB.server.URL, AccessToken: acceptanceTargetBToken},
	}, []config.UpstreamConfig{{
		ID: "source-a", Name: "Source A", Type: "newapi", BaseURL: upstream.URL, AccessToken: acceptanceUpstreamToken, DiscoveryMode: "token",
	}})
	router := newAcceptanceRouter(t, configPath)

	refresh := acceptanceData(t, acceptanceRequest(t, router, http.MethodPost, "/api/v1/upstreams/source-a/refresh", nil, http.StatusOK))
	assets := refresh["assets"].([]any)
	if len(assets) != 1 || assets[0].(map[string]any)["id"] != "source-a:token:7" || refresh["refreshed"] != true {
		t.Fatalf("refresh data = %#v", refresh)
	}
	listed := acceptanceData(t, acceptanceRequest(t, router, http.MethodGet, "/api/v1/upstreams/source-a/assets", nil, http.StatusOK))
	if len(listed["assets"].([]any)) != 1 || listed["refreshed"] != true {
		t.Fatalf("assets data = %#v", listed)
	}
	for targetID, cell := range acceptanceMatrixCells(t, router) {
		if cell["status"] != "unsynced" {
			t.Fatalf("initial matrix cell %s = %#v", targetID, cell)
		}
	}

	for _, targetID := range []string{"target-a", "target-b"} {
		validation := acceptanceData(t, acceptanceRequest(t, router, http.MethodPost, "/api/v1/targets/"+targetID+"/connection-tests", nil, http.StatusOK))
		if validation["reachable"] != true || validation["authenticated"] != true || validation["authorized"] != true ||
			validation["validation_status"] != "verified" {
			t.Fatalf("target %s validation = %#v", targetID, validation)
		}
	}
	targetA.mu.Lock()
	targetAChannelCount := len(targetA.channels)
	targetA.mu.Unlock()
	targetB.mu.Lock()
	targetBChannelCount := len(targetB.channels)
	targetB.mu.Unlock()
	if targetAChannelCount != 0 || targetBChannelCount != 0 {
		t.Fatalf("connection tests mutated targets: target-a=%d target-b=%d", targetAChannelCount, targetBChannelCount)
	}

	syncEnvelope := acceptanceRequest(t, router, http.MethodPost, "/api/v1/sync", map[string]any{
		"upstream_id": "source-a",
		"units": []map[string]any{
			{"unit_id": "u-a", "asset_id": "source-a:token:7", "target_id": "target-a", "upstream_group": "default", "settings": map[string]any{"models": []string{"gpt-4.1"}, "target_group": "default", "priority": 0, "weight": 100}},
			{"unit_id": "u-b", "asset_id": "source-a:token:7", "target_id": "target-b", "upstream_group": "default", "settings": map[string]any{"models": []string{"gpt-4.1"}, "target_group": "default", "priority": 0, "weight": 100}},
		},
		"grant": map[string]any{"security_proof": acceptanceProof, "allow_auth_file": false},
	}, http.StatusOK)
	results := acceptanceData(t, syncEnvelope)["units"].([]any)
	if len(results) != 2 || results[0].(map[string]any)["status"] != "synced" || results[0].(map[string]any)["channel_id"] != "101" ||
		results[1].(map[string]any)["status"] != "synced" || results[1].(map[string]any)["channel_id"] != "201" {
		t.Fatalf("sync results = %#v", results)
	}
	mappings := acceptanceMappingsByTarget(t, configPath)
	if len(mappings) != 2 || mappings["target-a"].TargetChannelID != "101" || mappings["target-b"].TargetChannelID != "201" {
		t.Fatalf("persisted mappings = %#v", mappings)
	}

	if !targetA.mutate(101, func(channel *acceptanceChannel) { channel.Weight = 70 }) {
		t.Fatal("target A channel 101 was not created")
	}
	reconcileA := acceptanceData(t, acceptanceRequest(t, router, http.MethodPost, "/api/v1/targets/target-a/reconcile", nil, http.StatusOK))
	states := reconcileA["mappings"].([]any)
	drift := states[0].(map[string]any)
	if len(states) != 1 || drift["status"] != "drifted" || drift["drift"].(map[string]any)["weight"].(map[string]any)["actual"] != float64(70) {
		t.Fatalf("target A reconcile = %#v", reconcileA)
	}
	if cell := acceptanceMatrixCells(t, router)["target-a"]; cell["status"] != "drifted" {
		t.Fatalf("drifted matrix cell = %#v", cell)
	}
	acceptanceRequest(t, router, http.MethodPost, "/api/v1/targets/target-a/drift/accept", map[string]any{
		"upstream_asset_id": "source-a:token:7", "channel_id": "101",
	}, http.StatusOK)
	if got := acceptanceMappingsByTarget(t, configPath)["target-a"].Snapshot.Weight; got != 70 {
		t.Fatalf("accepted snapshot weight = %d, want 70", got)
	}

	if !targetB.remove(201) {
		t.Fatal("target B channel 201 was not created")
	}
	reconcileB := acceptanceData(t, acceptanceRequest(t, router, http.MethodPost, "/api/v1/targets/target-b/reconcile", nil, http.StatusOK))
	removed := reconcileB["mappings"].([]any)
	if len(removed) != 1 || removed[0].(map[string]any)["status"] != "removed" {
		t.Fatalf("target B reconcile = %#v", reconcileB)
	}
	mappings = acceptanceMappingsByTarget(t, configPath)
	if len(mappings) != 1 || mappings["target-a"].TargetChannelID != "101" {
		t.Fatalf("mappings after removal = %#v", mappings)
	}
	if cell := acceptanceMatrixCells(t, router)["target-b"]; cell["status"] != "unsynced" {
		t.Fatalf("removed matrix cell = %#v", cell)
	}

	updated := acceptanceData(t, acceptanceRequest(t, router, http.MethodPut, "/api/v1/targets/target-a/channels/101", map[string]any{
		"name": "edited", "base_url": "https://edited.example.test", "models": []string{"gpt-4.1-mini"},
		"group": "edited", "priority": 3, "weight": 60, "enabled": false,
	}, http.StatusOK))
	if updated["id"] != "101" || updated["name"] != "edited" || updated["enabled"] != false || updated["managed"] != true {
		t.Fatalf("updated channel = %#v", updated)
	}
	updatedMapping := acceptanceMappingsByTarget(t, configPath)["target-a"]
	if updatedMapping.Snapshot.Weight != 60 || updatedMapping.Snapshot.Group != "edited" || len(updatedMapping.Snapshot.Models) != 1 || updatedMapping.Snapshot.Models[0] != "gpt-4.1-mini" {
		t.Fatalf("updated mapping = %#v", updatedMapping)
	}
	acceptanceRequest(t, router, http.MethodDelete, "/api/v1/targets/target-a/channels/101", nil, http.StatusOK)
	if targetA.channelCount() != 0 || len(acceptanceMappingsByTarget(t, configPath)) != 0 {
		t.Fatalf("channel delete did not clear target and YAML mappings")
	}
	for targetID, cell := range acceptanceMatrixCells(t, router) {
		if cell["status"] != "unsynced" {
			t.Fatalf("final matrix cell %s = %#v", targetID, cell)
		}
	}
}

func TestSystemAcceptanceReconcilePaginationFailurePreservesMapping(t *testing.T) {
	target := newAcceptanceTarget(t, acceptanceTargetAToken, 302)
	target.channels[301] = acceptanceChannel{
		ID: 301, Type: 1, Name: "existing", Status: 1, Models: "gpt-4.1", Group: "default", Weight: 100,
	}
	target.failSecondPage = true
	target.reportedTotal = 2
	configPath := t.TempDir() + "/synchub.yaml"
	wantMapping := platform.SyncMapping{
		UpstreamAssetID: "source-a:channel:7", TargetID: "target-a", TargetChannelID: "301",
		SourceProvider: platform.ProviderOpenAI, AssetKind: platform.AssetStaticAPIKey,
		Snapshot: platform.ChannelSnapshot{Models: []string{"gpt-4.1"}, Group: "default", Weight: 100},
	}
	createAcceptanceConfig(t, configPath, []config.TargetConfig{{
		ID: "target-a", Name: "Target A", Type: "newapi", BaseURL: target.server.URL, AccessToken: acceptanceTargetAToken,
	}}, []config.UpstreamConfig{{
		ID: "source-a", Name: "Source A", Type: "newapi", BaseURL: target.server.URL,
		AccessToken: acceptanceUpstreamToken, DiscoveryMode: "token", SyncMappings: []config.SyncMapping{wantMapping},
	}})
	router := newAcceptanceRouter(t, configPath)

	envelope := acceptanceRequest(t, router, http.MethodPost, "/api/v1/targets/target-a/reconcile", nil, http.StatusBadGateway)
	errorData, ok := envelope["error"].(map[string]any)
	if envelope["success"] != false || !ok || errorData["code"] != "upstream_failure" {
		t.Fatalf("reconcile failure = %#v", envelope)
	}
	target.mu.Lock()
	pages := append([]int(nil), target.listPages...)
	target.mu.Unlock()
	if fmt.Sprint(pages) != "[1 2]" {
		t.Fatalf("target list pages = %v, want [1 2]", pages)
	}
	mappings := acceptanceMappingsByTarget(t, configPath)
	if len(mappings) != 1 || mappings["target-a"].TargetChannelID != wantMapping.TargetChannelID || mappings["target-a"].Snapshot.Weight != wantMapping.Snapshot.Weight {
		t.Fatalf("mapping changed after incomplete pagination = %#v", mappings)
	}
}
