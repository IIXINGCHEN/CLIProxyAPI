package executor

import (
	"net/http"
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

func TestBuildGeminiFilesRequest_UsesUploadURLOverride(t *testing.T) {
	exec := &GeminiExecutor{}
	auth := &cliproxyauth.Auth{Attributes: map[string]string{"base_url": "https://generativelanguage.googleapis.com"}}
	opts := cliproxyexecutor.Options{Metadata: map[string]any{cliproxyexecutor.GeminiFilesUploadURLMetadataKey: "https://u.example/upload?v=1"}}
	method, endpoint, _, err := exec.buildGeminiFilesRequest("files.upload", auth, cliproxyexecutor.Request{}, opts)
	if err != nil || method != http.MethodPost || endpoint != "https://u.example/upload?v=1" {
		t.Fatalf("unexpected: method=%q endpoint=%q err=%v", method, endpoint, err)
	}
}
