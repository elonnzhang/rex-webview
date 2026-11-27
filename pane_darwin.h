// Rex 窗格覆盖层，用 MyGo 窗口的 NSWindow 句柄定位和转发键盘事件。
#ifndef REXWV_PANE_DARWIN_H
#define REXWV_PANE_DARWIN_H

#include <stdint.h>

// rexwvTick 的回答，对应 follow.go 的 tickOut。
typedef struct {
    int visible;
    double x, y, w, h; // 屏幕上的位置，原点在主屏幕左上角
    double radius;
    double px, py, pw, ph; // 整个窗格连同标题栏
    int focused;
    int focus; // 0 不动，1 网页窗口接管键盘，2 还给 Rex，3 新布局确认后恢复
} RexWVTick;

// 在主线程给 MyGo 的隐藏窗口接上覆盖层。等跟到窗格再显示。
void rexwv_attach_overlay(uintptr_t window);
void rexwv_activate_host(void);
int rexwv_is_main_thread(void);
void rexwv_detach_overlay(void);
// rexwv_pref 读 Rex App 的设置 Workspace.<key>，没有时返回 NULL；返回的字符串要 free。
char *rexwv_pref(const char *key);
void rexwv_pref_sync(void);

#endif
