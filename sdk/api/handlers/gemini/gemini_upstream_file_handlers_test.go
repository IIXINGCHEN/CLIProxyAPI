package gemini

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
)

func TestGeminiUpstreamFileHandler_WriteProxyError_StructuredJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewGeminiUpstreamFileAPIHandler(&handlers.BaseAPIHandler{})
	r.POST("/upload/v1beta/files", h.UploadFile)

	req := httptest.NewRequest(http.MethodPost, "/upload/v1beta/files", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v body=%s", err, rec.Body.String())
	}
	raw, ok := body["error"]
	if !ok {
		t.Fatalf("expected response.error, got %v", body)
	}
	if _, ok := raw.(map[string]any); !ok {
		t.Fatalf("expected response.error to be object, got %T", raw)
	}
}
