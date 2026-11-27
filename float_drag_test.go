package rexwebview

import (
	"context"
	"testing"
	"time"
)

func dragFollower() *follower {
	f := newFollower("s", "web", nil, nil)
	f.floatingPanes = true
	f.chrome = chrome{Header: 36}
	f.place = &placement{Rect: rect{0.1, 0.2, 0.5, 0.5}, floating: true, layerID: "float"}
	return f
}

func TestFloatDragHeaderGrip(t *testing.T) {
	frame := rect{100, 200, 400, 300}
	for _, tc := range []struct {
		name string
		x, y float64
		want bool
	}{
		{"title", 200, 180, true},
		{"page", 200, 200, false},
		{"top resize edge", 200, 168, false},
		{"left resize edge", 103, 180, false},
		{"right resize edge", 496, 180, false},
		{"outside", 510, 180, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := dragFollower()
			if got := f.beginFloatDrag(tc.x, tc.y, frame); got != tc.want {
				t.Fatalf("beginFloatDrag = %v, want %v", got, tc.want)
			}
		})
	}
	for _, scenario := range []string{"tiled", "hidden header", "other session", "no placement"} {
		f := dragFollower()
		switch scenario {
		case "tiled":
			f.place.floating = false
		case "hidden header":
			f.chrome.Header = 0
		case "other session":
			f.shown = "other"
		case "no placement":
			f.place = nil
		}
		if f.beginFloatDrag(200, 180, frame) {
			t.Errorf("%s started a float drag", scenario)
		}
	}
}

func TestFloatDragFollowsPointerBeforeLayoutReply(t *testing.T) {
	f := dragFollower() // 没有 Rex API：拖动路径不能同步查询布局。
	now := time.Unix(1, 0)
	f.now = func() time.Time { return now }
	frame := rect{-500, 200, 400, 300} // 也覆盖主屏幕左侧的显示器坐标
	if !f.beginFloatDrag(-400, 180, frame) {
		t.Fatal("title drag did not start")
	}
	now = now.Add(time.Second)
	if !f.holdFloatDrag() {
		t.Fatal("a held header press must protect the gesture before its first movement")
	}
	for _, tc := range []struct {
		x, y float64
		want rect
	}{{-380.4, 169.6, rect{-480, 190, 400, 300}}, {-340.6, 230.6, rect{-441, 251, 400, 300}}} {
		got, ok := f.moveFloatDrag(tc.x, tc.y, false)
		if !ok || got != tc.want || !f.holdFloatDrag() {
			t.Fatalf("drag lost its preview: %+v, active=%v", got, ok)
		}
	}
	// 松手的位置可能和最后一次 dragged 事件不同；先移到松手位置，再请求布局。
	got, ok := f.moveFloatDrag(-330, 240, true)
	if want := (rect{-430, 260, 400, 300}); !ok || got != want {
		t.Fatalf("mouse-up frame = %+v, active=%v; want %+v", got, ok, want)
	}
	select {
	case <-f.kick:
	default:
		t.Fatal("mouse-up did not request a layout refresh")
	}
	if !f.holdFloatDrag() {
		t.Fatal("old layout pulled the page back before Rex replied")
	}
	if _, ok := f.moveFloatDrag(-320, 250, false); ok {
		t.Fatal("a released drag accepted another mouse move")
	}
	f.place.Rect.X = 0.2 // Rex 现在确认了落点
	f.appliedSeq = f.drag.releaseSeq
	if f.holdFloatDrag() {
		t.Fatal("confirmed layout did not resume normal tracking")
	}
}

func dragView(t *testing.T, x float64) map[string]any {
	v := decodeView(t, `{"active_window_id":"w","windows":[{"window_id":"w","layers":[
		{"kind":"floating","layer_id":"float","bounds":[0.1,0.2,0.5,0.5],"layout":{"block_id":"web","rect":[0.1,0.2,0.5,0.5]}}
	]}]}`)
	layer := objects(objects(v["windows"])[0]["layers"])[0]
	layer["bounds"].([]any)[0] = x
	layer["layout"].(map[string]any)["rect"].([]any)[0] = x
	return v
}

func TestFloatDragIgnoresPreviousDragQuery(t *testing.T) {
	f := dragFollower()
	f.now = func() time.Time { return time.Unix(1, 0) }
	frame := rect{100, 200, 400, 300}
	f.beginFloatDrag(200, 180, frame)
	f.moveFloatDrag(250, 180, true)
	<-f.kick
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	oldView := dragView(t, 0.2)
	f.rex = focusRex{view: func(ctx context.Context) (map[string]any, error) {
		close(started)
		select {
		case <-release:
			return oldView, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	go func() { f.refresh(t.Context()); close(done) }()
	<-started
	// 第二次拖动先松手，第一次拖动的查询才返回。
	f.beginFloatDrag(200, 180, frame)
	f.moveFloatDrag(300, 180, true)
	close(release)
	<-done
	if !f.holdFloatDrag() {
		t.Fatal("the previous drag's query ended the current preview")
	}
	newView := dragView(t, 0.3)
	f.rex = focusRex{view: func(context.Context) (map[string]any, error) { return newView, nil }}
	f.refresh(t.Context())
	if f.holdFloatDrag() {
		t.Fatal("a fresh query confirming the current drag did not resume tracking")
	}
}

func TestFloatDragIgnoresIntermediatePositionAtRelease(t *testing.T) {
	f := dragFollower()
	f.now = func() time.Time { return time.Unix(1, 0) }
	f.beginFloatDrag(200, 180, rect{100, 200, 400, 300})
	f.moveFloatDrag(250, 180, false)
	view := dragView(t, 0.2)
	f.rex = focusRex{view: func(context.Context) (map[string]any, error) { return view, nil }}
	f.refresh(t.Context()) // 拖动过程中已经收到了中间位置
	f.moveFloatDrag(300, 180, true)
	if !f.holdFloatDrag() {
		t.Fatal("a mid-drag position ended the release grace period")
	}
	f.refresh(t.Context()) // 松手后的查询仍报告相同的中间位置
	if !f.holdFloatDrag() {
		t.Fatal("the unchanged intermediate position was treated as the final position")
	}
	view = dragView(t, 0.3)
	f.refresh(t.Context())
	if f.holdFloatDrag() {
		t.Fatal("the confirmed final position did not resume tracking")
	}
}

func TestFloatDragReleaseTimeoutAndCancellation(t *testing.T) {
	for _, scenario := range []string{"timeout", "session changed", "pane hidden", "different layer", "new click", "detach"} {
		t.Run(scenario, func(t *testing.T) {
			f := dragFollower()
			now := time.Unix(1, 0)
			f.now = func() time.Time { return now }
			frame := rect{100, 200, 400, 300}
			f.beginFloatDrag(200, 180, frame)
			switch scenario {
			case "timeout":
				f.moveFloatDrag(250, 230, true)
				now = now.Add(floatDragReleaseGrace - time.Millisecond)
				if !f.holdFloatDrag() {
					t.Fatal("release grace ended too early")
				}
				now = now.Add(time.Millisecond)
			case "session changed":
				f.shown = "other"
			case "pane hidden":
				f.place = nil
			case "different layer":
				f.place.layerID = "different"
			case "new click":
				f.beginFloatDrag(200, 250, frame) // 新点击不在标题栏
			case "detach":
				f.cancelFloatDrag()
			}
			if f.holdFloatDrag() {
				t.Fatalf("%s kept a stale drag alive", scenario)
			}
			if _, ok := f.moveFloatDrag(300, 280, true); ok {
				t.Fatal("canceled drag moved the window on mouse-up")
			}
		})
	}
}

func BenchmarkFloatDragMove(b *testing.B) {
	f := dragFollower()
	f.beginFloatDrag(200, 180, rect{100, 200, 400, 300})
	b.ReportAllocs()
	for b.Loop() {
		f.moveFloatDrag(250, 230, false)
	}
}
