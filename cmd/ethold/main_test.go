package main

import "testing"

func TestFormatVersion(t *testing.T) {
	tests := []struct {
		name     string
		tag      string
		isExact  bool
		shortSHA string
		expected string
	}{
		{
			name:     "exact tag",
			tag:      "v1.0.0",
			isExact:  true,
			shortSHA: "3262ef1",
			expected: "v1.0.0",
		},
		{
			name:     "commits past tag",
			tag:      "v1.0.0",
			isExact:  false,
			shortSHA: "3262ef1",
			expected: "v1.0.0-dev-3262ef1",
		},
		{
			name:     "no tag with git commit",
			tag:      "",
			isExact:  false,
			shortSHA: "3262ef1",
			expected: "dev-3262ef1",
		},
		{
			name:     "commits past tag without sha",
			tag:      "v1.0.0",
			isExact:  false,
			shortSHA: "",
			expected: "v1.0.0-dev",
		},
		{
			name:     "no git info fallback",
			tag:      "",
			isExact:  false,
			shortSHA: "",
			expected: "dev",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatVersion(tt.tag, tt.isExact, tt.shortSHA)
			if got != tt.expected {
				t.Errorf("formatVersion(%q, %v, %q) = %q; want %q", tt.tag, tt.isExact, tt.shortSHA, got, tt.expected)
			}
		})
	}
}

func TestGetVersion(t *testing.T) {
	t.Run("explicit version takes precedence", func(t *testing.T) {
		old := version
		defer func() { version = old }()

		version = "v2.3.4"
		if got := getVersion(); got != "v2.3.4" {
			t.Errorf("expected v2.3.4, got %q", got)
		}
	})

	t.Run("detects git or buildinfo version in repo", func(t *testing.T) {
		old := version
		defer func() { version = old }()

		version = ""
		v := getVersion()
		if v == "" {
			t.Error("expected non-empty version")
		}
	})
}
