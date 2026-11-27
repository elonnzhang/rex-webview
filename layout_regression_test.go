package rexwebview

import (
	"encoding/json/v2"
	"testing"
	"time"
)

// Leaf PR #87：标签栏在哪一沿，工具栏就应占哪一沿；不能改变画布的可用尺寸。
func TestTabBarEdge(t *testing.T) {
	for _, density := range []string{"comfortable", "compact"} {
		t.Run(density, func(t *testing.T) {
			edge, toolbar := 8.0, 46.0
			if density == "compact" {
				edge, toolbar = 0, 41
			}
			prefs := map[string]string{"paneDensity": density, "tabStyle": "horizontal", "toolbarBehavior": "alwaysShow"}
			configured := func() chrome {
				return chromeFrom(func(k string) (string, bool) { v, ok := prefs[k]; return v, ok })
			}
			defaults := configured()
			prefs["tabBarEdge"] = "top"
			top := configured()
			prefs["tabBarEdge"] = "bottom"
			bottom := configured()
			if defaults != top || top.Top != toolbar || top.Bottom != edge || top.floatTop != toolbar ||
				bottom.Top != edge || bottom.Bottom != toolbar || bottom.floatTop != edge {
				t.Fatalf("top = %+v, bottom = %+v", top, bottom)
			}
			win := rect{100, 50, 1000, 700}
			for _, floating := range []bool{false, true} {
				p := placement{Rect: rect{0, 0, 1, 1}, floating: floating}
				a, b := top.body(p, win), bottom.body(p, win)
				border := 0.0
				if floating || density != "compact" {
					border = 1
				}
				if a.W != b.W || a.H != b.H || a.Y-b.Y != toolbar-edge || b.maxY() != win.maxY()-toolbar-border {
					t.Errorf("floating=%v: top body %+v, bottom body %+v", floating, a, b)
				}
			}
			prefs["toolbarBehavior"] = "autoHide"
			hidden := configured()
			if hidden.Top != edge || hidden.Bottom != edge || hidden.floatTop != edge {
				t.Fatalf("hidden toolbar reserves space: %+v", hidden)
			}
			prefs["toolbarBehavior"], prefs["tabStyle"], prefs["sidebarWidth"] = "alwaysShow", "vertical", "230"
			for _, side := range []string{"left", "right"} {
				prefs["sidebarPosition"], prefs["tabBarEdge"] = side, "top"
				a := configured()
				prefs["tabBarEdge"] = "bottom"
				b := configured()
				if a != b || b.Top != edge || b.Bottom != edge || b.floatTop != edge+34 ||
					(side == "left" && b.Left != 230) || (side == "right" && b.Right != 230) {
					t.Fatalf("%s sidebar affected by tab edge: %+v, %+v", side, a, b)
				}
			}
		})
	}
}

func decodeView(t *testing.T, raw string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestLayerOcclusionInScreenCoordinates(t *testing.T) {
	view := decodeView(t, `{"active_window_id":"w","windows":[{"window_id":"w","layers":[
		{"kind":"tiled","layout":{"block_id":"web","rect":[0,0,1,0.5]}},
		{"kind":"floating","bounds":[0,0.48,1,0.1],"layout":{"block_id":"upper","rect":[0,0.48,1,0.1]}}
	]}]}`)
	prefs := map[string]string{"tabStyle": "vertical"}
	f := newFollower("s", "web", nil, func(k string) (string, bool) { v, ok := prefs[k]; return v, ok })
	f.loadPrefs()
	now := time.Unix(1, 0)
	f.now = func() time.Time { return now }
	f.apply(view)
	now = now.Add(time.Second)
	in := tickIn{HostFound: true, Host: rect{0, 0, 1000, 716}}
	// 比例相交，但浮层画布比平铺画布低 34 点，屏幕上并不遮住正文。
	if out := f.tick(in); !out.Visible {
		t.Fatal("normalized overlap hid a pane that is exposed on screen")
	}
	upper := objects(objects(view["windows"])[0]["layers"])[1]
	upper["bounds"] = []any{0.0, 0.4, 1.0, 0.1}
	f.apply(view)
	if out := f.tick(in); out.Visible {
		t.Fatal("a floating layer covering the body did not hide the webview")
	}
	upper["state"] = "minimized"
	f.apply(view)
	if out := f.tick(in); !out.Visible {
		t.Fatal("a minimized upper layer still hides the webview")
	}
	delete(upper, "state")
	// 比例只贴边，隐藏标题后浮层画布的偏移却让它在屏幕上遮住下半窗格。
	prefs["paneHeaderVisibility"] = "never"
	f.loadPrefs()
	base := objects(objects(view["windows"])[0]["layers"])[0]
	base["layout"].(map[string]any)["rect"] = []any{0.0, 0.5, 1.0, 0.5}
	f.apply(view)
	now = now.Add(time.Second)
	if out := f.tick(in); out.Visible {
		t.Fatal("normalized edge contact missed a floating layer covering the body on screen")
	}
	delete(upper, "bounds")
	f.apply(view)
	if out := f.tick(in); out.Visible {
		t.Fatal("unknown upper-layer bounds must give way to Rex")
	}
}

func TestCompactSplitMetrics(t *testing.T) {
	c := chromeFrom(func(k string) (string, bool) { return "compact", k == "paneDensity" })
	p := placement{Rect: rect{0, 0, 0.5, 1}, gapOffset: rect{0, 0, -0.5, 0}}
	if body := c.body(p, rect{0, 0, 1000, 700}); body != (rect{0, 75, 500, 625}) {
		t.Fatalf("compact split should use a 1pt divider and 34pt header: %+v", body)
	}
}

func TestFloatingLayerFollowsWithoutSettling(t *testing.T) {
	view := decodeView(t, `{"active_window_id":"w","windows":[{"window_id":"w","layers":[
		{"kind":"floating","layer_id":"float","bounds":[0.125,0.25,0.5,0.5],"layout":{"block_id":"web","rect":[0.125,0.25,0.5,0.5]}}
	]}]}`)
	f := newFollower("s", "web", nil, func(k string) (string, bool) { return "compact", k == "paneDensity" })
	f.floatingPanes = true
	f.loadPrefs()
	now := time.Unix(1, 0)
	f.now = func() time.Time { return now }
	f.apply(view)
	now = now.Add(time.Second)
	in := tickIn{HostFound: true, Host: rect{0, 0, 1000, 700}}
	first := f.tick(in)
	layer := objects(objects(view["windows"])[0]["layers"])[0]
	for _, bounds := range [][]any{{0.25, 0.25, 0.5, 0.5}, {0.25, 0.25, 0.625, 0.625}} {
		layer["bounds"] = bounds
		layer["layout"].(map[string]any)["rect"] = bounds
		f.apply(view)
		if out := f.tick(in); !out.Visible || out.Frame == first.Frame || out.Radius != 10 {
			t.Fatalf("float movement/resize should remain visible with card corners: %+v", out)
		}
	}
	layer["layout"].(map[string]any)["rect"] = []any{0.25, 0.25, 0.3125, 0.625}
	f.apply(view)
	if out := f.tick(in); out.Visible {
		t.Fatal("a change to the pane's slot must still wait for layout animation")
	}
	now = now.Add(settleTime)
	if out := f.tick(in); !out.Visible {
		t.Fatal("the changed pane did not return after settling")
	}
}

func TestArrayLayoutAndUnknownLayer(t *testing.T) {
	view := decodeView(t, `{"active_window_id":"w","windows":[{"window_id":"w","layers":[
		{"kind":"floating","layout":[{"block_id":"web","rect":[0,0,1,1]}]}
	]}]}`)
	if !viewContains(view, "web") || place(view, "web", true) == nil {
		t.Fatal("an array layout lost its pane")
	}
	objects(objects(view["windows"])[0]["layers"])[0]["kind"] = "unknown"
	if place(view, "web", true) != nil {
		t.Fatal("an unknown layer kind was treated as a supported canvas")
	}
	if !viewContains(view, "web") {
		t.Fatal("an unsupported layer must not be reported as a closed pane")
	}
}
