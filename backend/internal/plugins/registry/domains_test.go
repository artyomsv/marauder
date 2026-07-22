package registry

import "testing"

func TestActiveDomain_NoResolver_ReturnsEmpty(t *testing.T) {
	SetDomainResolver(nil)
	if got := ActiveDomain("kinozal"); got != "" {
		t.Errorf("ActiveDomain = %q, want empty", got)
	}
}

func TestActiveDomain_WithResolver_ReturnsOverride(t *testing.T) {
	SetDomainResolver(func(name string) DomainConfig {
		if name == "kinozal" {
			return DomainConfig{Active: "kinozal.me"}
		}
		return DomainConfig{}
	})
	t.Cleanup(func() { SetDomainResolver(nil) })
	if got := ActiveDomain("kinozal"); got != "kinozal.me" {
		t.Errorf("ActiveDomain = %q, want kinozal.me", got)
	}
	if got := ActiveDomain("rutracker"); got != "" {
		t.Errorf("unconfigured tracker ActiveDomain = %q, want empty", got)
	}
}

func TestDomainAllowed_Table(t *testing.T) {
	SetDomainResolver(func(string) DomainConfig { return DomainConfig{Custom: []string{"kinozal.example"}} })
	t.Cleanup(func() { SetDomainResolver(nil) })
	known := []string{"kinozal.tv", "kinozal.me"}
	tests := []struct {
		name string
		host string
		want bool
	}{
		{"known host", "kinozal.tv", true},
		{"known host uppercase", "KINOZAL.TV", true},
		{"known host www", "www.kinozal.me", true},
		{"custom host", "kinozal.example", true},
		{"unknown host", "evil.example", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DomainAllowed("kinozal", tt.host, known); got != tt.want {
				t.Errorf("DomainAllowed(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}
