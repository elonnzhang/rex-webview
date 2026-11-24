package main

import (
	"context"
	_ "embed"
	"encoding/base64"
	"flag"
	"log"

	rexwebview "github.com/elonnzhang/rex-webview"
)

//go:embed index.html
var page []byte

func main() {
	floating := flag.Bool("float", false, "also follow floating Rex panes (experimental)")
	flag.Parse()
	if err := rexwebview.Run(context.Background(), rexwebview.Options{
		URL:                       "data:text/html;charset=utf-8;base64," + base64.StdEncoding.EncodeToString(page),
		ExperimentalFloatingPanes: *floating,
	}); err != nil {
		log.Fatal(err)
	}
}
