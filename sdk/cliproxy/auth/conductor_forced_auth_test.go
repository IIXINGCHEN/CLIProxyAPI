package auth

import (
	"context"
	"errors"
	"sync"
	"testing"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

type recordingExecutor struct {
	provider string
	mu       sync.Mutex
	lastAuth string
}

func (e *recordingExecutor) Identifier() string { return e.provider }

func (e *recordingExecutor) Execute(_ context.Context, auth *Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	e.mu.Lock()
	if auth != nil {
		e.lastAuth = auth.ID
	}
	e.mu.Unlock()
	return cliproxyexecutor.Response{Payload: []byte("ok")}, nil
}

func (e *recordingExecutor) ExecuteStream(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (<-chan cliproxyexecutor.StreamChunk, error) {
	return nil, nil
}

func (e *recordingExecutor) Refresh(context.Context, *Auth) (*Auth, error) { return nil, nil }

func (e *recordingExecutor) CountTokens(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}

func (e *recordingExecutor) LastAuthID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastAuth
}

type erroringExecutor struct {
	provider string
	mu       sync.Mutex
	calls    int
}

func (e *erroringExecutor) Identifier() string { return e.provider }
func (e *erroringExecutor) Execute(_ context.Context, _ *Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	return cliproxyexecutor.Response{}, errors.New("boom")
}
func (e *erroringExecutor) ExecuteStream(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (<-chan cliproxyexecutor.StreamChunk, error) {
	return nil, nil
}
func (e *erroringExecutor) Refresh(context.Context, *Auth) (*Auth, error) { return nil, nil }
func (e *erroringExecutor) CountTokens(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}
func (e *erroringExecutor) Calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func TestManagerExecute_ForceAuthID(t *testing.T) {
	m := NewManager(nil, &RoundRobinSelector{}, NoopHook{})
	exec := &recordingExecutor{provider: "gemini"}
	m.RegisterExecutor(exec)

	if _, err := m.Register(context.Background(), &Auth{ID: "a", Provider: "gemini"}); err != nil {
		t.Fatalf("register auth a: %v", err)
	}
	if _, err := m.Register(context.Background(), &Auth{ID: "b", Provider: "gemini"}); err != nil {
		t.Fatalf("register auth b: %v", err)
	}

	resp, err := m.Execute(context.Background(), []string{"gemini"}, cliproxyexecutor.Request{Model: ""}, cliproxyexecutor.Options{
		Metadata: map[string]any{ForceAuthIDMetadataKey: "b"},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if got := exec.LastAuthID(); got != "b" {
		t.Fatalf("expected forced auth %q, got %q", "b", got)
	}
	if v, _ := resp.Metadata[SelectedAuthIDMetadataKey].(string); v != "b" {
		t.Fatalf("expected response metadata auth id %q, got %q", "b", v)
	}
}

func TestManagerExecute_NoRetry_TriesSingleAuth(t *testing.T) {
	m := NewManager(nil, &RoundRobinSelector{}, NoopHook{})
	exec := &erroringExecutor{provider: "gemini"}
	m.RegisterExecutor(exec)

	if _, err := m.Register(context.Background(), &Auth{ID: "a", Provider: "gemini"}); err != nil {
		t.Fatalf("register auth a: %v", err)
	}
	if _, err := m.Register(context.Background(), &Auth{ID: "b", Provider: "gemini"}); err != nil {
		t.Fatalf("register auth b: %v", err)
	}

	_, _ = m.Execute(context.Background(), []string{"gemini"}, cliproxyexecutor.Request{Model: ""}, cliproxyexecutor.Options{
		Metadata: map[string]any{NoRetryMetadataKey: true},
	})

	if got := exec.Calls(); got != 1 {
		t.Fatalf("expected 1 call, got %d", got)
	}
}
