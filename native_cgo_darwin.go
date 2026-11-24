//go:build darwin && cgo

package rexwebview

/*
#cgo CFLAGS: -fobjc-arc -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
#include "pane_darwin.h"
*/
import "C"

import "unsafe"

func nativeIsMainThread() bool    { return C.rexwv_is_main_thread() != 0 }
func nativeAttach(window uintptr) { C.rexwv_attach_overlay(C.uintptr_t(window)) }
func nativeDetach()               { C.rexwv_detach_overlay() }
func nativeActivateHost()         { C.rexwv_activate_host() }
func nativePrefSync()             { C.rexwv_pref_sync() }

func nativePref(name string) (string, bool) {
	cs := C.CString(name)
	defer C.free(unsafe.Pointer(cs))
	v := C.rexwv_pref(cs)
	if v == nil {
		return "", false
	}
	defer C.free(unsafe.Pointer(v))
	return C.GoString(v), true
}

//export rexwvTick
func rexwvTick(found C.int, x, y, w, h C.double, hostActive, selfActive C.int, out *C.RexWVTick) {
	t := paneTick(tickIn{HostFound: found != 0, Host: rect{float64(x), float64(y), float64(w), float64(h)}, HostActive: hostActive != 0, SelfActive: selfActive != 0})
	if t.Visible {
		out.visible = 1
	}
	out.x, out.y, out.w, out.h = C.double(t.Frame.X), C.double(t.Frame.Y), C.double(t.Frame.W), C.double(t.Frame.H)
	out.radius = C.double(t.Radius)
	out.px, out.py, out.pw, out.ph = C.double(t.Pane.X), C.double(t.Pane.Y), C.double(t.Pane.W), C.double(t.Pane.H)
	if t.Focused {
		out.focused = 1
	}
	out.focus = C.int(t.Focus)
}

//export rexwvKey
func rexwvKey(chord *C.char) C.int {
	if paneKey(C.GoString(chord)) {
		return 1
	}
	return 0
}

//export rexwvHostActivated
func rexwvHostActivated() { paneHostActivated() }
