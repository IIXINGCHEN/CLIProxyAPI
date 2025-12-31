// Package filestore provides file caching and management for Gemini File API uploads.
// It handles local file storage, metadata tracking, and automatic cleanup of expired files.
package filestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	// DefaultFileExpirationHours matches Gemini API's 48-hour file retention policy
	DefaultFileExpirationHours = 48
	// MetadataFileExtension for metadata JSON files
	MetadataFileExtension = ".meta.json"
)

var ErrFileNotFound = errors.New("file not found")
var ErrFileTooLarge = errors.New("file too large")

// FileMetadata stores information about an uploaded file
type FileMetadata struct {
	FileID       string    `json:"fileId"`       // Unique identifier for the file
	Name         string    `json:"name"`         // Display name of the file
	DisplayName  string    `json:"displayName"`  // User-provided display name
	MimeType     string    `json:"mimeType"`     // MIME type of the file
	SizeBytes    int64     `json:"sizeBytes"`    // File size in bytes
	SHA256Hash   string    `json:"sha256Hash"`   // SHA256 hash of the file content
	URI          string    `json:"uri"`          // Gemini-compatible file URI (files/{fileId})
	State        string    `json:"state"`        // File state: PROCESSING, ACTIVE, FAILED
	CreatedAt    time.Time `json:"createdAt"`    // Upload timestamp
	ExpiresAt    time.Time `json:"expiresAt"`    // Expiration timestamp
	UploadSource string    `json:"uploadSource"` // Source of upload (local, proxy)
	APIKey       string    `json:"apiKey"`       // Associated API key (hashed for privacy)
}

// GeminiFileStore manages local file storage for Gemini File API
type GeminiFileStore struct {
	baseDir          string
	filesDir         string
	metadataDir      string
	expirationHours  int
	mu               sync.RWMutex
	cleanupTicker    *time.Ticker
	cleanupStop      chan struct{}
	maxTotalSizeMB   int64 // Maximum total storage size in MB (0 = unlimited)
	currentTotalSize int64 // Current total storage size in bytes
}

// NewGeminiFileStore creates a new file store instance
func NewGeminiFileStore(baseDir string, expirationHours int, maxTotalSizeMB int64) (*GeminiFileStore, error) {
	if baseDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get current working directory: %w", err)
		}
		baseDir = filepath.Join(cwd, "gemini-files")
	}

	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve base directory: %w", err)
	}

	if expirationHours <= 0 {
		expirationHours = DefaultFileExpirationHours
	}

	filesDir := filepath.Join(absBase, "files")
	metadataDir := filepath.Join(absBase, "metadata")

	// Create directories
	for _, dir := range []string{filesDir, metadataDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	store := &GeminiFileStore{
		baseDir:         absBase,
		filesDir:        filesDir,
		metadataDir:     metadataDir,
		expirationHours: expirationHours,
		cleanupStop:     make(chan struct{}),
		maxTotalSizeMB:  maxTotalSizeMB,
	}

	// Calculate initial total size
	if err := store.calculateTotalSize(); err != nil {
		log.WithError(err).Warn("gemini file store: failed to calculate initial storage size")
	}

	// Start cleanup goroutine (runs every hour)
	store.startCleanupRoutine()

	log.Infof("gemini file store initialized: %s (expiration: %dh, max size: %dMB)",
		absBase, expirationHours, maxTotalSizeMB)

	return store, nil
}

// SaveFile saves an uploaded file with its metadata
func (s *GeminiFileStore) SaveFile(ctx context.Context, content io.Reader, name, mimeType, displayName, apiKey string) (*FileMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Generate unique file ID
	fileID := generateFileID()

	// Create temporary file for content
	tempPath := filepath.Join(s.filesDir, fileID+".tmp")
	tempFile, err := os.Create(tempPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tempPath)

	// Calculate hash while writing
	hasher := sha256.New()
	writer := io.MultiWriter(tempFile, hasher)

	written, err := io.Copy(writer, content)
	if err != nil {
		tempFile.Close()
		return nil, fmt.Errorf("failed to write file content: %w", err)
	}
	tempFile.Close()

	// Check storage quota
	if s.maxTotalSizeMB > 0 {
		maxBytes := s.maxTotalSizeMB * 1024 * 1024
		if s.currentTotalSize+written > maxBytes {
			return nil, fmt.Errorf("storage quota exceeded: current %d MB, limit %d MB",
				s.currentTotalSize/(1024*1024), s.maxTotalSizeMB)
		}
	}

	// Move to final location
	finalPath := filepath.Join(s.filesDir, fileID)
	if err := os.Rename(tempPath, finalPath); err != nil {
		return nil, fmt.Errorf("failed to finalize file: %w", err)
	}

	// Update total size
	s.currentTotalSize += written

	// Create metadata
	metadata := &FileMetadata{
		FileID:       fileID,
		Name:         name,
		DisplayName:  displayName,
		MimeType:     mimeType,
		SizeBytes:    written,
		SHA256Hash:   hex.EncodeToString(hasher.Sum(nil)),
		URI:          fmt.Sprintf("files/%s", fileID),
		State:        "ACTIVE",
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(time.Duration(s.expirationHours) * time.Hour),
		UploadSource: "local",
		APIKey:       hashAPIKey(apiKey),
	}

	// Save metadata
	if err := s.saveMetadata(metadata); err != nil {
		os.Remove(finalPath) // Cleanup file on metadata save failure
		s.currentTotalSize -= written
		return nil, fmt.Errorf("failed to save metadata: %w", err)
	}

	log.Infof("gemini file store: saved file %s (%d bytes, expires %s)",
		fileID, written, metadata.ExpiresAt.Format(time.RFC3339))

	return metadata, nil
}

// GetFile retrieves file metadata by ID
func (s *GeminiFileStore) GetFile(ctx context.Context, fileID string) (*FileMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.loadMetadata(fileID)
}

func (s *GeminiFileStore) GetFileForAPIKey(ctx context.Context, fileID, apiKey string) (*FileMetadata, error) {
	metadata, err := s.GetFile(ctx, fileID)
	if err != nil {
		if errors.Is(err, ErrFileNotFound) {
			return nil, ErrFileNotFound
		}
		return nil, err
	}
	if !s.isOwnedByAPIKey(metadata, apiKey) || s.isExpired(metadata, time.Now()) {
		return nil, ErrFileNotFound
	}
	return metadata, nil
}

// GetFileContent opens the file content for reading
func (s *GeminiFileStore) GetFileContent(ctx context.Context, fileID string) (io.ReadCloser, *FileMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !isValidFileID(fileID) {
		return nil, nil, ErrFileNotFound
	}

	metadata, err := s.loadMetadata(fileID)
	if err != nil {
		return nil, nil, err
	}

	// Check if file is expired
	if time.Now().After(metadata.ExpiresAt) {
		return nil, nil, ErrFileNotFound
	}

	filePath := filepath.Join(s.filesDir, fileID)
	file, err := os.Open(filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open file: %w", err)
	}

	return file, metadata, nil
}

func (s *GeminiFileStore) GetFileContentForAPIKey(ctx context.Context, fileID, apiKey string) (io.ReadCloser, *FileMetadata, error) {
	metadata, err := s.GetFileForAPIKey(ctx, fileID, apiKey)
	if err != nil {
		return nil, nil, err
	}
	file, _, err := s.GetFileContent(ctx, fileID)
	if err != nil {
		if errors.Is(err, ErrFileNotFound) {
			return nil, nil, ErrFileNotFound
		}
		return nil, nil, err
	}
	return file, metadata, nil
}

func (s *GeminiFileStore) ReadFileBytesForAPIKey(ctx context.Context, fileID, apiKey string, maxBytes int64) ([]byte, *FileMetadata, error) {
	file, metadata, err := s.GetFileContentForAPIKey(ctx, fileID, apiKey)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()

	if err := ensureFileSizeAllowed(metadata, maxBytes); err != nil {
		return nil, nil, err
	}
	data, err := readAllWithinLimit(file, maxBytes)
	if err != nil {
		return nil, nil, err
	}
	return data, metadata, nil
}

func ensureFileSizeAllowed(metadata *FileMetadata, maxBytes int64) error {
	if maxBytes <= 0 {
		return nil
	}
	if metadata == nil {
		return nil
	}
	if metadata.SizeBytes <= maxBytes {
		return nil
	}
	return ErrFileTooLarge
}

func readAllWithinLimit(r io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return io.ReadAll(r)
	}
	limited := io.LimitReader(r, maxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, ErrFileTooLarge
	}
	return data, nil
}

// ListFiles returns all non-expired files
func (s *GeminiFileStore) ListFiles(ctx context.Context) ([]*FileMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.metadataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata directory: %w", err)
	}

	var files []*FileMetadata
	now := time.Now()

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), MetadataFileExtension) {
			continue
		}

		fileID := strings.TrimSuffix(entry.Name(), MetadataFileExtension)
		metadata, err := s.loadMetadata(fileID)
		if err != nil {
			log.WithError(err).Warnf("gemini file store: failed to load metadata for %s", fileID)
			continue
		}

		// Skip expired files
		if s.isExpired(metadata, now) {
			continue
		}

		files = append(files, metadata)
	}

	return files, nil
}

func (s *GeminiFileStore) ListFilesForAPIKey(ctx context.Context, apiKey string) ([]*FileMetadata, error) {
	files, err := s.ListFiles(ctx)
	if err != nil {
		return nil, err
	}
	return filterByAPIKey(files, apiKey), nil
}

// DeleteFile removes a file and its metadata
func (s *GeminiFileStore) DeleteFile(ctx context.Context, fileID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.deleteFileUnsafe(fileID)
}

func (s *GeminiFileStore) DeleteFileForAPIKey(ctx context.Context, fileID, apiKey string) error {
	metadata, err := s.GetFileForAPIKey(ctx, fileID, apiKey)
	if err != nil {
		return err
	}
	if s.isExpired(metadata, time.Now()) {
		return ErrFileNotFound
	}
	return s.DeleteFile(ctx, fileID)
}

// Close stops the cleanup routine and releases resources
func (s *GeminiFileStore) Close() error {
	if s.cleanupTicker != nil {
		s.cleanupTicker.Stop()
	}
	if s.cleanupStop != nil {
		close(s.cleanupStop)
	}
	return nil
}

// deleteFileUnsafe removes file and metadata without locking (caller must hold lock)
func (s *GeminiFileStore) deleteFileUnsafe(fileID string) error {
	if !isValidFileID(fileID) {
		return ErrFileNotFound
	}

	filePath := filepath.Join(s.filesDir, fileID)
	metaPath := filepath.Join(s.metadataDir, fileID+MetadataFileExtension)

	// Get file size before deletion
	if info, err := os.Stat(filePath); err == nil {
		s.currentTotalSize -= info.Size()
	}

	// Remove file
	if err := os.Remove(filePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to remove file: %w", err)
	}

	// Remove metadata
	if err := os.Remove(metaPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to remove metadata: %w", err)
	}

	log.Debugf("gemini file store: deleted file %s", fileID)
	return nil
}

// saveMetadata writes metadata to disk
func (s *GeminiFileStore) saveMetadata(metadata *FileMetadata) error {
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	metaPath := filepath.Join(s.metadataDir, metadata.FileID+MetadataFileExtension)
	if err := os.WriteFile(metaPath, data, 0o600); err != nil {
		return fmt.Errorf("failed to write metadata file: %w", err)
	}

	return nil
}

// loadMetadata reads metadata from disk
func (s *GeminiFileStore) loadMetadata(fileID string) (*FileMetadata, error) {
	if !isValidFileID(fileID) {
		return nil, ErrFileNotFound
	}

	metaPath := filepath.Join(s.metadataDir, fileID+MetadataFileExtension)

	data, err := os.ReadFile(metaPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrFileNotFound
		}
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	var metadata FileMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	return &metadata, nil
}

func isValidFileID(fileID string) bool {
	if len(fileID) != 32 {
		return false
	}
	for i := 0; i < len(fileID); i++ {
		c := fileID[i]
		isDigit := c >= '0' && c <= '9'
		isLowerHex := c >= 'a' && c <= 'f'
		isUpperHex := c >= 'A' && c <= 'F'
		if !isDigit && !isLowerHex && !isUpperHex {
			return false
		}
	}
	return true
}

func (s *GeminiFileStore) isExpired(metadata *FileMetadata, now time.Time) bool {
	if metadata == nil {
		return true
	}
	return now.After(metadata.ExpiresAt)
}

func (s *GeminiFileStore) isOwnedByAPIKey(metadata *FileMetadata, apiKey string) bool {
	if metadata == nil || metadata.APIKey == "" {
		return apiKey == ""
	}
	return metadata.APIKey == hashAPIKey(apiKey)
}

func filterByAPIKey(files []*FileMetadata, apiKey string) []*FileMetadata {
	if len(files) == 0 {
		return nil
	}
	out := make([]*FileMetadata, 0, len(files))
	want := hashAPIKey(apiKey)
	for _, metadata := range files {
		if metadata != nil && metadata.APIKey == want {
			out = append(out, metadata)
		}
	}
	return out
}

// startCleanupRoutine starts a background goroutine to clean up expired files
func (s *GeminiFileStore) startCleanupRoutine() {
	s.cleanupTicker = time.NewTicker(1 * time.Hour)

	go func() {
		for {
			select {
			case <-s.cleanupTicker.C:
				s.cleanupExpiredFiles()
			case <-s.cleanupStop:
				return
			}
		}
	}()
}

// cleanupExpiredFiles removes files past their expiration time
func (s *GeminiFileStore) cleanupExpiredFiles() {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.metadataDir)
	if err != nil {
		log.WithError(err).Error("gemini file store: failed to read metadata directory during cleanup")
		return
	}

	now := time.Now()
	deletedCount := 0

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), MetadataFileExtension) {
			continue
		}

		fileID := strings.TrimSuffix(entry.Name(), MetadataFileExtension)
		metadata, err := s.loadMetadata(fileID)
		if err != nil {
			log.WithError(err).Warnf("gemini file store: failed to load metadata for %s during cleanup", fileID)
			continue
		}

		if now.After(metadata.ExpiresAt) {
			if err := s.deleteFileUnsafe(fileID); err != nil {
				log.WithError(err).Warnf("gemini file store: failed to delete expired file %s", fileID)
			} else {
				deletedCount++
			}
		}
	}

	if deletedCount > 0 {
		log.Infof("gemini file store: cleaned up %d expired files (total size now: %d MB)",
			deletedCount, s.currentTotalSize/(1024*1024))
	}
}

// calculateTotalSize calculates the current total storage size
func (s *GeminiFileStore) calculateTotalSize() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var totalSize int64
	entries, err := os.ReadDir(s.filesDir)
	if err != nil {
		return fmt.Errorf("failed to read files directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		totalSize += info.Size()
	}

	s.currentTotalSize = totalSize
	return nil
}

// GenerateFileID creates a unique file identifier
func GenerateFileID() string {
	timestamp := time.Now().UnixNano()
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d", timestamp)))
	return hex.EncodeToString(hash[:16]) // Use first 16 bytes (32 hex chars)
}

// generateFileID is an internal alias for GenerateFileID
func generateFileID() string {
	return GenerateFileID()
}

// hashAPIKey creates a one-way hash of the API key for privacy
func hashAPIKey(apiKey string) string {
	if apiKey == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(hash[:8]) // Use first 8 bytes (16 hex chars)
}
