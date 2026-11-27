package rexwebview

import (
	"context"
	"encoding/json/v2"
	"slices"
	"strings"
	"sync"
	"time"
)

// follower 让网页窗口跟着 Rex 里的一个窗格：布局从 session.view 来，会话有事件时重新取；
// Rex 的界面占了多少从它的设置里读，每秒读一次。窗口每一帧来问一次 tick，该在哪、该不该显示、焦点该给谁。
// 做法照着 leaf 的 src/browser.swift。这里只有逻辑，真正动窗口的在 pane_darwin.m（cgo）或 native_purego_darwin.go（不开 cgo）。

// settleTime 是窗格换了位置后先让开多久。Rex 挪窗格带动画，跟着动画走会慢半拍，等它停稳了再回来。
const settleTime = 100 * time.Millisecond

// shownPoll 是多久看一次 Rex 眼下显示哪个会话。切到别的会话的 tab 时服务器不发事件，只能看 App 的设置；
// 读一次只要几微秒，看得勤一点，网页窗口才能跟着马上让开。
const shownPoll = 30 * time.Millisecond

// rexAPI 是 follower 用到的 rex 命令行，测试里换成假的。
type rexAPI interface {
	// Call 调服务器的一个方法（rex api call）。
	Call(ctx context.Context, method string, in any) (map[string]any, error)
	// Output 运行一个 rex 子命令，返回标准输出。
	Output(ctx context.Context, args ...string) ([]byte, error)
	// Stream 运行一个一直输出的 rex 子命令，逐行交给 fn，直到 ctx 取消或命令退出。
	Stream(ctx context.Context, fn func(line []byte), args ...string) error
}

type follower struct {
	ctx            context.Context // 运行期间的所有 rex 子进程共用这个上下文
	work           sync.WaitGroup
	session, block string
	floatingPanes  bool // 跟随普通大小的浮动窗格
	rex            rexAPI
	pref           func(name string) (string, bool) // Rex App 的设置 Workspace.<名字>
	now            func() time.Time
	syncPrefs      func()            // 读设置之前先取到最新的
	gone           func()            // 窗格没了
	activateHost   func()            // 让 Rex 到前面来
	appearance     func(name string) // Rex 的深浅色设置：light、dark，别的是跟着系统

	kick chan struct{} // 该重新取布局了

	mu             sync.Mutex
	chrome         chrome
	shown          string // Rex 眼下显示的会话，空的是不知道
	place          *placement
	drag           *floatDrag // 原生鼠标事件驱动的浮窗拖动预览
	refreshSeq     uint64     // 已开始的布局查询
	appliedSeq     uint64     // 最近完成并应用的布局查询
	settleUntil    time.Time
	missing        int
	wasFocused     bool
	focusDirty     bool   // 取到了新的布局，下一次 tick 要对一下焦点
	reclaimToken   uint64 // 接回焦点的请求；请求之前开始的布局查询不能用来接回焦点
	reclaimPending bool
	reclaimReady   bool
	reservedKeys   []string // 留给调用方处理的按键
	bindings       []binding
	prefix         []string // 按了一半的一串键
	appClient      string
}

func newFollower(session, block string, rex rexAPI, pref func(string) (string, bool)) *follower {
	f := &follower{
		session: session, block: block, rex: rex, pref: pref, now: time.Now,
		syncPrefs: func() {}, gone: func() {}, activateHost: func() {}, appearance: func(string) {},
		kick: make(chan struct{}, 1),
	}
	return f
}

// loadPrefs 重新读 Rex App 的设置。
func (f *follower) loadPrefs() {
	f.syncPrefs()
	c, shown := chromeFrom(f.pref), shownSession(f.pref)
	look, _ := f.pref("appearance")
	f.mu.Lock()
	f.chrome, f.shown = c, shown
	f.mu.Unlock()
	f.appearance(look)
}

// loadShown 只重新读 Rex 眼下显示的会话。
func (f *follower) loadShown() {
	f.syncPrefs()
	shown := shownSession(f.pref)
	f.mu.Lock()
	f.shown = shown
	f.mu.Unlock()
}

// run 一直跟到 ctx 取消。
func (f *follower) run(ctx context.Context) {
	f.loadPrefs()
	f.work.Go(func() { f.loadBindings(ctx) })
	// 单独一个循环：下面取布局的 rex 命令可能卡上好几秒，不能耽误它
	f.work.Go(func() {
		t := time.NewTicker(shownPoll)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				f.loadShown()
			}
		}
	})
	f.work.Go(func() {
		for ctx.Err() == nil {
			_ = f.rex.Stream(ctx, func(line []byte) {
				if changesLayout(line) {
					f.refreshSoon()
				}
			}, "events", "--session", f.session, "--json")
			// rex events 退出了（服务器重启、连接断了）：过一会儿再连
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
	})
	prefs := time.NewTicker(time.Second)
	defer prefs.Stop()
	// 事件漏了也不至于一直不对：隔一阵自己取一次。不取得更勤，因为每个 rex 命令都会在各个会话里留下一对连接事件
	safety := time.NewTicker(5 * time.Second)
	defer safety.Stop()
	f.refresh(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-prefs.C:
			f.loadPrefs()
		case <-safety.C:
			f.refresh(ctx)
		case <-f.kick:
			f.refresh(ctx)
		}
	}
}

func (f *follower) refreshSoon() {
	select {
	case f.kick <- struct{}{}:
	default:
	}
}

// changesLayout 说明一个事件之后要不要重新取布局。每个 rex 命令（包括这里自己取布局的那个）都会连上来再断开，
// 跟着这两种事件取就停不下来了。
func changesLayout(line []byte) bool {
	var ev struct {
		Payload struct {
			Event struct {
				Type string `json:"type"`
			} `json:"event"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(line, &ev); err != nil || ev.Payload.Event.Type == "" {
		return true
	}
	t := ev.Payload.Event.Type
	return t != "client_connected" && t != "client_disconnected"
}

func (f *follower) refresh(ctx context.Context) {
	f.mu.Lock()
	token := f.reclaimToken
	f.refreshSeq++
	seq := f.refreshSeq
	f.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	view, err := f.rex.Call(ctx, "session.view", map[string]any{"session_id": f.session})
	if err != nil || view == nil {
		f.mu.Lock()
		f.place = nil
		if token == f.reclaimToken {
			f.reclaimPending, f.reclaimReady = false, false
		}
		f.mu.Unlock()
		return
	}
	f.apply(view)
	f.mu.Lock()
	f.appliedSeq = seq
	if f.reclaimPending && token == f.reclaimToken {
		f.reclaimReady = true
	}
	f.mu.Unlock()
}

// requestFocusAfterRefresh 用新查询确认焦点，避免面板关闭或 Rex 激活时拿旧布局抢回键盘。
func (f *follower) requestFocusAfterRefresh() {
	f.mu.Lock()
	f.reclaimToken++
	f.reclaimPending, f.reclaimReady = true, false
	f.mu.Unlock()
	f.refreshSoon()
}

// apply 收下一份新的布局。
func (f *follower) apply(view map[string]any) {
	f.mu.Lock()
	if viewContains(view, f.block) {
		f.missing = 0
	} else if f.missing++; f.missing >= 2 {
		// 连着两次都没有：窗格在 Rex 里关掉了
		f.mu.Unlock()
		f.gone()
		return
	}
	next := place(view, f.block, f.floatingPanes)
	if !next.followsLayer(f.place) && ((next == nil) != (f.place == nil) || (next != nil && (next.Rect != f.place.Rect || next.gapOffset != f.place.gapOffset || next.floating != f.place.floating))) {
		f.settleUntil = f.now().Add(settleTime)
	}
	f.place = next
	f.focusDirty = true
	f.mu.Unlock()
}

// tickIn 是窗口每一帧告诉 follower 的：Rex 的窗口在哪，Rex 和网页窗口谁在前面。
type tickIn struct {
	HostFound              bool
	Host                   rect
	HostActive, SelfActive bool
	ReclaimBlocked         bool // 刚转发给 Rex 的快捷键仍在 1 秒保护期内
}

// 焦点该怎么动。
const (
	focusKeep    = iota
	focusTake    // 网页窗口接管键盘
	focusHost    // 把键盘还给 Rex
	focusReclaim // 新布局确认后接回键盘；原生层还须检查用户点击和刚转发的按键
)

// tickOut 是 follower 的回答。
type tickOut struct {
	Visible bool
	Frame   rect // 屏幕上的位置，原点在主屏幕左上角
	Pane    rect // 整个窗格连同标题栏在屏幕上的位置，同样的原点
	Radius  float64
	Focused bool // 窗格是不是 Rex 里有焦点的那个
	Focus   int
}

func (f *follower) tick(in tickIn) tickOut {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out tickOut
	shown := f.shown == "" || f.shown == f.session
	if !shown {
		f.reclaimPending, f.reclaimReady = false, false
	}
	out.Focused = f.place != nil && f.place.Focused
	if f.focusDirty {
		f.focusDirty = false
		switch {
		case out.Focused && !f.wasFocused && in.HostActive && shown && !f.reclaimPending:
			out.Focus = focusTake
		case !out.Focused && f.wasFocused && in.SelfActive && shown:
			// Rex 把焦点挪到别的窗格了：转发过去的键，或者别处调的 API
			out.Focus = focusHost
		}
		f.wasFocused = out.Focused
	}
	if f.place == nil || !in.HostFound || f.now().Before(f.settleUntil) {
		return out
	}
	// Rex 换到别的会话去了，窗格不在画面上
	if !shown {
		return out
	}
	body := f.chrome.body(*f.place, in.Host)
	if body.W <= 80 || body.H <= 60 || f.chrome.covered(*f.place, body, in.Host) {
		return out
	}
	out.Visible, out.Frame, out.Radius = true, body, f.chrome.Radius
	if f.place.floating {
		out.Radius = 10
	}
	out.Pane = rect{body.X, body.Y - f.chrome.Header, body.W, body.H + f.chrome.Header}
	if f.reclaimPending && f.reclaimReady && (!out.Focused || !in.HostActive || !in.ReclaimBlocked) {
		f.reclaimPending, f.reclaimReady = false, false
		if out.Focused && in.HostActive {
			out.Focus = focusReclaim
		}
	}
	return out
}

// becameKey 在点进网页窗口的窗口时把 Rex 的焦点也挪到这个窗格上。
func (f *follower) becameKey() {
	f.mu.Lock()
	focused := f.place == nil || f.place.Focused
	f.mu.Unlock()
	if focused {
		return
	}
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	_, _ = f.rex.Call(ctx, "session.focus_block", map[string]any{"session_id": f.session, "block_id": f.block})
}

// loadBindings 取要转发给 Rex 的键，还有显示这个会话的那个 App：动作是交给它去做的。
func (f *follower) loadBindings(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	keymap, _ := f.rex.Output(ctx, "keymap", "--json")
	bindings := parseBindings(keymap, f.reservedKeys)
	client := ""
	if res, err := f.rex.Call(ctx, "client.list", map[string]any{}); err == nil {
		for _, c := range objects(res["clients"]) {
			info, _ := c["info"].(map[string]any)
			if info["kind"] != "app" {
				continue
			}
			id, _ := c["client_id"].(string)
			sessions, _ := c["session_ids"].([]any)
			if client == "" || slices.Contains(sessions, any(f.session)) {
				client = id
			}
			if slices.Contains(sessions, any(f.session)) {
				break
			}
		}
	}
	f.mu.Lock()
	f.bindings, f.appClient = bindings, client
	f.mu.Unlock()
}

// key 处理网页窗口窗口里按下的一个键：Rex 绑了它就交给 Rex，返回 true；不然留给页面。
func (f *follower) key(chord string) bool {
	c, ok := normChord(chord)
	if !ok {
		return false
	}
	f.mu.Lock()
	if reservedChord(c, f.reservedKeys) {
		f.prefix = nil
		f.mu.Unlock()
		return false
	}
	typed := append(slices.Clone(f.prefix), c)
	hit, more := matchKeys(f.bindings, typed)
	switch {
	case hit != nil:
		b, client := *hit, f.appClient
		f.prefix = nil
		f.mu.Unlock()
		f.perform(b, client)
		return true
	case more:
		f.prefix = typed
		f.mu.Unlock()
		return true
	}
	// 一串键按到一半按错了：这个键也吃掉，免得打进页面里
	swallowed := len(f.prefix) > 0
	f.prefix = nil
	f.mu.Unlock()
	return swallowed
}

// perform 让 Rex 做一个动作。名字里带点的是 App 自己的动作，交给 App；不带点的是配置文件里定义的。
func (f *follower) perform(b binding, client string) {
	// 会打开 Rex 自己界面的动作要 Rex 在前面才接得到键盘；只是改窗格大小的就留在网页窗口里
	if !strings.HasPrefix(b.Action, "pane.resize") && b.Action != "pane.zoom" {
		f.activateHost()
	}
	f.work.Go(func() {
		ctx, cancel := context.WithTimeout(f.ctx, 30*time.Second)
		defer cancel()
		if !strings.Contains(b.Action, ".") {
			args := []string{"do", "-s", f.session, "-b", f.block, b.Action}
			if b.Args != nil {
				if j, err := json.Marshal(b.Args); err == nil {
					args = append(args, "--args", string(j))
				}
			}
			_, _ = f.rex.Output(ctx, args...)
			return
		}
		if client == "" {
			return
		}
		in := map[string]any{
			"client_id": client, "action": b.Action, "session_id": f.session, "block_id": f.block, "origin": "key",
		}
		if b.Args != nil {
			in["args"] = b.Args
		}
		_, _ = f.rex.Call(ctx, "client.perform", in)
	})
}

// where 是 follower 眼下知道的：窗格在不在当前 tab 的平铺布局里，在哪（比例），是不是有焦点的那个。
func (f *follower) where() any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]any{"placed": f.place != nil, "session": f.session, "block": f.block}
	if f.place != nil {
		r := f.place.Rect
		out["rect"] = []float64{r.X, r.Y, r.W, r.H}
		out["focused"] = f.place.Focused
	}
	return out
}
