package rexwebview

import (
	"context"
	"errors"
	"testing"
	"time"
)

type focusRex struct {
	waitingRex
	view func(context.Context) (map[string]any, error)
}

func (r focusRex) Call(ctx context.Context, _ string, _ any) (map[string]any, error) {
	return r.view(ctx)
}

func focusedView(t *testing.T) map[string]any {
	return decodeView(t, `{"active_window_id":"w","windows":[{"window_id":"w","focused_block_id":"web","layers":[
		{"kind":"tiled","layout":{"block_id":"web","rect":[0,0,1,1]}}
	]}]}`)
}

func TestFocusReclaimRequiresFreshLayout(t *testing.T) {
	view := focusedView(t)
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	f := newFollower("s", "web", focusRex{view: func(ctx context.Context) (map[string]any, error) {
		close(started)
		select {
		case <-release:
			return view, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}, func(string) (string, bool) { return "", false })
	f.loadPrefs()
	now := time.Unix(1, 0)
	f.now = func() time.Time { return now }
	go func() { f.refresh(t.Context()); close(done) }()
	<-started
	f.requestFocusAfterRefresh() // 面板关掉，但之前的查询还没返回
	close(release)
	<-done
	now = now.Add(time.Second)
	in := tickIn{HostFound: true, Host: rect{0, 0, 1000, 700}, HostActive: true}
	if out := f.tick(in); !out.Visible || out.Focus != focusKeep {
		t.Fatalf("an in-flight query reclaimed stale focus: %+v", out)
	}
	f.rex = focusRex{view: func(context.Context) (map[string]any, error) { return view, nil }}
	f.refresh(t.Context())
	if out := f.tick(in); out.Focus != focusReclaim {
		t.Fatalf("a fresh focused layout did not reclaim focus: %+v", out)
	}
	if out := f.tick(in); out.Focus != focusKeep {
		t.Fatal("one request reclaimed focus more than once")
	}
}

func TestFocusReclaimRespectsNewFocusAndVisibility(t *testing.T) {
	for _, scenario := range []string{"focus moved", "other session", "query failed", "settling"} {
		t.Run(scenario, func(t *testing.T) {
			view := focusedView(t)
			var queryErr error
			f := newFollower("s", "web", focusRex{view: func(context.Context) (map[string]any, error) {
				return view, queryErr
			}}, func(string) (string, bool) { return "", false })
			f.loadPrefs()
			now := time.Unix(1, 0)
			f.now = func() time.Time { return now }
			f.apply(view)
			now = now.Add(time.Second)
			in := tickIn{HostFound: true, Host: rect{0, 0, 1000, 700}, HostActive: true}
			f.tick(in)
			f.requestFocusAfterRefresh()
			switch scenario {
			case "focus moved":
				objects(view["windows"])[0]["focused_block_id"] = "other"
			case "other session":
				f.shown = "other"
			case "query failed":
				queryErr = errors.New("Rex unavailable")
			case "settling":
				f.settleUntil = now.Add(settleTime)
			}
			f.refresh(t.Context())
			if out := f.tick(in); out.Focus != focusKeep {
				t.Fatalf("%s reclaimed focus: %+v", scenario, out)
			}
			if scenario == "settling" {
				now = now.Add(settleTime)
				if out := f.tick(in); !out.Visible || out.Focus != focusReclaim {
					t.Fatalf("focus did not return once layout settled: %+v", out)
				}
			}
		})
	}
}

func TestFocusReclaimWaitsForForwardedShortcut(t *testing.T) {
	view := focusedView(t)
	f := newFollower("s", "web", focusRex{view: func(context.Context) (map[string]any, error) { return view, nil }}, func(string) (string, bool) { return "", false })
	f.loadPrefs()
	now := time.Unix(1, 0)
	f.now = func() time.Time { return now }
	f.apply(view)
	now = now.Add(time.Second)
	in := tickIn{HostFound: true, Host: rect{0, 0, 1000, 700}, HostActive: true}
	f.tick(in)
	f.requestFocusAfterRefresh()
	f.refresh(t.Context())
	in.ReclaimBlocked = true
	if out := f.tick(in); out.Focus != focusKeep {
		t.Fatal("focus reclaimed during a forwarded shortcut's protection period")
	}
	in.ReclaimBlocked = false
	if out := f.tick(in); out.Focus != focusReclaim {
		t.Fatal("the shortcut protection consumed the pending focus reclaim")
	}
	if out := f.tick(in); out.Focus != focusKeep {
		t.Fatal("focus reclaimed more than once")
	}
}
