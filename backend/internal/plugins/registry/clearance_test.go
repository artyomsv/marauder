package registry

import (
	"context"
	"errors"
	"testing"
)

type stubProvider struct {
	clearance   Clearance
	err         error
	invalidated []string
}

func (s *stubProvider) Clearance(context.Context, string) (Clearance, error) {
	return s.clearance, s.err
}

func (s *stubProvider) InvalidateClearance(host string) {
	s.invalidated = append(s.invalidated, host)
}

func TestClearanceFor_NoProvider_ReturnsZeroAndNoError(t *testing.T) {
	SetClearanceProvider(nil)
	got, err := ClearanceFor(context.Background(), "https://rutracker.org/forum/login.php")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got.Valid() {
		t.Fatalf("Valid() = true, want false for the zero Clearance")
	}
}

func TestClearanceFor_WithProvider_ReturnsIt(t *testing.T) {
	t.Cleanup(func() { SetClearanceProvider(nil) })
	SetClearanceProvider(&stubProvider{clearance: Clearance{
		Cookies:   map[string]string{"cf_clearance": "abc"},
		UserAgent: "Mozilla/5.0 Chrome/148",
	}})
	got, err := ClearanceFor(context.Background(), "https://rutracker.org/forum/login.php")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !got.Valid() {
		t.Fatal("Valid() = false, want true")
	}
	if got.Cookies["cf_clearance"] != "abc" || got.UserAgent != "Mozilla/5.0 Chrome/148" {
		t.Fatalf("got %+v", got)
	}
}

func TestClearanceFor_ProviderError_Propagates(t *testing.T) {
	t.Cleanup(func() { SetClearanceProvider(nil) })
	sentinel := errors.New("solver down")
	SetClearanceProvider(&stubProvider{err: sentinel})
	if _, err := ClearanceFor(context.Background(), "https://rutracker.org/forum/login.php"); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
}

func TestInvalidateClearance_ForwardsProbeURL_AndIsSafeWithoutProvider(t *testing.T) {
	const probe = "https://rutracker.org/forum/login.php"
	SetClearanceProvider(nil)
	InvalidateClearance(probe) // must not panic

	sp := &stubProvider{}
	t.Cleanup(func() { SetClearanceProvider(nil) })
	SetClearanceProvider(sp)
	InvalidateClearance(probe)
	// The registry forwards the probe URL verbatim; deriving the per-host
	// cache key is the provider's job.
	if len(sp.invalidated) != 1 || sp.invalidated[0] != probe {
		t.Fatalf("invalidated = %v", sp.invalidated)
	}
}

func TestClearance_Valid_RequiresBothCookieAndUA(t *testing.T) {
	tests := []struct {
		name string
		c    Clearance
		want bool
	}{
		{"zero", Clearance{}, false},
		{"cookies only", Clearance{Cookies: map[string]string{"cf_clearance": "x"}}, false},
		{"ua only", Clearance{UserAgent: "x"}, false},
		{"both", Clearance{Cookies: map[string]string{"cf_clearance": "x"}, UserAgent: "y"}, true},
		{"empty cookie map", Clearance{Cookies: map[string]string{}, UserAgent: "y"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.Valid(); got != tt.want {
				t.Errorf("Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}
