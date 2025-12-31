package gemini

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/filestore"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const maxInlineFileBytes = 20 * 1024 * 1024

func rewriteLocalFileParts(ctx context.Context, payload []byte, store *filestore.GeminiFileStore, apiKey string) ([]byte, error) {
	if store == nil {
		return payload, nil
	}
	return applyLocalFilePartRefs(ctx, payload, store, apiKey, findLocalFilePartRefs(payload))
}

type filePartRef struct {
	contentIndex int
	partIndex    int
	fileURI      string
	mimeType     string
}

func findLocalFilePartRefs(payload []byte) []filePartRef {
	contents := gjson.GetBytes(payload, "contents")
	if !contents.Exists() || !contents.IsArray() {
		return nil
	}
	return findRefsInContents(contents.Array())
}

func findRefsInContents(contents []gjson.Result) []filePartRef {
	var out []filePartRef
	for ci, content := range contents {
		out = appendRefsFromContent(out, ci, content)
	}
	return out
}

func appendRefsFromContent(out []filePartRef, contentIndex int, content gjson.Result) []filePartRef {
	parts := content.Get("parts")
	if !parts.Exists() || !parts.IsArray() {
		return out
	}
	for pi, part := range parts.Array() {
		if ref, ok := parseLocalFilePart(contentIndex, pi, part); ok {
			out = append(out, ref)
		}
	}
	return out
}

func parseLocalFilePart(contentIndex, partIndex int, part gjson.Result) (filePartRef, bool) {
	fileURI := part.Get("file_data.file_uri").String()
	mimeType := part.Get("file_data.mime_type").String()
	if fileURI == "" {
		fileURI = part.Get("fileData.fileUri").String()
		mimeType = part.Get("fileData.mimeType").String()
	}
	if !strings.HasPrefix(fileURI, "files/") {
		return filePartRef{}, false
	}
	return filePartRef{
		contentIndex: contentIndex,
		partIndex:    partIndex,
		fileURI:      fileURI,
		mimeType:     mimeType,
	}, true
}

func inlineLocalFilePart(ctx context.Context, payload []byte, store *filestore.GeminiFileStore, apiKey string, ref filePartRef) ([]byte, error) {
	data, mimeType, err := loadInlineFile(ctx, store, apiKey, ref)
	if err != nil {
		return nil, err
	}
	return applyInlineData(payload, ref, mimeType, data), nil
}

func applyLocalFilePartRefs(ctx context.Context, payload []byte, store *filestore.GeminiFileStore, apiKey string, refs []filePartRef) ([]byte, error) {
	if len(refs) == 0 {
		return payload, nil
	}
	out := payload
	for _, ref := range refs {
		updated, err := inlineLocalFilePart(ctx, out, store, apiKey, ref)
		if err != nil {
			return nil, err
		}
		out = updated
	}
	return out, nil
}

func loadInlineFile(ctx context.Context, store *filestore.GeminiFileStore, apiKey string, ref filePartRef) ([]byte, string, error) {
	fileID := strings.TrimPrefix(ref.fileURI, "files/")
	data, meta, err := store.ReadFileBytesForAPIKey(ctx, fileID, apiKey, maxInlineFileBytes)
	if err != nil {
		return nil, "", normalizeReadError(err)
	}
	return data, resolveMimeType(ref, meta), nil
}

func resolveMimeType(ref filePartRef, metadata *filestore.FileMetadata) string {
	if ref.mimeType != "" {
		return ref.mimeType
	}
	if metadata != nil {
		return metadata.MimeType
	}
	return ""
}

func normalizeReadError(err error) error {
	if errors.Is(err, filestore.ErrFileNotFound) {
		return filestore.ErrFileNotFound
	}
	if errors.Is(err, filestore.ErrFileTooLarge) {
		return filestore.ErrFileTooLarge
	}
	return fmt.Errorf("read local file: %w", err)
}

func applyInlineData(payload []byte, ref filePartRef, mimeType string, data []byte) []byte {
	inlinePath := fmt.Sprintf("contents.%d.parts.%d.inline_data", ref.contentIndex, ref.partIndex)
	out, _ := sjson.SetBytes(payload, inlinePath+".mime_type", mimeType)
	out, _ = sjson.SetBytes(out, inlinePath+".data", base64.StdEncoding.EncodeToString(data))
	out, _ = sjson.DeleteBytes(out, fmt.Sprintf("contents.%d.parts.%d.file_data", ref.contentIndex, ref.partIndex))
	out, _ = sjson.DeleteBytes(out, fmt.Sprintf("contents.%d.parts.%d.fileData", ref.contentIndex, ref.partIndex))
	return out
}
