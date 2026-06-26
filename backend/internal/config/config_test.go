package config_test

import (
	"testing"
	"time"

	"github.com/artyomsv/marauder/backend/internal/config"
)

func TestConfig_ProgressDefaults(t *testing.T) {
	t.Setenv("MARAUDER_DB_URL", "postgres://x")
	t.Setenv("MARAUDER_MASTER_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.ProgressWatcherEnabled {
		t.Error("progress watcher should default enabled")
	}
	if c.ProgressPollInterval != 5*time.Second {
		t.Errorf("poll interval = %v, want 5s", c.ProgressPollInterval)
	}
}

func TestConfig_ProgressPollInterval_ZeroRejected(t *testing.T) {
	t.Setenv("MARAUDER_DB_URL", "postgres://x")
	t.Setenv("MARAUDER_MASTER_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	t.Setenv("MARAUDER_PROGRESS_POLL_INTERVAL", "0")
	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error for zero poll interval, got nil")
	}
}

func TestConfig_SSEHeartbeatDefault(t *testing.T) {
	t.Setenv("MARAUDER_DB_URL", "postgres://x")
	t.Setenv("MARAUDER_MASTER_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.SSEHeartbeatInterval != 25*time.Second {
		t.Errorf("SSEHeartbeatInterval = %v, want 25s", c.SSEHeartbeatInterval)
	}
}

func TestConfig_SSEHeartbeatInterval_ZeroRejected(t *testing.T) {
	t.Setenv("MARAUDER_DB_URL", "postgres://x")
	t.Setenv("MARAUDER_MASTER_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	t.Setenv("MARAUDER_SSE_HEARTBEAT_INTERVAL", "0")
	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error for zero SSE heartbeat interval, got nil")
	}
}
