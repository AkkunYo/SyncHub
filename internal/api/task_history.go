package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/AkkunYo/SyncHub/internal/modelcatalog"
	syncservice "github.com/AkkunYo/SyncHub/internal/sync"
	"github.com/gin-gonic/gin"
)

const taskHistoryCapacity = 100

var safeTaskCodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,63}$`)

type taskCounts struct {
	Total     int `json:"total"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
}

type taskItem struct {
	UnitID            string `json:"unit_id,omitempty"`
	KeyID             string `json:"key_id,omitempty"`
	AssetID           string `json:"asset_id,omitempty"`
	TargetID          string `json:"target_id,omitempty"`
	Status            string `json:"status"`
	Code              string `json:"code,omitempty"`
	ErrorCode         string `json:"error_code,omitempty"`
	ChannelID         string `json:"channel_id,omitempty"`
	ModelCount        int    `json:"model_count,omitempty"`
	Retryable         bool   `json:"retryable,omitempty"`
	RetryAfterSeconds int64  `json:"retry_after_seconds,omitempty"`
}

type taskRecord struct {
	TaskID      string     `json:"task_id"`
	Type        string     `json:"type"`
	Scope       string     `json:"scope"`
	Status      string     `json:"status"`
	Completed   bool       `json:"completed"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt time.Time  `json:"completed_at"`
	Summary     taskCounts `json:"summary"`
	Items       []taskItem `json:"items,omitempty"`
}

type taskLedger struct {
	mu      sync.RWMutex
	records []taskRecord
}

func newTaskLedger() *taskLedger {
	return &taskLedger{records: make([]taskRecord, 0, taskHistoryCapacity)}
}

func (l *taskLedger) add(record taskRecord) {
	if l == nil || strings.TrimSpace(record.TaskID) == "" {
		return
	}
	record = cloneTaskRecord(record)
	l.mu.Lock()
	defer l.mu.Unlock()
	for index := range l.records {
		if l.records[index].TaskID == record.TaskID {
			l.records = append(l.records[:index], l.records[index+1:]...)
			break
		}
	}
	l.records = append([]taskRecord{record}, l.records...)
	if len(l.records) > taskHistoryCapacity {
		l.records = l.records[:taskHistoryCapacity]
	}
}

func (l *taskLedger) list() []taskRecord {
	if l == nil {
		return []taskRecord{}
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	result := make([]taskRecord, len(l.records))
	for index := range l.records {
		result[index] = cloneTaskRecord(l.records[index])
	}
	return result
}

func (l *taskLedger) get(taskID string) (taskRecord, bool) {
	if l == nil {
		return taskRecord{}, false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, record := range l.records {
		if record.TaskID == taskID {
			return cloneTaskRecord(record), true
		}
	}
	return taskRecord{}, false
}

func cloneTaskRecord(record taskRecord) taskRecord {
	record.Items = append([]taskItem(nil), record.Items...)
	return record
}

func newTaskID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err == nil {
		return "task_" + hex.EncodeToString(value)
	}
	return "task_unavailable"
}

func (s *server) listTasks(c *gin.Context) {
	if validateNoQuery(c) != nil || requireEmptyBody(c) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	records := s.tasks.list()
	for index := range records {
		// Collection responses carry summaries only; details stay behind /:task_id.
		records[index].Items = nil
	}
	writeSuccess(c, http.StatusOK, gin.H{
		"tasks": records,
		"meta":  gin.H{"total": len(records), "capacity": taskHistoryCapacity},
	})
}

func (s *server) getTask(c *gin.Context) {
	taskID := c.Param("task_id")
	if validateNoQuery(c) != nil || requireEmptyBody(c) != nil || validateIdentifier(taskID) != nil {
		writeFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	record, ok := s.tasks.get(taskID)
	if !ok {
		writeFailure(c, http.StatusNotFound, "task_not_found")
		return
	}
	writeSuccess(c, http.StatusOK, record)
}

func syncTaskRecord(taskID, scope string, startedAt, completedAt time.Time, result syncservice.MultiResult) taskRecord {
	items := make([]taskItem, len(result.Units))
	counts := taskCounts{Total: len(result.Units)}
	for index, unit := range result.Units {
		status := string(unit.Status)
		if unit.Status == syncservice.TargetSynced {
			counts.Succeeded++
		} else {
			counts.Failed++
		}
		items[index] = taskItem{
			UnitID: unit.UnitID, AssetID: unit.AssetID, TargetID: unit.TargetID,
			Status: status, Code: safeTaskCode(unit.Code), ChannelID: unit.ChannelID,
			Retryable: unit.Retryable, RetryAfterSeconds: unit.RetryAfterSeconds,
		}
	}
	return taskRecord{
		TaskID: taskID, Type: "sync", Scope: scope, Status: aggregateTaskStatus(counts),
		Completed: true, StartedAt: startedAt, CompletedAt: completedAt, Summary: counts, Items: items,
	}
}

func discoveryTaskRecord(task modelcatalog.DiscoveryTask, scope string, startedAt, completedAt time.Time) taskRecord {
	items := make([]taskItem, len(task.Items))
	counts := taskCounts{Total: len(task.Items)}
	for index, item := range task.Items {
		if item.Status == modelcatalog.DiscoverySucceeded {
			counts.Succeeded++
		} else {
			counts.Failed++
		}
		items[index] = taskItem{
			KeyID: item.KeyID, Status: string(item.Status), ModelCount: item.ModelCount,
			ErrorCode: safeTaskCode(item.ErrorCode), Retryable: item.Retryable,
			RetryAfterSeconds: item.RetryAfterSeconds,
		}
	}
	return taskRecord{
		TaskID: task.TaskID, Type: "discover", Scope: scope, Status: string(task.Status),
		Completed: task.Completed, StartedAt: startedAt, CompletedAt: completedAt, Summary: counts, Items: items,
	}
}

func aggregateTaskStatus(counts taskCounts) string {
	switch {
	case counts.Failed == 0:
		return "succeeded"
	case counts.Succeeded == 0:
		return "failed"
	default:
		return "partially_failed"
	}
}

func safeTaskCode(value string) string {
	if safeTaskCodePattern.MatchString(value) {
		return value
	}
	return ""
}
