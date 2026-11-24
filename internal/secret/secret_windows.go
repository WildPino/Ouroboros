//go:build windows

package secret

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func protect(plain []byte) ([]byte, error) {
	in := blobOf(plain)
	ent := blobOf(entropy)
	var out windows.DataBlob
	err := windows.CryptProtectData(&in, nil, &ent, 0, nil,
		windows.CRYPTPROTECT_LOCAL_MACHINE|windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	if err != nil {
		return nil, err
	}
	return takeBlob(&out), nil
}

func unprotect(blob []byte) ([]byte, error) {
	in := blobOf(blob)
	ent := blobOf(entropy)
	var out windows.DataBlob
	err := windows.CryptUnprotectData(&in, nil, &ent, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	if err != nil {
		return nil, err
	}
	return takeBlob(&out), nil
}

func blobOf(b []byte) windows.DataBlob {
	if len(b) == 0 {
		return windows.DataBlob{}
	}
	return windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

// takeBlob copies a DPAPI output buffer into Go memory, wipes it and frees it.
func takeBlob(b *windows.DataBlob) []byte {
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(b.Data)))
	src := unsafe.Slice(b.Data, b.Size)
	out := make([]byte, len(src))
	copy(out, src)
	Clear(src)
	return out
}

// writeFileAtomic writes data to a new temporary file created with the given
// security descriptor, flushes it, and renames it over path through the open
// handle. Renaming by handle uses the DELETE right granted at creation, so it
// works even when the descriptor denies the caller any further access.
func writeFileAtomic(path string, data []byte, sddl string) (err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return fmt.Errorf("security descriptor: %w", err)
	}
	sa := windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(sa))

	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	tmp := abs + ".tmp-" + hex.EncodeToString(suffix[:])
	tmp16, err := windows.UTF16PtrFromString(tmp)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(tmp16, windows.GENERIC_WRITE|windows.DELETE, 0, &sa,
		windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(h), tmp)
	defer func() {
		if err != nil {
			setDeleteOnClose(h)
		}
		f.Close()
	}()

	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	return renameByHandle(h, abs)
}

// fileRenameInfo mirrors FILE_RENAME_INFO.
type fileRenameInfo struct {
	ReplaceIfExists uint32
	RootDirectory   windows.Handle
	FileNameLength  uint32
	FileName        [1]uint16
}

func renameByHandle(h windows.Handle, target string) error {
	name, err := windows.UTF16FromString(target)
	if err != nil {
		return err
	}
	name = name[:len(name)-1] // FileNameLength excludes the terminator.
	size := int(unsafe.Offsetof(fileRenameInfo{}.FileName)) + (len(name)+1)*2
	buf := make([]uint64, (size+7)/8) // uint64 backing keeps the struct aligned.
	info := (*fileRenameInfo)(unsafe.Pointer(&buf[0]))
	info.ReplaceIfExists = 1
	info.FileNameLength = uint32(len(name) * 2)
	copy(unsafe.Slice(&info.FileName[0], len(name)+1), name)
	return windows.SetFileInformationByHandle(h, windows.FileRenameInfo,
		(*byte)(unsafe.Pointer(info)), uint32(size))
}

func setDeleteOnClose(h windows.Handle) {
	del := uint32(1) // FILE_DISPOSITION_INFO.DeleteFile
	_ = windows.SetFileInformationByHandle(h, windows.FileDispositionInfo,
		(*byte)(unsafe.Pointer(&del)), uint32(unsafe.Sizeof(del)))
}
