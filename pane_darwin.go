//go:build darwin

package rexwebview

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo"
)

// 原生窗口桥接有两份实现：开了 cgo 用 pane_darwin.m（native_cgo_darwin.go），
// 没开时用 purego 直接调 AppKit 和 CoreGraphics（native_purego_darwin.go）。两份都提供：
//
//	nativeIsMainThread() bool
//	nativeAttach(window uintptr)  // 主线程，给 MyGo 的隐藏窗口接上覆盖层
//	nativeDetach()                // 主线程
//	nativeActivateHost()          // 任意线程，异步让 Rex 到前面来
//	nativePref(key string) (string, bool)
//	nativePrefSync()
//
// 桥接层每帧调 paneTick，按键调 paneKey，Rex 到前面来时调 paneHostActivated，都在主线程。

var (
	appStarted   atomic.Bool
	paneFollower *follower // AppKit 只在主线程回调
)

func (h *Host) run(parent context.Context) error {
	if !nativeIsMainThread() {
		return errors.New("rex-webview: call Run from the main goroutine")
	}
	if !appStarted.CompareAndSwap(false, true) {
		return errors.New("rex-webview: only one Host may run per process")
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	o, f := h.opts, h.follower
	overlay := f != nil && !o.Offscreen
	app := mygo.App
	app.SetName(o.Title)
	if overlay || o.Offscreen {
		app.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	}
	var closing atomic.Bool
	requestClose := func() {
		if ctx.Err() != nil {
			return
		}
		if o.OnClose == nil {
			cancel()
		} else if closing.CompareAndSwap(false, true) {
			go func() {
				defer closing.Store(false)
				o.OnClose()
			}()
		}
	}
	if o.Menu != nil {
		app.SetMenu(o.Menu(requestClose))
	} else {
		app.SetMenu(defaultMenu(o.Title, requestClose))
	}
	off := app.OnBeforeQuit(func(e *mygo.QuitEvent) {
		if ctx.Err() == nil {
			e.PreventDefault()
			requestClose()
		}
	})
	defer off()
	var stopQuit func() bool
	app.WhenReady(func() {
		stopQuit = context.AfterFunc(ctx, app.Quit)
		if ctx.Err() != nil {
			return
		}
		win := mygo.NewWindow(o.windowOptions(overlay))
		win.OnClose(func(e *mygo.CloseEvent) {
			if ctx.Err() == nil {
				e.PreventDefault()
				requestClose()
			}
		})
		if f != nil {
			f.syncPrefs = nativePrefSync
			f.appearance = func(name string) {
				source := mygo.ThemeSource(name)
				if source != mygo.ThemeLight && source != mygo.ThemeDark {
					source = mygo.ThemeSystem
				}
				if mygo.Theme.Source() != source {
					mygo.Theme.SetSource(source)
				}
			}
			f.gone = sync.OnceFunc(func() {
				if ctx.Err() != nil {
					return
				}
				if o.OnPaneGone == nil {
					cancel()
				} else {
					go o.OnPaneGone()
				}
			})
			f.ctx = ctx
			if overlay {
				paneFollower = f
				f.activateHost = nativeActivateHost
				win.OnFocus(func() { f.work.Go(f.becameKey) })
				nativeAttach(win.NativeHandle())
			}
			f.work.Go(func() { f.run(ctx) })
		}
		if o.OnWindowCreated != nil {
			o.OnWindowCreated(win)
		}
		if !overlay && !o.Offscreen {
			win.Show()
		}
	})
	err := app.Run()
	if stopQuit != nil {
		stopQuit()
	}
	cancel()
	if paneFollower != nil {
		nativeDetach()
		paneFollower = nil
	}
	if f != nil {
		f.work.Wait()
	}
	return err
}

func (o Options) windowOptions(overlay bool) mygo.WindowOptions {
	opts := mygo.WindowOptions{
		Title: o.Title, Width: 960, Height: 640, UseContentSize: true, Transparent: overlay,
		Page: mygo.PageOptions{
			Untrusted: true, DevTools: mygo.DevToolsDisabled,
			Session: mygo.NewEphemeralSession(), BackgroundThrottling: mygo.ThrottlingOff,
		},
	}
	if !overlay && !o.Offscreen {
		opts.MinWidth, opts.MinHeight = 320, 240
	}
	if o.ConfigureWindow != nil {
		o.ConfigureWindow(&opts)
	}
	// 调用方配置页面和外观，宿主决定加载的内容、首次展示和跟随窗格的方式。
	opts.URL, opts.Content, opts.Page.SessionState, opts.Hidden = o.URL, nil, nil, true
	if overlay || o.Offscreen {
		opts.Frameless = true
		opts.FullScreen, opts.Maximized, opts.Panel, opts.Modal, opts.Parent = false, false, false, false, nil
		opts.StateKey = ""
	}
	if overlay {
		opts.FitContent, opts.AlwaysOnTop, opts.DisableShadow = false, false, true
		opts.MinWidth, opts.MinHeight, opts.MaxWidth, opts.MaxHeight = 0, 0, 0, 0
	}
	return opts
}

func defaultMenu(title string, requestClose func()) *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: title, Submenu: []*mygo.MenuItem{
			{Label: "Close", Accelerator: "Cmd+W", Click: func(*mygo.MenuItem, *mygo.Window) { requestClose() }},
			{Role: mygo.RoleQuit},
		}},
	})
}

func rexPref(name string) (string, bool) { return nativePref(name) }

func paneTick(in tickIn) tickOut {
	if f := paneFollower; f != nil {
		return f.tick(in)
	}
	return tickOut{}
}

func paneKey(chord string) bool {
	f := paneFollower
	return f != nil && f.key(chord)
}

func paneHostActivated() {
	if f := paneFollower; f != nil {
		f.refreshSoon()
	}
}
