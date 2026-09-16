package ludusapi

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

const pluginRangeLogsPath = "/api/plugins/range-logs"

type pluginRangeLogStart struct {
	UserID  string `json:"userID"`
	RangeID string `json:"rangeID"`
}

type pluginRangeLogResult struct {
	ID string `json:"id"`
}

type pluginRangeLogFinish struct {
	Status string `json:"status"`
}

// Plugins send record IDs only. The host resolves the log path and owns both
// the PocketBase file upload and the normal history retention policy.
func registerPluginLogHistoryRoutes(se *core.ServeEvent, installPath string) {
	group := se.Router.Group(pluginRangeLogsPath)
	group.BindFunc(func(e *core.RequestEvent) error {
		host, _, err := net.SplitHostPort(e.Request.RemoteAddr)
		ip := net.ParseIP(host)
		if err != nil || ip == nil || !ip.IsLoopback() || e.Auth == nil || !e.Auth.IsSuperuser() {
			return e.ForbiddenError("Plugin log history requires a loopback superuser connection", nil)
		}
		return e.Next()
	})
	group.POST("", func(e *core.RequestEvent) error {
		var body pluginRangeLogStart
		if err := e.BindBody(&body); err != nil {
			return e.BadRequestError("Invalid log history request", err)
		}
		if _, err := e.App.FindRecordById("users", body.UserID); err != nil {
			return e.BadRequestError("Log user not found", err)
		}
		rangeRecord, err := e.App.FindRecordById("ranges", body.RangeID)
		if err != nil {
			return e.BadRequestError("Log range not found", err)
		}
		rangeName := rangeRecord.GetString("rangeID")
		if !filepath.IsLocal(rangeName) || filepath.Clean(rangeName) != rangeName {
			return e.BadRequestError("Invalid log range directory", nil)
		}
		logPath := filepath.Join(installPath, "ranges", rangeName, "ansible.log")
		id := createRunningLogHistory(e.App, body.UserID, body.RangeID, "", logPath, time.Now())
		if id == "" {
			return e.InternalServerError("Failed to create plugin log history", nil)
		}
		return e.JSON(http.StatusOK, pluginRangeLogResult{ID: id})
	})
	group.POST("/{logID}", func(e *core.RequestEvent) error {
		var body pluginRangeLogFinish
		if err := e.BindBody(&body); err != nil {
			return e.BadRequestError("Invalid log history result", err)
		}
		if body.Status != "success" && body.Status != "failure" {
			return e.BadRequestError("Log status must be success or failure", nil)
		}
		id := e.Request.PathValue("logID")
		record, err := e.App.FindRecordById("logs", id)
		if err != nil || record.GetString("range") == "" {
			return e.NotFoundError("Range log not found", err)
		}
		// An abort may already have archived this run. Never overwrite a
		// completed archive with the next playbook's mutable ansible.log.
		if record.GetString("status") != "running" && record.GetString("log") != "" {
			return e.NoContent(http.StatusNoContent)
		}
		logPath, ok := runningLogFilePathByLogID.Load(id)
		if !ok {
			return e.BadRequestError("Range log is not active in this host", nil)
		}
		if err := finalizeRunningLogHistoryByID(e.App, id, body.Status, logPath.(string), time.Now()); err != nil {
			return e.InternalServerError("Failed to archive plugin log history", err)
		}
		return e.NoContent(http.StatusNoContent)
	})
}

func (c *PocketBaseClient) startRangeLogHistory(ctx context.Context, userID, rangeID string) (string, error) {
	var result pluginRangeLogResult
	if err := c.requestJSON(ctx, http.MethodPost, pluginRangeLogsPath, nil, pluginRangeLogStart{UserID: userID, RangeID: rangeID}, &result); err != nil {
		return "", fmt.Errorf("start plugin log history: %w", err)
	}
	if result.ID == "" {
		return "", fmt.Errorf("host returned an empty plugin log history ID")
	}
	return result.ID, nil
}

func (c *PocketBaseClient) finishRangeLogHistory(ctx context.Context, id, status string) error {
	if err := c.requestJSON(ctx, http.MethodPost, pluginRangeLogsPath+"/"+id, nil, pluginRangeLogFinish{Status: status}, nil); err != nil {
		return fmt.Errorf("archive plugin log history: %w", err)
	}
	return nil
}
