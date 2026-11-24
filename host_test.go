package rexwebview

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestHostOutsideRex(t *testing.T) {
	t.Setenv("REX_SESSION", "one-variable-is-not-enough")
	t.Setenv("REX_BLOCK", "")
	keys := []string{"Command+Shift+S"}
	h, err := New(Options{URL: "about:blank", RexBin: "/no/such/rex", ReservedKeys: keys})
	if err != nil {
		t.Fatal(err)
	}
	if keys[0] != "Command+Shift+S" {
		t.Fatal("New modified the caller's slice")
	}
	keys[0] = "changed"
	if h.Where() != nil || h.opts.ReservedKeys[0] != "shift+cmd+s" || h.opts.Title != "WebView" {
		t.Fatalf("unexpected standalone host: %#v", h.opts)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := h.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if err := Run(ctx, Options{URL: "about:blank"}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", "cmd", "cmd+", "cmd+x y", "cmd+[Digit1]", "shift+shift+t"} {
		if _, err := New(Options{URL: "about:blank", ReservedKeys: []string{key}}); err == nil {
			t.Errorf("accepted malformed reserved key %q", key)
		}
	}
	for _, url := range []string{"", "./relative", "http://%"} {
		if _, err := New(Options{URL: url}); err == nil {
			t.Errorf("accepted %q", url)
		}
	}
}

func TestHostWhereAndRexLookup(t *testing.T) {
	t.Setenv("REX_SESSION", "session")
	t.Setenv("REX_BLOCK", "web")
	t.Setenv("PATH", "")
	dir := filepath.Join(t.TempDir(), "Application Support")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REX_BIN_DIR", dir)
	if _, err := New(Options{URL: "about:blank"}); err == nil {
		t.Fatal("missing rex was accepted")
	}
	bin := filepath.Join(dir, "rex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	h, err := New(Options{URL: "about:blank"})
	if err != nil {
		t.Fatal(err)
	}
	if got := h.follower.rex.(rexCLI).bin; got != bin {
		t.Fatalf("rex path = %q", got)
	}
	want := map[string]any{"session": "session", "block": "web", "placed": false}
	if got := h.Where(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Where = %#v", got)
	}
	h.follower.place = &placement{Rect: rect{0.5, 0, 0.5, 1}, Focused: true}
	want["placed"], want["focused"], want["rect"] = true, true, []float64{0.5, 0, 0.5, 1}
	out := h.Where().(map[string]any)
	out["rect"].([]float64)[0] = -1
	if got := h.Where(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Where = %#v", got)
	}
}

// 每个命令等到上下文取消才返回，这样可以检查事件流、焦点和按键转发都能随 Run 一起停。
type waitingRex struct{ started chan string }

func (r waitingRex) wait(ctx context.Context, name string) error {
	r.started <- name
	<-ctx.Done()
	return ctx.Err()
}
func (r waitingRex) Call(ctx context.Context, method string, _ any) (map[string]any, error) {
	return nil, r.wait(ctx, method)
}
func (r waitingRex) Output(ctx context.Context, args ...string) ([]byte, error) {
	return nil, r.wait(ctx, args[0])
}
func (r waitingRex) Stream(ctx context.Context, _ func([]byte), args ...string) error {
	return r.wait(ctx, args[0])
}

func TestFollowerStopsAllCommands(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := waitingRex{make(chan string, 8)}
	f := newFollower("s", "b", r, func(string) (string, bool) { return "", false })
	f.ctx, f.place = ctx, &placement{}
	f.work.Go(func() { f.run(ctx) })
	f.work.Go(f.becameKey)
	f.perform(binding{Action: "pane.zoom"}, "app")
	want := map[string]bool{"events": true, "session.view": true, "keymap": true, "session.focus_block": true, "client.perform": true}
	deadline := time.After(2 * time.Second)
	for len(want) > 0 {
		select {
		case name := <-r.started:
			delete(want, name)
		case <-deadline:
			t.Fatalf("commands did not start: %v", want)
		}
	}
	cancel()
	done := make(chan struct{})
	go func() { f.work.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("follower did not stop its commands")
	}
}
