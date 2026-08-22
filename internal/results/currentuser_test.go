// SPDX-License-Identifier: Apache-2.0
package results

import (
	"os"
	"testing"
)

// TestCurrentUser_FromUSER verifies that CurrentUser returns $USER when set.
func TestCurrentUser_FromUSER(t *testing.T) {
	orig := os.Getenv("USER")
	t.Cleanup(func() { os.Setenv("USER", orig) })
	os.Setenv("USER", "alice")
	if got := CurrentUser(); got != "alice" {
		t.Errorf("CurrentUser() = %q, want %q", got, "alice")
	}
}

// TestCurrentUser_FallbackToUSERNAME verifies the Windows-style fallback.
func TestCurrentUser_FallbackToUSERNAME(t *testing.T) {
	origUser := os.Getenv("USER")
	origUsername := os.Getenv("USERNAME")
	t.Cleanup(func() {
		os.Setenv("USER", origUser)
		os.Setenv("USERNAME", origUsername)
	})
	os.Unsetenv("USER")
	os.Setenv("USERNAME", "bob")
	if got := CurrentUser(); got != "bob" {
		t.Errorf("CurrentUser() = %q, want %q", got, "bob")
	}
}

// TestCurrentUser_FallbackUnknown verifies the "unknown" sentinel when neither
// USER nor USERNAME is set.
func TestCurrentUser_FallbackUnknown(t *testing.T) {
	origUser := os.Getenv("USER")
	origUsername := os.Getenv("USERNAME")
	t.Cleanup(func() {
		os.Setenv("USER", origUser)
		os.Setenv("USERNAME", origUsername)
	})
	os.Unsetenv("USER")
	os.Unsetenv("USERNAME")
	if got := CurrentUser(); got != "unknown" {
		t.Errorf("CurrentUser() = %q, want %q", got, "unknown")
	}
}

// TestAppend_SkipsCorruptLines verifies that All() gracefully skips lines
// that are not valid JSON.
func TestStore_SkipsCorruptLines(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	_ = s.Append(Record{Kind: KindIncident, Name: "fault-a", StartedAt: now})

	// Manually append a corrupt line directly to the file.
	f, _ := os.OpenFile(s.path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("not-valid-json\n")
	f.Close()

	all, err := s.All()
	if err != nil {
		t.Fatalf("All() returned error on corrupt file: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("expected 1 valid record, got %d", len(all))
	}
}
