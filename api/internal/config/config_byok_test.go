package config

import "testing"

func TestByokClearedNeedsTheFlagAndTheProvider(t *testing.T) {
	for _, c := range []struct {
		on        bool
		providers []string
		want      bool
	}{{false, []string{"inter"}, false}, {true, nil, false}, {true, []string{"other"}, false}, {true, []string{" inter "}, true}} {
		cfg := &Config{ByokLiveCollection: c.on, ByokLiveProviders: c.providers}
		if got := cfg.ByokCleared("inter"); got != c.want {
			t.Fatalf("%+v: %v", c, got)
		}
	}
}
