// MyGo 管窗口和网页。覆盖层在 Rex 窗口内定位，跟随窗格的大小、层级和键盘焦点。
#import <Cocoa/Cocoa.h>

#import "pane_darwin.h"
#import "_cgo_export.h"

static CFStringRef const kRexDomain = CFSTR("com.superlogical.rex");

@interface RexWVPane : NSObject
@property(nonatomic, strong) NSWindow *window;
@end

static RexWVPane *pane;

@implementation RexWVPane {
    // Rex 的窗口
    CGWindowID _host;
    pid_t _hostPid;
    BOOL _hasHost;

    NSTimer *_timer;
    id _keyMonitor;
    unsigned long _ticks;
    BOOL _shown;
    BOOL _covered;        // Rex 自己的别的窗口（命令面板、对话框）盖在窗格上
    BOOL _focusWhenShown; // 一显示出来就接管键盘
    BOOL _focused;        // 窗格是不是 Rex 里有焦点的那个
    NSRect _lastFrame;
    CGRect _pane;         // 整个窗格连同标题栏，原点在主屏幕左上角
    id _clickMonitor;     // 全局鼠标监听
    CGPoint _click;       // 最近一次点在别的应用里的位置，原点在主屏幕左上角
    NSDate *_clickAt;
    NSDate *_forwardedAt; // 上一次把键转给 Rex 的时候
}

// ---- 窗口列表 ----

// 屏幕上的窗口，从前到后。
static NSArray<NSDictionary *> *rexwv_windows(void) {
    return CFBridgingRelease(
        CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID));
}

static BOOL rexwv_bounds(NSDictionary *item, CGRect *out) {
    id dict = item[(__bridge NSString *)kCGWindowBounds];
    return [dict isKindOfClass:NSDictionary.class] &&
           CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)dict, out);
}

static int rexwv_int(NSDictionary *item, CFStringRef key) {
    id v = item[(__bridge NSString *)key];
    return [v isKindOfClass:NSNumber.class] ? [v intValue] : -1;
}

// 找 Rex 的窗口：最前面的那个。
- (BOOL)pickHost {
    for (NSDictionary *item in rexwv_windows()) {
        NSString *owner = item[(__bridge NSString *)kCGWindowOwnerName];
        if (![owner isKindOfClass:NSString.class]) continue;
        if (![owner isEqualToString:@"Rex"] && ![owner hasPrefix:@"Rex "]) continue;
        CGRect bounds;
        if (rexwv_int(item, kCGWindowLayer) != 0 || !rexwv_bounds(item, &bounds)) continue;
        if (bounds.size.width <= 200 || bounds.size.height <= 150) continue;
        _host = (CGWindowID)rexwv_int(item, kCGWindowNumber);
        _hostPid = (pid_t)rexwv_int(item, kCGWindowOwnerPID);
        _hasHost = YES;
        return YES;
    }
    _hasHost = NO;
    return NO;
}

// Rex 的窗口现在在哪，不在屏幕上了返回 NO。
- (BOOL)hostBounds:(CGRect *)out {
    if (!_hasHost) return NO;
    NSArray<NSDictionary *> *info =
        CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionIncludingWindow, _host));
    NSDictionary *item = info.firstObject;
    if (item == nil || ![item[(__bridge NSString *)kCGWindowIsOnscreen] boolValue]) return NO;
    return rexwv_bounds(item, out);
}

// Rex 有没有别的窗口在它的主窗口前面、压着 body：命令面板、对话框，或者第二个 Rex 窗口。
// 它们要留在上面、拿着键盘，网页窗口得让开。
- (BOOL)coveredByHost:(CGRect)body hostBounds:(CGRect)hostBounds {
    for (NSDictionary *item in rexwv_windows()) {
        CGWindowID number = (CGWindowID)rexwv_int(item, kCGWindowNumber);
        // 列表是从前到后的：到了 Rex 的窗口，后面的就压不着它了
        if (number == _host) return NO;
        CGRect bounds;
        if (rexwv_int(item, kCGWindowOwnerPID) != _hostPid || rexwv_int(item, kCGWindowLayer) != 0 || !rexwv_bounds(item, &bounds))
            continue;
        // Rex 的主窗口有时是叠在一起、一样大的两层。前面那层不算压着，改成跟着它
        if (CGRectEqualToRect(bounds, hostBounds)) {
            _host = number;
            return NO;
        }
        if (CGRectIntersectsRect(bounds, body)) return YES;
    }
    return NO;
}

// 网页窗口的窗口和 Rex 的窗口之间是不是只隔着网页窗口自己的窗口。
- (BOOL)directlyAboveHost {
    NSArray<NSDictionary *> *above =
        CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenAboveWindow, _host));
    CGWindowID mine = (CGWindowID)self.window.windowNumber;
    pid_t me = getpid();
    // above 是从前到后的，从紧挨着 Rex 的那个往前看
    for (NSDictionary *item in above.reverseObjectEnumerator) {
        if ((CGWindowID)rexwv_int(item, kCGWindowNumber) == mine) return YES;
        if (rexwv_int(item, kCGWindowLayer) != 0) continue;
        if (rexwv_int(item, kCGWindowOwnerPID) != me) return NO;
    }
    return NO;
}

- (BOOL)hostIsActive {
    return _hasHost && NSWorkspace.sharedWorkspace.frontmostApplication.processIdentifier == _hostPid;
}

// ---- 跟着窗格 ----

- (void)hide {
    if (!_shown) return;
    [self.window orderOut:nil];
    _shown = NO;
}

- (void)follow {
    _ticks++;
    BOOL selfActive = NSApp.isActive, hostActive = [self hostIsActive];
    // Rex 的窗口只在 Rex（或者网页窗口）在前面时才会动，别的时候一秒看几次就够了
    BOOL watching = selfActive || hostActive;
    if (!watching && _ticks % 10 != 0) return;

    CGRect host = CGRectZero;
    BOOL found = [self hostBounds:&host];
    if (!found && [self pickHost]) {
        found = [self hostBounds:&host];
        hostActive = [self hostIsActive];
    }
    RexWVTick t = {0};
    rexwvTick(found, host.origin.x, host.origin.y, host.size.width, host.size.height, hostActive, selfActive, &t);
    _focused = t.focused != 0;
    _pane = CGRectMake(t.px, t.py, t.pw, t.ph);
    if (!_focused || !hostActive) _focusWhenShown = NO;
    if (t.focus == 1) {
        _focusWhenShown = YES;
    } else if (t.focus == 2) {
        [self activateHost];
    }
    NSScreen *primary = NSScreen.screens.firstObject;
    if (!t.visible || !found || primary == nil) {
        [self hide];
        return;
    }
    CGRect body = CGRectMake(t.x, t.y, t.w, t.h);
    if (!watching || _ticks % 3 == 0) {
        BOOL covered = [self coveredByHost:body hostBounds:host];
        // 命令面板关掉了，焦点还在这个窗格上：把键盘接回来
        if (!covered && _covered && _focused && hostActive) _focusWhenShown = YES;
        _covered = covered;
    }
    if (_covered) {
        [self hide];
        return;
    }
    // AppKit 的原点在主屏幕的左下角
    NSRect frame = NSMakeRect(round(body.origin.x), round(primary.frame.size.height - CGRectGetMaxY(body)),
                              round(body.size.width), round(body.size.height));
    if (!NSEqualRects(frame, _lastFrame)) {
        [self.window setFrame:frame display:YES];
        _lastFrame = frame;
    }
    if (self.window.contentView.layer.cornerRadius != t.radius) self.window.contentView.layer.cornerRadius = t.radius;
    if (!_shown) {
        self.window.alphaValue = 0;
        [self.window orderWindow:NSWindowAbove relativeTo:_host];
        [NSAnimationContext runAnimationGroup:^(NSAnimationContext *context) {
            context.duration = 0.12;
            self.window.animator.alphaValue = 1;
        }];
    } else if (_ticks % 10 == 0 && ![self directlyAboveHost]) {
        [self.window orderWindow:NSWindowAbove relativeTo:_host];
    }
    _shown = YES;
    if (_focusWhenShown) {
        _focusWhenShown = NO;
        if (_focused && [self hostIsActive]) [self takeFocus];
    }
}

// ---- 键盘在谁手里 ----

- (void)takeFocus {
    if (!_shown) {
        _focusWhenShown = YES;
        return;
    }
    [NSApp activateIgnoringOtherApps:YES];
    [self.window makeKeyWindow];
    if (self.window.firstResponder == nil || self.window.firstResponder == self.window)
        [self.window selectNextKeyView:nil];
}

- (void)activateHost {
    if (!_hasHost) return;
    NSRunningApplication *app = [NSRunningApplication runningApplicationWithProcessIdentifier:_hostPid];
    if (app == nil) return;
    if (@available(macOS 14.0, *)) [NSApp yieldActivationToApplication:app];
    [app activateWithOptions:0];
}

// 最近 0.75 秒里点过鼠标、点在这个窗格外面：用户是点了 Rex 的侧栏、别的窗格才回到 Rex 的。
// 点击由全局鼠标监听记下来，它和激活通知谁先到不一定，所以等激活 0.25 秒后再问。
- (BOOL)clickedOutside {
    if (_clickAt == nil || -_clickAt.timeIntervalSinceNow > 0.75) return NO;
    return !CGRectContainsPoint(_pane, _click);
}

- (void)recordClick {
    NSScreen *primary = NSScreen.screens.firstObject;
    if (primary == nil) return;
    NSPoint m = NSEvent.mouseLocation; // 原点在主屏幕左下角
    _click = CGPointMake(m.x, primary.frame.size.height - m.y);
    _clickAt = [NSDate date];
}

- (void)appActivated:(NSNotification *)note {
    NSRunningApplication *app = note.userInfo[NSWorkspaceApplicationKey];
    if (!_hasHost || app.processIdentifier != _hostPid) return;
    // Rex 把它的窗口提到了网页窗口上面，马上回到上面去
    if (_shown) [self.window orderWindow:NSWindowAbove relativeTo:_host];
    rexwvHostActivated();
    // 回到 Rex 时焦点还在这个窗格上，键盘就该回到网页窗口手里。等一小会儿再看：
    // 也可能是点了 Rex 里别的窗格才到前面来的，那样焦点马上就不在这里了
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(0.25 * NSEC_PER_SEC)), dispatch_get_main_queue(), ^{
        if (!self->_shown || self->_covered || !self->_focused || ![self hostIsActive]) return;
        // 点了 Rex 里别处（比如侧栏里的 tab）：键盘留给 Rex。抢回来的话，这一下点击白点，tab 切不过去
        if ([self clickedOutside]) return;
        if (self->_forwardedAt != nil && -self->_forwardedAt.timeIntervalSinceNow < 1) return;
        [self takeFocus];
    });
}

// 一个键按 Rex 的写法写出来，比如 shift+cmd+p。修饰键的顺序和 geometry.go 的 chordMods 一样。
static NSString *rexwv_chord(NSEvent *event) {
    NSMutableArray<NSString *> *parts = [NSMutableArray array];
    NSEventModifierFlags flags = event.modifierFlags;
    if (flags & NSEventModifierFlagControl) [parts addObject:@"ctrl"];
    if (flags & NSEventModifierFlagOption) [parts addObject:@"alt"];
    if (flags & NSEventModifierFlagShift) [parts addObject:@"shift"];
    if (flags & NSEventModifierFlagCommand) [parts addObject:@"cmd"];
    NSString *key = nil;
    switch (event.keyCode) {
        case 123: key = @"left"; break;
        case 124: key = @"right"; break;
        case 125: key = @"down"; break;
        case 126: key = @"up"; break;
        case 49: key = @"space"; break;
        case 36: case 76: key = @"enter"; break;
        case 48: key = @"tab"; break;
        case 53: key = @"escape"; break;
        case 51: key = @"backspace"; break;
        case 117: key = @"delete"; break;
        case 115: key = @"home"; break;
        case 119: key = @"end"; break;
        case 116: key = @"pageup"; break;
        case 121: key = @"pagedown"; break;
    }
    if (key == nil) {
        // 不带修饰键时这个键打出来的字：shift+cmd+p 是 p，不是 P
        key = [event charactersByApplyingModifiers:0].lowercaseString;
        if (key.length == 0) key = event.charactersIgnoringModifiers.lowercaseString;
    }
    if (key.length == 0) return nil;
    [parts addObject:key];
    return [parts componentsJoinedByString:@"+"];
}

// Rex 绑了的键交给 Rex，返回 YES；别的留给页面和菜单。
- (BOOL)handleKey:(NSEvent *)event {
    if (event.window != self.window) return NO;
    NSString *chord = rexwv_chord(event);
    if (chord == nil || !rexwvKey((char *)chord.UTF8String)) return NO;
    _forwardedAt = [NSDate date];
    return YES;
}

// 只在覆盖层模式下接管窗口的摆放。MyGo 仍持有窗口和页面的 delegate。
- (void)attach:(NSWindow *)window {
    self.window = window;
    window.styleMask = NSWindowStyleMaskBorderless;
    window.collectionBehavior = NSWindowCollectionBehaviorTransient |
                                NSWindowCollectionBehaviorFullScreenAuxiliary |
                                NSWindowCollectionBehaviorIgnoresCycle;
    window.contentView.wantsLayer = YES;
    window.contentView.layer.masksToBounds = YES;
    _keyMonitor = [NSEvent addLocalMonitorForEventsMatchingMask:NSEventMaskKeyDown handler:^NSEvent *(NSEvent *event) {
        return [self handleKey:event] ? nil : event;
    }];
    // 点在别的应用里的鼠标。全局监听鼠标事件不需要辅助功能权限
    _clickMonitor = [NSEvent addGlobalMonitorForEventsMatchingMask:NSEventMaskLeftMouseDown | NSEventMaskRightMouseDown |
                                                                   NSEventMaskOtherMouseDown
                                                           handler:^(NSEvent *event) {
                                                               [self recordClick];
                                                           }];
    [self pickHost];
    [NSWorkspace.sharedWorkspace.notificationCenter addObserver:self
                                                      selector:@selector(appActivated:)
                                                          name:NSWorkspaceDidActivateApplicationNotification
                                                        object:nil];
    _timer = [NSTimer timerWithTimeInterval:1.0 / 60 target:self selector:@selector(follow) userInfo:nil repeats:YES];
    [NSRunLoop.mainRunLoop addTimer:_timer forMode:NSRunLoopCommonModes];
}

- (void)detach {
    _shown = NO;
    [_timer invalidate];
    _timer = nil;
    if (_keyMonitor != nil) [NSEvent removeMonitor:_keyMonitor];
    _keyMonitor = nil;
    if (_clickMonitor != nil) [NSEvent removeMonitor:_clickMonitor];
    _clickMonitor = nil;
    [NSWorkspace.sharedWorkspace.notificationCenter removeObserver:self];
    self.window = nil;
}

@end

int rexwv_is_main_thread(void) {
    return NSThread.isMainThread;
}

void rexwv_detach_overlay(void) {
    [pane detach];
    pane = nil;
}

void rexwv_attach_overlay(uintptr_t window) {
    pane = [RexWVPane new];
    [pane attach:(__bridge NSWindow *)(void *)window];
}

void rexwv_activate_host(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        [pane activateHost];
    });
}

char *rexwv_pref(const char *key) {
    @autoreleasepool {
        NSString *name = [@"Workspace." stringByAppendingString:@(key)];
        id value = CFBridgingRelease(CFPreferencesCopyAppValue((__bridge CFStringRef)name, kRexDomain));
        NSString *text = nil;
        if ([value isKindOfClass:NSString.class]) {
            text = value;
        } else if ([value isKindOfClass:NSNumber.class]) {
            text = [value stringValue]; // 开关是 1 和 0
        } else if ([value isKindOfClass:NSData.class]) {
            text = [[NSString alloc] initWithData:value encoding:NSUTF8StringEncoding];
        }
        return text == nil ? NULL : strdup(text.UTF8String);
    }
}

void rexwv_pref_sync(void) {
    CFPreferencesAppSynchronize(kRexDomain);
}
