// Package rexwebview hosts a web page in a Rex pane or a standalone macOS window.
package rexwebview

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"
)

// Options configures the web page and the native window. Only URL is required.
type Options struct {
	// URL is the absolute page URL, for example an http:, file: or data: URL.
	URL string
	// Title is the application name and initial window title. The default is WebView.
	Title string
	// RexBin selects the Rex executable. The default is $REX_BIN_DIR/rex, then rex on PATH.
	RexBin string
	// Offscreen keeps the window hidden while layout tracking continues.
	Offscreen bool
	// ExperimentalFloatingPanes also follows visible panes in a floating Rex layer.
	// Geometry is estimated from Rex layout data and local appearance settings.
	ExperimentalFloatingPanes bool
	// OnClose handles a close or quit request without destroying the page. To
	// accept, cancel Run's context; to keep the page open, just return. Repeated
	// requests are ignored while this callback is running.
	OnClose func()
	// OnPaneGone is called once when the tracked Rex pane disappears.
	// Both callbacks run off the UI thread. Each defaults to quitting Run.
	OnPaneGone func()
	// Menu builds the native menu on the UI thread. Use requestClose for actions
	// that should respect OnClose. Nil provides just Close (Cmd+W) and Quit (Cmd+Q).
	Menu func(requestClose func()) *mygo.Menu
	// ReservedKeys stay with the app instead of being forwarded to Rex. Use Rex
	// chord syntax, e.g. "cmd+s". Cmd+W and Cmd+Q always stay with the native app.
	ReservedKeys []string
	// ConfigureWindow adjusts MyGo's initial window and page options on the UI thread.
	// The host owns the URL, initial visibility and frameless Rex/offscreen presentation.
	ConfigureWindow func(*mygo.WindowOptions)
	// OnWindowCreated runs once on the UI thread before presentation. Use the MyGo
	// window to register native events or capture the page after content has loaded.
	OnWindowCreated func(*mygo.Window)
}

// Host is a single webview. A process may run only one Host, once.
type Host struct {
	opts     Options
	follower *follower
}

// New validates the options and detects a Rex pane from REX_BLOCK. If REX_SESSION
// is unset, it asks the running Rex server for the pane's session ID.
// It does not open a window or start a server.
func New(opts Options) (*Host, error) {
	return newHost(context.Background(), opts)
}

func newHost(ctx context.Context, opts Options) (*Host, error) {
	u, err := url.Parse(opts.URL)
	if err != nil || !u.IsAbs() {
		return nil, errors.New("rexwebview: URL must be an absolute URL")
	}
	opts.Title = cmp.Or(opts.Title, "WebView")
	opts.ReservedKeys = slices.Clone(opts.ReservedKeys)
	for i, key := range opts.ReservedKeys {
		chord, ok := normChord(key)
		if !ok {
			return nil, fmt.Errorf("rexwebview: invalid reserved key %q", key)
		}
		opts.ReservedKeys[i] = chord
	}
	h := &Host{opts: opts}
	if block := strings.TrimSpace(os.Getenv("REX_BLOCK")); block != "" {
		bin, err := findRex(opts.RexBin)
		if err != nil {
			return nil, fmt.Errorf("rexwebview: find rex executable: %w", err)
		}
		cli := rexCLI{bin}
		session, err := cli.sessionID(ctx)
		if err != nil {
			return nil, fmt.Errorf("rexwebview: find session for %s: %w", block, err)
		}
		h.follower = newFollower(session, block, cli, rexPref)
		h.follower.reservedKeys = opts.ReservedKeys
		h.follower.floatingPanes = opts.ExperimentalFloatingPanes
	}
	return h, nil
}

func findRex(bin string) (string, error) {
	if bin != "" {
		return exec.LookPath(bin)
	}
	if dir := os.Getenv("REX_BIN_DIR"); dir != "" {
		if path, err := exec.LookPath(filepath.Join(dir, "rex")); err == nil {
			return path, nil
		}
	}
	return exec.LookPath("rex")
}

// Run runs MyGo's event loop on the main goroutine until the window quits or
// ctx is canceled. Cancellation is a normal shutdown; it does not call OnClose.
func (h *Host) Run(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	return h.run(ctx)
}

// Where returns nil outside Rex. In Rex it returns a fresh map with session,
// block and placed. When placed, rect is [x,y,width,height] in pane-layout
// fractions, and focused reports whether Rex has focused this block.
func (h *Host) Where() any {
	if h.follower == nil {
		return nil
	}
	return h.follower.where()
}

// Run opens a page and runs the event loop on the main goroutine.
// Use New when the application also needs the host's Rex placement.
func Run(ctx context.Context, opts Options) error {
	h, err := newHost(ctx, opts)
	if err != nil {
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			return nil
		}
		return err
	}
	return h.Run(ctx)
}
