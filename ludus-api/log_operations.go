package ludusapi

import (
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// GetLogsFromFile - retrieves the logs as a string from a provided file
// and respects the tail or cursor query strings present in the Gin context
func GetLogsFromFile(e *core.RequestEvent, filePath string) error {
	logs, err := GetFileContents(filePath)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}

	returnCursor, err := lineCounter(filePath)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}

	tailLine := e.Request.URL.Query().Get("tail")
	cursor := e.Request.URL.Query().Get("cursor")
	if tailLine == "" && cursor == "" {
		// Return the whole log
		return e.JSON(http.StatusOK, map[string]any{"result": logs, "cursor": returnCursor})
	} else if tailLine != "" {
		// Truncate to the specified number of lines from the end of the file
		tailLineInt, err := strconv.Atoi(tailLine)
		if err != nil {
			return JSONError(e, http.StatusBadRequest, "Invalid number provided to tail: "+err.Error())
		}
		lines := strings.Split(logs, "\n")
		if len(lines) > tailLineInt {
			logs = strings.Join(lines[len(lines)-tailLineInt:], "\n")
		}
		return e.JSON(http.StatusOK, map[string]any{"result": logs, "cursor": returnCursor})
	} else if cursor != "" {
		// Return all lines after the cursor
		cursorInt, err := strconv.Atoi(cursor)
		if err != nil {
			return JSONError(e, http.StatusBadRequest, "Invalid number provided to cursor: "+err.Error())
		}
		lines := strings.Split(logs, "\n")
		if len(lines) > cursorInt {
			logs = strings.Join(lines[cursorInt:], "\n")
		}
		return e.JSON(http.StatusOK, map[string]any{"result": logs, "cursor": returnCursor})
	}
	return nil
}

// LogToFile - writes the provided log string to the provided file (truncating it first)
func logToFile(logFilePath string, log string, append bool) {
	var logFile *os.File
	if append {
		logFile, _ = os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0660)
	} else {
		logFile, _ = os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0660)
	}
	defer logFile.Close()
	logFile.WriteString(log)
}
