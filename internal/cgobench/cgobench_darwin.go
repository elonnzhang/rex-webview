//go:build darwin && cgo

// Package cgobench 用 cgo 做和 rex-webview 窗口桥接一样的原生调用，只给基准测试用，
// 和 native_purego_darwin.go 的同名操作对比。
package cgobench

/*
#cgo CFLAGS: -fobjc-arc
#cgo LDFLAGS: -framework Cocoa
#include "cgobench_darwin.h"
*/
import "C"

// PickHost 找最前面的 Rex 窗口，后面的函数都用它。找不到返回 false。
func PickHost() bool { return C.cgobench_pick_host() != 0 }

// MsgSend 发一次 [NSApp isActive]。
func MsgSend() { C.cgobench_msg_send() }

// HostIsActive 看 Rex 是不是前台应用。
func HostIsActive() { C.cgobench_host_is_active() }

// HostBounds 查 Rex 窗口的位置，写进 r（x, y, w, h）。
func HostBounds(r *[4]float64) bool { return C.cgobench_host_bounds((*C.double)(&r[0])) != 0 }

// Covered 看有没有 Rex 的别的窗口压住 host 里的一块区域。
func Covered(host *[4]float64) { C.cgobench_covered((*C.double)(&host[0])) }

var calls int

//export cgobenchCallback
func cgobenchCallback() { calls++ }

// Callbacks 让 C 代码回调 Go n 次。
func Callbacks(n int) { C.cgobench_callbacks(C.int(n)) }
