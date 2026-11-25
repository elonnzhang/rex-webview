package rexwebview

import (
	"bufio"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// rexCLI 用 rex 命令行实现 rexAPI。服务器没在运行时不去启动它。
type rexCLI struct{ bin string }

func (c rexCLI) command(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, c.bin, append([]string{"--autostart=false"}, args...)...)
}

func (c rexCLI) Output(ctx context.Context, args ...string) ([]byte, error) {
	return c.command(ctx, args...).Output()
}

// The CLI accepts a block as a session selector, but API payloads need the session's own ID.
func (c rexCLI) sessionID(ctx context.Context) (string, error) {
	if session := strings.TrimSpace(os.Getenv("REX_SESSION")); session != "" {
		return session, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := c.command(ctx, "whoami", "--json")
	cmd.WaitDelay = time.Second // A RexBin wrapper can leave a child holding its output pipes.
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return "", fmt.Errorf("rex whoami: %w", err)
	}
	var identity struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(out, &identity); err != nil {
		return "", fmt.Errorf("decode rex whoami: %w", err)
	}
	if session := strings.TrimSpace(identity.SessionID); session != "" {
		return session, nil
	}
	return "", errors.New("rex whoami returned no session_id for this pane")
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
