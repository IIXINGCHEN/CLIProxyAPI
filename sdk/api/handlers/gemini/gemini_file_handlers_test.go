package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/filestore"
)

type listFilesResponse struct {
	Files []fileResource `json:"files"`
}

type fileResource struct {
	Name string `json:"name"`
}

func newTestStore(t *testing.T) (*filestore.GeminiFileStore, string) {
	t.Helper()
	baseDir := t.TempDir()
	store, err := filestore.NewGeminiFileStore(baseDir, 48, 0)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	absDir, err := filepath.Abs(baseDir)
	if err != nil {
		t.Fatalf("abs base dir: %v", err)
	}
	return store, absDir
}

func newTestRouter(store *filestore.GeminiFileStore) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewGeminiFileAPIHandler(store)
	r.GET("/v1beta/files", h.ListFiles)
	r.GET("/v1beta/files/:fileId", h.GetFile)
	r.DELETE("/v1beta/files/:fileId", h.DeleteFile)
	return r
}

func saveTextFile(t *testing.T, store *filestore.GeminiFileStore, apiKey, filename, content string) *filestore.FileMetadata {
	t.Helper()
	metadata, err := store.SaveFile(context.Background(), strings.NewReader(content), filename, "text/plain", filename, apiKey)
	if err != nil {
		t.Fatalf("save file: %v", err)
	}
	return metadata
}

func doRequest(r http.Handler, method, path, apiKey string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func decodeJSON(t *testing.T, body string, out any) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), out); err != nil {
		t.Fatalf("decode json: %v", err)
	}
}

func readMetadata(t *testing.T, baseDir, fileID string) filestore.FileMetadata {
	t.Helper()
	path := filepath.Join(baseDir, "metadata", fileID+filestore.MetadataFileExtension)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	var meta filestore.FileMetadata
	decodeJSON(t, string(data), &meta)
	return meta
}

func writeMetadata(t *testing.T, baseDir, fileID string, meta filestore.FileMetadata) {
	t.Helper()
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	path := filepath.Join(baseDir, "metadata", fileID+filestore.MetadataFileExtension)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
}

func TestGeminiFiles_List_IsolatedByAPIKey(t *testing.T) {
	store, _ := newTestStore(t)
	defer store.Close()
	fileA := saveTextFile(t, store, "key-a", "a.txt", "aaa")
	_ = saveTextFile(t, store, "key-b", "b.txt", "bbb")
	r := newTestRouter(store)
	rec := doRequest(r, http.MethodGet, "/v1beta/files", "key-a")
	var resp listFilesResponse
	decodeJSON(t, rec.Body.String(), &resp)
	if len(resp.Files) != 1 || resp.Files[0].Name != fileA.URI {
		t.Fatalf("expected only %q, got %#v", fileA.URI, resp.Files)
	}
}

func TestGeminiFiles_Get_NotFoundForOtherAPIKey(t *testing.T) {
	store, _ := newTestStore(t)
	defer store.Close()
	fileA := saveTextFile(t, store, "key-a", "a.txt", "aaa")
	r := newTestRouter(store)
	rec := doRequest(r, http.MethodGet, "/v1beta/files/"+fileA.FileID, "key-b")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestGeminiFiles_Delete_NotFoundForOtherAPIKey(t *testing.T) {
	store, _ := newTestStore(t)
	defer store.Close()
	fileA := saveTextFile(t, store, "key-a", "a.txt", "aaa")
	r := newTestRouter(store)
	rec := doRequest(r, http.MethodDelete, "/v1beta/files/"+fileA.FileID, "key-b")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestGeminiFiles_Get_Expired_NotFound(t *testing.T) {
	store, baseDir := newTestStore(t)
	defer store.Close()
	fileA := saveTextFile(t, store, "key-a", "a.txt", "aaa")
	meta := readMetadata(t, baseDir, fileA.FileID)
	meta.ExpiresAt = time.Now().Add(-1 * time.Minute)
	writeMetadata(t, baseDir, fileA.FileID, meta)
	r := newTestRouter(store)
	rec := doRequest(r, http.MethodGet, "/v1beta/files/"+fileA.FileID, "key-a")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestGeminiFiles_Get_InvalidFileID_NotFound(t *testing.T) {
	store, _ := newTestStore(t)
	defer store.Close()
	r := newTestRouter(store)
	rec := doRequest(r, http.MethodGet, "/v1beta/files/..", "key-a")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestGeminiFiles_Delete_InvalidFileID_NotFound(t *testing.T) {
	store, _ := newTestStore(t)
	defer store.Close()
	r := newTestRouter(store)
	rec := doRequest(r, http.MethodDelete, "/v1beta/files/%5c..%5c..%5csecret", "key-a")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestGeminiFiles_UploadResumableStart_ReturnsUploadURLWithKey(t *testing.T) {
	store, _ := newTestStore(t)
	defer store.Close()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewGeminiFileAPIHandler(store)
	r.POST("/upload/v1beta/files", h.UploadFile)

	req := httptest.NewRequest(http.MethodPost, "/upload/v1beta/files", strings.NewReader(`{"file":{"displayName":"x"}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer key-a")
	req.Header.Set("X-Goog-Upload-Protocol", "resumable")
	req.Header.Set("X-Goog-Upload-Command", "start")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	uploadURL := rec.Header().Get("X-Goog-Upload-URL")
	if !strings.Contains(uploadURL, "upload_id=") {
		t.Fatalf("expected upload_id in url, got %q", uploadURL)
	}
	if !strings.Contains(uploadURL, "key=key-a") {
		t.Fatalf("expected key in url, got %q", uploadURL)
	}
}
