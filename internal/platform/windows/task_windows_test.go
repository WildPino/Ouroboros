//go:build windows

package windows

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestUntrustedWriters(t *testing.T) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	me := accountName(u.User.Sid)

	// A file in a user's temporary directory is writable by that user.
	p := filepath.Join(t.TempDir(), "prog.exe")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := UntrustedWriters(p)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(got, func(s string) bool { return strings.HasSuffix(s, me) }) {
		t.Fatalf("got %v, want %s among them", got, me)
	}

	// With only SYSTEM and Administrators in a protected list, at most the
	// owner remains.
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FR;;;BU)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	got, err = UntrustedWriters(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range got {
		if !strings.HasPrefix(w, "owner ") {
			t.Fatalf("unexpected writer %q in %v", w, got)
		}
	}
}

func TestQueryTask(t *testing.T) {
	st, err := QueryTask(t.Context(), `\OuroborosTestNoSuchTask\Nope`)
	if err != nil || st != TaskMissing {
		t.Fatalf("missing task: %v, %v", st, err)
	}
	// A task every Windows installation has; querying it changes nothing.
	st, err = QueryTask(t.Context(), `\Microsoft\Windows\Defrag\ScheduledDefrag`)
	if err != nil || st == TaskMissing {
		t.Fatalf("defrag task: %v, %v", st, err)
	}
}
