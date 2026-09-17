package api

import (
	"net/http"

	"github.com/embymedia/embymedia/internal/domain"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

func (s *Server) handleListShareSubscriptions(c echo.Context) error {
	subs, err := s.db.ListShareSubscriptions(c.Request().Context(), false)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, map[string]any{"subscriptions": subs})
}

func (s *Server) handleCreateShareSubscription(c echo.Context) error {
	var payload struct {
		Name      string `json:"name"`
		Provider  string `json:"provider"`
		URL       string `json:"url"`
		Password  string `json:"password"`
		TargetCID string `json:"target_cid"`
		Active    bool   `json:"active"`
	}
	if err := c.Bind(&payload); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if payload.Name == "" || payload.Provider == "" || payload.URL == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "name, provider and url are required")
	}

	id := uuid.NewString()
	subObj := domain.ShareSubscription{
		ID:        id,
		Name:      payload.Name,
		Provider:  payload.Provider,
		URL:       payload.URL,
		Password:  payload.Password,
		TargetCID: payload.TargetCID,
		Active:    payload.Active,
	}
	if err := s.db.CreateShareSubscription(c.Request().Context(), subObj); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create subscription")
	}

	sub, err := s.db.GetShareSubscription(c.Request().Context(), id)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	return c.JSON(http.StatusCreated, sub)
}

func (s *Server) handleUpdateShareSubscription(c echo.Context) error {
	id := c.Param("id")
	var payload struct {
		Name      string `json:"name"`
		Provider  string `json:"provider"`
		URL       string `json:"url"`
		Password  string `json:"password"`
		TargetCID string `json:"target_cid"`
		Active    bool   `json:"active"`
	}
	if err := c.Bind(&payload); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if payload.Name == "" || payload.Provider == "" || payload.URL == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "name, provider and url are required")
	}

	if err := s.db.UpdateShareSubscription(c.Request().Context(), id, payload.Name, payload.Provider, payload.URL, payload.Password, payload.TargetCID, payload.Active); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to update subscription")
	}

	sub, err := s.db.GetShareSubscription(c.Request().Context(), id)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	return c.JSON(http.StatusOK, sub)
}

func (s *Server) handleDeleteShareSubscription(c echo.Context) error {
	id := c.Param("id")
	if err := s.db.DeleteShareSubscription(c.Request().Context(), id); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}
