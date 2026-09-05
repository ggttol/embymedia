package service

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

type TaskQueueService struct {
	db       *storage.DB
	cron     *cron.Cron
	driveSvc *DriveService
	embySvc  *EmbyService
	mu       sync.Mutex
	stopChan chan struct{}
}

func NewTaskQueueService(db *storage.DB, driveSvc *DriveService, embySvc *EmbyService) *TaskQueueService {
	return &TaskQueueService{
		db:       db,
		cron:     cron.New(cron.WithSeconds()),
		driveSvc: driveSvc,
		embySvc:  embySvc,
		stopChan: make(chan struct{}),
	}
}

func (s *TaskQueueService) Start(ctx context.Context) {
	s.cron.Start()
	// Background worker for async task processing
	go s.workerLoop(ctx)
}

func (s *TaskQueueService) Stop() {
	s.cron.Stop()
	close(s.stopChan)
}

func (s *TaskQueueService) Enqueue(taskType string, payload map[string]any) (*domain.AsyncTask, error) {
	task := &domain.AsyncTask{
		ID:        uuid.New().String(),
		Type:      taskType,
		Payload:   payload,
		Status:    "pending",
		Progress:  0,
		CreatedAt: time.Now(),
	}

	if err := s.db.CreateAsyncTask(task); err != nil {
		return nil, err
	}
	return task, nil
}

func (s *TaskQueueService) workerLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.processPendingTasks(ctx)
		}
	}
}

func (s *TaskQueueService) processPendingTasks(ctx context.Context) {
	tasks, err := s.db.ListAsyncTasks("pending", 5)
	if err != nil || len(tasks) == 0 {
		return
	}

	for _, task := range tasks {
		s.executeTask(ctx, task)
	}
}

func (s *TaskQueueService) executeTask(ctx context.Context, task domain.AsyncTask) {
	_ = s.db.UpdateAsyncTaskStatus(task.ID, "running", 10, "")

	var err error
	switch task.Type {
	case "organize_files":
		err = s.runOrganizeFiles(ctx, task)
	case "scrape_metadata":
		err = s.runScrapeMetadata(ctx, task)
	case "sync_library":
		err = s.runSyncLibrary(ctx, task)
	case "check_dead_links":
		err = s.runCheckDeadLinks(ctx, task)
	default:
		err = fmt.Errorf("unknown task type: %s", task.Type)
	}

	if err != nil {
		log.Printf("Task %s (%s) failed: %v", task.ID, task.Type, err)
		_ = s.db.UpdateAsyncTaskStatus(task.ID, "failed", 0, err.Error())
	} else {
		_ = s.db.UpdateAsyncTaskStatus(task.ID, "completed", 100, "")
	}
}

func (s *TaskQueueService) runOrganizeFiles(ctx context.Context, task domain.AsyncTask) error {
	// Simulated file organization logic
	time.Sleep(100 * time.Millisecond)
	_ = s.db.UpdateAsyncTaskStatus(task.ID, "running", 50, "")
	time.Sleep(100 * time.Millisecond)
	return nil
}

func (s *TaskQueueService) runScrapeMetadata(ctx context.Context, task domain.AsyncTask) error {
	time.Sleep(100 * time.Millisecond)
	_ = s.db.UpdateAsyncTaskStatus(task.ID, "running", 60, "")
	time.Sleep(100 * time.Millisecond)
	return nil
}

func (s *TaskQueueService) runSyncLibrary(ctx context.Context, task domain.AsyncTask) error {
	if s.embySvc == nil {
		return fmt.Errorf("Emby service is unavailable")
	}
	return s.embySvc.RefreshLibrary("")
}

func (s *TaskQueueService) runCheckDeadLinks(ctx context.Context, task domain.AsyncTask) error {
	time.Sleep(100 * time.Millisecond)
	return nil
}
