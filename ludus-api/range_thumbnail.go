package ludusapi

import (
	"fmt"
	"net/url"

	"ludusapi/models"
)

func rangeThumbnailURL(rangeRecord *models.Range) string {
	if rangeRecord == nil {
		return ""
	}

	thumbnail := rangeRecord.GetString("thumbnail")
	if thumbnail == "" {
		return ""
	}

	return fmt.Sprintf("/api/files/%s/%s/%s", rangeRecord.CollectionName(), rangeRecord.Id, url.PathEscape(thumbnail))
}
