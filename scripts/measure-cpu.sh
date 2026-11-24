#!/bin/sh
# 在 Rex 窗格里分别运行 cgo 和不开 cgo 的 examples/hello，比较整个进程的 CPU 时间和内存。
# 要在 Rex 的终端里运行（需要 REX_SESSION 和 REX_BLOCK）。每轮会在当前窗格旁边分出一个窗格，测完关掉。
# 用法：scripts/measure-cpu.sh [轮数] [每次秒数]
set -eu
rounds=${1:-3}
secs=${2:-20}
: "${REX_SESSION:?run this inside a Rex terminal}"
: "${REX_BLOCK:?run this inside a Rex terminal}"

dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT
cd "$(dirname "$0")/.."
go build -o "$dir/hello-cgo" ./examples/hello
CGO_ENABLED=0 go build -o "$dir/hello-nocgo" ./examples/hello
ls -l "$dir/hello-cgo" "$dir/hello-nocgo" | awk '{print $9, $5, "bytes"}'

measure() {
    bin=$1
    rex split -s "$REX_SESSION" -b "$REX_BLOCK" --focus=false -- "$bin" >/dev/null
    sleep 4 # 跳过启动
    pid=$(pgrep -nf "^$bin")
    start=$(ps -p "$pid" -o cputime= | tr -d ' ')
    sleep "$secs"
    end=$(ps -p "$pid" -o cputime= | tr -d ' ')
    rss=$(ps -p "$pid" -o rss= | tr -d ' ')
    front=$(osascript -e 'tell application "System Events" to get name of first process whose frontmost is true' 2>/dev/null || echo '?')
    kill "$pid"
    echo "$(basename "$bin") cpu_start=$start cpu_end=$end rss_kb=$rss front=$front"
    sleep 2
}

i=0
while [ "$i" -lt "$rounds" ]; do
    measure "$dir/hello-cgo"
    measure "$dir/hello-nocgo"
    i=$((i + 1))
done
