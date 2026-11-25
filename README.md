# rex-webview

[English](README.en.md)

用 Go 在 Rex 窗格里显示网页。SDK 把 MyGo 的 WebKit 窗口对齐到本机 Rex 窗格，跟随分屏、缩放和焦点。从普通终端启动同一程序会打开独立窗口。需要 macOS 和 Go 1.27.3 或更新版本。cgo 可选：`CGO_ENABLED=0` 时窗口桥接改用 purego 直接调 AppKit，功能相同。

## 试一下

在 Rex 的平铺窗格里进入仓库目录，运行 `go run ./examples/hello`。点一下网页，按 **⌘W** 关闭，或按 **⌘Q** 退出这个 WebView 应用。用同一个 shell 启动时，退出后会回到提示符。示例把 HTML 编入程序，通过 `data:` URL 加载，无需启动服务器。[示例源码](examples/hello/main.go)

示例默认也跟随 Rex **浮动窗格**（实验性），在已有的浮动窗格里直接运行即可；加 `-float=false` 只跟平铺窗格。该参数只控制跟随，窗格的创建、移动和层级仍由 Rex 控制。

要在窗格里打开自己的网页，运行 `go run ./examples/url 'http://localhost:3000'`。把地址换成正在运行的服务的完整 URL。省略地址时，示例打开 Google。

## 接入 Go 程序

从 `main` 调用，只有 `URL` 必填。SDK 目前在本地开发，将两个模块放在同级目录，在应用模块运行 `go mod edit -replace github.com/elonnzhang/rex-webview=../rex-webview` 和 `go get github.com/elonnzhang/rex-webview@v0.0.0` 即可引用。

```go
package main

import (
    "context"
    "log"

    rexwebview "github.com/elonnzhang/rex-webview"
)

func main() {
    if err := rexwebview.Run(context.Background(), rexwebview.Options{
        URL: "data:text/plain,Hello!",
    }); err != nil {
        log.Fatal(err)
    }
}
```

`Run` 在主 goroutine 上运行到窗口关闭或 context 取消。每个进程可以运行一个宿主一次。需要读取布局时用 `h, err := rexwebview.New(opts)`，成功后调用 `h.Run(ctx)`。`h.Where()` 在 Rex 外返回 `nil`，在 Rex 内返回 `session`、`block`、`placed`。可以定位时还有 `focused` 和 `rect`，分别表示 Rex 焦点，以及相对于窗格布局的 `[左, 上, 宽, 高]`。

## 通过 Options 定制

| 选项 | 用法 |
| --- | --- |
| `Title`、`RexBin` | 设置初始标题和 Rex 可执行文件路径。`REX_BLOCK` 指定要跟随的窗格。没有 `REX_SESSION` 时，SDK 通过 `rex whoami --json` 获取会话 ID。可执行文件默认从 `$REX_BIN_DIR/rex`、`PATH` 顺序查找。 |
| `Menu` | 传入 `func(requestClose func()) *mygo.Menu`，应用自己提供完整的原生菜单、翻译和快捷键。关闭动作调用 `requestClose()`。默认菜单提供关闭和退出。 |
| `ReservedKeys` | 例如 `[]string{"cmd+s"}`。把这些键留给页面或原生菜单处理，其他已匹配的快捷键可转给 Rex。`cmd+w` 和 `cmd+q` 始终留给应用。 |
| `OnClose`、`OnPaneGone` | 默认关闭宿主。需要保存时，在回调里保存成功后取消传给 `Run` 的 context，保存失败可以保留页面重试。`OnPaneGone` 在窗格消失时单独调用一次。两者在后台 goroutine 上执行，直接取消 context 会跳过关闭请求回调。 |
| `ConfigureWindow` | 接收 `*mygo.WindowOptions`，可以改尺寸、背景或页面设置。默认使用临时会话、关闭开发工具，并用 `Page.Untrusted` 禁用网页到 Go 的桥接。保留这些默认值时请逐字段修改。宿主管理起始 URL、可见性及覆盖窗口的几何和层级。 |
| `OnWindowCreated` | 界面线程在首次展示之前交付 `*mygo.Window`。可注册 MyGo 事件。需要截图时，等页面加载和绘制完成，直接调用 `win.CapturePage()`，由应用决定如何使用 PNG。 |
| `Offscreen` | 隐藏 MyGo 窗口，仍加载页面并跟踪 Rex 布局，适用于自动测试。`rex capture` 读到的是底层终端。 |
| `ExperimentalFloatingPanes` | 设为 `true` 跟随普通大小的浮动窗格。最小化、切换标签、被更高的浮层遮住时隐藏，恢复后重新定位。 |

例如，编辑器可以通过 `OnClose` 决定何时退出。`save` 是应用自己的保存函数。

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()
err := rexwebview.Run(ctx, rexwebview.Options{
    URL: pageURL,
    ReservedKeys: []string{"cmd+s"},
    OnClose: func() {
        if err := save(); err != nil { log.Print(err); return }
        cancel()
    },
})
```

## 浮动窗格和显示边界

在 Rex preview.21（API 18）里，`session.view` 的浮层按从后到前排列。浮层里分屏后，每个叶节点的 `rect` 已经是窗口布局坐标，可以直接定位。上层浮窗即使只压住目标一角，宿主也会隐藏整个网页，让 Rex 的控件能被点击。

SDK 根据本地 Rex 设置估算标题栏、侧边栏和间距。浮层内边距和拖动时的逐帧贴合仍需在桌面核对。多个 Rex 桌面窗口共存时，定位依赖前台 Rex 窗口。覆盖层需要和 Rex 运行在同一台 Mac 上。坐标换算及焦点处理见[技术原理](docs/technical-principles.md)。

## 验证

`go test ./...` 和 `go vet ./...` 无需 Rex。在有图形会话的 macOS 上运行 `REXWEBVIEW_TEST_OFFSCREEN=1 go test -race -count=1 ./...`，会额外验证真实 WebKit 渲染、窗口配置、自定义关闭和窗格消失退出。以上命令前加 `CGO_ENABLED=0` 可以测试不用 cgo 的窗口桥接，`-race` 需要 cgo，这时要去掉。两种构建的测试结果和性能对比见[测试报告](docs/test-report.md)。

要验证 URL 示例，运行 `REXWEBVIEW_TEST_URL_GUI=1 go test -count=1 ./examples/url`。测试在后台打开一个独立窗口，访问临时的本地 HTTP 服务，然后关闭窗口。

本库采用 [MIT 许可](LICENSE)。窗格跟随和几何计算改写自 [Leaf Browser](https://github.com/DeepanshuMishraa/leaf)，保留其 [MIT 许可](LICENSE.leaf)。
