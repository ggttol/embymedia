package service

import (
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/embymedia/embymedia/internal/storage"
)

const defaultWebhookDebounce = 5 * time.Second

// CloudDriveWebhookService debounces CloudDrive2 changes into one real Emby refresh task.
type CloudDriveWebhookService struct {
	db    *storage.DB
	queue *TaskQueueService

	mu      sync.Mutex
	timer   *time.Timer
	pending int
	closed  bool
}

// NewCloudDriveWebhookService creates a stopped debounce accumulator.
func NewCloudDriveWebhookService(db *storage.DB, queue *TaskQueueService) *CloudDriveWebhookService {
	return &CloudDriveWebhookService{db: db, queue: queue}
}

func (s *CloudDriveWebhookService) delay() time.Duration {
	value, _ := s.db.GetSetting("clouddrive_webhook_debounce_seconds")
	if value == "" {
		return defaultWebhookDebounce
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 1 || seconds > 300 {
		return defaultWebhookDebounce
	}
	return time.Duration(seconds) * time.Second
}

// Notify records one change and resets the debounce timer.
func (s *CloudDriveWebhookService) Notify() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("CloudDrive webhook service is closed")
	}
	s.pending++
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(s.delay(), s.flush)
	return nil
}

func (s *CloudDriveWebhookService) flush() {
	s.mu.Lock()
	if s.closed || s.pending == 0 {
		s.mu.Unlock()
		return
	}
	count := s.pending
	s.pending = 0
	s.timer = nil
	s.mu.Unlock()
	if _, err := s.queue.Enqueue("emby_refresh", map[string]any{"change_count": count}); err != nil {
		log.Printf("enqueue CloudDrive webhook refresh: %v", err)
	}
}

// Close prevents further notifications and cancels a pending debounce timer.
func (s *CloudDriveWebhookService) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.pending = 0
}
