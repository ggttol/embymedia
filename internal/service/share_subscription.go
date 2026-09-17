package service

import (
	"context"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
	"github.com/google/uuid"
)

type ShareSubscriptionService struct {
	db *storage.DB
}

func NewShareSubscriptionService(db *storage.DB) *ShareSubscriptionService {
	return &ShareSubscriptionService{db: db}
}

func (s *ShareSubscriptionService) Create(ctx context.Context, sub domain.ShareSubscription) (domain.ShareSubscription, error) {
	sub.ID = uuid.NewString()
	if sub.LastCursorTime.IsZero() {
		sub.LastCursorTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	sub.Active = true
	err := s.db.CreateShareSubscription(ctx, sub)
	return sub, err
}

func (s *ShareSubscriptionService) List(ctx context.Context, activeOnly bool) ([]domain.ShareSubscription, error) {
	return s.db.ListShareSubscriptions(ctx, activeOnly)
}

func (s *ShareSubscriptionService) Delete(ctx context.Context, id string) error {
	return s.db.DeleteShareSubscription(ctx, id)
}

func (s *ShareSubscriptionService) Toggle(ctx context.Context, id string, active bool) error {
	return s.db.ToggleShareSubscription(ctx, id, active)
}
