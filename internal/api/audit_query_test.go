package api

import (
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestParseAuditQueryDefaultsAndFilters(t *testing.T) {
	e := echo.New()
	request := httptest.NewRequest("GET", "/api/v1/audit-logs?limit=25&offset=2&protocol=mcp&agent=Hermes&action=c115_search&status=error&from=2026-09-06T01%3A00%3A00%2B08%3A00&to=2026-09-06T02%3A00%3A00%2B08%3A00&q=100%25_", nil)
	query, err := parseAuditQuery(e.NewContext(request, httptest.NewRecorder()))
	if err != nil {
		t.Fatalf("parse audit query: %v", err)
	}
	if query.Limit != 25 || query.Offset != 2 || query.Protocol != "mcp" || query.Agent != "Hermes" || query.Action != "c115_search" || query.Status != "error" || query.Q != "100%_" || query.From == nil || query.To == nil {
		t.Fatalf("query = %+v", query)
	}
}

func TestParseAuditQueryRejectsInvalidValues(t *testing.T) {
	cases := []string{
		"?limit=0", "?limit=101", "?limit=no", "?offset=-1", "?offset=no",
		"?protocol=rest", "?status=accepted", "?from=not-a-time", "?to=not-a-time",
		"?from=2026-09-07T00%3A00%3A00Z&to=2026-09-06T00%3A00%3A00Z",
	}
	for _, rawQuery := range cases {
		t.Run(rawQuery, func(t *testing.T) {
			e := echo.New()
			request := httptest.NewRequest("GET", "/api/v1/audit-logs"+rawQuery, nil)
			if _, err := parseAuditQuery(e.NewContext(request, httptest.NewRecorder())); err == nil {
				t.Fatal("invalid audit query was accepted")
			}
		})
	}
}
