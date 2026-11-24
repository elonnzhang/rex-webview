//go:build darwin

package rexwebview

// 两种构建共用的基准。和 cgo 对比时分别运行：
//
//	go test -run '^$' -bench . -benchmem .
//	CGO_ENABLED=0 go test -run '^$' -bench . -benchmem .
//
// 结果见 docs/test-report.md。查 Rex 窗口的几项要求屏幕上有 Rex 窗口，没有时跳过。

import (
	"testing"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// 读一项 Rex 设置，每秒做若干次。
func BenchmarkPref(b *testing.B) {
	for b.Loop() {
		nativePref("appearance")
	}
}

// 同步设置再读 Rex 显示的会话，每 30 ms 做一次。
func BenchmarkShownSession(b *testing.B) {
	for b.Loop() {
		nativePrefSync()
		shownSession(nativePref)
	}
}

// MyGo 的 macOS 后端两种构建下都走 purego，看 CGO_ENABLED 会不会改变它的开销。
func BenchmarkPuregoMsg(b *testing.B) {
	lib, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_GLOBAL|purego.RTLD_NOW)
	if err != nil {
		b.Fatal(err)
	}
	fn, err := purego.Dlsym(lib, "objc_msgSend")
	if err != nil {
		b.Fatal(err)
	}
	cls, s := uintptr(objc.GetClass("NSObject")), uintptr(objc.RegisterName("hash"))
	for b.Loop() {
		purego.SyscallN(fn, cls, s)
	}
}
