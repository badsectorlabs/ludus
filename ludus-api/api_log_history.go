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
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
)

func maxLogHistoryEntries() int {
	if ServerConfiguration.MaxLogHistory > 0 {
		return ServerConfiguration.MaxLogHistory
	}
	return 10
}

// saveLogHistory persists a completed log file to the logs PocketBase collection.
// rangeID and userID are PocketBase record IDs. rangeID may be empty for template logs.
func saveLogHistory(app core.App, userID string, rangeID string, status string, logFilePath string, startTime time.Time) {
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
func logHistoryEntryFromRecord(record *core.Record) dto.LogHistoryEntry {
	return dto.LogHistoryEntry{
		Id:      record.Id,
		Status:  record.GetString("status"),
		Start:   record.GetDateTime("start").Time(),
		End:     record.GetDateTime("end").Time(),
		Created: record.GetDateTime("created").Time(),
	}
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
		entries = append(entries, logHistoryEntryFromRecord(record))
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
	if err != nil {
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
		entries = append(entries, logHistoryEntryFromRecord(record))
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
