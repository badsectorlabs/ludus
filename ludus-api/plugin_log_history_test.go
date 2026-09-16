package ludusapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ludusapi/dto"
	"ludusapi/models"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

func TestPluginRangeLogHistoryArchivesEachRun(t *testing.T) {
	t.Setenv("LUDUS_INSTALL_PATH", t.TempDir())
	previousLogger, previousConfig := logger, ServerConfiguration
	logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	ServerConfiguration.MaxLogHistory = 2
	t.Cleanup(func() { logger, ServerConfiguration = previousLogger, previousConfig })

	pb := pocketbase.NewWithConfig(pocketbase.Config{DefaultDataDir: t.TempDir(), HideStartBanner: true})
	if err := pb.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pb.ResetBootstrapState() })
	if err := pb.RunAllMigrations(); err != nil {
		t.Fatal(err)
	}
	newRecord := func(collection string, fields map[string]any) *core.Record {
		t.Helper()
		c, err := pb.FindCollectionByNameOrId(collection)
		if err != nil {
			t.Fatal(err)
		}
		r := core.NewRecord(c)
		for key, value := range fields {
			r.Set(key, value)
		}
		if err := pb.Save(r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	user := newRecord("users", map[string]any{"email": "logs@example.com", "password": "log-history-password", "userID": "CIA", "userNumber": 1})
	rangeRecord := newRecord("ranges", map[string]any{"rangeID": "CIA/subrange", "rangeNumber": 1, "name": "Plugin logs", "rangeState": "SUCCESS"})
	targetRange := &models.Range{}
	targetRange.SetProxyRecord(rangeRecord)
	superuser := newRecord(core.CollectionNameSuperusers, map[string]any{"email": "plugin@example.com", "password": "plugin-history-password"})
	token, err := superuser.NewAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	installPath := t.TempDir()
	logPath := filepath.Join(installPath, "ranges", targetRange.RangeId(), "ansible.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0700); err != nil {
		t.Fatal(err)
	}
	pbRouter, err := apis.NewRouter(pb)
	if err != nil {
		t.Fatal(err)
	}
	registerPluginLogHistoryRoutes(&core.ServeEvent{App: pb, Router: pbRouter}, installPath)
	group := pbRouter.Group(APIBasePath + "/range/logs/history")
	group.BindFunc(func(e *core.RequestEvent) error {
		e.Set("range", targetRange)
		return e.Next()
	})
	group.GET("", GetRangeLogHistory)
	group.GET("/{logID}", GetRangeLogHistoryByID)
	mux, err := pbRouter.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(mux)
	defer api.Close()
	client, err := NewPocketBaseClient(PocketBaseConnection{URL: api.URL, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx := context.Background()
	writeLog := func(content string) {
		t.Helper()
		if err := os.WriteFile(logPath, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	start := func() string {
		t.Helper()
		id, err := client.startRangeLogHistory(ctx, user.Id, targetRange.Id)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { runningLogFilePathByLogID.Delete(id); runningRangeLogIDByRangeID.Delete(targetRange.Id) })
		return id
	}
	assertLog := func(id, status, content string) {
		t.Helper()
		var detail dto.LogHistoryDetailResponse
		if err := client.requestJSON(ctx, http.MethodGet, APIBasePath+"/range/logs/history/"+id, nil, nil, &detail); err != nil {
			t.Fatal(err)
		}
		if detail.Status != status || detail.Result != content {
			t.Fatalf("log %s: status=%q content=%q; want status=%q content=%q", id, detail.Status, detail.Result, status, content)
		}
	}

	failed := start()
	writeLog("VM one: WinRM timeout\n")
	assertLog(failed, "running", "VM one: WinRM timeout\n")
	if err := client.finishRangeLogHistory(ctx, failed, "failure"); err != nil {
		t.Fatal(err)
	}
	succeeded := start()
	writeLog("VM two: anti-sandbox applied\n")
	if err := client.finishRangeLogHistory(ctx, succeeded, "success"); err != nil {
		t.Fatal(err)
	}
	assertLog(failed, "failure", "VM one: WinRM timeout\n")
	assertLog(succeeded, "success", "VM two: anti-sandbox applied\n")
	var history []dto.LogHistoryEntry
	if err := client.requestJSON(ctx, http.MethodGet, APIBasePath+"/range/logs/history", nil, nil, &history); err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("history contains %d runs, want two separate VM runs", len(history))
	}

	aborted := start()
	writeLog("VM three: interrupted\n")
	if err := finalizeRunningLogHistoryByID(pb, aborted, "aborted", logPath, time.Now()); err != nil {
		t.Fatal(err)
	}
	writeLog("a later operation overwrote the current log\n")
	if err := client.finishRangeLogHistory(ctx, aborted, "failure"); err != nil {
		t.Fatal(err)
	}
	assertLog(aborted, "aborted", "VM three: interrupted\n")
	if err := client.requestJSON(ctx, http.MethodGet, APIBasePath+"/range/logs/history", nil, nil, &history); err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Id == failed || history[1].Id == failed {
		t.Fatalf("retention did not remove oldest run: %+v", history)
	}
	currentRange, err := pb.FindRecordById("ranges", targetRange.Id)
	if err != nil || currentRange.GetString("rangeState") != "SUCCESS" {
		t.Fatalf("plugin log history changed range deployment state: %v, %v", currentRange, err)
	}

	// A regular API restart reconciles the shared row while an admin-host
	// plugin can still be running. Its final output must still be archived.
	reconciled := start()
	writeLog("VM four: completed after sibling host restart\n")
	if err := startupReconcileRunningLogHistory(pb); err != nil {
		t.Fatal(err)
	}
	if err := client.finishRangeLogHistory(ctx, reconciled, "success"); err != nil {
		t.Fatal(err)
	}
	assertLog(reconciled, "aborted", "VM four: completed after sibling host restart\n")

	userToken, err := user.NewAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, address, token string }{
		{"unauthenticated", "127.0.0.1:1234", ""},
		{"ordinary user", "127.0.0.1:1234", userToken},
		{"remote superuser", "192.0.2.1:1234", token},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, _ := json.Marshal(pluginRangeLogStart{UserID: user.Id, RangeID: targetRange.Id})
			req := httptest.NewRequest(http.MethodPost, pluginRangeLogsPath, bytes.NewReader(body))
			req.RemoteAddr = test.address
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", test.token)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, req)
			if response.Code != http.StatusForbidden {
				t.Fatalf("unauthorized log creation: status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
