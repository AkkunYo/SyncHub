package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AkkunYo/SyncHub/internal/config"
	"github.com/AkkunYo/SyncHub/internal/modelcatalog"
	"github.com/AkkunYo/SyncHub/internal/platform"
	"github.com/AkkunYo/SyncHub/internal/probe"
	syncservice "github.com/AkkunYo/SyncHub/internal/sync"
)

const taskHistorySyncBody = `{"upstream_id":"source-a","units":[{"unit_id":"u-1","asset_id":"source-a:channel:7:key:0","target_id":"target-a","settings":{"models":["gpt-4.1"],"target_group":"default","weight":100}}],"grant":{"security_proof":"request-only-proof"}}`

type taskHistoryCatalog struct {
	task modelcatalog.DiscoveryTask
}

func (*taskHistoryCatalog) ListKeys(context.Context, config.UpstreamConfig, platform.UpstreamAdapter) ([]modelcatalog.Key, error) {
	return nil, nil
}

func (c *taskHistoryCatalog) Discover(context.Context, config.UpstreamConfig, platform.UpstreamAdapter, []string) (modelcatalog.DiscoveryTask, error) {
	return c.task, nil
}

func (*taskHistoryCatalog) Models(string, string) (modelcatalog.KeyModels, bool) {
	return modelcatalog.KeyModels{}, false
}

func (*taskHistoryCatalog) Probe(context.Context, config.UpstreamConfig, platform.UpstreamAdapter, string, string, probe.Protocol) (modelcatalog.ModelProbe, error) {
	return modelcatalog.ModelProbe{}, nil
}

func (*taskHistoryCatalog) MutateKey(context.Context, string, string, func() error) error {
	return nil
}

func (*taskHistoryCatalog) MutateUpstream(context.Context, string, func() error) error {
	return nil
}

func TestTaskHistoryStartsEmptyAndValidatesReads(t *testing.T) {
	router := newTestEnvironment().router(t)

	recorder, envelope := request(t, router, http.MethodGet, "/api/v1/tasks", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	data := dataObject(t, envelope)
	if tasks := data["tasks"].([]any); len(tasks) != 0 {
		t.Fatalf("tasks=%#v, want empty", tasks)
	}
	meta := data["meta"].(map[string]any)
	if meta["total"] != float64(0) || meta["capacity"] != float64(100) {
		t.Fatalf("meta=%#v", meta)
	}

	tests := []struct {
		path   string
		status int
		code   string
	}{
		{path: "/api/v1/tasks?limit=1", status: http.StatusBadRequest, code: "invalid_request"},
		{path: "/api/v1/tasks/not%20valid", status: http.StatusBadRequest, code: "invalid_request"},
		{path: "/api/v1/tasks/task_missing", status: http.StatusNotFound, code: "task_not_found"},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			recorder, envelope := request(t, router, http.MethodGet, test.path, "", "")
			if recorder.Code != test.status || errorCode(t, envelope) != test.code {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestSyncCreatesSanitizedTaskSummaryAndDetail(t *testing.T) {
	env := newTestEnvironment()
	secretDetail := "should-not-be-retained-" + testSecret
	env.syncer.multiResult = syncservice.MultiResult{Units: []syncservice.UnitResult{{
		UnitID: "u-1", AssetID: "source-a:channel:7:key:0", TargetID: "target-a",
		Status: syncservice.TargetFailed, Code: "rate_limited", Retryable: true, RetryAfterSeconds: 45,
		EffectiveModels: []string{secretDetail}, ExcludedModels: []string{}, Warnings: []string{secretDetail},
	}}}
	router := env.router(t)

	recorder, envelope := request(t, router, http.MethodPost, "/api/v1/sync", taskHistorySyncBody, "application/json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("sync status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	syncData := dataObject(t, envelope)
	taskID, _ := syncData["task_id"].(string)
	if taskID == "" || len(syncData["units"].([]any)) != 1 {
		t.Fatalf("sync data=%#v", syncData)
	}

	recorder, envelope = request(t, router, http.MethodGet, "/api/v1/tasks", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	tasks := dataObject(t, envelope)["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("tasks=%#v", tasks)
	}
	summary := tasks[0].(map[string]any)
	assertTaskSummary(t, summary, taskID, "sync", "source-a", "failed", 1, 0, 1)
	if _, exists := summary["items"]; exists {
		t.Fatalf("list exposed detail items: %#v", summary)
	}

	recorder, envelope = request(t, router, http.MethodGet, "/api/v1/tasks/"+taskID, "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), secretDetail) || strings.Contains(recorder.Body.String(), "request-only-proof") ||
		strings.Contains(recorder.Body.String(), "effective_models") || strings.Contains(recorder.Body.String(), "warnings") {
		t.Fatalf("task detail retained sensitive or unbounded fields: %s", recorder.Body.String())
	}
	detail := dataObject(t, envelope)
	assertTaskSummary(t, detail, taskID, "sync", "source-a", "failed", 1, 0, 1)
	item := detail["items"].([]any)[0].(map[string]any)
	if item["unit_id"] != "u-1" || item["asset_id"] != "source-a:channel:7:key:0" || item["target_id"] != "target-a" ||
		item["status"] != "failed" || item["code"] != "rate_limited" || item["retryable"] != true || item["retry_after_seconds"] != float64(45) {
		t.Fatalf("item=%#v", item)
	}
}

func TestModelDiscoveryCreatesTaskWithExistingIdentifier(t *testing.T) {
	env := newTestEnvironment()
	dependencies := env.dependencies()
	dependencies.Models = &taskHistoryCatalog{task: modelcatalog.DiscoveryTask{
		TaskID: "model_discovery_fixed", KeyIDs: []string{"key-a", "key-b"}, Completed: true,
		Status: modelcatalog.TaskPartiallyFailed,
		Items: []modelcatalog.DiscoveryItem{
			{KeyID: "key-a", Status: modelcatalog.DiscoverySucceeded, ModelCount: 3},
			{KeyID: "key-b", Status: modelcatalog.DiscoveryRateLimited, ErrorCode: "rate_limited", Retryable: true, RetryAfterSeconds: 60},
		},
	}}
	router, err := NewRouter(dependencies)
	if err != nil {
		t.Fatal(err)
	}

	recorder, envelope := request(
		t, router, http.MethodPost, "/api/v1/upstreams/source-a/model-discoveries",
		`{"key_ids":["key-a","key-b"]}`, "application/json",
	)
	if recorder.Code != http.StatusAccepted || dataObject(t, envelope)["task_id"] != "model_discovery_fixed" {
		t.Fatalf("discovery status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder, envelope = request(t, router, http.MethodGet, "/api/v1/tasks/model_discovery_fixed", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	detail := dataObject(t, envelope)
	assertTaskSummary(t, detail, "model_discovery_fixed", "discover", "source-a", "partially_failed", 2, 1, 1)
	items := detail["items"].([]any)
	if first := items[0].(map[string]any); first["key_id"] != "key-a" || first["status"] != "succeeded" || first["model_count"] != float64(3) {
		t.Fatalf("first=%#v", first)
	}
	if second := items[1].(map[string]any); second["key_id"] != "key-b" || second["error_code"] != "rate_limited" || second["retry_after_seconds"] != float64(60) {
		t.Fatalf("second=%#v", second)
	}
}

func TestTaskHistoryIsConcurrentNewestFirstAndBounded(t *testing.T) {
	env := newTestEnvironment()
	router := env.router(t)

	const concurrentRequests = 16
	ids := make(chan string, concurrentRequests)
	errs := make(chan error, concurrentRequests)
	var wait sync.WaitGroup
	for range concurrentRequests {
		wait.Add(1)
		go func() {
			defer wait.Done()
			recorder, envelope := request(t, router, http.MethodPost, "/api/v1/sync", taskHistorySyncBody, "application/json")
			if recorder.Code != http.StatusOK {
				errs <- fmt.Errorf("sync status=%d body=%s", recorder.Code, recorder.Body.String())
				return
			}
			id, _ := dataObject(t, envelope)["task_id"].(string)
			if id == "" {
				errs <- fmt.Errorf("missing task id: %s", recorder.Body.String())
				return
			}
			ids <- id
		}()
	}
	wait.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		t.Error(err)
	}
	seen := make(map[string]struct{}, concurrentRequests)
	for id := range ids {
		seen[id] = struct{}{}
	}
	if len(seen) != concurrentRequests {
		t.Fatalf("unique concurrent task ids=%d, want %d", len(seen), concurrentRequests)
	}

	var evictedID string
	var newestID string
	for i := 0; i < 101; i++ {
		recorder, envelope := request(t, router, http.MethodPost, "/api/v1/sync", taskHistorySyncBody, "application/json")
		if recorder.Code != http.StatusOK {
			t.Fatalf("sync %d status=%d body=%s", i, recorder.Code, recorder.Body.String())
		}
		id := dataObject(t, envelope)["task_id"].(string)
		if i == 0 {
			evictedID = id
		}
		newestID = id
	}

	recorder, envelope := request(t, router, http.MethodGet, "/api/v1/tasks", "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	data := dataObject(t, envelope)
	tasks := data["tasks"].([]any)
	if len(tasks) != 100 || data["meta"].(map[string]any)["total"] != float64(100) {
		t.Fatalf("task count=%d meta=%#v", len(tasks), data["meta"])
	}
	if tasks[0].(map[string]any)["task_id"] != newestID {
		t.Fatalf("first task=%#v, newest=%s", tasks[0], newestID)
	}

	recorder, envelope = request(t, router, http.MethodGet, "/api/v1/tasks/"+evictedID, "", "")
	if recorder.Code != http.StatusNotFound || errorCode(t, envelope) != "task_not_found" {
		t.Fatalf("evicted status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder, _ = request(t, router, http.MethodGet, "/api/v1/tasks/"+newestID, "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("newest status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func assertTaskSummary(t *testing.T, task map[string]any, taskID, taskType, scope, status string, total, succeeded, failed int) {
	t.Helper()
	if task["task_id"] != taskID || task["type"] != taskType || task["scope"] != scope || task["status"] != status || task["completed"] != true {
		t.Fatalf("task summary=%#v", task)
	}
	startedAt, startedErr := time.Parse(time.RFC3339Nano, task["started_at"].(string))
	completedAt, completedErr := time.Parse(time.RFC3339Nano, task["completed_at"].(string))
	if startedErr != nil || completedErr != nil || completedAt.Before(startedAt) {
		t.Fatalf("timestamps started=%v (%v) completed=%v (%v)", startedAt, startedErr, completedAt, completedErr)
	}
	summary := task["summary"].(map[string]any)
	if summary["total"] != float64(total) || summary["succeeded"] != float64(succeeded) || summary["failed"] != float64(failed) {
		t.Fatalf("summary=%#v", summary)
	}
}
