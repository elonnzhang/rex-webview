//go:build darwin && !cgo

package rexwebview

// 不开 cgo 时的窗口桥接：做的事和 pane_darwin.m 一样，用 purego 直接发 Objective-C 消息、
// 调 CoreGraphics 和 CoreFoundation。MyGo 自己的 macOS 后端也是这么做的。
// 这里的方法除了 nativePref、nativePrefSync、nativeActivateHost，都只在主线程调用。

import (
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
)

type id = objc.ID

type cgPoint struct{ X, Y float64 }
type cgSize struct{ W, H float64 }
type cgRect struct {
	Origin cgPoint
	Size   cgSize
}

const (
	cgListOnScreenOnly          = 1 << 0
	cgListOnScreenAboveWindow   = 1 << 1
	cgListIncludingWindow       = 1 << 3
	cgListExcludeDesktopElement = 1 << 4

	nsWindowAbove      = 1
	nsEventMaskKeyDown = 1 << 10
	// 左、右、其他鼠标键按下
	nsEventMaskMouseDown = 1<<1 | 1<<3 | 1<<25
	nsUTF8Encoding       = 4

	nsCollectionTransient           = 1 << 3
	nsCollectionIgnoresCycle        = 1 << 6
	nsCollectionFullScreenAuxiliary = 1 << 8
	nsModShift                      = 1 << 17
	nsModControl                    = 1 << 18
	nsModOption                     = 1 << 19
	nsModCommand                    = 1 << 20
)

var (
	msgSendFn, poolPushFn, poolPopFn uintptr

	// 带浮点数或结构体的消息走 RegisterFunc 的包装，别的走 SyscallN，少一层反射
	msgFloat    func(obj id, sel objc.SEL) float64
	msgSetFloat func(obj id, sel objc.SEL, v float64)
	msgRect     func(obj id, sel objc.SEL) cgRect
	msgSetFrame func(obj id, sel objc.SEL, r cgRect, display bool)
	msgTimer    func(cls id, sel objc.SEL, interval float64, repeats bool, block objc.Block) id

	cgWindowListCopyWindowInfo func(option, relativeTo uint32) uintptr
	cgRectFromDict             func(dict uintptr, out *cgRect) bool
	msgPoint                   func(obj id, sel objc.SEL) cgPoint
	cfRelease                  func(ref uintptr)
	cfPrefCopyAppValue         func(key, app id) uintptr
	cfPrefAppSynchronize       func(app id) bool

	kWindowNumber, kWindowOwnerName, kWindowOwnerPID, kWindowLayer, kWindowBounds, kWindowIsOnscreen id
	kDidActivateApp, kWorkspaceAppKey, kRunLoopCommonModes                                           id
	rexDomain                                                                                        id

	selMu      sync.Mutex
	selCache   = map[string]objc.SEL{}
	classCache = map[string]id{}
)

var loadNative = sync.OnceFunc(func() {
	lib := func(path string) uintptr {
		h, err := purego.Dlopen(path, purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			panic("rex-webview: " + err.Error())
		}
		return h
	}
	sym := func(h uintptr, name string) uintptr {
		p, err := purego.Dlsym(h, name)
		if err != nil {
			panic("rex-webview: " + err.Error())
		}
		return p
	}
	// 导出的 NSString / CFStringRef 常量：符号地址里存着对象指针
	constant := func(h uintptr, name string) id {
		p := sym(h, name)
		return **(**id)(unsafe.Pointer(&p))
	}
	libObjC := lib("/usr/lib/libobjc.A.dylib")
	libCF := lib("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
	libCG := lib("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics")
	libFoundation := lib("/System/Library/Frameworks/Foundation.framework/Foundation")
	libAppKit := lib("/System/Library/Frameworks/AppKit.framework/AppKit")

	msgSendFn = sym(libObjC, "objc_msgSend")
	poolPushFn = sym(libObjC, "objc_autoreleasePoolPush")
	poolPopFn = sym(libObjC, "objc_autoreleasePoolPop")
	// amd64 上大于 16 字节的结构体返回值走 objc_msgSend_stret
	stret := msgSendFn
	if runtime.GOARCH == "amd64" {
		stret = sym(libObjC, "objc_msgSend_stret")
	}
	purego.RegisterFunc(&msgFloat, msgSendFn)
	purego.RegisterFunc(&msgSetFloat, msgSendFn)
	purego.RegisterFunc(&msgRect, stret)
	purego.RegisterFunc(&msgSetFrame, msgSendFn)
	purego.RegisterFunc(&msgTimer, msgSendFn)

	purego.RegisterLibFunc(&cgWindowListCopyWindowInfo, libCG, "CGWindowListCopyWindowInfo")
	purego.RegisterLibFunc(&cgRectFromDict, libCG, "CGRectMakeWithDictionaryRepresentation")
	purego.RegisterFunc(&msgPoint, msgSendFn)
	purego.RegisterLibFunc(&cfRelease, libCF, "CFRelease")
	purego.RegisterLibFunc(&cfPrefCopyAppValue, libCF, "CFPreferencesCopyAppValue")
	purego.RegisterLibFunc(&cfPrefAppSynchronize, libCF, "CFPreferencesAppSynchronize")

	kWindowNumber = constant(libCG, "kCGWindowNumber")
	kWindowOwnerName = constant(libCG, "kCGWindowOwnerName")
	kWindowOwnerPID = constant(libCG, "kCGWindowOwnerPID")
	kWindowLayer = constant(libCG, "kCGWindowLayer")
	kWindowBounds = constant(libCG, "kCGWindowBounds")
	kWindowIsOnscreen = constant(libCG, "kCGWindowIsOnscreen")
	kDidActivateApp = constant(libAppKit, "NSWorkspaceDidActivateApplicationNotification")
	kWorkspaceAppKey = constant(libAppKit, "NSWorkspaceApplicationKey")
	kRunLoopCommonModes = constant(libFoundation, "NSRunLoopCommonModes")
	rexDomain = newNSString("com.superlogical.rex") // 进程里一直用，不释放
})

// ---- Objective-C ----

func sel(name string) objc.SEL {
	selMu.Lock()
	defer selMu.Unlock()
	s, ok := selCache[name]
	if !ok {
		s = objc.RegisterName(name)
		selCache[name] = s
	}
	return s
}

func class(name string) id {
	selMu.Lock()
	defer selMu.Unlock()
	c, ok := classCache[name]
	if !ok {
		c = id(objc.GetClass(name))
		classCache[name] = c
	}
	return c
}

// msg 发一条参数和返回值都是整数或指针的消息。
func msg(obj id, name string, args ...uintptr) uintptr {
	all := make([]uintptr, 0, 2+len(args))
	all = append(all, uintptr(obj), uintptr(sel(name)))
	r, _, _ := purego.SyscallN(msgSendFn, append(all, args...)...)
	return r
}

func msgID(obj id, name string, args ...uintptr) id { return id(msg(obj, name, args...)) }

// BOOL 只看最低一个字节
func msgBool(obj id, name string, args ...uintptr) bool { return byte(msg(obj, name, args...)) != 0 }

func boolArg(b bool) uintptr {
	if b {
		return 1
	}
	return 0
}

func isKind(obj id, className string) bool {
	return obj != 0 && msgBool(obj, "isKindOfClass:", uintptr(class(className)))
}

// newNSString 返回一个要自己 release 的 NSString。
func newNSString(s string) id {
	b := append([]byte(s), 0)
	str := msgID(msgID(class("NSString"), "alloc"), "initWithBytes:length:encoding:",
		uintptr(unsafe.Pointer(&b[0])), uintptr(len(s)), nsUTF8Encoding)
	runtime.KeepAlive(b)
	return str
}

func goString(str id) string {
	if str == 0 {
		return ""
	}
	p := msg(str, "UTF8String")
	if p == 0 {
		return ""
	}
	n := msg(str, "lengthOfBytesUsingEncoding:", nsUTF8Encoding)
	return string(unsafe.Slice(*(**byte)(unsafe.Pointer(&p)), n))
}

// withPool 在当前线程上开一个 autorelease pool。不在主线程时要先锁住线程，pool 是跟线程走的。
func withPool(fn func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool, _, _ := purego.SyscallN(poolPushFn)
	defer purego.SyscallN(poolPopFn, pool)
	fn()
}

func nsApp() id { return msgID(class("NSApplication"), "sharedApplication") }

// afterOnMain 过 d 秒在主线程跑一次 fn。
func afterOnMain(d float64, fn func()) {
	block := objc.NewBlock(func(objc.Block, id) { withPool(fn) })
	timer := msgTimer(class("NSTimer"), sel("timerWithTimeInterval:repeats:block:"), d, false, block)
	block.Release() // 定时器自己拷了一份
	msg(msgID(class("NSRunLoop"), "mainRunLoop"), "addTimer:forMode:", uintptr(timer), uintptr(kRunLoopCommonModes))
}

// ---- 窗口列表 ----

// eachWindow 把 CGWindowListCopyWindowInfo 的结果从前到后交给 fn，fn 返回 false 就停。
// reverse 是从后到前。
func eachWindow(option, relativeTo uint32, reverse bool, fn func(item id) bool) {
	list := cgWindowListCopyWindowInfo(option, relativeTo)
	if list == 0 {
		return
	}
	defer cfRelease(list)
	arr := id(list)
	n := int(msg(arr, "count"))
	for i := range n {
		if reverse {
			i = n - 1 - i
		}
		if !fn(msgID(arr, "objectAtIndex:", uintptr(i))) {
			return
		}
	}
}

func windowInt(item, key id) int {
	v := msgID(item, "objectForKey:", uintptr(key))
	if !isKind(v, "NSNumber") {
		return -1
	}
	return int(int32(msg(v, "intValue")))
}

func windowBounds(item id, out *cgRect) bool {
	v := msgID(item, "objectForKey:", uintptr(kWindowBounds))
	return isKind(v, "NSDictionary") && cgRectFromDict(uintptr(v), out)
}

// ---- 跟着窗格 ----

type purePane struct {
	window   id
	host     uint32 // Rex 的窗口
	hostPid  int32
	hasHost  bool
	timer    id
	monitor  id
	observer id
	ticks    uint64
	shown    bool
	covered  bool // Rex 自己的别的窗口（命令面板、对话框）盖在窗格上
	// 一显示出来就接管键盘
	focusWhenShown bool
	focused        bool // 窗格是不是 Rex 里有焦点的那个
	lastFrame      cgRect
	pane           rect      // 整个窗格连同标题栏，原点在主屏幕左上角
	clicks         id        // 全局鼠标监听
	click          cgPoint   // 最近一次点在别的应用里的位置，原点在主屏幕左上角
	clickAt        time.Time // 那次点击的时间
	forwardedAt    time.Time // 上一次把键转给 Rex 的时候
}

var nativePane *purePane // 只在主线程读写

// pickHost 找 Rex 的窗口：最前面的那个。
func (p *purePane) pickHost() bool {
	p.hasHost = false
	eachWindow(cgListOnScreenOnly|cgListExcludeDesktopElement, 0, false, func(item id) bool {
		owner := msgID(item, "objectForKey:", uintptr(kWindowOwnerName))
		if !isKind(owner, "NSString") {
			return true
		}
		name := goString(owner)
		if name != "Rex" && !strings.HasPrefix(name, "Rex ") {
			return true
		}
		var b cgRect
		if windowInt(item, kWindowLayer) != 0 || !windowBounds(item, &b) || b.Size.W <= 200 || b.Size.H <= 150 {
			return true
		}
		p.host = uint32(windowInt(item, kWindowNumber))
		p.hostPid = int32(windowInt(item, kWindowOwnerPID))
		p.hasHost = true
		return false
	})
	return p.hasHost
}

// hostBounds 是 Rex 的窗口现在在哪，不在屏幕上了返回 false。
func (p *purePane) hostBounds(out *cgRect) bool {
	if !p.hasHost {
		return false
	}
	ok := false
	eachWindow(cgListIncludingWindow, p.host, false, func(item id) bool {
		v := msgID(item, "objectForKey:", uintptr(kWindowIsOnscreen))
		ok = v != 0 && msgBool(v, "boolValue") && windowBounds(item, out)
		return false
	})
	return ok
}

// coveredByHost 看 Rex 有没有别的窗口在它的主窗口前面、压着 body：命令面板、对话框，或者第二个 Rex 窗口。
// 它们要留在上面、拿着键盘，网页窗口得让开。
func (p *purePane) coveredByHost(body, hostBounds cgRect) bool {
	covered := false
	eachWindow(cgListOnScreenOnly|cgListExcludeDesktopElement, 0, false, func(item id) bool {
		number := uint32(windowInt(item, kWindowNumber))
		// 列表是从前到后的：到了 Rex 的窗口，后面的就压不着它了
		if number == p.host {
			return false
		}
		var b cgRect
		if int32(windowInt(item, kWindowOwnerPID)) != p.hostPid || windowInt(item, kWindowLayer) != 0 || !windowBounds(item, &b) {
			return true
		}
		// Rex 的主窗口有时是叠在一起、一样大的两层。前面那层不算压着，改成跟着它
		if b == hostBounds {
			p.host = number
			return false
		}
		if intersects(b, body) {
			covered = true
			return false
		}
		return true
	})
	return covered
}

func intersects(a, b cgRect) bool {
	return a.Size.W > 0 && a.Size.H > 0 && b.Size.W > 0 && b.Size.H > 0 &&
		a.Origin.X < b.Origin.X+b.Size.W && b.Origin.X < a.Origin.X+a.Size.W &&
		a.Origin.Y < b.Origin.Y+b.Size.H && b.Origin.Y < a.Origin.Y+a.Size.H
}

// directlyAboveHost 看网页窗口和 Rex 的窗口之间是不是只隔着网页窗口自己的窗口。
func (p *purePane) directlyAboveHost() bool {
	mine := uint32(msg(p.window, "windowNumber"))
	me := int32(os.Getpid())
	above := false
	// 列表是从前到后的，从紧挨着 Rex 的那个往前看
	eachWindow(cgListOnScreenAboveWindow, p.host, true, func(item id) bool {
		if uint32(windowInt(item, kWindowNumber)) == mine {
			above = true
			return false
		}
		if windowInt(item, kWindowLayer) != 0 {
			return true
		}
		return int32(windowInt(item, kWindowOwnerPID)) == me
	})
	return above
}

func (p *purePane) hostIsActive() bool {
	if !p.hasHost {
		return false
	}
	front := msgID(msgID(class("NSWorkspace"), "sharedWorkspace"), "frontmostApplication")
	return front != 0 && int32(msg(front, "processIdentifier")) == p.hostPid
}

func (p *purePane) hide() {
	if !p.shown {
		return
	}
	msg(p.window, "orderOut:", 0)
	p.shown = false
}

func (p *purePane) orderAboveHost() {
	msg(p.window, "orderWindow:relativeTo:", nsWindowAbove, uintptr(p.host))
}

func (p *purePane) follow() {
	p.ticks++
	selfActive, hostActive := msgBool(nsApp(), "isActive"), p.hostIsActive()
	// Rex 的窗口只在 Rex（或者网页窗口）在前面时才会动，别的时候一秒看几次就够了
	watching := selfActive || hostActive
	if !watching && p.ticks%10 != 0 {
		return
	}
	var host cgRect
	found := p.hostBounds(&host)
	if !found && p.pickHost() {
		found = p.hostBounds(&host)
		hostActive = p.hostIsActive()
	}
	t := paneTick(tickIn{
		HostFound: found, Host: rect{host.Origin.X, host.Origin.Y, host.Size.W, host.Size.H},
		HostActive: hostActive, SelfActive: selfActive,
	})
	p.focused = t.Focused
	p.pane = t.Pane
	if !p.focused || !hostActive {
		p.focusWhenShown = false
	}
	switch t.Focus {
	case focusTake:
		p.focusWhenShown = true
	case focusHost:
		p.activateHost()
	}
	screens := msgID(class("NSScreen"), "screens")
	if !t.Visible || !found || screens == 0 || msg(screens, "count") == 0 {
		p.hide()
		return
	}
	primary := msgRect(msgID(screens, "objectAtIndex:", 0), sel("frame"))
	body := cgRect{cgPoint{t.Frame.X, t.Frame.Y}, cgSize{t.Frame.W, t.Frame.H}}
	if !watching || p.ticks%3 == 0 {
		covered := p.coveredByHost(body, host)
		// 命令面板关掉了，焦点还在这个窗格上：把键盘接回来
		if !covered && p.covered && p.focused && hostActive {
			p.focusWhenShown = true
		}
		p.covered = covered
	}
	if p.covered {
		p.hide()
		return
	}
	// AppKit 的原点在主屏幕的左下角
	frame := cgRect{
		cgPoint{roundHalfAway(body.Origin.X), roundHalfAway(primary.Size.H - (body.Origin.Y + body.Size.H))},
		cgSize{roundHalfAway(body.Size.W), roundHalfAway(body.Size.H)},
	}
	if frame != p.lastFrame {
		msgSetFrame(p.window, sel("setFrame:display:"), frame, true)
		p.lastFrame = frame
	}
	if layer := msgID(msgID(p.window, "contentView"), "layer"); layer != 0 && msgFloat(layer, sel("cornerRadius")) != t.Radius {
		msgSetFloat(layer, sel("setCornerRadius:"), t.Radius)
	}
	if !p.shown {
		msgSetFloat(p.window, sel("setAlphaValue:"), 0)
		p.orderAboveHost()
		win := p.window
		block := objc.NewBlock(func(_ objc.Block, ctx id) {
			msgSetFloat(ctx, sel("setDuration:"), 0.12)
			msgSetFloat(msgID(win, "animator"), sel("setAlphaValue:"), 1)
		})
		msg(class("NSAnimationContext"), "runAnimationGroup:", uintptr(block))
		block.Release()
	} else if p.ticks%10 == 0 && !p.directlyAboveHost() {
		p.orderAboveHost()
	}
	p.shown = true
	if p.focusWhenShown {
		p.focusWhenShown = false
		if p.focused && p.hostIsActive() {
			p.takeFocus()
		}
	}
}

// roundHalfAway 和 C 的 round 一样，.5 往远离 0 的方向取整。
func roundHalfAway(v float64) float64 {
	if v < 0 {
		return -float64(int64(-v + 0.5))
	}
	return float64(int64(v + 0.5))
}

// ---- 键盘在谁手里 ----

func (p *purePane) takeFocus() {
	if !p.shown {
		p.focusWhenShown = true
		return
	}
	msg(nsApp(), "activateIgnoringOtherApps:", 1)
	msg(p.window, "makeKeyWindow")
	if r := msgID(p.window, "firstResponder"); r == 0 || r == p.window {
		msg(p.window, "selectNextKeyView:", 0)
	}
}

func (p *purePane) activateHost() {
	if !p.hasHost {
		return
	}
	app := msgID(class("NSRunningApplication"), "runningApplicationWithProcessIdentifier:", uintptr(p.hostPid))
	if app == 0 {
		return
	}
	// macOS 14 起要先把激活权让给 Rex
	if msgBool(nsApp(), "respondsToSelector:", uintptr(sel("yieldActivationToApplication:"))) {
		msg(nsApp(), "yieldActivationToApplication:", uintptr(app))
	}
	msg(app, "activateWithOptions:", 0)
}

// clickedOutside 看最近 0.75 秒里有没有点过鼠标、点在这个窗格外面：用户是点了 Rex 的侧栏、别的窗格才回到 Rex 的。
// 点击由全局鼠标监听记下来，它和激活通知谁先到不一定，所以等激活 0.25 秒后再问。
func (p *purePane) clickedOutside() bool {
	if p.clickAt.IsZero() || time.Since(p.clickAt) > 750*time.Millisecond {
		return false
	}
	x, y := p.click.X, p.click.Y
	in := x >= p.pane.X && x < p.pane.maxX() && y >= p.pane.Y && y < p.pane.maxY()
	return !in
}

// recordClick 记下一次点在别的应用里的鼠标，坐标换成原点在主屏幕左上角。
func (p *purePane) recordClick() {
	screens := msgID(class("NSScreen"), "screens")
	if screens == 0 || msg(screens, "count") == 0 {
		return
	}
	primary := msgRect(msgID(screens, "objectAtIndex:", 0), sel("frame"))
	m := msgPoint(class("NSEvent"), sel("mouseLocation")) // 原点在主屏幕左下角
	p.click, p.clickAt = cgPoint{m.X, primary.Size.H - m.Y}, time.Now()
}

func (p *purePane) appActivated(note id) {
	info := msgID(note, "userInfo")
	if info == 0 || !p.hasHost {
		return
	}
	app := msgID(info, "objectForKey:", uintptr(kWorkspaceAppKey))
	if app == 0 || int32(msg(app, "processIdentifier")) != p.hostPid {
		return
	}
	// Rex 把它的窗口提到了网页窗口上面，马上回到上面去
	if p.shown {
		p.orderAboveHost()
	}
	paneHostActivated()
	// 回到 Rex 时焦点还在这个窗格上，键盘就该回到网页窗口手里。等一小会儿再看：
	// 也可能是点了 Rex 里别的窗格才到前面来的，那样焦点马上就不在这里了
	afterOnMain(0.25, func() {
		if nativePane != p || !p.shown || p.covered || !p.focused || !p.hostIsActive() {
			return
		}
		// 点了 Rex 里别处（比如侧栏里的 tab）：键盘留给 Rex。抢回来的话，这一下点击白点，tab 切不过去
		if p.clickedOutside() {
			return
		}
		if !p.forwardedAt.IsZero() && time.Since(p.forwardedAt) < time.Second {
			return
		}
		p.takeFocus()
	})
}

var namedKeys = map[uint16]string{
	123: "left", 124: "right", 125: "down", 126: "up", 49: "space", 36: "enter", 76: "enter",
	48: "tab", 53: "escape", 51: "backspace", 117: "delete", 115: "home", 119: "end",
	116: "pageup", 121: "pagedown",
}

// eventChord 把一个键按 Rex 的写法写出来，比如 shift+cmd+p。修饰键的顺序和 geometry.go 的 chordMods 一样。
func eventChord(event id) string {
	flags := msg(event, "modifierFlags")
	var parts []string
	for _, m := range []struct {
		bit  uintptr
		name string
	}{
		{nsModControl, "ctrl"},
		{nsModOption, "alt"},
		{nsModShift, "shift"},
		{nsModCommand, "cmd"},
	} {
		if flags&m.bit != 0 {
			parts = append(parts, m.name)
		}
	}
	key, ok := namedKeys[uint16(msg(event, "keyCode"))]
	if !ok {
		// 不带修饰键时这个键打出来的字：shift+cmd+p 是 p，不是 P
		key = goString(msgID(msgID(event, "charactersByApplyingModifiers:", 0), "lowercaseString"))
		if key == "" {
			key = goString(msgID(msgID(event, "charactersIgnoringModifiers"), "lowercaseString"))
		}
	}
	if key == "" {
		return ""
	}
	parts = append(parts, key)
	return strings.Join(parts, "+")
}

// handleKey 把 Rex 绑了的键交给 Rex，返回 true；别的留给页面和菜单。
func (p *purePane) handleKey(event id) bool {
	if msgID(event, "window") != p.window {
		return false
	}
	chord := eventChord(event)
	if chord == "" || !paneKey(chord) {
		return false
	}
	p.forwardedAt = time.Now()
	return true
}

// attach 只在覆盖层模式下接管窗口的摆放。MyGo 仍持有窗口和页面的 delegate。
func (p *purePane) attach(window id) {
	// 留一份引用，和 pane_darwin.m 的 strong 属性一样。退出时 MyGo 先放掉窗口，定时器还会再跑几次
	p.window = msgID(window, "retain")
	msg(window, "setStyleMask:", 0) // NSWindowStyleMaskBorderless
	msg(window, "setCollectionBehavior:", nsCollectionTransient|nsCollectionFullScreenAuxiliary|nsCollectionIgnoresCycle)
	content := msgID(window, "contentView")
	msg(content, "setWantsLayer:", 1)
	msg(msgID(content, "layer"), "setMasksToBounds:", 1)

	// 这些 block 都由 AppKit 拷走保存，这边的引用马上放掉
	keys := objc.NewBlock(func(_ objc.Block, event id) id {
		if nativePane == p && p.handleKey(event) {
			return 0
		}
		return event
	})
	p.monitor = msgID(msgID(class("NSEvent"), "addLocalMonitorForEventsMatchingMask:handler:", nsEventMaskKeyDown, uintptr(keys)), "retain")
	keys.Release()

	// 点在别的应用里的鼠标。全局监听鼠标事件不需要辅助功能权限
	clicked := objc.NewBlock(func(_ objc.Block, _ id) {
		if nativePane == p {
			p.recordClick()
		}
	})
	p.clicks = msgID(msgID(class("NSEvent"), "addGlobalMonitorForEventsMatchingMask:handler:", nsEventMaskMouseDown, uintptr(clicked)), "retain")
	clicked.Release()

	p.pickHost()

	activated := objc.NewBlock(func(_ objc.Block, note id) {
		if nativePane == p {
			p.appActivated(note)
		}
	})
	center := msgID(msgID(class("NSWorkspace"), "sharedWorkspace"), "notificationCenter")
	p.observer = msgID(msgID(center, "addObserverForName:object:queue:usingBlock:",
		uintptr(kDidActivateApp), 0, uintptr(msgID(class("NSOperationQueue"), "mainQueue")), uintptr(activated)), "retain")
	activated.Release()

	tick := objc.NewBlock(func(objc.Block, id) {
		if nativePane == p {
			p.follow()
		}
	})
	p.timer = msgID(msgTimer(class("NSTimer"), sel("timerWithTimeInterval:repeats:block:"), 1.0/60, true, tick), "retain")
	tick.Release()
	msg(msgID(class("NSRunLoop"), "mainRunLoop"), "addTimer:forMode:", uintptr(p.timer), uintptr(kRunLoopCommonModes))
}

func (p *purePane) detach() {
	p.shown = false
	if p.timer != 0 {
		msg(p.timer, "invalidate")
		msg(p.timer, "release")
		p.timer = 0
	}
	for _, m := range []*id{&p.monitor, &p.clicks} {
		if *m != 0 {
			msg(class("NSEvent"), "removeMonitor:", uintptr(*m))
			msg(*m, "release")
			*m = 0
		}
	}
	if p.observer != 0 {
		center := msgID(msgID(class("NSWorkspace"), "sharedWorkspace"), "notificationCenter")
		msg(center, "removeObserver:", uintptr(p.observer))
		msg(p.observer, "release")
		p.observer = 0
	}
	if p.window != 0 {
		msg(p.window, "release")
		p.window = 0
	}
}

// ---- pane_darwin.go 用到的入口 ----

func nativeIsMainThread() bool {
	loadNative()
	return msgBool(class("NSThread"), "isMainThread")
}

func nativeAttach(window uintptr) {
	loadNative()
	withPool(func() {
		nativePane = &purePane{}
		nativePane.attach(id(window))
	})
}

func nativeDetach() {
	if nativePane == nil {
		return
	}
	withPool(nativePane.detach)
	nativePane = nil
}

func nativeActivateHost() {
	// 和 dispatch_async 一样不等它做完；在主线程上调也不在当前的事件处理里做
	go mygo.RunOnMain(func() {
		if p := nativePane; p != nil {
			withPool(p.activateHost)
		}
	})
}

// nativePref 读 Rex App 的设置 Workspace.<key>。
func nativePref(key string) (text string, ok bool) {
	loadNative()
	withPool(func() {
		name := newNSString("Workspace." + key)
		defer msg(name, "release")
		ref := cfPrefCopyAppValue(name, rexDomain)
		if ref == 0 {
			return
		}
		defer cfRelease(ref)
		v := id(ref)
		switch {
		case isKind(v, "NSString"):
			text, ok = goString(v), true
		case isKind(v, "NSNumber"):
			text, ok = goString(msgID(v, "stringValue")), true // 开关是 1 和 0
		case isKind(v, "NSData"):
			s := msgID(msgID(class("NSString"), "alloc"), "initWithData:encoding:", uintptr(v), nsUTF8Encoding)
			if s != 0 {
				text, ok = goString(s), true
				msg(s, "release")
			}
		}
	})
	return text, ok
}

func nativePrefSync() {
	loadNative()
	cfPrefAppSynchronize(rexDomain)
}
