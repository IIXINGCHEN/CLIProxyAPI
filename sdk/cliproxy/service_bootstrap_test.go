package cliproxy

import (
	"context"
	"sync"
	"testing"

	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

type memStore struct {
	mu    sync.Mutex
	auths map[string]*coreauth.Auth
}

func (s *memStore) List(ctx context.Context) ([]*coreauth.Auth, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*coreauth.Auth, 0, len(s.auths))
	for _, a := range s.auths {
		out = append(out, a.Clone())
	}
	return out, nil
}

func (s *memStore) Save(ctx context.Context, auth *coreauth.Auth) (string, error) {
	_ = ctx
	if auth == nil || auth.ID == "" {
		return "", nil
	}
	s.mu.Lock()
	if s.auths == nil {
		s.auths = make(map[string]*coreauth.Auth)
	}
	s.auths[auth.ID] = auth.Clone()
	s.mu.Unlock()
	return auth.ID, nil
}

func (s *memStore) Delete(ctx context.Context, id string) error {
	_ = ctx
	s.mu.Lock()
	delete(s.auths, id)
	s.mu.Unlock()
	return nil
}

func TestServiceBootstrapCoreAuths_RegistersGeminiExecutor(t *testing.T) {
	cfg := &config.Config{
		AuthDir: t.TempDir(),
		GeminiKey: []config.GeminiKey{
			{APIKey: "g1"},
		},
	}

	manager := coreauth.NewManager(&memStore{}, &coreauth.RoundRobinSelector{}, nil)
	svc := &Service{cfg: cfg, coreManager: manager}
	svc.bootstrapCoreAuths(context.Background())

	auth, err := manager.PickAuth(context.Background(), "gemini", "", coreexecutor.Options{})
	if err != nil {
		t.Fatalf("expected PickAuth to succeed, got error: %v", err)
	}
	if auth == nil || auth.ID == "" {
		t.Fatalf("expected PickAuth to return an auth entry")
	}
}
