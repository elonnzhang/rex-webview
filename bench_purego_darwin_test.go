//go:build darwin && !cgo

package rexwebview

// native_purego_darwin.go 里每帧要做的操作。cgo 版本见 bench_cgo_darwin_test.go，基准名字一一对应。

import (
	"testing"

	"github.com/ebitengine/purego/objc"
)

func benchPane(b *testing.B) *purePane {
	loadNative()
	p := &purePane{}
	if !p.pickHost() {
		b.Skip("no Rex window on screen")
	}
	return p
}

func BenchmarkMsgSend(b *testing.B) {
	loadNative()
	app := nsApp()
	for b.Loop() {
		msgBool(app, "isActive")
	}
}

func BenchmarkHostIsActive(b *testing.B) {
	p := benchPane(b)
	for b.Loop() {
		withPool(func() { p.hostIsActive() })
	}
}

func BenchmarkHostBounds(b *testing.B) {
	p := benchPane(b)
	var r cgRect
	for b.Loop() {
		withPool(func() { p.hostBounds(&r) })
	}
}

func BenchmarkCovered(b *testing.B) {
	p := benchPane(b)
	var host cgRect
	p.hostBounds(&host)
	body := cgRect{cgPoint{host.Origin.X + 10, host.Origin.Y + 40}, cgSize{300, 300}}
	for b.Loop() {
		withPool(func() { p.coveredByHost(body, host) })
	}
}

// AppKit 回调 Go 的开销：让 NSArray 对每个元素调一次 purego block。
func BenchmarkCallback(b *testing.B) {
	loadNative()
	const n = 1000
	arr := msgID(msgID(class("NSMutableArray"), "alloc"), "init")
	defer msg(arr, "release")
	item := msgID(class("NSObject"), "new")
	defer msg(item, "release")
	for range n {
		msg(arr, "addObject:", uintptr(item))
	}
	count := 0
	block := objc.NewBlock(func(_ objc.Block, _ id, _ uint, _ *bool) { count++ })
	defer block.Release()
	for b.Loop() {
		msg(arr, "enumerateObjectsUsingBlock:", uintptr(block))
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*n), "ns/callback")
}
