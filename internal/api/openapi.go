package api

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

func openAPISchema() map[string]any {
	accountID := stringSchema("Managed 115 account ID; omit to use the default account")
	targetCID := stringSchema("Destination 115 directory CID; omit or use 0 for root")
	fileID := stringSchema("115 file or directory ID")
	shareBody := schemaObject([]string{"url"}, map[string]any{
		"url": stringSchema("115 share URL or code"), "password": stringSchema("Optional extraction code"),
		"target_cid": targetCID, "account_id": accountID,
	})
	paths := map[string]any{
		"/hooks/clouddrive2": map[string]any{"post": map[string]any{
			"operationId": "receiveCloudDriveWebhook", "summary": "Debounce one authenticated CloudDrive2 filesystem event",
			"security":    []any{map[string]any{"WebhookSecret": []any{}}},
			"requestBody": map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object"}}}},
			"responses":   map[string]any{"202": map[string]any{"description": "Event accepted for debounce"}, "401": map[string]any{"description": "Invalid webhook secret"}},
		}},
		"/api/v1/home/summary": map[string]any{"get": apiOperation("getHomeSummary", "Read resource-index summary", nil, nil)},
		"/api/v1/sources":      map[string]any{"get": apiOperation("listResourceSources", "List resource-index sources", nil, nil)},
		"/api/v1/trends":       map[string]any{"get": apiOperation("listSearchTrends", "List resource search trends", nil, nil)},
		"/api/v1/search": map[string]any{"get": apiOperation("searchResources", "Search indexed netdisk resources", []any{
			queryParameter("q", "Search query", true), queryParameter("channel", "Optional source channel", false),
			queryParameter("health_status", "Optional health status", false), integerQueryParameter("offset", "Result offset"), integerQueryParameter("limit", "Page size"),
		}, nil)},
		"/api/v1/links/{id}":           map[string]any{"get": apiOperation("getResourceLink", "Read one indexed resource link", []any{pathParameter("id", "Resource link ID")}, nil)},
		"/api/v1/links/{id}/save":      map[string]any{"post": apiOperation("saveResourceLink", "Save one indexed link to 115", []any{pathParameter("id", "Resource link ID")}, schemaObject(nil, map[string]any{"target_cid": targetCID}))},
		"/api/v1/cid-map":              map[string]any{"get": apiOperation("getCIDMap", "Read configured 115 category CIDs", nil, nil)},
		"/api/v1/files/save_share":     map[string]any{"post": apiOperation("save115Share", "Save a 115 share", nil, shareBody)},
		"/api/v1/files/share_snapshot": map[string]any{"post": apiOperation("snapshot115Share", "Inspect a 115 share without saving", nil, schemaObject([]string{"url"}, map[string]any{"url": stringSchema("115 share URL or code"), "password": stringSchema("Optional extraction code")}))},
		"/api/v1/accounts": map[string]any{
			"get": apiOperation("list115Accounts", "List managed 115 accounts without credentials", nil, nil),
			"post": apiOperation("create115Account", "Create or update a managed 115 account", nil, schemaObject([]string{"name", "cookie"}, map[string]any{
				"id": accountID, "name": stringSchema("Account display name"), "cookie": stringSchema("115 browser cookie"), "is_default": map[string]any{"type": "boolean"},
			})),
		},
		"/api/v1/accounts/{id}":    map[string]any{"delete": apiOperation("delete115Account", "Delete a managed 115 account", []any{pathParameter("id", "Managed account ID")}, nil)},
		"/api/v1/files":            map[string]any{"get": apiOperation("list115Files", "List files and directories in 115", []any{queryParameter("account_id", "Managed account ID", false), queryParameter("cid", "Directory CID", false)}, nil)},
		"/api/v1/files/mkdir":      map[string]any{"post": apiOperation("create115Directory", "Create a directory in 115", nil, schemaObject([]string{"name"}, map[string]any{"account_id": accountID, "parent_cid": stringSchema("Parent CID"), "name": stringSchema("Directory name")}))},
		"/api/v1/files/rename":     map[string]any{"post": apiOperation("rename115File", "Rename a 115 file or directory", nil, schemaObject([]string{"file_id", "new_name"}, map[string]any{"account_id": accountID, "file_id": fileID, "new_name": stringSchema("New name")}))},
		"/api/v1/files/move":       map[string]any{"post": apiOperation("move115Files", "Move 115 files or directories", nil, schemaObject([]string{"file_ids", "target_cid"}, map[string]any{"account_id": accountID, "file_ids": stringArraySchema("File and directory IDs"), "target_cid": targetCID}))},
		"/api/v1/files/delete":     map[string]any{"post": apiOperation("delete115Files", "Move 115 files or directories to the recycle bin", nil, schemaObject([]string{"file_ids"}, map[string]any{"account_id": accountID, "file_ids": stringArraySchema("File and directory IDs")}))},
		"/api/v1/offline/download": map[string]any{"post": apiOperation("add115OfflineDownloads", "Submit 115 offline downloads", nil, schemaObject([]string{"urls"}, map[string]any{"account_id": accountID, "urls": stringArraySchema("Magnet, ed2k, or HTTP URLs"), "target_cid": targetCID}))},
		"/api/v1/emby/libraries":   map[string]any{"get": apiOperation("listEmbyLibraries", "List Emby media libraries", nil, nil)},
		"/api/v1/emby/refresh":     map[string]any{"post": apiOperation("refreshEmbyLibrary", "Refresh an Emby library or the entire server", []any{queryParameter("library_id", "Optional Emby library ID", false)}, nil)},
		"/api/v1/emby/match":       map[string]any{"post": apiOperation("matchEmbyItem", "Apply TMDB metadata and images to an Emby item", nil, schemaObject([]string{"item_id", "tmdb_id"}, map[string]any{"item_id": stringSchema("Emby item ID"), "tmdb_id": stringSchema("TMDB ID")}))},
		"/api/v1/emby/items/{id}":  map[string]any{"get": apiOperation("getEmbyItem", "Inspect one exact Emby item", []any{pathParameter("id", "Emby item ID")}, nil)},
		"/api/v1/mounts":           map[string]any{"get": apiOperation("listCloudDriveMounts", "List and verify CloudDrive2 mounts", nil, nil)},
		"/api/v1/mounts/remount":   map[string]any{"post": apiOperation("remountCloudDrive", "Remount CloudDrive2 after playback is stopped", nil, schemaObject([]string{"confirm_playback_stopped"}, map[string]any{"confirm_playback_stopped": map[string]any{"type": "boolean", "const": true}}))},
		"/api/v1/tasks": map[string]any{
			"get": apiOperation("listSchedules", "List scheduled tasks", nil, nil),
			"post": apiOperation("createSchedule", "Create or update a scheduled real operation", nil, schemaObject([]string{"type", "name", "enabled"}, map[string]any{
				"id": stringSchema("Optional schedule ID"), "type": stringSchema("Supported background task type"), "name": stringSchema("Schedule name"),
				"cron_expr": stringSchema("Cron expression with seconds"), "enabled": map[string]any{"type": "boolean"}, "params": stringSchema("JSON object encoded as a string"),
			})),
		},
		"/api/v1/tasks/{id}/run": map[string]any{"post": apiOperation("runSchedule", "Run a scheduled task immediately", []any{pathParameter("id", "Schedule ID")}, nil)},
		"/api/v1/tasks/batch": map[string]any{"post": apiOperation("submitTaskBatch", "Submit 1 to 100 validated background tasks", nil, schemaObject([]string{"operations"}, map[string]any{
			"operations": map[string]any{"type": "array", "minItems": 1, "maxItems": 100, "items": schemaObject([]string{"action", "payload"}, map[string]any{"action": stringSchema("Supported background task type"), "payload": map[string]any{"type": "object"}})},
		}))},
		"/api/v1/async-tasks":             map[string]any{"get": apiOperation("listAsyncTasks", "List background tasks", []any{queryParameter("status", "Optional task status", false)}, nil)},
		"/api/v1/async-tasks/{id}":        map[string]any{"get": apiOperation("getAsyncTask", "Read one background task", []any{pathParameter("id", "Task ID")}, nil)},
		"/api/v1/async-tasks/{id}/cancel": map[string]any{"post": apiOperation("cancelAsyncTask", "Cancel pending or running work", []any{pathParameter("id", "Task ID")}, nil)},
		"/api/v1/async-tasks/{id}/retry":  map[string]any{"post": apiOperation("retryAsyncTask", "Create a reviewed retry from failed or cancelled work", []any{pathParameter("id", "Task ID")}, nil)},
		"/api/v1/async-tasks/{id}/runs":   map[string]any{"get": apiOperation("listTaskRuns", "List durable execution attempts and logs", []any{pathParameter("id", "Task ID")}, nil)},
		"/api/v1/settings": map[string]any{
			"get":  apiOperation("getSettings", "Read redacted settings and live dependency health", nil, nil),
			"post": apiOperation("updateSettings", "Atomically update validated settings", nil, map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}),
		},
		"/api/v1/settings/check": map[string]any{"post": apiOperation("checkSettings", "Check one configured dependency", nil, schemaObject(nil, map[string]any{"component": map[string]any{"type": "string", "enum": []string{"c115", "emby", "clouddrive", "resource"}}}))},
		"/api/v1/tokens": map[string]any{
			"get": apiOperation("listAgentTokens", "List Agent tokens without secret values", nil, nil),
			"post": apiOperation("createAgentToken", "Create an Agent token and return its secret once", nil, schemaObject([]string{"name", "permissions"}, map[string]any{
				"name": stringSchema("Agent name"), "permissions": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"read", "write"}}},
				"rate_limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 600, "default": 20},
			})),
		},
		"/api/v1/tokens/{id}": map[string]any{"delete": apiOperation("revokeAgentToken", "Revoke an Agent token", []any{pathParameter("id", "Token ID")}, nil)},
		"/api/v1/audit-logs":  map[string]any{"get": apiOperation("listAgentAudits", "List bounded REST and MCP audit records", []any{integerQueryParameter("limit", "Maximum 100 records")}, nil)},
	}
	return map[string]any{
		"openapi": "3.1.0",
		"info":    map[string]any{"title": "EmbyMedia V2 API", "version": "2.0.0", "description": "Standalone 115, Emby, CloudDrive2, task, settings and Agent operations", "license": map[string]any{"name": "MIT", "identifier": "MIT"}},
		"servers": []any{map[string]any{"url": "/", "description": "Current EmbyMedia server"}},
		"components": map[string]any{"securitySchemes": map[string]any{
			"AgentToken":    map[string]any{"type": "apiKey", "in": "header", "name": "X-Agent-Token", "description": "Agent token issued by the administrator"},
			"WebhookSecret": map[string]any{"type": "apiKey", "in": "header", "name": "X-Webhook-Secret", "description": "CloudDrive2 webhook secret"},
		}},
		"paths": paths,
	}
}
