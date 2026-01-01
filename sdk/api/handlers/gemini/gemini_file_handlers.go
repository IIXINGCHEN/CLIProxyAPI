// Package gemini provides HTTP handlers for Gemini File API endpoints.
package gemini

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/filestore"
	log "github.com/sirupsen/logrus"
)

const (
	// MaxMultipartFileSize limits multipart uploads to 2GB (Gemini's limit)
	MaxMultipartFileSize = 2 * 1024 * 1024 * 1024 // 2GB
)

// GeminiFileAPIHandler handles Gemini File API operations
type GeminiFileAPIHandler struct {
	fileStore *filestore.GeminiFileStore
}

func (h *GeminiFileAPIHandler) LocalFileStore() *filestore.GeminiFileStore {
	if h == nil {
		return nil
	}
	return h.fileStore
}

// NewGeminiFileAPIHandler creates a new file API handler
func NewGeminiFileAPIHandler(store *filestore.GeminiFileStore) *GeminiFileAPIHandler {
	return &GeminiFileAPIHandler{
		fileStore: store,
	}
}

// UploadFile handles file upload requests (both multipart and resumable)
// POST /upload/v1beta/files
func (h *GeminiFileAPIHandler) UploadFile(c *gin.Context) {
	// Check upload type from header
	uploadType := c.GetHeader("X-Goog-Upload-Protocol")

	switch uploadType {
	case "resumable":
		h.handleResumableUpload(c)
	case "multipart", "":
		h.handleMultipartUpload(c)
	default:
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": fmt.Sprintf("unsupported upload protocol: %s", uploadType),
				"status":  "INVALID_ARGUMENT",
			},
		})
	}
}

// handleMultipartUpload processes standard multipart file uploads
func (h *GeminiFileAPIHandler) handleMultipartUpload(c *gin.Context) {
	// Parse multipart form (limit to 2GB)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxMultipartFileSize)
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil { // 32MB in memory
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": fmt.Sprintf("failed to parse multipart form: %v", err),
				"status":  "INVALID_ARGUMENT",
			},
		})
		return
	}

	// Get file from form
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": "missing or invalid 'file' field in multipart form",
				"status":  "INVALID_ARGUMENT",
			},
		})
		return
	}
	defer file.Close()

	// Get optional display name
	displayName := c.Request.FormValue("display_name")
	if displayName == "" {
		displayName = header.Filename
	}

	// Detect MIME type
	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = detectMimeType(header.Filename)
	}

	// Extract API key from context or header
	apiKey := extractAPIKey(c)

	// Save file
	metadata, err := h.fileStore.SaveFile(c.Request.Context(), file, header.Filename, mimeType, displayName, apiKey)
	if err != nil {
		log.WithError(err).Error("gemini file upload: failed to save file")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": "failed to save file",
				"status":  "INTERNAL",
			},
		})
		return
	}

	// Return Gemini-compatible response
	c.JSON(http.StatusOK, gin.H{
		"file": gin.H{
			"name":           metadata.URI,
			"displayName":    metadata.DisplayName,
			"mimeType":       metadata.MimeType,
			"sizeBytes":      strconv.FormatInt(metadata.SizeBytes, 10),
			"createTime":     metadata.CreatedAt.Format("2006-01-02T15:04:05.000000Z"),
			"updateTime":     metadata.CreatedAt.Format("2006-01-02T15:04:05.000000Z"),
			"expirationTime": metadata.ExpiresAt.Format("2006-01-02T15:04:05.000000Z"),
			"sha256Hash":     metadata.SHA256Hash,
			"uri":            metadata.URI,
			"state":          metadata.State,
		},
	})
}

// handleResumableUpload processes resumable upload protocol
func (h *GeminiFileAPIHandler) handleResumableUpload(c *gin.Context) {
	command := c.GetHeader("X-Goog-Upload-Command")

	switch command {
	case "start":
		h.handleResumableStart(c)
	case "upload, finalize":
		h.handleResumableFinalize(c)
	case "query":
		h.handleResumableQuery(c)
	default:
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": fmt.Sprintf("unsupported resumable command: %s", command),
				"status":  "INVALID_ARGUMENT",
			},
		})
	}
}

// handleResumableStart initiates a resumable upload session
func (h *GeminiFileAPIHandler) handleResumableStart(c *gin.Context) {
	// Read metadata from request body
	var metadata struct {
		File struct {
			DisplayName string `json:"displayName"`
		} `json:"file"`
	}

	if err := c.ShouldBindJSON(&metadata); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": fmt.Sprintf("invalid metadata: %v", err),
				"status":  "INVALID_ARGUMENT",
			},
		})
		return
	}

	// Generate upload session ID
	sessionID := filestore.GenerateFileID()

	// Return session URL
	uploadURL := fmt.Sprintf("/upload/v1beta/files?upload_id=%s", sessionID)

	c.Header("X-Goog-Upload-URL", uploadURL)
	c.Header("X-Goog-Upload-Status", "active")
	c.Header("X-Goog-Upload-Chunk-Granularity", "262144") // 256KB chunks

	c.Status(http.StatusOK)
}

// handleResumableFinalize completes a resumable upload
func (h *GeminiFileAPIHandler) handleResumableFinalize(c *gin.Context) {
	uploadID := c.Query("upload_id")
	if uploadID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": "missing upload_id parameter",
				"status":  "INVALID_ARGUMENT",
			},
		})
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxMultipartFileSize)

	// Get content type and size
	contentType := c.GetHeader("X-Goog-Upload-Header-Content-Type")
	if contentType == "" {
		contentType = c.ContentType()
	}

	// Get display name from initial metadata or generate one
	displayName := fmt.Sprintf("upload-%s", uploadID[:8])

	// Extract API key
	apiKey := extractAPIKey(c)

	// Read file content
	body := c.Request.Body
	defer body.Close()

	// Save file
	metadata, err := h.fileStore.SaveFile(c.Request.Context(), body, uploadID, contentType, displayName, apiKey)
	if err != nil {
		log.WithError(err).Error("gemini file upload: failed to save resumable file")
		c.Header("X-Goog-Upload-Status", "failed")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": "failed to save file",
				"status":  "INTERNAL",
			},
		})
		return
	}

	// Return success
	c.Header("X-Goog-Upload-Status", "final")
	c.JSON(http.StatusOK, gin.H{
		"file": gin.H{
			"name":           metadata.URI,
			"displayName":    metadata.DisplayName,
			"mimeType":       metadata.MimeType,
			"sizeBytes":      strconv.FormatInt(metadata.SizeBytes, 10),
			"createTime":     metadata.CreatedAt.Format("2006-01-02T15:04:05.000000Z"),
			"updateTime":     metadata.CreatedAt.Format("2006-01-02T15:04:05.000000Z"),
			"expirationTime": metadata.ExpiresAt.Format("2006-01-02T15:04:05.000000Z"),
			"sha256Hash":     metadata.SHA256Hash,
			"uri":            metadata.URI,
			"state":          metadata.State,
		},
	})
}

// handleResumableQuery checks the status of a resumable upload
func (h *GeminiFileAPIHandler) handleResumableQuery(c *gin.Context) {
	uploadID := c.Query("upload_id")
	if uploadID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": "missing upload_id parameter",
				"status":  "INVALID_ARGUMENT",
			},
		})
		return
	}

	c.Header("X-Goog-Upload-Status", "active")
	c.Header("X-Goog-Upload-Size-Received", "0")

	c.Status(http.StatusOK)
}

// GetFile retrieves file metadata
// GET /v1beta/files/{fileId}
func (h *GeminiFileAPIHandler) GetFile(c *gin.Context) {
	fileID := c.Param("fileId")
	if fileID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": "missing fileId parameter",
				"status":  "INVALID_ARGUMENT",
			},
		})
		return
	}

	// Remove "files/" prefix if present
	fileID = strings.TrimPrefix(fileID, "files/")

	apiKey := extractAPIKey(c)
	metadata, err := h.fileStore.GetFileForAPIKey(c.Request.Context(), fileID, apiKey)
	if err != nil {
		if !errors.Is(err, filestore.ErrFileNotFound) {
			log.WithError(err).Errorf("gemini file API: failed to get file %s", fileID)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": gin.H{
					"message": "failed to get file",
					"status":  "INTERNAL",
				},
			})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"message": fmt.Sprintf("file not found: %s", fileID),
				"status":  "NOT_FOUND",
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"name":           metadata.URI,
		"displayName":    metadata.DisplayName,
		"mimeType":       metadata.MimeType,
		"sizeBytes":      strconv.FormatInt(metadata.SizeBytes, 10),
		"createTime":     metadata.CreatedAt.Format("2006-01-02T15:04:05.000000Z"),
		"updateTime":     metadata.CreatedAt.Format("2006-01-02T15:04:05.000000Z"),
		"expirationTime": metadata.ExpiresAt.Format("2006-01-02T15:04:05.000000Z"),
		"sha256Hash":     metadata.SHA256Hash,
		"uri":            metadata.URI,
		"state":          metadata.State,
	})
}

// ListFiles lists all available files
// GET /v1beta/files
func (h *GeminiFileAPIHandler) ListFiles(c *gin.Context) {
	apiKey := extractAPIKey(c)
	files, err := h.fileStore.ListFilesForAPIKey(c.Request.Context(), apiKey)
	if err != nil {
		log.WithError(err).Error("gemini file API: failed to list files")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": "failed to list files",
				"status":  "INTERNAL",
			},
		})
		return
	}

	fileList := make([]gin.H, 0, len(files))
	for _, metadata := range files {
		fileList = append(fileList, gin.H{
			"name":           metadata.URI,
			"displayName":    metadata.DisplayName,
			"mimeType":       metadata.MimeType,
			"sizeBytes":      strconv.FormatInt(metadata.SizeBytes, 10),
			"createTime":     metadata.CreatedAt.Format("2006-01-02T15:04:05.000000Z"),
			"updateTime":     metadata.CreatedAt.Format("2006-01-02T15:04:05.000000Z"),
			"expirationTime": metadata.ExpiresAt.Format("2006-01-02T15:04:05.000000Z"),
			"sha256Hash":     metadata.SHA256Hash,
			"uri":            metadata.URI,
			"state":          metadata.State,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"files": fileList,
	})
}

// DeleteFile deletes a file
// DELETE /v1beta/files/{fileId}
func (h *GeminiFileAPIHandler) DeleteFile(c *gin.Context) {
	fileID := c.Param("fileId")
	if fileID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": "missing fileId parameter",
				"status":  "INVALID_ARGUMENT",
			},
		})
		return
	}

	// Remove "files/" prefix if present
	fileID = strings.TrimPrefix(fileID, "files/")

	apiKey := extractAPIKey(c)
	if err := h.fileStore.DeleteFileForAPIKey(c.Request.Context(), fileID, apiKey); err != nil {
		if errors.Is(err, filestore.ErrFileNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{
					"message": fmt.Sprintf("file not found: %s", fileID),
					"status":  "NOT_FOUND",
				},
			})
			return
		}
		log.WithError(err).Errorf("gemini file API: failed to delete file %s", fileID)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": "failed to delete file",
				"status":  "INTERNAL",
			},
		})
		return
	}

	c.Status(http.StatusNoContent)
}

// detectMimeType attempts to detect MIME type from file extension
func detectMimeType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".mp4":
		return "video/mp4"
	case ".mpeg", ".mpg":
		return "video/mpeg"
	case ".mov":
		return "video/quicktime"
	case ".avi":
		return "video/x-msvideo"
	case ".webm":
		return "video/webm"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".aac":
		return "audio/aac"
	case ".pdf":
		return "application/pdf"
	case ".txt":
		return "text/plain"
	case ".html":
		return "text/html"
	case ".json":
		return "application/json"
	case ".xml":
		return "application/xml"
	default:
		return "application/octet-stream"
	}
}

// extractAPIKey extracts API key from gin context or headers
func extractAPIKey(c *gin.Context) string {
	// Try gin context first (set by auth middleware)
	if apiKey, exists := c.Get("apiKey"); exists {
		if keyStr, ok := apiKey.(string); ok {
			return keyStr
		}
	}

	// Try Authorization header
	authHeader := c.GetHeader("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimPrefix(authHeader, "Bearer ")
	}

	// Try X-Goog-Api-Key header
	if key := c.GetHeader("X-Goog-Api-Key"); key != "" {
		return key
	}

	// Try query parameter
	if key := c.Query("key"); key != "" {
		return key
	}

	return ""
}
