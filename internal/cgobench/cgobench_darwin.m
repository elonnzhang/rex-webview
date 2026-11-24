// 和 pane_darwin.m 里对应方法的逻辑一样，只是拿出来单独计时。
#import <Cocoa/Cocoa.h>

#include "cgobench_darwin.h"
#include "_cgo_export.h"

static CGWindowID host;
static pid_t hostPid;

static BOOL bounds(NSDictionary *item, CGRect *out) {
    id dict = item[(__bridge NSString *)kCGWindowBounds];
    return [dict isKindOfClass:NSDictionary.class] &&
           CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)dict, out);
}

static int number(NSDictionary *item, CFStringRef key) {
    id v = item[(__bridge NSString *)key];
    return [v isKindOfClass:NSNumber.class] ? [v intValue] : -1;
}

static NSArray<NSDictionary *> *windows(void) {
    return CFBridgingRelease(
        CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID));
}

int cgobench_pick_host(void) {
    @autoreleasepool {
        for (NSDictionary *item in windows()) {
            NSString *owner = item[(__bridge NSString *)kCGWindowOwnerName];
            if (![owner isKindOfClass:NSString.class]) continue;
            if (![owner isEqualToString:@"Rex"] && ![owner hasPrefix:@"Rex "]) continue;
            CGRect b;
            if (number(item, kCGWindowLayer) != 0 || !bounds(item, &b)) continue;
            if (b.size.width <= 200 || b.size.height <= 150) continue;
            host = (CGWindowID)number(item, kCGWindowNumber);
            hostPid = (pid_t)number(item, kCGWindowOwnerPID);
            return 1;
        }
        return 0;
    }
}

int cgobench_msg_send(void) { return NSApplication.sharedApplication.isActive; }

int cgobench_host_is_active(void) {
    @autoreleasepool {
        return NSWorkspace.sharedWorkspace.frontmostApplication.processIdentifier == hostPid;
    }
}

int cgobench_host_bounds(double *out) {
    @autoreleasepool {
        NSArray<NSDictionary *> *info =
            CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionIncludingWindow, host));
        NSDictionary *item = info.firstObject;
        if (item == nil || ![item[(__bridge NSString *)kCGWindowIsOnscreen] boolValue]) return 0;
        return bounds(item, (CGRect *)out);
    }
}

int cgobench_covered(const double *hostBounds) {
    @autoreleasepool {
        CGRect h = *(const CGRect *)hostBounds;
        CGRect body = CGRectMake(h.origin.x + 10, h.origin.y + 40, 300, 300);
        for (NSDictionary *item in windows()) {
            if ((CGWindowID)number(item, kCGWindowNumber) == host) return 0;
            CGRect b;
            if (number(item, kCGWindowOwnerPID) != hostPid || number(item, kCGWindowLayer) != 0 || !bounds(item, &b))
                continue;
            if (CGRectEqualToRect(b, h)) return 0;
            if (CGRectIntersectsRect(b, body)) return 1;
        }
        return 0;
    }
}

void cgobench_callbacks(int n) {
    for (int i = 0; i < n; i++) cgobenchCallback();
}
