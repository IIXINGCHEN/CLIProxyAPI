package gemini

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestShouldTreatAsResumableUpload_ProtocolResumable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/upload/v1beta/files", nil)
	req.Header.Set("X-Goog-Upload-Protocol", "resumable")
	c.Request = req

	if !shouldTreatAsResumableUpload(c) {
		t.Fatalf("expected resumable=true")
	}
}

func TestShouldTreatAsResumableUpload_CommandHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/upload/v1beta/files", nil)
	req.Header.Set("X-Goog-Upload-Command", "upload, finalize")
	c.Request = req

	if !shouldTreatAsResumableUpload(c) {
		t.Fatalf("expected resumable=true")
	}
}

func TestShouldTreatAsResumableUpload_QueryUploadID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/upload/v1beta/files?upload_id=abc", nil)
	c.Request = req

	if !shouldTreatAsResumableUpload(c) {
		t.Fatalf("expected resumable=true")
	}
}

func TestShouldTreatAsResumableUpload_DefaultFalse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/upload/v1beta/files", nil)
	c.Request = req

	if shouldTreatAsResumableUpload(c) {
		t.Fatalf("expected resumable=false")
	}
}
