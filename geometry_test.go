package rexwebview

import (
	"encoding/json/v2"
	"testing"
)

func TestPanePlacement(t *testing.T) {
	const layout = `{"active_window_id":"w","windows":[{"window_id":"w","focused_block_id":"pad","layers":[
		{"kind":"tiled","layout":{"rect":[0,0,1,1],"split":{"direction":"horizontal","ratio":0.5,"before":{"block_id":"left","rect":[0,0,0.5,1]},"after":{"block_id":"pad","rect":[0.5,0,0.5,1]}}}},
		{"kind":"floating","bounds":[0,0,0.5,1],"layout":{"block_id":"float","rect":[0,0,1,1]}}
	]}]}`
	var view map[string]any
	if err := json.Unmarshal([]byte(layout), &view); err != nil {
		t.Fatal(err)
	}
	check := func(want *placement) {
		t.Helper()
		got := place(view, "pad", false)
		if (got == nil) != (want == nil) || (got != nil && *got != *want) {
			t.Fatalf("place = %+v; want %+v", got, want)
		}
	}
	right := &placement{Rect: rect{0.5, 0, 0.5, 1}, Focused: true, gapOffset: rect{0.5, 0, -0.5, 0}}
	check(right) // 浮层恰好贴着左边，不遮住右窗格
	win := objects(view["windows"])[0]
	overlay := objects(win["layers"])[1]
	overlay["bounds"] = []any{0.25, 0.0, 0.5, 1.0}
	check(nil)
	overlay["state"] = "minimized"
	check(right)
	win["zoomed_block_id"] = "left"
	check(nil)
	win["zoomed_block_id"] = "pad"
	check(&placement{Rect: rect{0, 0, 1, 1}, Focused: true})
	view["active_window_id"] = "other"
	check(nil)
	if !viewContains(view, "pad") {
		t.Error("switching tabs made the follower think its pane was closed")
	}
}

// Rex API 18 返回的分屏 rect 已经包含浮层的位置和尺寸。升降浮层会改变 layers 的顺序，
// 单独 focus_block 却不会，所以即使被遮住的块有焦点也要隐藏。
func TestFloatingSplitPlacement(t *testing.T) {
	const layout = `{"active_window_id":"w","windows":[{"window_id":"w","focused_block_id":"right","layers":[
		{"kind":"tiled","layout":{"block_id":"base","rect":[0,0,1,1]}},
		{"kind":"floating","bounds":[0.125,0.25,0.5,0.625],"layout":{"rect":[0.125,0.25,0.5,0.625],"split":{"direction":"horizontal",
		  "before":{"rect":[0.125,0.25,0.125,0.625],"block_id":"left"},"after":{"rect":[0.25,0.25,0.375,0.625],"block_id":"right"}}}},
		{"kind":"floating","bounds":[0.375,0.375,0.25,0.25],"layout":{"rect":[0.375,0.375,0.25,0.25],"block_id":"top"}}
	]}]}`
	var view map[string]any
	if err := json.Unmarshal([]byte(layout), &view); err != nil {
		t.Fatal(err)
	}
	check := func(block string, enabled bool, want *placement) {
		t.Helper()
		got := place(view, block, enabled)
		if (got == nil) != (want == nil) || (got != nil && *got != *want) {
			t.Fatalf("place(%q, floating=%v) = %+v; want %+v", block, enabled, got, want)
		}
	}
	right := &placement{Rect: rect{0.25, 0.25, 0.375, 0.625}, Focused: true, floating: true, gapOffset: rect{0.75, 0, -0.75, 0}}
	check("right", true, nil)
	check("left", true, &placement{Rect: rect{0.125, 0.25, 0.125, 0.625}, floating: true, gapOffset: rect{0, 0, -0.25, 0}})
	win := objects(view["windows"])[0]
	layers := win["layers"].([]any)
	layers[1], layers[2] = layers[2], layers[1]
	check("right", true, right)
	check("right", false, nil)
	check("top", true, nil)
	layers[2].(map[string]any)["state"] = "minimized"
	check("right", true, nil)
	if !viewContains(view, "right") {
		t.Fatal("minimized pane reported gone")
	}
	check("top", true, &placement{Rect: rect{0.375, 0.375, 0.25, 0.25}, floating: true})
	delete(layers[2].(map[string]any), "state")
	win["zoomed_block_id"] = "right"
	check("right", true, &placement{Rect: rect{0, 0, 1, 1}, Focused: true})
}

func TestPaneBody(t *testing.T) {
	prefs := map[string]string{"tabStyle": "vertical", "sidebarPosition": "right", "sidebarWidth": "240"}
	c := chromeFrom(func(key string) (string, bool) { v, ok := prefs[key]; return v, ok })
	// 主窗口左上角在 (100, 50)，扣去边距、右侧栏、窗格间隙和标题栏后才是正文。
	got := c.body(placement{Rect: rect{0.5, 0.5, 0.5, 0.5}, gapOffset: rect{0.5, 0.5, -0.5, -0.5}}, rect{100, 50, 1048, 616})
	if want := (rect{513, 398, 394, 259}); got != want {
		t.Errorf("pane body = %+v; want %+v", got, want)
	}
}
