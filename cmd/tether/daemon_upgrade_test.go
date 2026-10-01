package main

import (
	"path/filepath"
	"testing"
)

func TestDaemonCommandRequiresExactExecutableAndMode(t *testing.T) {
	exe := filepath.Join(string(filepath.Separator), "Users", "Jeff Huen", ".local", "bin", "tether")
	for _, tc := range []struct {
		name, command string
		owned         bool
	}{
		{"owned daemon", exe + " daemon\n", true},
		{"daemon options", exe + " daemon --listen 127.0.0.1:9333", true},
		{"different executable", exe + "-other daemon", false},
		{"different mode", exe + " connect user@server", false},
		{"mode prefix", exe + " daemon-helper", false},
		{"daemon mentioned as argument", exe + " status daemon", false},
		{"truncated command column", "/Users/jeffhuen/ " + exe + " daemon", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDaemonCommand(exe, tc.command); got != tc.owned {
				t.Fatalf("ownership = %v, want %v for %q", got, tc.owned, tc.command)
			}
		})
	}
}
