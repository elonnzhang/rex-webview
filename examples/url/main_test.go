package main_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestURLCommand(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "url-example")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	withoutRex := append(os.Environ(), "REX_SESSION=test", "REX_BLOCK=test", "REX_BIN_DIR="+dir, "PATH="+dir)
	for _, tc := range []struct {
		name   string
		args   []string
		status int
		output string
	}{
		{"help", []string{"-h"}, 0, "URL"},
		{"relative", []string{"relative.html"}, 1, "absolute URL"},
		{"empty", []string{""}, 1, "absolute URL"},
		{"extra", []string{"data:text/plain,one", "data:text/plain,two"}, 2, "URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), bin, tc.args...)
			cmd.Env = withoutRex
			out, err := cmd.CombinedOutput()
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != tc.status || !bytes.Contains(out, []byte(tc.output)) {
				t.Fatalf("exit = %v (%v); output = %q", cmd.ProcessState, err, out)
			}
		})
	}

	t.Run("loads supplied URL in WebKit", func(t *testing.T) {
		if runtime.GOOS != "darwin" || os.Getenv("REXWEBVIEW_TEST_URL_GUI") != "1" {
			t.Skip("set REXWEBVIEW_TEST_URL_GUI=1 on macOS with cgo for a background window")
		}
		requested := make(chan string, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/chosen" {
				select {
				case requested <- r.URL.RequestURI():
				default:
				}
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("URL example test"))
		}))
		defer server.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		const path = "/chosen?one=1&two=2"
		cmd := exec.CommandContext(ctx, bin, "-float=false", server.URL+path)
		cmd.Env = append(os.Environ(), "REX_SESSION=", "REX_BLOCK=", "REX_CLIENT=", "MYGO_DEV_BACKGROUND=1", "MYGO_GENERATE=")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			t.Fatalf("exited before loading the page: %v\n%s", err, stderr.Bytes())
		case <-ctx.Done():
			<-done
			t.Fatalf("page not requested: %v\n%s", ctx.Err(), stderr.Bytes())
		case got := <-requested:
			_ = cmd.Process.Signal(syscall.SIGTERM)
			err := <-done
			if got != path || err != nil {
				t.Fatalf("requested %q, exit %v\n%s", got, err, stderr.Bytes())
			}
		}
	})
}
