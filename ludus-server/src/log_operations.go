package ludusapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// GetLogsFromFile - retrieves the logs as a string from a provided file
// and respects the tail or cursor query strings present in the Gin context
func GetLogsFromFile(c *gin.Context, filePath string) {
	logs, err := GetFileContents(filePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	returnCursor, err := lineCounter(filePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	tailLine, tailInQueryString := c.GetQuery("tail")
	cursor, cursorInQueryString := c.GetQuery("cursor")
	if (!tailInQueryString || tailLine == "") && (!cursorInQueryString || cursor == "") {
		// Return the whole log
		c.JSON(http.StatusOK, gin.H{"result": logs, "cursor": returnCursor})
	} else if tailInQueryString && tailLine != "" {
		// Truncate to the specified number of lines from the end of the file
		tailLineInt, err := strconv.Atoi(tailLine)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"error": "Invalid number provided to tail: " + err.Error()})
		}
		lines := strings.Split(logs, "\n")
		if len(lines) > tailLineInt {
			logs = strings.Join(lines[len(lines)-tailLineInt:], "\n")
		}
		c.JSON(http.StatusOK, gin.H{"result": logs, "cursor": returnCursor})
	} else if cursorInQueryString && cursor != "" {
		// Return all lines after the cursor
		cursorInt, err := strconv.Atoi(cursor)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"error": "Invalid number provided to cursor: " + err.Error()})
		}
		lines := strings.Split(logs, "\n")
		if len(lines) > cursorInt {
			logs = strings.Join(lines[cursorInt:], "\n")
		}
		c.JSON(http.StatusOK, gin.H{"result": logs, "cursor": returnCursor})
	}
}
