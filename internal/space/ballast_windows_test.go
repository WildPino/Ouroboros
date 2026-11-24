//go:build windows

package space

import (
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The ballast is only useful if its space is really allocated, not sparse.
func TestBallastIsNotSparse(t *testing.T) {
	p := filepath.Join(t.TempDir(), "repo.ballast")
	const size = 64 << 20
	if err := CreateBallast(p, size); err != nil {
		t.Fatal(err)
	}
	p16, err := windows.UTF16PtrFromString(p)
	if err != nil {
		t.Fatal(err)
	}
	attrs, err := windows.GetFileAttributes(p16)
	if err != nil {
		t.Fatal(err)
	}
	if attrs&windows.FILE_ATTRIBUTE_SPARSE_FILE != 0 {
		t.Fatal("ballast is sparse")
	}
	// GetCompressedFileSize returns the bytes actually allocated on disk.
	var high uint32
	low, err := getCompressedFileSize(p16, &high)
	if err != nil {
		t.Fatal(err)
	}
	if allocated := uint64(high)<<32 | uint64(low); allocated < size {
		t.Fatalf("allocated %d bytes, want at least %d", allocated, size)
	}
}

var procGetCompressedFileSizeW = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCompressedFileSizeW")

func getCompressedFileSize(name *uint16, high *uint32) (uint32, error) {
	r, _, err := procGetCompressedFileSizeW.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(high)))
	if uint32(r) == 0xFFFFFFFF && err != windows.ERROR_SUCCESS {
		return 0, err
	}
	return uint32(r), nil
}
