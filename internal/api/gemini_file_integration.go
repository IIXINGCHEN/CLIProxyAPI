// Package server_ext provides extensions for integrating Gemini File API into the main server.
package api

import (
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/filestore"
	geminihandlers "github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers/gemini"
	log "github.com/sirupsen/logrus"
)

// InitializeGeminiFileStore creates and initializes a Gemini file store from config
func InitializeGeminiFileStore(cfg *config.Config) (*filestore.GeminiFileStore, error) {
	if cfg == nil || !cfg.GeminiFileCache.Enable {
		return nil, nil // File cache disabled
	}

	storagePath := cfg.GeminiFileCache.StoragePath
	expirationHours := cfg.GeminiFileCache.ExpirationHours
	maxTotalSizeMB := cfg.GeminiFileCache.MaxTotalSizeMB

	store, err := filestore.NewGeminiFileStore(storagePath, expirationHours, maxTotalSizeMB)
	if err != nil {
		return nil, err
	}

	log.Info("gemini file store enabled")
	return store, nil
}

// AttachGeminiFileHandler attaches file handler to Gemini API handler if file store is available
func AttachGeminiFileHandler(geminiHandler *geminihandlers.GeminiAPIHandler, fileStore *filestore.GeminiFileStore) {
	if geminiHandler == nil || fileStore == nil {
		return
	}

	geminiHandler.FileHandler = geminihandlers.NewGeminiFileAPIHandler(fileStore)
	log.Debug("gemini file handler attached")
}
