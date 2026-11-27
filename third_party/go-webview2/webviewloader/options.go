//go:build windows

package webviewloader

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// The runtime refuses a null options object, so the loader passes this
// fixed one: no additional browser arguments, the system language, and no
// single sign-on with the Windows account.

var (
	ole32           = windows.NewLazySystemDLL("ole32.dll")
	coTaskMemAlloc  = ole32.NewProc("CoTaskMemAlloc")
	iidIUnknown     = windows.GUID{Data1: 0x00000000, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidEnvOptions   = windows.GUID{Data1: 0x2FDE08A8, Data2: 0x1E9A, Data3: 0x4766, Data4: [8]byte{0x8C, 0x05, 0x95, 0xA9, 0xCE, 0xB9, 0xD1, 0xC5}}
	envOptionsVtbl  = newEnvOptionsVtbl()
	fixedEnvOptions = &envOptions{vtbl: envOptionsVtbl}
)

const (
	sOK          = 0
	eNoInterface = 0x80004002
	eOutOfMemory = 0x8007000E
	eNotImpl     = 0x80004001
	ePointer     = 0x80004003
)

type envOptionsVtblT struct {
	QueryInterface                            uintptr
	AddRef                                    uintptr
	Release                                   uintptr
	GetAdditionalBrowserArguments             uintptr
	PutAdditionalBrowserArguments             uintptr
	GetLanguage                               uintptr
	PutLanguage                               uintptr
	GetTargetCompatibleBrowserVersion         uintptr
	PutTargetCompatibleBrowserVersion         uintptr
	GetAllowSingleSignOnUsingOSPrimaryAccount uintptr
	PutAllowSingleSignOnUsingOSPrimaryAccount uintptr
}

type envOptions struct {
	vtbl *envOptionsVtblT
}

func newEnvOptionsVtbl() *envOptionsVtblT {
	str := func(s string) uintptr {
		return windows.NewCallback(func(_ *envOptions, out **uint16) uintptr {
			return putString(out, s)
		})
	}
	put := windows.NewCallback(func(_ *envOptions, _ uintptr) uintptr { return eNotImpl })
	return &envOptionsVtblT{
		QueryInterface: windows.NewCallback(func(this *envOptions, iid *windows.GUID, out *unsafe.Pointer) uintptr {
			if out == nil {
				return ePointer
			}
			if *iid == iidIUnknown || *iid == iidEnvOptions {
				*out = unsafe.Pointer(this)
				return sOK
			}
			*out = nil
			return eNoInterface
		}),
		AddRef:                            windows.NewCallback(func(_ *envOptions) uintptr { return 1 }),
		Release:                           windows.NewCallback(func(_ *envOptions) uintptr { return 1 }),
		GetAdditionalBrowserArguments:     str(""),
		PutAdditionalBrowserArguments:     put,
		GetLanguage:                       str(""),
		PutLanguage:                       put,
		GetTargetCompatibleBrowserVersion: str(MinimumVersion),
		PutTargetCompatibleBrowserVersion: put,
		GetAllowSingleSignOnUsingOSPrimaryAccount: windows.NewCallback(func(_ *envOptions, out *int32) uintptr {
			if out == nil {
				return ePointer
			}
			*out = 0
			return sOK
		}),
		PutAllowSingleSignOnUsingOSPrimaryAccount: put,
	}
}

// putString returns s in memory from CoTaskMemAlloc, which the caller frees.
func putString(out **uint16, s string) uintptr {
	if out == nil {
		return ePointer
	}
	u, err := windows.UTF16FromString(s)
	if err != nil {
		return ePointer
	}
	n := uintptr(len(u)) * 2
	p, _, _ := coTaskMemAlloc.Call(n)
	if p == 0 {
		return eOutOfMemory
	}
	// p is memory from CoTaskMemAlloc, outside the Go heap.
	buf := *(**uint16)(unsafe.Pointer(&p))
	copy(unsafe.Slice(buf, len(u)), u)
	*out = buf
	return sOK
}
