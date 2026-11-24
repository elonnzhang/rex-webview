package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	rexwebview "github.com/elonnzhang/rex-webview"
)

func main() {
	floating := flag.Bool("float", true, "also follow floating Rex panes (experimental)")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "usage: url [-float=false] [URL]")
		flag.PrintDefaults()
	}
	flag.Parse()
	url := "https://www.google.com"
	switch flag.NArg() {
	case 0:
	case 1:
		url = flag.Arg(0)
	default:
		flag.Usage()
		os.Exit(2)
	}
	if err := rexwebview.Run(context.Background(), rexwebview.Options{
		URL:                       url,
		ExperimentalFloatingPanes: *floating,
	}); err != nil {
		log.Fatal(err)
	}
}
