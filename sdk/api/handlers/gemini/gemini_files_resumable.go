package gemini

import (
	"strings"

	"github.com/gin-gonic/gin"
)

func shouldTreatAsResumableUpload(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(c.GetHeader("X-Goog-Upload-Protocol")), "resumable") {
		return true
	}
	if strings.TrimSpace(c.GetHeader("X-Goog-Upload-Command")) != "" {
		return true
	}
	if strings.TrimSpace(c.Query("upload_id")) != "" {
		return true
	}
	return false
}
