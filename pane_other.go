//go:build !darwin

package rexwebview

import (
	"context"
	"errors"
)

var errNoWebView = errors.New("rex webview: the native webview requires macOS")

func (*Host) run(context.Context) error { return errNoWebView }
func rexPref(string) (string, bool)     { return "", false }
