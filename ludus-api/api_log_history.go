package ludusapi

import (
	"fmt"
	"io"
	"ludusapi/dto"
	"ludusapi/models"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
)

var runningLogFilePathByLogID sync.Map
var runningRangeLogIDByRangeID sync.Map
var runningTemplateLogIDByUserAndName sync.Map

func maxLogHistoryEntries() int {
	if ServerConfiguration.MaxLogHistory > 0 {
		return ServerConfiguration.MaxLogHistory
	}
	return 10
}

func templateLogMapKey(userID string, templateName string) string {
	return fmt.Sprintf("%s:%s", userID, templateName)
}

func createRunningLogHistory(app core.App, userID string, rangeID string, templateName string, logFilePath string, startTime time.Time) string {
	logsCollection, err := app.FindCollectionByNameOrId("logs")
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to find logs collection: %v", err))
		return ""
	}

	record := core.NewRecord(logsCollection)
	record.Set("user", userID)
	if rangeID != "" {
		record.Set("range", rangeID)
	}
	if templateName != "" {
		templateID := findTemplateRecordIDByName(app, templateName)
		if templateID != "" {
			record.Set("template", templateID)
		}
	}
	record.Set("status", "running")
	record.Set("start", startTime)
	record.Set("end", time.Time{})

	if err := app.Save(record); err != nil {
		logger.Error(fmt.Sprintf("Failed to create running log history record: %v", err))
		return ""
	}

	runningLogFilePathByLogID.Store(record.Id, logFilePath)
	if rangeID != "" {
		runningRangeLogIDByRangeID.Store(rangeID, record.Id)
	}
	if templateName != "" {
		runningTemplateLogIDByUserAndName.Store(templateLogMapKey(userID, templateName), record.Id)
	}

	logger.Debug(fmt.Sprintf("Created running log history record %s", record.Id))
	return record.Id
}

func finalizeRunningLogHistoryByID(app core.App, logID string, status string, logFilePath string, endTime time.Time) {
	if logID == "" {
		return
	}

	record, err := app.FindRecordById("logs", logID)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to find running log history record %s: %v", logID, err))
		return
	}
	if record.GetString("status") == "aborted" && status != "aborted" {
		status = "aborted"
	}

	record.Set("status", status)
	record.Set("end", endTime)

	logBytes, readErr := os.ReadFile(logFilePath)
	if readErr == nil && len(logBytes) > 0 {
		baseName := filepath.Base(logFilePath)
		baseNameNoExt := strings.TrimSuffix(baseName, filepath.Ext(baseName))
		logFileName := fmt.Sprintf("%s-%s.log", baseNameNoExt, time.Now().Format("2006-01-02T15-04-05"))
		logFile, fileErr := filesystem.NewFileFromBytes(logBytes, logFileName)
		if fileErr != nil {
			logger.Error(fmt.Sprintf("Failed to create log file for history finalization: %v", fileErr))
		} else {
			record.Set("log", logFile)
		}
	} else if readErr != nil {
		logger.Error(fmt.Sprintf("Failed to read log file for history finalization: %v", readErr))
	}

	if err := app.Save(record); err != nil {
		logger.Error(fmt.Sprintf("Failed to finalize log history record %s: %v", logID, err))
		return
	}
	if logFilePath != "" {
		if err := os.Remove(logFilePath); err != nil && !os.IsNotExist(err) {
			logger.Error(fmt.Sprintf("Failed to remove finalized local log file %s: %v", logFilePath, err))
		}
	}

	runningLogFilePathByLogID.Delete(logID)
	if rangeID := record.GetString("range"); rangeID != "" {
		runningRangeLogIDByRangeID.Delete(rangeID)
		pruneLogHistory(app, "range = {:value}", rangeID)
	} else {
		pruneLogHistory(app, "user = {:value} && range = ''", record.GetString("user"))
	}
}

func finalizeRunningRangeLogHistory(app core.App, rangeID string, status string, logFilePath string, endTime time.Time) {
	logIDRaw, ok := runningRangeLogIDByRangeID.Load(rangeID)
	if !ok {
		return
	}
	logID, ok := logIDRaw.(string)
	if !ok {
		return
	}
	finalizeRunningLogHistoryByID(app, logID, status, logFilePath, endTime)
}

func finalizeRunningTemplateLogHistory(app core.App, userID string, templateName string, status string, logFilePath string, endTime time.Time) {
	logIDRaw, ok := runningTemplateLogIDByUserAndName.Load(templateLogMapKey(userID, templateName))
	if !ok {
		return
	}
	logID, ok := logIDRaw.(string)
	if !ok {
		return
	}
	runningTemplateLogIDByUserAndName.Delete(templateLogMapKey(userID, templateName))
	finalizeRunningLogHistoryByID(app, logID, status, logFilePath, endTime)
}

func getRunningTemplateLogPathByLogID(app core.App, userID string, logID string) (string, bool) {
	if logID == "" {
		return "", false
	}
	record, err := app.FindRecordById("logs", logID)
	if err != nil {
		return "", false
	}
	if record.GetString("user") != userID || record.GetString("range") != "" || record.GetString("status") != "running" {
		return "", false
	}
	logPathRaw, ok := runningLogFilePathByLogID.Load(logID)
	if !ok {
		return "", false
	}
	logPath, ok := logPathRaw.(string)
	return logPath, ok
}

func getRunningTemplateLogPathByUserAndName(userID string, templateName string) (string, bool) {
	logIDRaw, ok := runningTemplateLogIDByUserAndName.Load(templateLogMapKey(userID, templateName))
	if !ok {
		return "", false
	}
	logID, ok := logIDRaw.(string)
	if !ok {
		return "", false
	}
	logPathRaw, ok := runningLogFilePathByLogID.Load(logID)
	if !ok {
		return "", false
	}
	logPath, ok := logPathRaw.(string)
	return logPath, ok
}

func getLatestRunningTemplateLogPath(app core.App, userID string) (string, bool) {
	records, err := app.FindRecordsByFilter(
		"logs",
		"user = {:userID} && range = '' && status = 'running'",
		"-created",
		1, 0,
		dbx.Params{"userID": userID},
	)
	if err != nil || len(records) == 0 {
		return "", false
	}
	logPathRaw, ok := runningLogFilePathByLogID.Load(records[0].Id)
	if !ok {
		return "", false
	}
	logPath, ok := logPathRaw.(string)
	return logPath, ok
}

// saveLogHistory persists a completed log file to the logs PocketBase collection.
// rangeID and userID are PocketBase record IDs. rangeID may be empty for template logs.
// templateName is optional and used to populate the logs.template relation when possible.
func saveLogHistory(app core.App, userID string, rangeID string, templateName string, status string, logFilePath string, startTime time.Time) {
	logBytes, err := os.ReadFile(logFilePath)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to read log file for history: %v", err))
		return
	}

	if len(logBytes) == 0 {
		logger.Debug("Skipping log history save: log file is empty")
		return
	}

	logsCollection, err := app.FindCollectionByNameOrId("logs")
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to find logs collection: %v", err))
		return
	}

	record := core.NewRecord(logsCollection)
	record.Set("user", userID)
	if rangeID != "" {
		record.Set("range", rangeID)
	}
	if templateName != "" {
		templateID := findTemplateRecordIDByName(app, templateName)
		if templateID != "" {
			record.Set("template", templateID)
		}
	}
	record.Set("status", status)
	record.Set("start", startTime)
	record.Set("end", time.Now())

	logFileName := fmt.Sprintf("%s-%s.log", filepath.Base(logFilePath), time.Now().Format("2006-01-02T15-04-05"))
	logFile, err := filesystem.NewFileFromBytes(logBytes, logFileName)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to create log file for history: %v", err))
		return
	}
	record.Set("log", logFile)

	if err := app.Save(record); err != nil {
		logger.Error(fmt.Sprintf("Failed to save log history record: %v", err))
		return
	}

	logger.Debug(fmt.Sprintf("Saved log history record %s", record.Id))

	// Prune old entries
	if rangeID != "" {
		pruneLogHistory(app, "range = {:value}", rangeID)
	} else {
		pruneLogHistory(app, "user = {:value} && range = ''", userID)
	}
}

// findTemplateRecordIDByName returns the first template record ID matching a template name.
// If no record is found (or lookup fails), it returns an empty string.
func findTemplateRecordIDByName(app core.App, templateName string) string {
	records, err := app.FindRecordsByFilter(
		"templates",
		"name = {:name}",
		"",
		1, 0,
		dbx.Params{"name": templateName},
	)
	if err != nil || len(records) == 0 {
		return ""
	}
	return records[0].Id
}

// pruneLogHistory deletes log history entries beyond the max, keeping the most recent.
// filter is a PocketBase filter expression with a {:value} placeholder.
func pruneLogHistory(app core.App, filter string, filterValue string) {
	records, err := app.FindRecordsByFilter("logs",
		filter,
		"-created",
		0, 0,
		dbx.Params{"value": filterValue},
	)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to query logs for pruning: %v", err))
		return
	}

	if len(records) <= maxLogHistoryEntries() {
		return
	}

	// Records are already sorted by created desc from FindRecordsByFilter
	for _, record := range records[maxLogHistoryEntries():] {
		if err := app.Delete(record); err != nil {
			logger.Error(fmt.Sprintf("Failed to delete old log history record %s: %v", record.Id, err))
		}
	}
}

// logHistoryEntryFromRecord converts a PocketBase record to a LogHistoryEntry DTO.
func logHistoryEntryFromRecord(record *core.Record, templateName string) dto.LogHistoryEntry {
	return dto.LogHistoryEntry{
		Id:       record.Id,
		Template: templateName,
		Status:   record.GetString("status"),
		Start:    record.GetDateTime("start").Time(),
		End:      record.GetDateTime("end").Time(),
		Created:  record.GetDateTime("created").Time(),
	}
}

func getTemplateNameFromLogRecord(app core.App, record *core.Record) string {
	templateID := record.GetString("template")
	if templateID == "" {
		return ""
	}
	templateRecord, err := app.FindRecordById("templates", templateID)
	if err != nil {
		return ""
	}
	return templateRecord.GetString("name")
}

// logHistoryDetailFromRecord converts a PocketBase record and log content to a LogHistoryDetailResponse DTO.
func logHistoryDetailFromRecord(record *core.Record, logContent []byte) dto.LogHistoryDetailResponse {
	return dto.LogHistoryDetailResponse{
		Id:      record.Id,
		Status:  record.GetString("status"),
		Start:   record.GetDateTime("start").Time(),
		End:     record.GetDateTime("end").Time(),
		Created: record.GetDateTime("created").Time(),
		Result:  string(logContent),
	}
}

// GetRangeLogHistory lists log history entries for a range.
func GetRangeLogHistory(e *core.RequestEvent) error {
	targetRange, err := GetRange(e)
	if err != nil {
		return err
	}

	records, err := e.App.FindRecordsByFilter("logs",
		"range = {:rangeID}",
		"-created",
		0, 0,
		dbx.Params{"rangeID": targetRange.Id},
	)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error querying log history: %v", err))
	}

	entries := make([]dto.LogHistoryEntry, 0, len(records))
	for _, record := range records {
		entries = append(entries, logHistoryEntryFromRecord(record, ""))
	}

	return e.JSON(http.StatusOK, entries)
}

// GetRangeLogHistoryByID retrieves a specific historical log entry's content.
func GetRangeLogHistoryByID(e *core.RequestEvent) error {
	logID := e.Request.PathValue("logID")
	if logID == "" {
		return JSONError(e, http.StatusBadRequest, "logID not provided")
	}

	record, err := e.App.FindRecordById("logs", logID)
	if err != nil {
		return JSONError(e, http.StatusNotFound, "Log entry not found")
	}

	// Verify the log belongs to the user's range
	targetRange, err := GetRange(e)
	if err != nil {
		return err
	}
	if record.GetString("range") != targetRange.Id {
		return JSONError(e, http.StatusForbidden, "Log entry does not belong to this range")
	}

	logContent, err := readLogFileFromRecord(e.App, record)
	if err != nil && record.GetString("status") == "running" {
		if logPathRaw, ok := runningLogFilePathByLogID.Load(record.Id); ok {
			if logPath, pathOK := logPathRaw.(string); pathOK {
				logContent, err = os.ReadFile(logPath)
			}
		}
	}
	if err != nil {
		if err.Error() == "log file is missing from record" {
			return JSONError(e, http.StatusNotFound, "Log file not found (range deploy possibly aborted before log file created)")
		}
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error reading log file: %v", err))
	}

	return e.JSON(http.StatusOK, logHistoryDetailFromRecord(record, logContent))
}

// GetTemplateLogHistory lists log history entries for a user's template builds.
func GetTemplateLogHistory(e *core.RequestEvent) error {
	user := e.Get("user").(*models.User)

	records, err := e.App.FindRecordsByFilter("logs",
		"user = {:userID} && range = ''",
		"-created",
		0, 0,
		dbx.Params{"userID": user.Id},
	)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error querying log history: %v", err))
	}

	entries := make([]dto.LogHistoryEntry, 0, len(records))
	for _, record := range records {
		entries = append(entries, logHistoryEntryFromRecord(record, getTemplateNameFromLogRecord(e.App, record)))
	}

	return e.JSON(http.StatusOK, entries)
}

// GetTemplateLogHistoryByID retrieves a specific historical template log entry's content.
func GetTemplateLogHistoryByID(e *core.RequestEvent) error {
	logID := e.Request.PathValue("logID")
	if logID == "" {
		return JSONError(e, http.StatusBadRequest, "logID not provided")
	}

	record, err := e.App.FindRecordById("logs", logID)
	if err != nil {
		return JSONError(e, http.StatusNotFound, "Log entry not found")
	}

	// Verify the log belongs to the requesting user
	user := e.Get("user").(*models.User)
	if record.GetString("user") != user.Id {
		return JSONError(e, http.StatusForbidden, "Log entry does not belong to this user")
	}

	logContent, err := readLogFileFromRecord(e.App, record)
	if err != nil && record.GetString("status") == "running" {
		if logPathRaw, ok := runningLogFilePathByLogID.Load(record.Id); ok {
			if logPath, pathOK := logPathRaw.(string); pathOK {
				logContent, err = os.ReadFile(logPath)
			}
		}
	}
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error reading log file: %v", err))
	}

	return e.JSON(http.StatusOK, logHistoryDetailFromRecord(record, logContent))
}

// readLogFileFromRecord reads the log file content from a PocketBase record's file field.
func readLogFileFromRecord(app core.App, record *core.Record) ([]byte, error) {
	logFileName := record.GetString("log")
	if logFileName == "" {
		return nil, fmt.Errorf("log file is missing from record")
	}

	fsClient, err := app.NewFilesystem()
	if err != nil {
		return nil, err
	}
	defer fsClient.Close()

	filePath := path.Join(record.BaseFilesPath(), logFileName)
	fileReader, err := fsClient.GetReader(filePath)
	if err != nil {
		return nil, err
	}
	defer fileReader.Close()

	return io.ReadAll(fileReader)
}
