package gemini

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/filestore"
	"github.com/tidwall/gjson"
)

func TestRewriteLocalFileParts_InlinesFileData(t *testing.T) {
	store, err := filestore.NewGeminiFileStore(t.TempDir(), 48, 0)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	meta, err := store.SaveFile(context.Background(), strings.NewReader("hello"), "a.txt", "text/plain", "a.txt", "key-a")
	if err != nil {
		t.Fatalf("save file: %v", err)
	}

	req := []byte(`{"contents":[{"role":"user","parts":[{"file_data":{"file_uri":"` + meta.URI + `","mime_type":"text/plain"}}]}]}`)
	out, err := rewriteLocalFileParts(context.Background(), req, store, "key-a")
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if gjson.GetBytes(out, "contents.0.parts.0.file_data").Exists() {
		t.Fatalf("expected file_data removed")
	}
	inline := gjson.GetBytes(out, "contents.0.parts.0.inline_data")
	if !inline.Exists() {
		t.Fatalf("expected inline_data")
	}
	dataB64 := inline.Get("data").String()
	decoded, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	if string(decoded) != "hello" {
		t.Fatalf("expected content %q, got %q", "hello", string(decoded))
	}
}

func TestRewriteLocalFileParts_OtherAPIKeyRejected(t *testing.T) {
	store, err := filestore.NewGeminiFileStore(t.TempDir(), 48, 0)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	meta, err := store.SaveFile(context.Background(), strings.NewReader("hello"), "a.txt", "text/plain", "a.txt", "key-a")
	if err != nil {
		t.Fatalf("save file: %v", err)
	}

	req := []byte(`{"contents":[{"role":"user","parts":[{"file_data":{"file_uri":"` + meta.URI + `"}}]}]}`)
	_, err = rewriteLocalFileParts(context.Background(), req, store, "key-b")
	if !errors.Is(err, filestore.ErrFileNotFound) {
		t.Fatalf("expected not found error, got: %v", err)
	}
}

func TestRewriteLocalFileParts_IgnoresNonLocalFileID(t *testing.T) {
	store, err := filestore.NewGeminiFileStore(t.TempDir(), 48, 0)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	req := []byte(`{"contents":[{"role":"user","parts":[{"file_data":{"file_uri":"files/abc123","mime_type":"audio/mpeg"}}]}]}`)
	out, err := rewriteLocalFileParts(context.Background(), req, store, "key-a")
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if !gjson.GetBytes(out, "contents.0.parts.0.file_data.file_uri").Exists() {
		t.Fatalf("expected file_data retained")
	}
	if gjson.GetBytes(out, "contents.0.parts.0.inline_data").Exists() {
		t.Fatalf("expected inline_data absent")
	}
}
