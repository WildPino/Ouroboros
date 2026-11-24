//go:build windows

package secret

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// testSDDL extends the default ACL with the current user, so that tests
// running without elevation can read back the file they wrote.
func testSDDL(t *testing.T) string {
	t.Helper()
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return DefaultSDDL + "(A;;FA;;;" + u.User.Sid.String() + ")"
}

func TestProtectRoundTrip(t *testing.T) {
	plain := []byte("correct horse battery staple")
	blob, err := protect(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, plain) {
		t.Fatal("ciphertext contains the plaintext")
	}
	got, err := unprotect(blob)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("got %q", got)
	}
}

func TestUnprotectRejectsCorruptBlob(t *testing.T) {
	blob, err := protect([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	blob[len(blob)/2] ^= 0xFF
	if _, err := unprotect(blob); err == nil {
		t.Fatal("want error for corrupted blob")
	}
}

func TestStoreSaveLoad(t *testing.T) {
	dir := t.TempDir()
	s := NewStoreWithSDDL(filepath.Join(dir, "sub", FileName), testSDDL(t))

	if ok, err := s.Exists(); err != nil || ok {
		t.Fatalf("Exists before save = %v, %v", ok, err)
	}
	if _, err := s.Load(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load before save: %v, want ErrNotFound", err)
	}
	if err := s.Save(nil); err == nil {
		t.Fatal("Save(empty) should fail")
	}

	for _, pw := range []string{"first password", "second, longer password"} {
		if err := s.Save([]byte(pw)); err != nil {
			t.Fatalf("Save(%q): %v", pw, err)
		}
		got, err := s.Load()
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != pw {
			t.Fatalf("Load = %q, want %q", got, pw)
		}
	}

	raw, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("password")) {
		t.Fatal("file contains the plaintext password")
	}
	entries, _ := os.ReadDir(filepath.Dir(s.Path()))
	if len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}

	if err := s.Remove(); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(); err != nil {
		t.Fatalf("second Remove: %v", err)
	}
}

func TestStoreAppliesDefaultACL(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), FileName))
	if err := s.Save([]byte("pw")); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(s.Path(), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if got := sd.String(); got != DefaultSDDL {
		t.Fatalf("DACL = %s, want %s", got, DefaultSDDL)
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		if _, err := os.ReadFile(s.Path()); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("non-elevated read: err = %v, want permission denied", err)
		}
	}
	// Replacing a file the caller can no longer open must still work.
	if err := s.Save([]byte("pw2")); err != nil {
		t.Fatalf("second Save over restricted file: %v", err)
	}
}
