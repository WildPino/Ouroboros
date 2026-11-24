//go:build windows

package restic

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/WildPino/ouroboros/internal/backend"
)

// denyRead replaces the ACL of path so that the current user may write but
// not read it. The parent directory still allows deleting it during cleanup.
func denyRead(t *testing.T, path string) {
	t.Helper()
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FW;;;" + u.User.Sid.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationPartialBackup(t *testing.T) {
	if windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("an elevated restic may read the file through backup privileges")
	}
	env := newIntegrationEnv(t)
	ctx := context.Background()
	src := filepath.Join(env.dir, "src")
	writeFile(t, filepath.Join(src, "ok.txt"), "readable")
	locked := filepath.Join(src, "locked.txt")
	writeFile(t, locked, "unreadable")
	denyRead(t, locked)

	b := env.backend(t, filepath.Join(env.dir, "repo"))
	if err := b.Init(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := b.Backup(ctx, backend.Job{Name: "docs", Paths: []string{src}})
	if err != nil {
		t.Fatalf("partial backup must not fail: %v", err)
	}
	if !res.Incomplete || res.SnapshotID == "" {
		t.Fatalf("result %+v", res)
	}
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "locked.txt") {
		t.Fatalf("warnings %q", res.Warnings)
	}
}
