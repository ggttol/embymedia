package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
	"github.com/google/uuid"
)

const taskPollInterval = 500 * time.Millisecond

var supportedTaskTypes = []string{
	"c115_offline_download",
	"c115_save_share",
	"quark_to_115_import",
	"emby_match",
	"emby_refresh",
	"emby_missing_posters",
	"emby_metadata_repair",
	"series_auto_fill",
	"strm_sync",
	"strm_verify",
}

// SupportedTaskTypes returns the task types backed by real provider operations.
func SupportedTaskTypes() []string {
	return append([]string(nil), supportedTaskTypes...)
}

// TaskQueueService executes persisted provider operations and owns their cancellation contexts.
type TaskQueueService struct {
	db       *storage.DB
	driveSvc *DriveService
	embySvc  *EmbyService
	mediaSvc *MediaService

	mediaMu sync.Mutex

	mu      sync.Mutex
	running map[string]context.CancelFunc
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewTaskQueueService creates a stopped persistent task queue.
func NewTaskQueueService(db *storage.DB, driveSvc *DriveService, embySvc *EmbyService) *TaskQueueService {
	return &TaskQueueService{db: db, driveSvc: driveSvc, embySvc: embySvc, mediaSvc: NewMediaService(db), running: make(map[string]context.CancelFunc)}
}

// ErrMediaMutationBusy means a worker or another deletion owns media mutation.
var ErrMediaMutationBusy = errors.New("media mutation is busy; retry after the active operation completes")

// TryMediaMutation runs fn exclusively with worker tasks, or returns ErrMediaMutationBusy
// without calling fn. The callback must not reenter this method or wait for queued tasks.
func (s *TaskQueueService) TryMediaMutation(fn func() error) error {
	if !s.mediaMu.TryLock() {
		return ErrMediaMutationBusy
	}
	defer s.mediaMu.Unlock()
	return fn()
}

// Start recovers interrupted state and starts processing pending tasks.
func (s *TaskQueueService) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return nil
	}
	if err := s.db.RecoverInterruptedAsyncTasks(); err != nil {
		return fmt.Errorf("recover interrupted tasks: %w", err)
	}
	workerCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	go s.workerLoop(workerCtx, s.done)
	return nil
}

// Stop cancels the worker and waits for active provider calls to return.
func (s *TaskQueueService) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	done := s.done
	s.cancel = nil
	s.done = nil
	s.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		<-done
	}
}

func stringPayload(payload map[string]any, key string, required bool) (string, error) {
	value, present := payload[key]
	if !present {
		if required {
			return "", fmt.Errorf("%s is required", key)
		}
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	text = strings.TrimSpace(text)
	if required && text == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return text, nil
}

func stringSlicePayload(payload map[string]any, key string) ([]string, error) {
	value, present := payload[key]
	if !present {
		return nil, fmt.Errorf("%s is required", key)
	}
	switch values := value.(type) {
	case []string:
		if len(values) == 0 {
			return nil, fmt.Errorf("%s must not be empty", key)
		}
		return values, nil
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			text, ok := value.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("%s must contain non-empty strings", key)
			}
			result = append(result, strings.TrimSpace(text))
		}
		if len(result) == 0 {
			return nil, fmt.Errorf("%s must not be empty", key)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}
}

func optionalStringSlicePayload(payload map[string]any, key string) ([]string, error) {
	value, present := payload[key]
	if !present || value == nil {
		return nil, nil
	}
	switch values := value.(type) {
	case []string:
		result := make([]string, 0, len(values))
		seen := make(map[string]struct{}, len(values))
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				return nil, fmt.Errorf("%s must contain non-empty strings", key)
			}
			if _, ok := seen[value]; ok {
				return nil, fmt.Errorf("%s must not contain duplicates", key)
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
		return result, nil
	case []any:
		result := make([]string, 0, len(values))
		seen := make(map[string]struct{}, len(values))
		for _, raw := range values {
			value, ok := raw.(string)
			value = strings.TrimSpace(value)
			if !ok || value == "" {
				return nil, fmt.Errorf("%s must contain non-empty strings", key)
			}
			if _, ok := seen[value]; ok {
				return nil, fmt.Errorf("%s must not contain duplicates", key)
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}
}
func validateQuarkImportPayload(payload map[string]any, allowAutofillBinding bool) error {
	for _, key := range []string{"quark_account_id", "quark_target_id", "share_url", "c115_account_id"} {
		if _, err := stringPayload(payload, key, true); err != nil {
			return err
		}
	}
	priorTaskID, err := stringPayload(payload, "prior_import_task_id", false)
	if err != nil {
		return err
	}
	parentTaskID, err := stringPayload(payload, "parent_task_id", false)
	if err != nil {
		return err
	}
	selected, err := optionalStringSlicePayload(payload, "selected_source_ids")
	if err != nil {
		return err
	}
	selections, err := shareSelectionsPayload(payload, "selected_source_manifest")
	if err != nil {
		return err
	}
	bindingKeys := []string{"autofill_library_name", "autofill_library_id", "autofill_library_cid", "autofill_series_id", "autofill_tmdb_id", "autofill_series_folder"}
	bound := false
	for _, key := range bindingKeys {
		value, err := stringPayload(payload, key, false)
		if err != nil {
			return err
		}
		bound = bound || value != ""
	}
	expected, err := optionalStringSlicePayload(payload, "expected_episodes")
	if err != nil {
		return err
	}
	if !bound {
		if len(selected) > 0 || len(selections) > 0 || len(expected) > 0 || parentTaskID != "" {
			return fmt.Errorf("source selections, parent_task_id, and expected_episodes are reserved for internal autofill imports")
		}
		if priorTaskID != "" && !allowAutofillBinding {
			return fmt.Errorf("prior_import_task_id is reserved for internal retry")
		}
		return nil
	}
	if !allowAutofillBinding {
		return fmt.Errorf("autofill destination bindings are internal and cannot be submitted directly")
	}
	if len(selected) == 0 {
		return fmt.Errorf("selected_source_ids is required for autofill transfer")
	}
	if len(expected) == 0 {
		return fmt.Errorf("expected_episodes is required for autofill transfer")
	}
	if parentTaskID == "" {
		return fmt.Errorf("parent_task_id is required for autofill transfer")
	}
	if len(selections) != len(selected) {
		return fmt.Errorf("selected_source_manifest must describe every selected source")
	}
	for index := range selected {
		if selections[index].ID != selected[index] {
			return fmt.Errorf("selected_source_manifest order must match selected_source_ids")
		}
	}
	for _, key := range bindingKeys {
		value, _ := stringPayload(payload, key, false)
		if value == "" {
			return fmt.Errorf("%s is required for autofill transfer", key)
		}
	}
	return nil
}

func validateTask(taskType string, payload map[string]any) error {
	switch taskType {
	case "emby_refresh":
		_, err := stringPayload(payload, "library_id", false)
		return err
	case "emby_match":
		if _, err := stringPayload(payload, "item_id", true); err != nil {
			return err
		}
		_, err := stringPayload(payload, "tmdb_id", true)
		return err
	case "emby_missing_posters":
		return nil
	case "emby_metadata_repair":
		_, _, err := resolveMetadataRepairSpec(payload)
		return err
	case "series_auto_fill":
		_, err := resolveSeriesAutoFillSpec(payload)
		return err
	case "c115_save_share":
		rawURL, err := stringPayload(payload, "url", true)
		if err != nil {
			return err
		}
		password, err := stringPayload(payload, "password", false)
		if err != nil {
			return err
		}
		if _, _, err := ParseShareCode(rawURL, password); err != nil {
			return err
		}
		for _, key := range []string{"target_cid", "account_id"} {
			if _, err := stringPayload(payload, key, false); err != nil {
				return err
			}
		}
		return nil
	case "c115_offline_download":
		urls, err := stringSlicePayload(payload, "urls")
		if err != nil {
			return err
		}
		for _, rawURL := range urls {
			if err := ValidateOfflineURL(rawURL); err != nil {
				return err
			}
		}
		for _, key := range []string{"target_cid", "account_id"} {
			if _, err := stringPayload(payload, key, false); err != nil {
				return err
			}
		}
		return nil
	case "quark_to_115_import":
		return validateQuarkImportPayload(payload, false)
	case "strm_sync", "strm_verify":
		_, err := stringPayload(payload, "library", false)
		return err
	default:
		return fmt.Errorf("unsupported task type %q; supported types: %s", taskType, strings.Join(supportedTaskTypes, ", "))
	}
}

// ValidateTask validates a task request without enqueuing it.
func ValidateTask(taskType string, payload map[string]any) error {
	return validateTask(strings.TrimSpace(taskType), payload)
}

// Enqueue validates and persists one standalone task for asynchronous execution.
func (s *TaskQueueService) Enqueue(taskType string, payload map[string]any) (*domain.AsyncTask, error) {
	return s.enqueue(taskType, payload, "")
}

// EnqueueScheduled atomically links an execution to the schedule that launched it.
func (s *TaskQueueService) EnqueueScheduled(scheduleID, taskType string, payload map[string]any) (*domain.AsyncTask, error) {
	return s.enqueue(taskType, payload, scheduleID)
}

func (s *TaskQueueService) enqueue(taskType string, payload map[string]any, scheduleID string) (*domain.AsyncTask, error) {
	taskType = strings.TrimSpace(taskType)
	if payload == nil {
		payload = map[string]any{}
	}
	if err := validateTask(taskType, payload); err != nil {
		return nil, err
	}
	return s.persistTask(taskType, payload, scheduleID)
}

func (s *TaskQueueService) enqueueAutofillQuarkImport(payload map[string]any) (*domain.AsyncTask, error) {
	if err := validateQuarkImportPayload(payload, true); err != nil {
		return nil, err
	}
	return s.persistTask("quark_to_115_import", payload, "")
}

func (s *TaskQueueService) persistTask(taskType string, payload map[string]any, scheduleID string) (*domain.AsyncTask, error) {
	now := time.Now()
	task := &domain.AsyncTask{
		ID:          uuid.NewString(),
		Type:        taskType,
		ScheduleID:  scheduleID,
		Payload:     payload,
		Status:      "pending",
		MaxAttempts: 1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	var err error
	if scheduleID == "" {
		err = s.db.CreateAsyncTask(task)
	} else {
		err = s.db.CreateScheduledAsyncTask(task, scheduleID)
	}
	if err != nil {
		return nil, err
	}
	return task, nil
}

func (s *TaskQueueService) workerLoop(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(taskPollInterval)
	defer ticker.Stop()
	for {
		s.processPendingTasks(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *TaskQueueService) processPendingTasks(ctx context.Context) {
	tasks, err := s.db.ListPendingAsyncTasks(5)
	if err != nil {
		log.Printf("list pending tasks: %v", err)
		return
	}
	for _, task := range tasks {
		if ctx.Err() != nil {
			return
		}
		s.executeTask(ctx, task)
	}
}

func (s *TaskQueueService) executeTask(parent context.Context, task domain.AsyncTask) {
	// Leave the task pending while a user deletion owns the media, so Stop and Cancel
	// never wait for an unrelated HTTP request to release the mutation lock.
	if !s.mediaMu.TryLock() {
		return
	}
	defer s.mediaMu.Unlock()
	if parent.Err() != nil {
		return
	}
	if _, claimed, err := s.db.BeginAsyncTask(task.ID); err != nil {
		log.Printf("claim task %s: %v", task.ID, err)
		return
	} else if !claimed {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	s.mu.Lock()
	s.running[task.ID] = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.running, task.ID)
		s.mu.Unlock()
	}()
	if err := s.db.AppendTaskLog(task.ID, "started "+task.Type); err != nil {
		message := "persist task start log: " + err.Error()
		if finishErr := s.db.FinishAsyncTask(task.ID, "failed", 0, "", message, "failed: "+message); finishErr != nil {
			log.Printf("finish task %s after log failure: %v", task.ID, finishErr)
		}
		return
	}
	if task.Type != "quark_to_115_import" {
		if err := s.db.UpdateAsyncTaskProgress(task.ID, 10); err != nil {
			message := "persist task progress: " + err.Error()
			if finishErr := s.db.FinishAsyncTask(task.ID, "failed", 0, "", message, "failed: "+message); finishErr != nil {
				log.Printf("finish task %s after progress failure: %v", task.ID, finishErr)
			}
			return
		}
	}
	result, err := s.run(ctx, task)
	encoded := []byte(nil)
	if result != nil {
		var encodeErr error
		encoded, encodeErr = json.Marshal(result)
		if encodeErr != nil {
			err = errors.Join(err, fmt.Errorf("encode task result: %w", encodeErr))
		}
	}
	if err != nil {
		status := "failed"
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			status = "cancelled"
		}
		progress := 10.0
		if current, readErr := s.db.GetAsyncTask(task.ID); readErr == nil {
			progress = current.Progress
		}
		if persistErr := s.db.FinishAsyncTask(task.ID, status, progress, string(encoded), err.Error(), status+": "+err.Error()); persistErr != nil {
			log.Printf("finish task %s: %v", task.ID, persistErr)
		}
		return
	}
	if err := s.db.FinishAsyncTask(task.ID, "completed", 100, string(encoded), "", "completed "+task.Type); err != nil {
		log.Printf("finish task %s: %v", task.ID, err)
	}
}

func (s *TaskQueueService) taskProgress(taskID string, start, end float64) func(float64, string) error {
	return func(progress float64, message string) error {
		progress = min(100, max(0, progress))
		mapped := min(99, start+(end-start)*progress/100)
		if err := s.db.UpdateAsyncTaskProgress(taskID, mapped); err != nil {
			return err
		}
		if message != "" {
			return s.db.AppendTaskLog(taskID, message)
		}
		return nil
	}
}

func (s *TaskQueueService) runSTRMSync(ctx context.Context, task domain.AsyncTask, start, end float64) (STRMResult, error) {
	library, _ := stringPayload(task.Payload, "library", false)
	result, err := s.mediaSvc.SyncSTRMWithProgress(ctx, library, s.taskProgress(task.ID, start, end))
	if err != nil {
		return result, err
	}
	if err := s.db.AppendTaskLog(task.ID, fmt.Sprintf("STRM sync media=%d created=%d updated=%d removed=%d removed_directories=%d prune=%s", result.MediaFiles, result.Created, result.Updated, result.Removed, result.RemovedDirectories, result.PruneStatus)); err != nil {
		return result, err
	}
	if err := s.db.AppendTaskLog(task.ID, fmt.Sprintf("STRM findings valid=%d missing=%d invalid=%d", result.Valid, result.Missing, result.Invalid)); err != nil {
		return result, err
	}
	return result, nil
}

func (s *TaskQueueService) run(ctx context.Context, task domain.AsyncTask) (map[string]any, error) {
	switch task.Type {
	case "emby_refresh":
		libraryID, _ := stringPayload(task.Payload, "library_id", false)
		if libraryID != "" {
			if err := s.embySvc.RefreshLibraryCtx(ctx, libraryID); err != nil {
				return nil, err
			}
			if err := s.db.AppendTaskLog(task.ID, "Emby accepted the item refresh; this endpoint does not expose completion state"); err != nil {
				return nil, err
			}
			return map[string]any{"library_id": libraryID, "accepted": true, "completion_tracked": false}, nil
		}
		strm, err := s.runSTRMSync(ctx, task, 10, 75)
		if err != nil {
			return nil, err
		}
		scan, err := s.embySvc.RunLibraryScanCtx(ctx, s.taskProgress(task.ID, 80, 99))
		if err != nil {
			return nil, err
		}
		if err := s.db.AppendTaskLog(task.ID, "Emby full-library scan reached its recorded terminal state"); err != nil {
			return nil, err
		}
		return map[string]any{
			"strm": strm, "library_id": "", "accepted": true, "completion_tracked": true,
			"emby_task_id": scan.TaskID, "emby_status": scan.Status,
			"started_at": scan.StartedAt, "completed_at": scan.CompletedAt,
		}, nil
	case "emby_match":
		itemID, _ := stringPayload(task.Payload, "item_id", true)
		tmdbID, _ := stringPayload(task.Payload, "tmdb_id", true)
		if err := s.embySvc.MatchMediaCtx(ctx, itemID, tmdbID); err != nil {
			return nil, err
		}
		return map[string]any{"item_id": itemID, "tmdb_id": tmdbID, "matched": true}, nil
	case "emby_missing_posters":
		repair, err := s.embySvc.RepairMissingPostersCtx(ctx, s.taskProgress(task.ID, 10, 99))
		if err != nil {
			return nil, err
		}
		if err := s.db.AppendTaskLog(task.ID, fmt.Sprintf("Emby poster repair found=%d requested=%d downloaded=%d repaired=%d remaining=%d failed=%d", repair.Found, repair.RepairRequested, repair.CandidateDownloaded, repair.Repaired, repair.Remaining, len(repair.Failed))); err != nil {
			return nil, err
		}
		return map[string]any{
			"found": repair.Found, "repair_requested": repair.RepairRequested, "candidate_downloaded": repair.CandidateDownloaded, "repaired": repair.Repaired,
			"remaining": repair.Remaining, "failed": repair.Failed, "remaining_items": repair.RemainingItems, "timed_out": repair.TimedOut,
		}, nil
	case "emby_metadata_repair":
		limit, autoApply, _ := resolveMetadataRepairSpec(task.Payload)
		repair, err := s.embySvc.RepairMetadataCtx(ctx, limit, autoApply, s.taskProgress(task.ID, 10, 99))
		if err != nil {
			return nil, err
		}
		if err := s.db.AppendTaskLog(task.ID, fmt.Sprintf("Emby metadata repair scanned=%d missing=%d processed=%d matched=%d review=%d no_match=%d", repair.Scanned, repair.MissingIdentity, repair.Processed, repair.AutoMatched, repair.NeedsReview, repair.NoMatch)); err != nil {
			return nil, err
		}
		return map[string]any{"scanned": repair.Scanned, "missing_identity": repair.MissingIdentity, "processed": repair.Processed, "auto_matched": repair.AutoMatched, "needs_review": repair.NeedsReview, "no_match": repair.NoMatch, "items": repair.Items}, nil
	case "series_auto_fill":
		return s.runSeriesAutoFill(ctx, task)
	case "c115_save_share":
		rawURL, _ := stringPayload(task.Payload, "url", true)
		password, _ := stringPayload(task.Payload, "password", false)
		targetCID, _ := stringPayload(task.Payload, "target_cid", false)
		accountID, _ := stringPayload(task.Payload, "account_id", false)
		count, title, err := s.driveSvc.SaveShareCtx(ctx, accountID, rawURL, password, targetCID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"count": count, "title": title, "target_cid": targetCID}, nil
	case "c115_offline_download":
		urls, _ := stringSlicePayload(task.Payload, "urls")
		targetCID, _ := stringPayload(task.Payload, "target_cid", false)
		accountID, _ := stringPayload(task.Payload, "account_id", false)
		ids, err := s.driveSvc.AddOfflineTasks(ctx, accountID, urls, targetCID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"task_ids": ids, "target_cid": targetCID}, nil
	case "quark_to_115_import":
		return s.runQuarkTo115Import(ctx, task)
	case "strm_sync":
		result, err := s.runSTRMSync(ctx, task, 10, 99)
		if err != nil {
			return nil, err
		}
		return map[string]any{"strm": result}, nil
	case "strm_verify":
		library, _ := stringPayload(task.Payload, "library", false)
		result, err := s.mediaSvc.VerifySTRMWithProgress(ctx, library, s.taskProgress(task.ID, 10, 99))
		if err != nil {
			return nil, err
		}
		if err := s.db.AppendTaskLog(task.ID, fmt.Sprintf("STRM findings valid=%d missing=%d invalid=%d", result.Valid, result.Missing, result.Invalid)); err != nil {
			return nil, err
		}
		return map[string]any{"strm": result}, nil
	default:
		return nil, fmt.Errorf("unsupported task type %q", task.Type)
	}
}

// Cancel cancels a pending task or signals its active provider call.
func (s *TaskQueueService) Cancel(taskID string) (*domain.AsyncTask, error) {
	task, err := s.db.GetAsyncTask(taskID)
	if err != nil {
		return nil, err
	}
	switch task.Status {
	case "pending":
		changed, err := s.db.CancelPendingAsyncTask(taskID)
		if err != nil {
			return nil, err
		}
		if !changed {
			return nil, fmt.Errorf("task %s changed state before cancellation", taskID)
		}
	case "running":
		if task.Type == "quark_to_115_import" {
			if err := s.db.RequestCrossDriveCancellation(taskID); err != nil {
				return nil, err
			}
		}
		s.mu.Lock()
		cancel := s.running[taskID]
		s.mu.Unlock()
		if cancel == nil {
			return nil, fmt.Errorf("task %s is running outside this worker", taskID)
		}
		cancel()
	default:
		return nil, fmt.Errorf("task %s is already %s", taskID, task.Status)
	}
	return s.db.GetAsyncTask(taskID)
}

// Retry creates a new task from a failed or cancelled task after operator review.
func (s *TaskQueueService) Retry(taskID string) (*domain.AsyncTask, error) {
	task, err := s.db.GetAsyncTask(taskID)
	if err != nil {
		return nil, err
	}
	if task.Status != "failed" && task.Status != "cancelled" {
		return nil, fmt.Errorf("task %s is %s, not failed or cancelled", taskID, task.Status)
	}
	payload := make(map[string]any, len(task.Payload)+1)
	for key, value := range task.Payload {
		payload[key] = value
	}
	if task.Type == "quark_to_115_import" {
		payload["prior_import_task_id"] = task.ID
		return s.enqueueAutofillQuarkImport(payload)
	}
	return s.enqueue(task.Type, payload, task.ScheduleID)
}
