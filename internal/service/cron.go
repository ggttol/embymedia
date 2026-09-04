package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
	"github.com/robfig/cron/v3"
)

type CronManager struct {
	db     *storage.DB
	cron   *cron.Cron
	entryMap map[string]cron.EntryID
	mu     sync.Mutex
}

func NewCronManager(db *storage.DB) *CronManager {
	return &CronManager{
		db:       db,
		cron:     cron.New(cron.WithSeconds()),
		entryMap: make(map[string]cron.EntryID),
	}
}

func (cm *CronManager) Start(ctx context.Context) error {
	cm.cron.Start()

	tasks, err := cm.db.ListTasks()
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if t.Enabled && t.CronExpr != "" {
			_ = cm.ScheduleTask(t)
		}
	}

	go func() {
		<-ctx.Done()
		cm.cron.Stop()
	}()
	return nil
}

func (cm *CronManager) ScheduleTask(task domain.ScheduledTask) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if entryID, exists := cm.entryMap[task.ID]; exists {
		cm.cron.Remove(entryID)
		delete(cm.entryMap, task.ID)
	}

	if !task.Enabled || task.CronExpr == "" {
		return nil
	}

	entryID, err := cm.cron.AddFunc(task.CronExpr, func() {
		now := time.Now()
		task.LastRunAt = &now
		task.Status = "running"
		_ = cm.db.SaveTask(&task)
		// Execute task logic here...
		time.Sleep(100 * time.Millisecond)
		task.Status = "idle"
		_ = cm.db.SaveTask(&task)
	})
	if err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}

	cm.entryMap[task.ID] = entryID
	return nil
}
