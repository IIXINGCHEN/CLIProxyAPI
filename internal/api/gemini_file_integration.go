// Package server_ext provides extensions for integrating Gemini File API into the main server.
package api

import (
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/filestore"
	geminihandlers "github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers/gemini"
	log "github.com/sirupsen/logrus"
)

// InitializeGeminiFileSupport attaches Gemini Files API handlers based on config.
// It returns a local file store when running in "local" mode (caller may keep a reference for shutdown),
// otherwise returns nil.
func InitializeGeminiFileSupport(cfg *config.Config, geminiHandler *geminihandlers.GeminiAPIHandler) (*filestore.GeminiFileStore, error) {
	if cfg == nil || !cfg.GeminiFileCache.Enable {
		return nil, nil // File cache disabled
	}

	mode := strings.ToLower(strings.TrimSpace(cfg.GeminiFileCache.Mode))
	if mode == "" {
		mode = "upstream"
	}

	switch mode {
	case "upstream":
		AttachGeminiUpstreamFileHandler(geminiHandler)
		log.Info("gemini file handler enabled (mode=upstream)")
		return nil, nil
	case "local":
		store, err := initializeGeminiLocalFileStore(cfg)
		if err != nil {
			return nil, err
		}
		AttachGeminiFileHandler(geminiHandler, store)
		log.Info("gemini file handler enabled (mode=local)")
		return store, nil
	default:
		log.Warnf("gemini file handler disabled: unsupported mode %q (expected \"upstream\" or \"local\")", mode)
		return nil, nil
	}
}

func initializeGeminiLocalFileStore(cfg *config.Config) (*filestore.GeminiFileStore, error) {
	storagePath := cfg.GeminiFileCache.StoragePath
	expirationHours := cfg.GeminiFileCache.ExpirationHours
	maxTotalSizeMB := cfg.GeminiFileCache.MaxTotalSizeMB

	store, err := filestore.NewGeminiFileStore(storagePath, expirationHours, maxTotalSizeMB)
	if err != nil {
		return nil, err
	}
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

func AttachGeminiUpstreamFileHandler(geminiHandler *geminihandlers.GeminiAPIHandler) {
	if geminiHandler == nil || geminiHandler.BaseAPIHandler == nil {
		return
	}
	geminiHandler.FileHandler = geminihandlers.NewGeminiUpstreamFileAPIHandler(geminiHandler.BaseAPIHandler)
	log.Debug("gemini upstream file handler attached")
}
