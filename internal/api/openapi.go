package api

import "github.com/embymedia/embymedia/internal/product"

func schemaObject(required []string, properties map[string]any) map[string]any {
	schema := map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func stringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func stringArraySchema(description string) map[string]any {
	return map[string]any{"type": "array", "description": description, "items": map[string]any{"type": "string"}}
}

func pathParameter(name, description string) map[string]any {
	return map[string]any{"name": name, "in": "path", "required": true, "description": description, "schema": map[string]any{"type": "string"}}
}

func queryParameter(name, description string, required bool) map[string]any {
	return map[string]any{"name": name, "in": "query", "required": required, "description": description, "schema": map[string]any{"type": "string"}}
}

func integerQueryParameter(name, description string) map[string]any {
	return map[string]any{"name": name, "in": "query", "required": false, "description": description, "schema": map[string]any{"type": "integer", "minimum": 0}}
}

func apiOperation(id, summary string, parameters []any, body map[string]any) map[string]any {
	operation := map[string]any{
		"operationId": id,
		"summary":     summary,
		"security":    []any{map[string]any{"AgentToken": []any{}}},
		"responses": map[string]any{
			"200": map[string]any{"description": "Successful response", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{}}}},
			"201": map[string]any{"description": "Resource created"},
			"202": map[string]any{"description": "Operation accepted"},
			"204": map[string]any{"description": "Operation completed with no response body"},
			"400": map[string]any{"description": "Invalid request"},
			"401": map[string]any{"description": "Invalid Agent token"},
			"403": map[string]any{"description": "Insufficient permission or disabled safety switch"},
			"404": map[string]any{"description": "Resource not found"},
			"409": map[string]any{"description": "Operation conflicts with current state"},
			"413": map[string]any{"description": "Request body too large"},
			"429": map[string]any{"description": "Agent token rate limit exceeded"},
			"500": map[string]any{"description": "Local service failure"},
			"502": map[string]any{"description": "Upstream provider failed"},
			"503": map[string]any{"description": "Required provider or configuration unavailable"},
		},
	}
	if len(parameters) > 0 {
		operation["parameters"] = parameters
	}
	if body != nil {
		operation["requestBody"] = map[string]any{
			"required": true,
			"content":  map[string]any{"application/json": map[string]any{"schema": body}},
		}
	}
	return operation
}

func browserOperation(id, summary string, parameters []any) map[string]any {
	operation := apiOperation(id, summary, parameters, nil)
	operation["security"] = []any{map[string]any{"BrowserSession": []any{}}}
	return operation
}

func openAPISchema() map[string]any {
	provider := map[string]any{"type": "string", "enum": []string{"115", "quark"}, "description": "Drive provider"}
	accountID := stringSchema("Managed account ID for the selected provider")
	targetID := stringSchema("Destination provider directory ID; root is 0")
	fileID := stringSchema("Provider file or directory ID")
	shareBody := schemaObject([]string{"provider", "url"}, map[string]any{
		"provider": provider, "url": stringSchema("Provider share URL or code"), "password": stringSchema("Optional extraction code"),
		"target_cid": targetID, "account_id": accountID,
	})
	seriesAutoFillPayload := schemaObject([]string{"libraries"}, map[string]any{
		"libraries":              stringArraySchema("Eligible Emby libraries (电视剧追更 or 综艺追更)"),
		"series_ids":             stringArraySchema("Optional exact Series IDs; limits discovery to selected Series"),
		"transfer":               map[string]any{"type": "boolean", "default": true},
		"replace_completed_pack": map[string]any{"type": "boolean", "default": false},
		"candidate_limit":        map[string]any{"type": "integer", "minimum": 1, "maximum": 30, "default": 10},
		"max_series":             map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 20},
		"candidate_overrides":    map[string]any{"type": "object", "description": "Map exact Series ID to one resource ID when resources conflict", "additionalProperties": map[string]any{"type": "string"}},
	})
	paths := map[string]any{
		"/hooks/clouddrive2": map[string]any{"post": map[string]any{
			"operationId": "receiveCloudDriveWebhook", "summary": "Debounce one authenticated filesystem event into ordered STRM synchronization and Emby scanning",
			"security":    []any{map[string]any{"WebhookSecret": []any{}}},
			"requestBody": map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object"}}}},
			"responses":   map[string]any{"202": map[string]any{"description": "Event accepted for debounce"}, "401": map[string]any{"description": "Invalid webhook secret"}},
		}},
		"/api/v1/home/summary": map[string]any{"get": apiOperation("getHomeSummary", "Read resource-index summary", nil, nil)},
		"/api/v1/sources":      map[string]any{"get": apiOperation("listResourceSources", "List resource-index sources", nil, nil)},
		"/api/v1/trends":       map[string]any{"get": apiOperation("listSearchTrends", "List resource search trends", nil, nil)},
		"/api/v1/search": map[string]any{"get": apiOperation("searchResources", "Search indexed netdisk resources", []any{
			queryParameter("q", "Search query", true), queryParameter("channel", "Optional source channel", false),
			queryParameter("provider", "Optional provider filter (115 or quark)", false),
			queryParameter("health_status", "Optional source health status", false), integerQueryParameter("offset", "Result offset"), integerQueryParameter("limit", "Page size"),
		}, nil)},
		"/api/v1/links/{id}":           map[string]any{"get": apiOperation("getResourceLink", "Read one indexed resource link", []any{pathParameter("id", "Resource link ID")}, nil)},
		"/api/v1/links/{id}/save":      map[string]any{"post": apiOperation("saveResourceLink", "Save one indexed link to 115", []any{pathParameter("id", "Resource link ID")}, schemaObject(nil, map[string]any{"target_cid": targetID}))},
		"/api/v1/cid-map":              map[string]any{"get": apiOperation("getCIDMap", "Read configured 115 category CIDs", nil, nil)},
		"/api/v1/drive/share-save":     map[string]any{"post": apiOperation("saveDriveShare", "Save a provider share", nil, shareBody)},
		"/api/v1/drive/share-snapshot": map[string]any{"post": apiOperation("snapshotDriveShare", "Inspect a provider share without saving", nil, schemaObject([]string{"provider", "url"}, map[string]any{"provider": provider, "account_id": accountID, "url": stringSchema("Provider share URL or code"), "password": stringSchema("Optional extraction code")}))},
		"/api/v1/drive/accounts": map[string]any{
			"get": apiOperation("listDriveAccounts", "List provider accounts without credentials", []any{queryParameter("provider", "Drive provider", true)}, nil),
			"post": apiOperation("upsertDriveAccount", "Create or update a provider account", nil, schemaObject([]string{"type", "name"}, map[string]any{
				"id": accountID, "type": provider, "name": stringSchema("Account display name"), "cookie": stringSchema("Browser cookie; required for create and omitted to preserve on update"), "token": stringSchema("Optional 115 Open Platform access token; omitted to preserve on update"), "is_default": map[string]any{"type": "boolean"},
			})),
		},
		"/api/v1/drive/accounts/{id}": map[string]any{"delete": apiOperation("deleteDriveAccount", "Delete a managed provider account", []any{pathParameter("id", "Managed account ID")}, nil)},
		"/api/v1/drive/files":         map[string]any{"get": apiOperation("listDriveFiles", "List provider files and directories", []any{queryParameter("provider", "Drive provider", true), queryParameter("account_id", "Managed account ID", true), queryParameter("parent_id", "Directory ID", true)}, nil)},
		"/api/v1/drive/files/mkdir":   map[string]any{"post": apiOperation("createDriveDirectory", "Create a provider directory", nil, schemaObject([]string{"provider", "account_id", "parent_id", "name"}, map[string]any{"provider": provider, "account_id": accountID, "parent_id": targetID, "name": stringSchema("Directory name")}))},
		"/api/v1/drive/files/rename":  map[string]any{"post": apiOperation("renameDriveFile", "Rename a provider file or directory", nil, schemaObject([]string{"provider", "account_id", "file_id", "new_name"}, map[string]any{"provider": provider, "account_id": accountID, "file_id": fileID, "new_name": stringSchema("New name")}))},
		"/api/v1/drive/files/move":    map[string]any{"post": apiOperation("moveDriveFiles", "Move provider files or directories", nil, schemaObject([]string{"provider", "account_id", "file_ids", "target_id"}, map[string]any{"provider": provider, "account_id": accountID, "file_ids": stringArraySchema("File and directory IDs"), "target_id": targetID}))},
		"/api/v1/drive/files/delete":  map[string]any{"post": apiOperation("deleteDriveFiles", "Move provider objects to the recycle bin", nil, schemaObject([]string{"provider", "account_id", "parent_id", "file_ids"}, map[string]any{"provider": provider, "account_id": accountID, "parent_id": targetID, "file_ids": stringArraySchema("File and directory IDs")}))},
		"/api/v1/offline/download":    map[string]any{"post": apiOperation("add115OfflineDownloads", "Submit 115 offline downloads", nil, schemaObject([]string{"urls"}, map[string]any{"account_id": accountID, "urls": stringArraySchema("Magnet, ed2k, or HTTP URLs"), "target_cid": targetID}))},
		"/api/v1/emby/libraries":      map[string]any{"get": apiOperation("listEmbyLibraries", "List Emby media libraries", nil, nil)},
		"/api/v1/emby/refresh":        map[string]any{"post": apiOperation("refreshEmbyLibrary", "Refresh an Emby library or the entire server", []any{queryParameter("library_id", "Optional Emby library ID", false)}, nil)},
		"/api/v1/emby/match":          map[string]any{"post": apiOperation("matchEmbyItem", "Apply TMDB metadata and images to an Emby item", nil, schemaObject([]string{"item_id", "tmdb_id"}, map[string]any{"item_id": stringSchema("Emby item ID"), "tmdb_id": stringSchema("TMDB ID")}))},
		"/api/v1/emby/items/{id}":     map[string]any{"get": apiOperation("getEmbyItem", "Inspect one exact Emby item", []any{pathParameter("id", "Emby item ID")}, nil)},
		"/api/v1/mounts":              map[string]any{"get": apiOperation("listCloudDriveMounts", "List and verify CloudDrive2 mounts", nil, nil)},
		"/api/v1/mounts/remount":      map[string]any{"post": apiOperation("remountCloudDrive", "Remount CloudDrive2 only when Emby reports no active playback", nil, nil)},
		"/api/v1/tasks": map[string]any{
			"get": apiOperation("listSchedules", "List scheduled tasks", nil, nil),
			"post": apiOperation("createSchedule", "Create or update a scheduled real operation", nil, schemaObject([]string{"type", "name", "enabled"}, map[string]any{
				"id": stringSchema("Optional schedule ID"), "type": stringSchema("Supported background task type"), "name": stringSchema("Schedule name"),
				"cron_expr": stringSchema("Cron expression with seconds"), "enabled": map[string]any{"type": "boolean"}, "params": stringSchema("JSON object encoded as a string"),
			})),
		},
		"/api/v1/tasks/{id}/run": map[string]any{"post": apiOperation("runSchedule", "Create and return a schedule-linked execution immediately", []any{pathParameter("id", "Schedule ID")}, nil)},
		"/api/v1/tasks/batch": map[string]any{"post": apiOperation("submitTaskBatch", "Submit 1 to 100 validated background tasks", nil, schemaObject([]string{"operations"}, map[string]any{
			"operations": map[string]any{"type": "array", "minItems": 1, "maxItems": 100, "items": schemaObject([]string{"action", "payload"}, map[string]any{"action": stringSchema("Supported background task type"), "payload": map[string]any{"type": "object"}})},
		}))},
		"/api/v1/async-tasks":             map[string]any{"get": apiOperation("listAsyncTasks", "List background tasks", []any{queryParameter("status", "Optional task status", false)}, nil)},
		"/api/v1/async-tasks/{id}":        map[string]any{"get": apiOperation("getAsyncTask", "Read one background task", []any{pathParameter("id", "Task ID")}, nil)},
		"/api/v1/async-tasks/{id}/cancel": map[string]any{"post": apiOperation("cancelAsyncTask", "Cancel pending or running work", []any{pathParameter("id", "Task ID")}, nil)},
		"/api/v1/async-tasks/{id}/retry":  map[string]any{"post": apiOperation("retryAsyncTask", "Create a reviewed retry from failed or cancelled work", []any{pathParameter("id", "Task ID")}, nil)},
		"/api/v1/async-tasks/{id}/runs":   map[string]any{"get": apiOperation("listTaskRuns", "List durable attempts with start time, end time, progress, errors and logs", []any{pathParameter("id", "Task ID")}, nil)},
		"/api/v1/quark/share-imports": map[string]any{"post": apiOperation("importQuarkShareTo115", "Save a Quark share and transfer verified files to /emby/_待整理 in 115", nil, schemaObject([]string{"quark_account_id", "quark_target_id", "share_url"}, map[string]any{
			"quark_account_id": accountID, "quark_target_id": stringSchema("Quark directory ID"), "share_url": stringSchema("Quark share URL"), "share_password": stringSchema("Optional share password"), "c115_account_id": stringSchema("Optional 115 account; default is used when omitted"),
		}))},
		"/api/v1/quark/share-imports/{task_id}": map[string]any{"get": apiOperation("getQuarkShareImport", "Read structured Quark-to-115 import progress", []any{pathParameter("task_id", "Import task ID")}, nil)},
		"/api/v1/settings": map[string]any{
			"get":  apiOperation("getSettings", "Read redacted settings and live dependency health", nil, nil),
			"post": apiOperation("updateSettings", "Atomically update validated settings", nil, map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}),
		},
		"/api/v1/settings/check": map[string]any{"post": apiOperation("checkSettings", "Check one configured dependency", nil, schemaObject(nil, map[string]any{"component": map[string]any{"type": "string", "enum": []string{"c115", "quark", "emby", "clouddrive", "resource"}}}))},
		"/api/v1/tokens": map[string]any{
			"get": apiOperation("listAgentTokens", "List Agent tokens without secret values", nil, nil),
			"post": apiOperation("createAgentToken", "Create an Agent token and return its secret once", nil, schemaObject([]string{"name", "permissions"}, map[string]any{
				"name": stringSchema("Agent name"), "permissions": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"read", "write"}}},
				"rate_limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 600, "default": 120},
			})),
		},
		"/api/v1/tokens/{id}":                        map[string]any{"delete": apiOperation("revokeAgentToken", "Revoke an Agent token", []any{pathParameter("id", "Token ID")}, nil)},
		"/api/v1/audit-logs":                         map[string]any{"get": apiOperation("listAgentAudits", "List bounded REST and MCP audit records", auditLogParameters(), nil)},
		"/api/v1/destructive-approvals":              map[string]any{"get": browserOperation("listDestructiveApprovals", "List pending target-bound deletion requests", nil)},
		"/api/v1/destructive-approvals/{id}/approve": map[string]any{"post": browserOperation("approveDestructiveApproval", "Approve one exact deletion request", []any{pathParameter("id", "Approval ID")})},
		"/api/v1/destructive-approvals/{id}/reject":  map[string]any{"post": browserOperation("rejectDestructiveApproval", "Reject one exact deletion request", []any{pathParameter("id", "Approval ID")})},
	}
	paths["/api/v1/drive/files/delete"].(map[string]any)["post"].(map[string]any)["security"] = []any{map[string]any{"BrowserSession": []any{}}}
	components := map[string]any{
		"securitySchemes": map[string]any{
			"AgentToken":     map[string]any{"type": "apiKey", "in": "header", "name": "X-Agent-Token", "description": "Agent token issued by the administrator"},
			"BrowserSession": map[string]any{"type": "apiKey", "in": "cookie", "name": "embymedia_http_session", "description": "Authenticated browser session; Agent tokens cannot decide destructive approvals"},
			"WebhookSecret":  map[string]any{"type": "apiKey", "in": "header", "name": "X-Webhook-Secret", "description": "CloudDrive2 webhook secret"},
		},
		"schemas": map[string]any{
			"SeriesAutoFillPayload": seriesAutoFillPayload,
			"SeriesAutoFillResult": schemaObject(nil, map[string]any{
				"series_id": stringSchema("Exact Emby Series ID"), "series_name": stringSchema("Series display name"),
				"folder": stringSchema("Canonical Series folder"), "missing_episodes": stringArraySchema("Episodes missing before repair"),
				"matched_episodes": stringArraySchema("Episodes covered by validated sources"), "transferred_episodes": stringArraySchema("Episodes transferred to 115"),
				"remaining_episodes": stringArraySchema("Episodes still missing after verification"), "candidates_checked": map[string]any{"type": "integer", "minimum": 0},
				"candidate_evidence": map[string]any{"type": "array", "description": "Redacted evidence for each candidate; URLs and passwords are excluded", "items": map[string]any{"type": "object"}},
				"conflicts":          map[string]any{"type": "array", "description": "Cross-provider/resource episode conflicts", "items": map[string]any{"type": "object"}},
				"queued_imports":     map[string]any{"type": "array", "description": "Queued Quark child imports without credentials or share secrets", "items": map[string]any{"type": "object"}},
				"issue":              stringSchema("Failure or review reason"),
			}),
		},
	}
	return map[string]any{
		"openapi":    "3.1.0",
		"info":       map[string]any{"title": "EmbyMedia V2 API", "version": product.Version, "description": "Standalone drive, Emby, CloudDrive2, task, settings and Agent operations", "license": map[string]any{"name": "MIT", "identifier": "MIT"}},
		"servers":    []any{map[string]any{"url": "/", "description": "Current EmbyMedia server"}},
		"components": components,
		"paths":      paths,
	}
}
