//go:build darwin && cgo

package rexwebview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

// AppKit 必须从 main 运行，所以每个场景都放在独立进程的 TestMain 里。
func TestMain(m *testing.M) {
	if mode := os.Getenv("REXWEBVIEW_TEST_CHILD"); mode == "default" || mode == "callback" || mode == "pane-gone" {
		if err := offscreenScenario(mode); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestOffscreenHost(t *testing.T) {
	if os.Getenv("REXWEBVIEW_TEST_OFFSCREEN") != "1" {
		t.Skip("set REXWEBVIEW_TEST_OFFSCREEN=1 to test hidden WebKit windows")
	}
	for _, mode := range []string{"default", "callback", "pane-gone"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
			cmd.Env = append(os.Environ(), "REXWEBVIEW_TEST_CHILD="+mode, "REX_SESSION=", "REX_BLOCK=", "REX_CLIENT=", "MYGO_GENERATE=")
			if mode == "pane-gone" {
				// 输出两份都没有目标窗格的空布局，事件流本身应在 Run 退出时被杀掉。
				dir := filepath.Join(t.TempDir(), "App Support")
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				script := `#!/bin/sh
[ "$1" = --autostart=false ] || exit 2
if [ "$2" = events ]; then
    printf '%s\n' '{"payload":{"event":{"type":"layout"}}}'
    exec /bin/sleep 60
fi
printf '%s\n' '{}'
`
				if err := os.WriteFile(filepath.Join(dir, "rex"), []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
				cmd.Env = append(cmd.Env, "REX_SESSION=test", "REX_BLOCK=gone", "REX_BIN_DIR="+dir, "PATH="+dir)
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("hidden window: %v\n%s", err, out)
			}
		})
	}
}

func offscreenScenario(mode string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	o := Options{Offscreen: true, URL: "data:text/plain,Hello%20from%20Rex%20WebView!"}
	if mode == "pane-gone" {
		if err := Run(ctx, o); err != nil {
			return err
		}
		return ctx.Err()
	}
	created := make(chan *mygo.Window, 1)
	o.OnWindowCreated = func(win *mygo.Window) { created <- win }
	var accept atomic.Bool
	declined := make(chan struct{}, 1)
	if mode == "callback" {
		o.ConfigureWindow = func(w *mygo.WindowOptions) {
			w.Width, w.Height = 480, 320
			w.URL, w.Hidden = "about:blank", false // 页面和首次展示由宿主管理
		}
		o.Menu = func(requestClose func()) *mygo.Menu {
			return mygo.NewMenu([]*mygo.MenuItem{{ID: "custom", Label: "Finish", Click: func(*mygo.MenuItem, *mygo.Window) { requestClose() }}})
		}
		o.OnClose = func() {
			if accept.Load() {
				cancel()
				return
			}
			declined <- struct{}{}
		}
	}
	checked := make(chan error, 1)
	go func() {
		select {
		case <-ctx.Done():
			checked <- ctx.Err()
		case win := <-created:
			err := checkHiddenPage(ctx, win)
			if err == nil && mode == "callback" {
				err = checkCustomClose(ctx, win, declined, &accept)
			} else if err == nil {
				win.Close()
			}
			checked <- err
			if err != nil {
				cancel()
			}
		}
	}()
	if err := Run(ctx, o); err != nil {
		return err
	}
	if err := <-checked; err != nil {
		return err
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ctx.Err()
	}
	return nil
}

func checkCustomClose(ctx context.Context, win *mygo.Window, declined <-chan struct{}, accept *atomic.Bool) error {
	if w, h := win.Size(); w != 480 || h != 320 {
		return fmt.Errorf("unexpected configured size %d x %d", w, h)
	}
	item := mygo.App.Menu().ItemByID("custom")
	if item == nil {
		return errors.New("custom menu missing")
	}
	item.Click(item, win)
	select {
	case <-declined:
	case <-ctx.Done():
		return ctx.Err()
	}
	if got, err := win.Page().EvalContext(ctx, "document.body.textContent.trim()"); err != nil || got != "Hello from Rex WebView!" {
		return fmt.Errorf("page after declined close: %v, %v", got, err)
	}
	accept.Store(true)
	mygo.App.Quit()
	return nil
}

func checkHiddenPage(ctx context.Context, win *mygo.Window) error {
	if win.IsVisible() {
		return errors.New("offscreen window was shown")
	}
	for ctx.Err() == nil {
		if b, err := win.CapturePage(); err == nil {
			img, err := png.Decode(bytes.NewReader(b))
			if err != nil {
				return err
			}
			// 空白页也可能截到 PNG，至少要看到问候文字和背景两种颜色。
			bounds := img.Bounds()
			bg := img.At(bounds.Min.X, bounds.Min.Y)
			for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
				for x := bounds.Min.X; x < bounds.Max.X; x++ {
					if img.At(x, y) != bg {
						return nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(50 * time.Millisecond):
		}
	}
	return fmt.Errorf("no rendered greeting: %w", ctx.Err())
}
