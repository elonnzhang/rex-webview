# 测试报告：cgo 与 purego 窗口桥接

测试日期：2026-11-24。环境：Apple M4（10 核），macOS 27.3（26C5035r），Go 1.27.3 darwin/arm64，MyGo v0.10.0，purego v0.11.1，Rex App 0.1.0 (2061)。

窗口桥接有两份实现。开 cgo 时用 [pane_darwin.m](../pane_darwin.m)，`CGO_ENABLED=0` 时用 [native_purego_darwin.go](../native_purego_darwin.go)。两份的逻辑一一对应，布局计算和事件订阅都在共用的 Go 代码里。

## 2026-11-27：同步 Leaf Browser 和底部标签修正

本轮使用 Go 1.27.4 darwin/arm64，参考 Leaf 当前的 `src/browser.swift` 和 [PR #87](https://github.com/DeepanshuMishraa/leaf/pull/87)。以下性能数据仍是 2026-11-24 的记录，本轮未重测性能。

| 验证 | 结果 |
| --- | --- |
| `REXWEBVIEW_TEST_OFFSCREEN=1 REXWEBVIEW_TEST_URL_GUI=1 go test -race -count=1 ./...` | 通过，38 项检查 |
| `CGO_ENABLED=0 REXWEBVIEW_TEST_OFFSCREEN=1 REXWEBVIEW_TEST_URL_GUI=1 go test -count=1 ./...` | 通过，38 项检查 |
| `go vet ./...` 和 `CGO_ENABLED=0 go vet ./...` | 均通过 |
| `git diff --check` | 通过 |

新增回归覆盖顶部／底部横排标签、两种密度、隐藏工具栏和左右侧栏；检查平铺与浮窗的正文尺寸和下沿。紧凑分屏使用 34 点标题栏、1 点分隔条。浮窗遮挡测试覆盖比例相交但屏幕上不遮挡、比例贴边但屏幕上已遮挡、最小化以及上层范围未知。浮层移动和缩放不重新隐藏，层内叶节点的布局改变仍等待动画。

焦点回归模拟恢复请求之前已经开始的布局查询，确认旧查询不能授权恢复；新查询成功后只恢复一次。焦点移到其他窗格、切换会话、查询失败时不抢焦点，动画期间延迟到显示后恢复。两种原生实现都在 Rex 激活或面板关闭后发起新查询，并检查普通和浮动层级的 Rex 窗口；外部点击的保护按请求时间计算，避免查询慢时忘记点击。

隐藏窗口测试实际验证了 WebKit 渲染、自定义关闭和窗格消失退出；URL 示例测试访问临时本地 HTTP 服务后正常关闭。隐藏窗口不安装原生覆盖层，因此这一轮初始验证尚未在真实 Rex 桌面交互中确认浮窗拖动、面板遮挡和焦点恢复。初始实现依赖 Rex 发布布局，鼠标拖动预览的后续验证如下；浮层边缘缩放仍由 Rex 管理。

### 后续：迁移 Leaf 的标题栏鼠标跟随

cgo 和 purego 都增加了左键拖动及松手的全局监听，共用 `float_drag.go` 的状态与坐标计算。鼠标事件直接平移 AppKit 窗口，拖动期间跳过旧布局定位及定时窗口查询；松手先显示最终位置，再安排一次后台刷新，等待落点确认或最多 500 毫秒后恢复普通跟随。缩放边缘、没有标题栏的窗格、切换会话、隐藏或取消拖动分别处理。

- `REXWEBVIEW_TEST_OFFSCREEN=1 REXWEBVIEW_TEST_URL_GUI=1 go test -race -count=1 ./...`：提交前复核，56 项检查通过。
- `CGO_ENABLED=0 REXWEBVIEW_TEST_OFFSCREEN=1 REXWEBVIEW_TEST_URL_GUI=1 go test -count=1 ./...`：提交前复核，56 项检查通过。
- 两种构建的 `go vet ./...` 及 `git diff --check` 通过。
- 拖动回归覆盖标题栏与缩放边缘的命中、负屏幕坐标和位移取整、松手位置与最后一次拖动不同、旧布局等待、确认后的恢复、500 毫秒超时，以及取消后忽略鼠标事件。
- `go test -count=1 -run '^TestFloatDrag' -bench '^BenchmarkFloatDragMove$' -benchmem .`：初次迁移时在 Apple M4 Pro 上约 7.412 ns/op，0 B/op、0 allocs/op。这个结果只覆盖 Go 的状态锁和位移计算，不包含鼠标事件分发、AppKit 移窗或屏幕绘制，不能作为实际画面延迟。

提交前复核补充了三项回归：前一次拖动的查询不能结束新拖动的预览；拖动中的中间位置不能结束松手后的等待；快捷键保护期不能消耗尚未执行的焦点恢复请求。这三项在隔离副本恢复旧逻辑后均失败，修正后通过。原生层还在鼠标按下时检查实际命中的最上层普通／浮动窗口，只有目标 Rex 窗口才开始预览；长按期间保持拖动状态，避免尚未移动时被焦点恢复打断。窗口命中检查只在按下时进行。

已在 Rex 的“WebView 对比 · 修改前 / 修改后”tab 中运行 `HEAD bdea118` 的修改前版本和当前新版，并将两边放为浮窗供直接拖动比较。新版进程和 WebKit 页面标题“修改后 · 鼠标直接跟随”均已确认；本轮没有测量端到端拖动延迟。

## 结论

- 两种构建都能跟随 Rex 窗格，单元测试和 WebKit 隐藏窗口测试都通过。
- 不开 cgo 的版本整个进程多用约 2–3% 的单核 CPU，内存和二进制大小基本相同。
- 切到另一个会话的 tab 后，网页窗口原来要过 0–1 秒才消失。现在 Rex 显示的会话每 30 ms 读一次，按测试，设置变化后 200 ms 内就能发现。

## 功能测试

| 项目 | cgo | `CGO_ENABLED=0` |
| --- | --- | --- |
| `go vet ./...` | 通过 | 通过 |
| `go test ./...` | 通过（另外开 `-race` 跑过一遍） | 通过 |
| `REXWEBVIEW_TEST_OFFSCREEN=1 go test -run TestOffscreenHost` | 通过 | 通过 |
| 可执行文件链接的系统库（`otool -L`） | 含 Cocoa | 只有 libSystem、libresolv、CoreFoundation 和 Security，AppKit 在运行时加载 |
| 在 Rex 平铺窗格里运行 `examples/hello` | 网页贴住窗格正文 | 网页贴住窗格正文 |

offscreen 测试的 `pane-gone` 场景会在后台 goroutine 里读 Rex 设置，所以两种构建下的设置读取都被测到了。

下面几项没有在 `CGO_ENABLED=0` 下手动验证：命令面板盖住窗格时让开、关掉后接回键盘，切到别的窗格时交还焦点，以及把 Rex 快捷键转发给 Rex。

## 整个进程的 CPU 和内存

方法：在 Rex 里分出一个窗格运行 `examples/hello`，等 4 秒，再取 20 秒内的进程 CPU 时间（`ps -o cputime`）和最后的 RSS。两个版本交替运行，各跑 5 次。前台应用不受控制，表里按测试时的前台应用分组。

| 前台应用 | cgo，20 秒 CPU 时间 | 不开 cgo，20 秒 CPU 时间 |
| --- | --- | --- |
| Rex（定时器每秒做 60 次完整跟随） | 0.85 s、0.94 s，约 4.5% 单核 | 1.38 s、1.39 s、1.37 s，约 6.9% 单核 |
| 其他应用（每 10 帧才做一次完整跟随） | 0.66 s、0.45 s、0.44 s，约 2–3% 单核 | 1.07 s、1.09 s，约 5.4% 单核 |

| | cgo | 不开 cgo |
| --- | --- | --- |
| RSS | 84–94 MB | 83–91 MB |
| 启动后前 4 秒的 CPU 时间 | 0.27–0.37 s | 0.36–0.43 s |
| `examples/hello` 二进制大小 | 11.2 MB | 11.0 MB |

## 单项基准

方法：`go test -bench -count=5`，代码和运行方法见文末的[复现](#复现)。测试时屏幕上有 Rex 窗口。

| 操作 | 用在哪里 | cgo | 不开 cgo |
| --- | --- | --- | --- |
| 一次 Objective-C 消息（`isActive`） | 每帧若干次 | 21 ns | 68 ns，1 次分配 |
| 判断 Rex 是否在前台 | 每帧 1–2 次 | 80 ns | 400 ns，4 次分配 |
| AppKit 回调 Go | 每帧 1 次（定时器） | 27 ns | 410 ns，5 次分配 |
| 查 Rex 窗口位置 | 每帧 1 次 | 83–105 µs | 82–108 µs |
| 查有没有 Rex 窗口压住窗格 | 每 3 帧 1 次 | 183–232 µs | 220–272 µs |
| 读一项 Rex 设置 | 每秒若干次 | 0.5 µs | 1.3 µs |
| 同步并读取 Rex 显示的会话 | 每 30 ms 1 次 | 1.1 µs | 2.5 µs |
| MyGo 依赖的 purego 调用 | MyGo 后端 | 58 ns | 56 ns |

每帧的主要开销是向 WindowServer 查窗口列表，两种构建这部分一样。purego 的单次调用比 cgo 慢 3–15 倍，但按每秒 60 帧算，每秒只多几十微秒。整个进程实测每秒多出约 25 ms，单项基准解释不了这部分差距。MyGo 依赖的 purego 调用在两种构建下一样快，排除了 MyGo 普通调用变慢的可能。剩下的原因还没查明，下一步可以用 `sample` 对比两个进程的调用栈。

## 切换 tab 时的延迟

现象：切到另一个会话的 tab 后，网页窗口大约过 0.5 秒才消失。

原因：

- 切到别的会话时，Rex 服务器不发事件，`rex events --all` 里也没有对应的事件。唯一的信号是 Rex App 设置里的 `Workspace.lastFocusedSession`，跟随器原来每秒读一次，所以延迟在 0–1 秒之间。
- 同一会话里的布局变化走 `rex events`，之后调用一次 `session.view`。实测这次调用耗时 10–20 ms，不是瓶颈。
- `rex focus` 只改服务器上的焦点，不切换 App 显示的会话，所以没法用命令行模拟切 tab。

改动：[follow.go](../follow.go) 新开一个循环，每 30 ms 读一次 Rex 显示的会话。这个循环和取布局的循环分开，因为后者的 rex 命令可能卡住好几秒。侧栏、工具栏和外观等其他设置仍然每秒读一次。

验证：[`TestFollowerNoticesShownSessionQuickly`](../host_test.go) 检查设置变化后 200 ms 内能被发现。真实 Rex 里手动切 tab 的效果还没测。如果 Rex App 写这个设置本身就有延迟，轮询无法消除那部分延迟。

## 网页窗格有焦点时切不了 tab

现象：点一下网页所在窗格的标题栏，让它成为 Rex 里有焦点的窗格。之后在侧栏里点别的会话的 tab，切不过去。

原因：点标题栏后，跟随器把键盘交给网页窗口，网页进程成了前台应用。这时点 Rex 侧栏，第一下点击先让 Rex 回到前台。约 0.25 秒后，宿主检查到焦点仍在这个窗格，因为切到别的会话不改变这个会话里的焦点，于是又把网页窗口提到前台。再点一次 tab，同样的过程又来一遍，所以怎么点都切不过去。cgo 和不开 cgo 两种构建的逻辑相同，都有这个问题。

改动：原生层加了一个全局鼠标监听，记下每次点在别的应用里的时间和位置。全局监听鼠标事件不需要辅助功能权限。Rex 回到前台 0.25 秒后，宿主准备接回键盘之前，先看最近 0.75 秒里有没有点过鼠标。如果点过，并且点在窗格外，键盘就留给 Rex。窗格范围包含标题栏，由跟随器算出，通过 `tickOut.Pane` 交给原生层。点在窗格内（包括标题栏）、用 ⌘Tab 切回来，或者 Rex 被其他方式激活时，行为不变。判断放在延迟之后，是因为激活通知和鼠标事件谁先到不一定。

第一版改法用 `CGEventSourceSecondsSinceLastEventType` 查最近一次鼠标按下，在实机上不起作用：每次都读到超过 0.5 秒前，宿主照样把键盘抢回去。调试日志里每点一次侧栏，都是 Rex 回到前台、0.25 秒后网页又接管键盘，循环了十几次。具体为什么读不到还没查清，可能是触控板轻点没有被计入。

验证：`TestFloatingFrame` 检查 `Pane` 等于正文区加上标题栏。两种构建下的单元测试和 offscreen 测试都通过。在 Rex 里手动测试：点网页窗格的标题栏后，点侧栏里别的会话的 tab，可以一次切过去，来回切换多次都正常。点标题栏时，网页仍会接管键盘。

## 点别的窗格时网页闪一下

现象：网页窗口有键盘焦点时，点同一个 Rex 窗口里的别的窗格，网页会消失一下再出现。

原因：网页窗口和 Rex 窗口原来都在普通层级上，网页窗口靠 `orderWindow:relativeTo:` 贴在 Rex 窗口上面。网页窗口在前台时点 Rex，Rex 一激活，macOS 就把 Rex 的窗口提到普通层级的最前面，盖住网页。宿主收到激活通知后才把网页窗口放回上面，中间有一两帧露出下面的终端。网页窗口并没有隐藏，只是被盖住了。cgo 和不开 cgo 两种构建都有这个问题。

改动：Rex 或网页窗口在前台时，网页窗口放到浮动层级（`NSFloatingWindowLevel`），Rex 的普通窗口提到最前也压不住它。切到第三个应用时放回普通层级，再贴回 Rex 窗口上面，别的应用的窗口仍能盖住它。在 Rex 和网页窗口之间来回切换时不降层级，因为两边的激活通知谁先到不一定。命令面板等 Rex 的其他窗口仍由遮挡检查处理，盖住窗格时网页让开。

验证：两种构建的单元测试和 offscreen 测试都通过。在 Rex 里手动测试（不开 cgo）：网页有焦点时点同一窗口里的别的窗格，网页不再闪。切到别的应用时别的窗口能否盖住网页、命令面板盖住窗格时网页是否让开，还没有在改动后手动确认。

## 不开 cgo 时退出崩溃

现象：不开 cgo 的版本收到 SIGTERM 退出时，报 `SIGSEGV`。

原因：MyGo 收到 SIGTERM 后按 Quit 处理，先释放了窗口，但跟随用的定时器还在运行，又给已经释放的窗口发了消息。cgo 版本用 strong 属性持有窗口，所以不会崩。purego 版本原来只存了裸指针。

改动：`attach` 时 retain 窗口，`detach` 时再 release。连续 3 次启动后发 SIGTERM，都正常退出。

## 复现

功能测试：

```sh
go vet ./... && CGO_ENABLED=0 go vet ./...
go test -race -count=1 ./... && CGO_ENABLED=0 go test -count=1 ./...
REXWEBVIEW_TEST_OFFSCREEN=1 go test -count=1 ./...
CGO_ENABLED=0 REXWEBVIEW_TEST_OFFSCREEN=1 go test -count=1 ./...
```

整个进程的 CPU 和内存：在 Rex 的终端里运行 [scripts/measure-cpu.sh](../scripts/measure-cpu.sh)。默认跑 3 轮，每次 20 秒，`scripts/measure-cpu.sh 5 20` 跑 5 轮。每次会在当前窗格旁边分出一个窗格，测完就关掉。

单项基准：

```sh
go test -run '^$' -bench . -benchmem -count=5 .
CGO_ENABLED=0 go test -run '^$' -bench . -benchmem -count=5 .
```

两种构建下基准的名字一一对应。不开 cgo 的基准在 [bench_purego_darwin_test.go](../bench_purego_darwin_test.go)，直接调用 `native_purego_darwin.go` 里的函数。cgo 的基准在 [bench_cgo_darwin_test.go](../bench_cgo_darwin_test.go)，调用 [internal/cgobench](../internal/cgobench) 里照 `pane_darwin.m` 逻辑写的 Objective-C 函数。共用的基准在 [bench_darwin_test.go](../bench_darwin_test.go)。查 Rex 窗口的几项要求屏幕上有 Rex 窗口，没有时跳过。`internal/cgobench` 只被测试导入，不会编进库里。
