# rex-webview

[中文](README.md)

Display a web page in a Rex pane from Go. The SDK aligns a MyGo WebKit window with the local Rex pane and follows splits, zoom and focus. Starting the same program from a regular terminal opens a standalone window. Requires macOS and Go 1.27.3 or later. cgo is optional: with `CGO_ENABLED=0` the window bridge calls AppKit through purego with the same behavior.

## Try it

From the repository in a tiled Rex pane, run `go run ./examples/hello`. Click the page and press **⌘W** to close it or **⌘Q** to quit the WebView app. If you started it from a shell prompt, you return to that prompt. The example embeds HTML and loads it from a `data:` URL, so it runs without a server. [Example source](examples/hello/main.go)

The example also follows **floating Rex panes** by default (experimental), so you can run it in an existing floating pane. Pass `-float=false` to follow tiled panes only. The flag only controls tracking. Rex owns pane creation, movement and stacking.

To open your own page in the pane, run `go run ./examples/url 'http://localhost:3000'`. Replace the address with the full URL of your running service. Omit the URL to open Google.

## Use it from Go

Call `Run` from `main`. Only `URL` is required. For local development, place the two modules in sibling directories, then run `go mod edit -replace github.com/elonnzhang/rex-webview=../rex-webview` and `go get github.com/elonnzhang/rex-webview@v0.0.0` in the application module.

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

`Run` blocks on the main goroutine until the window quits or its context is canceled. A process can run one host, once. For layout access, use `h, err := rexwebview.New(opts)` and then `h.Run(ctx)` on success. `h.Where()` returns `nil` outside Rex, or `session`, `block` and `placed` inside Rex. When placed, it also returns `focused` and `rect`, the Rex focus state and `[x, y, width, height]` in fractions of the pane layout.

## Options

| Option | What it controls |
| --- | --- |
| `Title`, `RexBin` | Initial title and Rex executable path. `REX_BLOCK` identifies the pane to follow. If `REX_SESSION` is unset, the SDK gets the session ID from `rex whoami --json`. The default executable lookup checks `$REX_BIN_DIR/rex`, then `PATH`. |
| `Menu` | Supply `func(requestClose func()) *mygo.Menu` to build a complete native menu with your own labels and accelerators. Call `requestClose()` from your Close action. The default menu offers Close and Quit. |
| `ReservedKeys` | Keep keys such as `[]string{"cmd+s"}` available to the page or native menu. Other matching shortcuts can go to Rex. `cmd+w` and `cmd+q` always stay with the app. |
| `OnClose`, `OnPaneGone` | Both default to quitting. To save first, cancel the context passed to `Run` after saving successfully. Leave it active to keep the page open for another attempt. `OnPaneGone` runs separately, once, when the pane disappears. Both callbacks run on background goroutines. Canceling the context directly bypasses the close-request callback. |
| `ConfigureWindow` | Receives `*mygo.WindowOptions` to adjust size, background or page settings. Defaults include an ephemeral session, disabled developer tools and `Page.Untrusted` to disable the page-to-Go bridge. Modify individual fields to keep those defaults. The host manages the starting URL, visibility and overlay geometry and stacking. |
| `OnWindowCreated` | Receives `*mygo.Window` on the UI thread before presentation. Register MyGo events here. For screenshots, wait for the content to load and paint, then call `win.CapturePage()` and handle the PNG in your app. |
| `Offscreen` | Keeps the window hidden while the page loads and Rex layout tracking runs. Useful for automated tests. `rex capture` reads the underlying terminal. |
| `ExperimentalFloatingPanes` | Set to `true` to follow unzoomed floating panes. The overlay hides when minimized, on another tab or covered by a higher layer, and follows again when visible. |

An editor can decide when to quit in `OnClose`. Here, `save` is the application's save function.

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

## Floating panes and display limits

In Rex preview.21 (API 18), `session.view` lists layers from back to front. Each leaf `rect` in a split floating layer is already in window-layout coordinates. The host converts higher floating layers and the page body to screen coordinates before checking overlap. Even a partial overlap with the body hides the entire WebView so Rex's controls remain clickable. Moving or resizing the same floating layer keeps the page visible when its internal layout stays the same; split changes still wait for the layout animation.

The SDK estimates headers, sidebar width and gaps from local Rex preferences, including top or bottom horizontal tabs. Compact mode uses preview.23's 34-point header and 1-point divider. Bottom tabs follow [Leaf PR #87](https://github.com/DeepanshuMishraa/leaf/pull/87). Dragging a floating pane's header moves the page directly with the pointer, without waiting for Rex layout responses or redrawing the page. On release, the host holds the preview until the layout confirms the position, for up to 500 milliseconds. The outer header edges remain available for Rex resizing. Floating-layer insets and actual drag latency still need desktop verification. With multiple Rex desktop windows, placement depends on the front Rex window. The overlay and Rex must run on the same Mac. See the [technical notes (Chinese)](docs/technical-principles.md) for the coordinate conversion and focus handling.

## Verification

`go test ./...` and `go vet ./...` run without Rex. In a graphical macOS session, `REXWEBVIEW_TEST_OFFSCREEN=1 go test -race -count=1 ./...` also exercises WebKit rendering, window options, custom close handling and shutdown when the pane disappears. Prefix these commands with `CGO_ENABLED=0` to test the window bridge that does not use cgo, and drop `-race`, which needs cgo. The [test report](docs/test-report.md) (in Chinese) compares results and performance for both builds.

To test the URL example, run `REXWEBVIEW_TEST_URL_GUI=1 go test -count=1 ./examples/url`. The test opens a standalone window in the background, visits a temporary local HTTP server, and closes the window.

Licensed under the [MIT license](LICENSE). Pane tracking and geometry are adapted from [Leaf Browser](https://github.com/DeepanshuMishraa/leaf) under its [MIT license](LICENSE.leaf).
