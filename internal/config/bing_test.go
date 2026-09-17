package config

import (
	"strings"
	"testing"
)

func TestBingSyncSettingsDefaultsAndValidation(t *testing.T) {
	settings := DefaultRuntimeSettings(t.TempDir())
	if !settings.BingSyncEnabled || settings.BingSyncIntervalHours != 24 {
		t.Fatalf("Bing sync defaults = enabled %v, interval %d", settings.BingSyncEnabled, settings.BingSyncIntervalHours)
	}
	for _, interval := range []int{0, MaxBingSyncIntervalHours + 1} {
		settings.BingSyncIntervalHours = interval
		if err := ValidateRuntimeSettings(settings); err == nil || !strings.Contains(err.Error(), "bingSyncIntervalHours") {
			t.Fatalf("interval %d error = %v", interval, err)
		}
	}
	settings.BingSyncEnabled = false
	settings.BingSyncIntervalHours = 1
	if err := ValidateRuntimeSettings(settings); err != nil {
		t.Fatalf("disabled Bing sync settings: %v", err)
	}
}
