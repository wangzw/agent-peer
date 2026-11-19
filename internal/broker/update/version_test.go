package update

import "testing"

func TestNewer(t *testing.T) {
	tests := []struct {
		name      string
		current   string
		candidate string
		want      bool
	}{
		{"newer patch", "0.1.0", "0.1.1", true},
		{"newer minor", "0.1.0", "0.2.0", true},
		{"newer major", "1.0.0", "2.0.0", true},
		{"same version", "0.1.0", "0.1.0", false},
		{"older candidate", "0.2.0", "0.1.0", false},
		{"current is dev", "dev", "0.2.0", false},
		{"current is e2e", "e2e", "0.2.0", false},
		{"current is a snapshot", "0.1.0-SNAPSHOT-abc123", "0.2.0", false},
		{"current is a prerelease", "0.2.0-rc.1", "0.2.0", false},
		{"candidate is dev", "0.1.0", "dev", false},
		{"candidate is a snapshot", "0.1.0", "0.2.0-SNAPSHOT-abc123", false},
		{"candidate is a prerelease", "0.1.0", "0.2.0-rc.1", false},
		{"leading v is accepted", "v0.1.0", "v0.2.0", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Newer(tc.current, tc.candidate); got != tc.want {
				t.Errorf("Newer(%q, %q) = %v, want %v", tc.current, tc.candidate, got, tc.want)
			}
		})
	}
}
