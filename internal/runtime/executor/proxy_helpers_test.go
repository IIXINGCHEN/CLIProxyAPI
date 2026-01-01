package executor

import "testing"

func TestDefaultDialer_HasFallbackDelay(t *testing.T) {
	d := defaultDialer()
	if d == nil {
		t.Fatalf("expected dialer")
	}
	if d.FallbackDelay <= 0 {
		t.Fatalf("expected FallbackDelay > 0, got %v", d.FallbackDelay)
	}
}
