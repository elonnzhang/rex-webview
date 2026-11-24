//go:build darwin && cgo

package rexwebview

/*
#cgo CFLAGS: -fobjc-arc -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
#include "pane_darwin.h"
*/
import "C"

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/egoist/mygo"
)

var (
	appStarted   atomic.Bool
	paneFollower *follower // AppKit 只在主线程回调
)

func (h *Host) run(parent context.Context) error {
	if C.rexwv_is_main_thread() == 0 {
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
			f.syncPrefs = func() { C.rexwv_pref_sync() }
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
				f.activateHost = func() { C.rexwv_activate_host() }
				win.OnFocus(func() { f.work.Go(f.becameKey) })
				C.rexwv_attach_overlay(C.uintptr_t(win.NativeHandle()))
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
		C.rexwv_detach_overlay()
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

func rexPref(name string) (string, bool) {
	cs := C.CString(name)
	defer C.free(unsafe.Pointer(cs))
	v := C.rexwv_pref(cs)
	if v == nil {
		return "", false
	}
	defer C.free(unsafe.Pointer(v))
	return C.GoString(v), true
}

//export rexwvTick
func rexwvTick(found C.int, x, y, w, h C.double, hostActive, selfActive C.int, out *C.RexWVTick) {
	f := paneFollower
	if f == nil {
		return
	}
	t := f.tick(tickIn{HostFound: found != 0, Host: rect{float64(x), float64(y), float64(w), float64(h)}, HostActive: hostActive != 0, SelfActive: selfActive != 0})
	if t.Visible {
		out.visible = 1
	}
	out.x, out.y, out.w, out.h = C.double(t.Frame.X), C.double(t.Frame.Y), C.double(t.Frame.W), C.double(t.Frame.H)
	out.radius = C.double(t.Radius)
	if t.Focused {
		out.focused = 1
	}
	out.focus = C.int(t.Focus)
}

//export rexwvKey
func rexwvKey(chord *C.char) C.int {
	if f := paneFollower; f != nil && f.key(C.GoString(chord)) {
		return 1
	}
	return 0
}

//export rexwvHostActivated
func rexwvHostActivated() {
	if f := paneFollower; f != nil {
		f.refreshSoon()
	}
}
