package rexwebview

import (
	"bufio"
	"context"
	"encoding/json/v2"
	"os/exec"
)

// rexCLI 用 rex 命令行实现 rexAPI。服务器没在运行时不去启动它。
type rexCLI struct{ bin string }

func (c rexCLI) command(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, c.bin, append([]string{"--autostart=false"}, args...)...)
}

func (c rexCLI) Output(ctx context.Context, args ...string) ([]byte, error) {
	return c.command(ctx, args...).Output()
}

func (c rexCLI) Call(ctx context.Context, method string, in any) (map[string]any, error) {
	arg, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	out, err := c.Output(ctx, "api", "call", method, string(arg))
	if err != nil {
		return nil, err
	}
	var res map[string]any
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, err
	}
	return res, nil
}

func (c rexCLI) Stream(ctx context.Context, fn func(line []byte), args ...string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := c.command(ctx, args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	lines := bufio.NewScanner(out)
	lines.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for lines.Scan() {
		fn(lines.Bytes())
	}
	if err := lines.Err(); err != nil {
		// 扫描失败后不再读 stdout，先停止事件流再回收子进程。
		cancel()
		_ = cmd.Wait()
		return err
	}
	return cmd.Wait()
}
