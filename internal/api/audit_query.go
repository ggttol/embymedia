package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/embymedia/embymedia/internal/storage"
	"github.com/labstack/echo/v4"
)

func parseAuditQuery(c echo.Context) (storage.AuditQuery, error) {
	query := storage.AuditQuery{Limit: 20, Protocol: "all"}
	if raw := c.QueryParam("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			return query, fmt.Errorf("limit must be an integer from 1 to 100")
		}
		query.Limit = value
	}
	if raw := c.QueryParam("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			return query, fmt.Errorf("offset must be a non-negative integer")
		}
		query.Offset = value
	}
	if raw := c.QueryParam("protocol"); raw != "" {
		if raw != "all" && raw != "mcp" {
			return query, fmt.Errorf("protocol must be all or mcp")
		}
		query.Protocol = raw
	}
	query.Agent = c.QueryParam("agent")
	query.Action = c.QueryParam("action")
	query.Status = c.QueryParam("status")
	if query.Status != "" && query.Status != "success" && query.Status != "error" && query.Status != "denied" {
		return query, fmt.Errorf("status must be success, error, or denied")
	}
	parseTime := func(name string) (*time.Time, error) {
		raw := c.QueryParam(name)
		if raw == "" {
			return nil, nil
		}
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, fmt.Errorf("%s must be an RFC3339 timestamp", name)
		}
		return &value, nil
	}
	var err error
	if query.From, err = parseTime("from"); err != nil {
		return query, err
	}
	if query.To, err = parseTime("to"); err != nil {
		return query, err
	}
	if query.From != nil && query.To != nil && query.From.After(*query.To) {
		return query, fmt.Errorf("from must not be later than to")
	}
	query.Q = c.QueryParam("q")
	return query, nil
}

func (s *Server) queryAuditLogs(c echo.Context) error {
	query, err := parseAuditQuery(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	result, err := s.db.QueryAuditLogs(c.Request().Context(), query)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, result)
}
