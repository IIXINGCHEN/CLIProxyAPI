package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	configaccess "github.com/router-for-me/CLIProxyAPI/v6/internal/access/config_access"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v6/sdk/access"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func TestAuthMiddleware_ErrorShape_IsObject(t *testing.T) {
	configaccess.Register()
	gin.SetMode(gin.TestMode)

	root := &config.SDKConfig{
		APIKeys: []string{"k1"},
	}
	providers, err := sdkaccess.BuildProviders(root)
	if err != nil {
		t.Fatalf("BuildProviders error: %v", err)
	}
	manager := sdkaccess.NewManager()
	manager.SetProviders(providers)

	r := gin.New()
	r.Use(AuthMiddleware(manager))
	r.GET("/v1beta/files", func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/v1beta/files", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v body=%s", err, rec.Body.String())
	}
	rawErr, ok := body["error"]
	if !ok {
		t.Fatalf("expected response.error, got %v", body)
	}
	errObj, ok := rawErr.(map[string]any)
	if !ok {
		t.Fatalf("expected response.error to be object, got %T", rawErr)
	}
	if msg, _ := errObj["message"].(string); msg == "" {
		t.Fatalf("expected response.error.message to be non-empty, got %v", errObj)
	}
}
