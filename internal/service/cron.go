package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

// CronManager persists schedules and submits their real operation to the task queue.
type CronManager struct {
	db       *storage.DB
	queue    *TaskQueueService
	cron     *cron.Cron
	entryMap map[string]cron.EntryID
	mu       sync.Mutex
}

// NewCronManager creates a stopped scheduler.
func NewCronManager(db *storage.DB, queue *TaskQueueService) *CronManager {
	return &CronManager{db: db, queue: queue, cron: cron.New(cron.WithSeconds(), cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger))), entryMap: make(map[string]cron.EntryID)}
}

// Start restores enabled schedules and starts dispatching them.
func (cm *CronManager) Start(_ context.Context) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	tasks, err := cm.db.ListTasks()
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if task.Enabled && task.CronExpr != "" {
			if err := cm.registerTask(task); err != nil {
				return fmt.Errorf("restore schedule %s: %w", task.ID, err)
			}
		}
	}
	cm.cron.Start()
	return nil
}

// Stop waits for running schedule callbacks to finish.
func (cm *CronManager) Stop() {
	<-cm.cron.Stop().Done()
}

func taskPayload(task domain.ScheduledTask) (map[string]any, error) {
	payload := map[string]any{}
	if task.Params != "" {
		if err := json.Unmarshal([]byte(task.Params), &payload); err != nil {
			return nil, fmt.Errorf("task params must be a JSON object: %w", err)
		}
	}
	if err := validateTask(task.Type, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// registerTask requires cm.mu until the entry and captured task are fully published.
func (cm *CronManager) registerTask(task domain.ScheduledTask) error {
	payload, err := taskPayload(task)
	if err != nil {
		return err
	}
	var entryID cron.EntryID
	entryID, err = cm.cron.AddFunc(task.CronExpr, func() {
		cm.mu.Lock()
		defer cm.mu.Unlock()
		// Removed entries may already have a callback waiting to run.
		if currentID, exists := cm.entryMap[task.ID]; !exists || currentID != entryID {
			return
		}
		now := time.Now()
		task.LastRunAt = &now
		queued, enqueueErr := cm.queue.EnqueueScheduled(task.ID, task.Type, payload)
		if enqueueErr != nil {
			task.Status = "failed"
			task.Error = enqueueErr.Error()
		} else {
			task.Status = "idle"
			task.Error = ""
			task.Result = queued.ID
		}
		next := cm.cron.Entry(entryID).Next
		task.NextRunAt = &next
		if err := cm.db.SaveTask(&task); err != nil {
			return
		}
	})
	if err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}
	cm.entryMap[task.ID] = entryID
	next := cm.cron.Entry(entryID).Schedule.Next(time.Now())
	task.NextRunAt = &next
	if err := cm.db.SaveTask(&task); err != nil {
		cm.cron.Remove(entryID)
		delete(cm.entryMap, task.ID)
		return err
	}
	return nil
}

// RunTask atomically queues one manual execution and records it on the schedule.
func (cm *CronManager) RunTask(id string) (*domain.AsyncTask, *domain.ScheduledTask, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	task, err := cm.db.GetTask(id)
	if err != nil {
		return nil, nil, err
	}
	payload, err := taskPayload(*task)
	if err != nil {
		return nil, nil, err
	}
	queued, err := cm.queue.EnqueueScheduled(task.ID, task.Type, payload)
	if err != nil {
		return nil, nil, err
	}
	task.LastRunAt = &queued.CreatedAt
	task.Result = queued.ID
	task.Error = ""
	return queued, task, nil
}

// ScheduleTask validates, persists, and activates a scheduled task.
func (cm *CronManager) ScheduleTask(task *domain.ScheduledTask) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if task.ID == "" {
		task.ID = uuid.NewString()
	}
	if task.Name == "" {
		return fmt.Errorf("task name is required")
	}
	if _, err := taskPayload(*task); err != nil {
		return err
	}
	if task.Enabled {
		parser := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
		if _, err := parser.Parse(task.CronExpr); err != nil {
			return fmt.Errorf("invalid cron expression: %w", err)
		}
	}
	if entryID, exists := cm.entryMap[task.ID]; exists {
		cm.cron.Remove(entryID)
		delete(cm.entryMap, task.ID)
	}
	if task.Enabled {
		if task.CronExpr == "" {
			return fmt.Errorf("cron expression is required for enabled task")
		}
		task.Status = "idle"
	} else {
		task.Status = "paused"
		task.NextRunAt = nil
	}
	if !task.Enabled {
		return cm.db.SaveTask(task)
	}
	if err := cm.registerTask(*task); err != nil {
		return err
	}
	stored, err := cm.db.GetTask(task.ID)
	if err != nil {
		return err
	}
	*task = *stored
	return nil
}

// DeleteTask removes one schedule and its active cron registration.
func (cm *CronManager) DeleteTask(id string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if _, err := cm.db.GetTask(id); err != nil {
		return fmt.Errorf("scheduled task not found")
	}
	if entryID, exists := cm.entryMap[id]; exists {
		cm.cron.Remove(entryID)
		delete(cm.entryMap, id)
	}
	return cm.db.DeleteTask(id)
}
