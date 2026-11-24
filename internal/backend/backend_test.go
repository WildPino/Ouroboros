package backend

import "testing"

func TestSnapshotJobAndPinned(t *testing.T) {
	tests := []struct {
		tags   []string
		job    string
		pinned bool
	}{
		{nil, "", false},
		{[]string{TagOuroboros, JobTag("docs")}, "docs", false},
		{[]string{TagOuroboros, JobTag("docs"), TagPinned}, "docs", true},
		{[]string{"job:"}, "", false},
		{[]string{"other", JobTag("data-volume")}, "data-volume", false},
	}
	for _, tt := range tests {
		s := Snapshot{Tags: tt.tags}
		if got := s.Job(); got != tt.job {
			t.Errorf("Job(%v) = %q, want %q", tt.tags, got, tt.job)
		}
		if got := s.Pinned(); got != tt.pinned {
			t.Errorf("Pinned(%v) = %v, want %v", tt.tags, got, tt.pinned)
		}
	}
}
