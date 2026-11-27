//go:build windows

package edge

import "unsafe"

// Interface identifiers of the event handlers implemented in Go, from the
// WebView2 SDK header.
var (
	iidIUnknown                     = NewGUID("{00000000-0000-0000-C000-000000000046}")
	iidEnvironmentCompletedHandler  = NewGUID("{4E8A3389-C9D8-4BD2-B6B5-124FEE6CC14D}")
	iidControllerCompletedHandler   = NewGUID("{6C4819F3-C9B7-4260-8127-C9F5BDE7F68C}")
	iidWebMessageReceivedHandler    = NewGUID("{57213F19-00E6-49FA-8E07-898EA01ECBD2}")
	iidPermissionRequestedHandler   = NewGUID("{15E1C6A3-C72A-4DF3-91D7-D097FBEC6BFD}")
	iidWebResourceRequestedHandler  = NewGUID("{AB00B74C-15F1-4646-80E8-E76341D25D71}")
	iidNavigationCompletedHandler   = NewGUID("{D33A35BF-1C49-4F98-93AB-006E0533FE1C}")
	iidAcceleratorKeyPressedHandler = NewGUID("{B29C7E28-FA79-41A8-8E44-65811C76DCB2}")
)

const (
	sOK          = 0
	eNoInterface = 0x80004002
	ePointer     = 0x80004003
)

// queryHandler implements QueryInterface for a handler written in Go: it
// returns the handler itself for IUnknown and for the handler's own
// interface, and refuses any other. The runtime queries the handlers it is
// given, and a success that leaves the result empty makes it fail.
func queryHandler(this unsafe.Pointer, own *GUID, refiid *GUID, object *unsafe.Pointer) uintptr {
	if object == nil {
		return ePointer
	}
	if refiid != nil && (*refiid == *iidIUnknown || *refiid == *own) {
		*object = this
		return sOK
	}
	*object = nil
	return eNoInterface
}
