package rexwebview

import (
	"encoding/json/v2"
	"testing"
	"time"
)

// 从 session.view 一路测到交给 AppKit 的屏幕矩形；浮层外框不是分屏接缝。
func TestFloatingFrame(t *testing.T) {
	const snapshot = `{"active_window_id":"w","windows":[{"window_id":"w","layers":[
		{"kind":"tiled","bounds":[0,0,1,1],"layout":{"block_id":"base","rect":[0,0,1,1]}},
		{"kind":"floating","bounds":[0.125,0.25,0.5,0.5],"layout":{"block_id":"web","rect":[0.125,0.25,0.5,0.5]}}
	]}]}`
	decode := func(raw string) map[string]any {
		t.Helper()
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	view := decode(snapshot)
	prefs := map[string]string{"tabStyle": "vertical", "sidebarPosition": "right", "sidebarWidth": "323", "toolbarBehavior": "alwaysShow"}
	f := newFollower("s", "web", nil, func(k string) (string, bool) { v, ok := prefs[k]; return v, ok })
	f.floatingPanes = true
	f.loadPrefs()
	win := objects(view["windows"])[0]
	layer := objects(win["layers"])[1]
	check := func(label string, host, want rect) {
		t.Helper()
		now := time.Unix(1, 0)
		f.now = func() time.Time { return now }
		f.apply(view)
		now = now.Add(time.Second)
		out := f.tick(tickIn{HostFound: true, Host: host})
		if !out.Visible || out.Frame != want {
			t.Errorf("%s: frame = %+v, visible %t; want %+v", label, out.Frame, out.Visible, want)
		}
		// 窗格连同标题栏：点在这里面不算点了 Rex 的别处
		if pane := (rect{want.X, want.Y - f.chrome.Header, want.W, want.H + f.chrome.Header}); out.Pane != pane {
			t.Errorf("%s: pane = %+v; want %+v", label, out.Pane, pane)
		}
	}
	// 布局区宽 1600、高 1000。浮层外框 (308,342)-(1108,842)，留 1 点原生描边和 36 点标题栏。
	host := rect{100, 50, 1931, 1050}
	check("whole float", host, rect{309, 378, 798, 463})
	layer["layout"] = decode(`{"rect":[0.125,0.25,0.5,0.5],"split":{"direction":"horizontal","ratio":0.5,
		"before":{"block_id":"other","rect":[0.125,0.25,0.25,0.5]},"after":{"block_id":"web","rect":[0.375,0.25,0.25,0.5]}}}`)
	check("right child keeps the internal gap", host, rect{713, 378, 394, 463})
	// 1:3 分割时，右窗格从 (800-8)/4+8 = 206 点处开始；旧算法算成 204。
	layer["state"] = "minimized"
	base := objects(win["layers"])[0]
	base["layout"] = decode(`{"rect":[0,0,1,1],"split":{"direction":"horizontal","ratio":0.25,
		"before":{"block_id":"other","rect":[0,0,0.25,1]},"after":{"block_id":"web","rect":[0.25,0,0.75,1]}}}`)
	check("off-center tiled divider", rect{100, 50, 1131, 650}, rect{315, 94, 592, 597})
	// 外层在 1/4 处分左右，右子树再上下对分；两个方向的接缝分别计算。
	base["layout"] = decode(`{"rect":[0,0,1,1],"split":{"direction":"horizontal","ratio":0.25,
		"before":{"block_id":"left","rect":[0,0,0.25,1]},"after":{"rect":[0.25,0,0.75,1],"split":{"direction":"vertical","ratio":0.5,
		"before":{"block_id":"top","rect":[0.25,0,0.75,0.5]},"after":{"block_id":"web","rect":[0.25,0.5,0.75,0.5]}}}}}`)
	check("nested bottom right", rect{100, 50, 1131, 650}, rect{315, 415, 592, 276})
	// 几何信息不污染 Where 的比例输出。
	if f.place.Rect != (rect{0.25, 0.5, 0.75, 0.5}) {
		t.Fatal(f.place)
	}
	delete(layer, "state")
	base["layout"] = nil
	// 已放大的浮窗走全布局算法，不借用浮层的外框或标题区。
	win["zoomed_block_id"] = "web"
	check("zoom", host, rect{109, 94, 1598, 997})
	delete(win, "zoomed_block_id")
	prefs["paneHeaderVisibility"] = "never"
	f.loadPrefs()
	check("hidden header on a smaller window", rect{100, 50, 1131, 650}, rect{413, 243, 194, 298})
	prefs["sidebarPosition"] = "left"
	f.loadPrefs()
	check("left sidebar", rect{100, 50, 1131, 650}, rect{728, 243, 194, 298})
	prefs["paneDensity"] = "compact"
	f.loadPrefs()
	check("compact left sidebar", rect{100, 50, 1131, 650}, rect{728, 239, 199, 306})
}
