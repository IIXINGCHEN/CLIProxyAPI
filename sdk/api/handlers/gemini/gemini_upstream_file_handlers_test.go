package gemini

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestGeminiUpstreamFileHandler_NormalizeOfficialErrorPayload_FillsMessageAndStatus(t *testing.T) {
	payload := []byte(`{"error":{"message":"","status":"Bad Gateway"},"message":"upstream failed"}`)
	out := normalizeOfficialErrorPayload(payload, http.StatusBadGateway)

	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("unmarshal normalized: %v payload=%s", err, string(out))
	}
	errObj, ok := obj["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected normalized.error to be object, got %T", obj["error"])
	}

	msg, _ := errObj["message"].(string)
	if msg != "upstream failed" {
		t.Fatalf("expected error.message to be filled, got %q", msg)
	}

	st, _ := errObj["status"].(string)
	if st != "UNAVAILABLE" {
		t.Fatalf("expected error.status to be google status, got %q", st)
	}
}

func TestGeminiUpstreamFileHandler_BuildProxyUploadURL_IncludesAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/upload/v1beta/files", nil)
	req.Header.Set("X-Goog-Api-Key", "key-a")
	req.Host = "127.0.0.1:8317"
	c.Request = req

	url := buildProxyUploadURL(c, "upload-123")
	if !strings.Contains(url, "upload_id=upload-123") {
		t.Fatalf("expected upload_id in url, got %q", url)
	}
	if !strings.Contains(url, "key=key-a") {
		t.Fatalf("expected key in url, got %q", url)
	}
}
