//go:build darwin && cgo

package rexwebview

// 和 bench_purego_darwin_test.go 同名的基准，用 cgo 做同样的原生调用（逻辑照 pane_darwin.m）。

import (
	"testing"

	"github.com/elonnzhang/rex-webview/internal/cgobench"
)

func needHost(b *testing.B) {
	if !cgobench.PickHost() {
		b.Skip("no Rex window on screen")
	}
}

func BenchmarkMsgSend(b *testing.B) {
	for b.Loop() {
		cgobench.MsgSend()
	}
}

func BenchmarkHostIsActive(b *testing.B) {
	needHost(b)
	for b.Loop() {
		cgobench.HostIsActive()
	}
}

func BenchmarkHostBounds(b *testing.B) {
	needHost(b)
	var r [4]float64
	for b.Loop() {
		cgobench.HostBounds(&r)
	}
}

func BenchmarkCovered(b *testing.B) {
	needHost(b)
	var host [4]float64
	cgobench.HostBounds(&host)
	for b.Loop() {
		cgobench.Covered(&host)
	}
}

func BenchmarkCallback(b *testing.B) {
	const n = 1000
	for b.Loop() {
		cgobench.Callbacks(n)
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*n), "ns/callback")
}
