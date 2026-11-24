package rexwebview

import (
	"encoding/json/v2"
	"math"
	"slices"
	"strconv"
	"strings"
)

// 网页窗口盖在 Rex 的窗格上，位置是这样算出来的（做法和数值都来自 leaf 的 src/browser.swift，MIT）：
//
//   - session.view 给出窗格在窗口里的位置，是占“放窗格的那块地方”的比例；
//   - 系统的窗口列表给出 Rex 窗口在屏幕上的位置；
//   - Rex 自己的界面（侧边栏、工具栏、窗格之间的空隙、窗格的标题栏）占了多少，从 Rex App 的设置里读。
//
// Rex 没有公开这些尺寸，下面 chromeFrom 里的数是对着 Rex 0.1.0 preview 量出来的，Rex 改了界面就要跟着改。

// rect 是一个矩形。用在屏幕上时单位是点，原点在主屏幕的左上角，y 向下。
type rect struct{ X, Y, W, H float64 }

func (r rect) maxX() float64 { return r.X + r.W }
func (r rect) maxY() float64 { return r.Y + r.H }

func (r rect) intersects(o rect) bool {
	return r.X < o.maxX() && o.X < r.maxX() && r.Y < o.maxY() && o.Y < r.maxY()
}

// placement 是窗格在当前 tab 里的位置（比例）和它是不是 Rex 里有焦点的那个。
type placement struct {
	Rect     rect
	Focused  bool
	floating bool
	// gapOffset 是逐级分屏时分给当前叶节点的接缝修正，以一个完整 gap 为单位。
	gapOffset rect
}

func parseRect(v any) (rect, bool) {
	a, ok := v.([]any)
	if !ok || len(a) != 4 {
		return rect{}, false
	}
	var n [4]float64
	for i, x := range a {
		f, ok := x.(float64)
		if !ok {
			return rect{}, false
		}
		n[i] = f
	}
	return rect{n[0], n[1], n[2], n[3]}, true
}

// 沿 session.view 的 split.before / split.after 找叶节点。服务端的 rect 没扣分隔条；
// Rex 将每一级父窗格先减一个 gap，再把余下宽度或高度按 ratio 分给两个孩子。
func findBlock(block string, node any) (rect, rect, bool) {
	n, _ := node.(map[string]any)
	if n["block_id"] == block {
		r, ok := parseRect(n["rect"])
		return r, rect{}, ok
	}
	split, _ := n["split"].(map[string]any)
	if split == nil {
		return rect{}, rect{}, false
	}
	parent, valid := parseRect(n["rect"])
	for _, branch := range []string{"before", "after"} {
		leaf, offset, ok := findBlock(block, split[branch])
		if !ok {
			continue
		}
		// 这一层 gap 缩短父窗格的可分空间。右/下子树还须从分隔条另一侧开始。
		var after float64
		if branch == "after" {
			after = 1
		}
		if valid && parent.W > 0 && parent.H > 0 {
			if split["direction"] == "vertical" {
				offset.Y += after - (leaf.Y-parent.Y)/parent.H
				offset.H -= leaf.H / parent.H
			} else {
				offset.X += after - (leaf.X-parent.X)/parent.W
				offset.W -= leaf.W / parent.W
			}
		}
		return leaf, offset, true
	}
	return rect{}, rect{}, false
}

// objects 把一个 JSON 数组里的对象取出来，不是对象的跳过。
func objects(v any) []map[string]any {
	a, _ := v.([]any)
	out := make([]map[string]any, 0, len(a))
	for _, x := range a {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// viewContains 说明会话里还有没有这个 block，在哪个 tab 里都算。
func viewContains(view map[string]any, block string) bool {
	for _, w := range objects(view["windows"]) {
		for _, layer := range objects(w["layers"]) {
			for _, b := range objects(layer["blocks"]) {
				if b["block_id"] == block {
					return true
				}
			}
			if _, _, ok := findBlock(block, layer["layout"]); ok {
				return true
			}
		}
	}
	return false
}

// place 返回当前标签里没被遮住的窗格；浮动窗格需要显式开启。Rex 给的是窗口比例，
// 浮层内的分屏也一样，不用再乘一遍浮层的 bounds。
func place(view map[string]any, block string, floatingPanes bool) *placement {
	active, _ := view["active_window_id"].(string)
	var win map[string]any
	for _, w := range objects(view["windows"]) {
		if w["window_id"] == active && active != "" {
			win = w
		}
	}
	if win == nil {
		return nil
	}
	focused := win["focused_block_id"] == block
	if zoomed, _ := win["zoomed_block_id"].(string); zoomed != "" {
		if zoomed != block {
			return nil
		}
		return &placement{Rect: rect{0, 0, 1, 1}, Focused: focused}
	}
	// layers 从后往前排。只有目标层后面的条目有可能挡着它，和谁拿着键盘焦点无关。
	layers := objects(win["layers"])
	for i, layer := range layers {
		if layer["state"] == "minimized" || (layer["kind"] != "tiled" && !floatingPanes) {
			continue
		}
		at, offset, found := findBlock(block, layer["layout"])
		if !found {
			continue
		}
		for _, upper := range layers[i+1:] {
			if upper["state"] == "minimized" || upper["layout"] == nil {
				continue
			}
			bounds, ok := parseRect(upper["bounds"])
			if !ok || bounds.intersects(at) {
				return nil
			}
		}
		return &placement{Rect: at, Focused: focused, gapOffset: offset, floating: layer["kind"] == "floating"}
	}
	return nil
}

// chrome 是 Rex 窗口里不放窗格的地方：四边留的，窗格之间的空隙，窗格标题栏的高度，还有窗格的圆角。
type chrome struct {
	Top, Left, Right, Bottom float64
	Gap, Header, Radius      float64
	floatTop                 float64 // 浮层画布的上边，不是平铺的首行标题所在的位置
}

// chromeFrom 按 Rex App 的设置算出 chrome。pref 读 com.superlogical.rex 里 Workspace.<名字> 的值。
func chromeFrom(pref func(name string) (string, bool)) chrome {
	get := func(name string) string {
		v, _ := pref(name)
		return v
	}
	c := chrome{Top: 46, Left: 8, Right: 8, Bottom: 8, Gap: 8, Header: 36, Radius: 10}
	edge := 8.0
	if get("paneDensity") == "compact" {
		edge = 0
		c = chrome{Top: 41, Gap: 2, Header: 35}
	}
	headers, ok := pref("paneHeaderVisibility")
	if !ok {
		headers = "always"
		if shows, ok := pref("showsPaneHeaders"); ok && shows == "0" {
			headers = "never"
		}
	}
	if headers != "always" {
		c.Header = 0
	}
	// tab 竖着排在侧边栏里，或者工具栏不是一直显示：上面就没有工具栏
	vertical := get("tabStyle") == "vertical"
	toolbar, ok := pref("toolbarBehavior")
	if vertical || (ok && toolbar != "alwaysShow") {
		c.Top = edge
	}
	c.floatTop = c.Top
	if vertical && (!ok || toolbar == "alwaysShow") {
		// preview.21 在竖排常驻工具栏下，浮层画布比平铺区多留一条标题区。
		// 这是 2x 桌面上的估算，单位是点，不随 Retina 像素密度翻倍。
		const titlebarHeight = 34
		c.floatTop += titlebarHeight
	}
	if vertical && get("sidebarCollapsed") != "1" {
		width := 240.0
		if w, err := strconv.ParseFloat(get("sidebarWidth"), 64); err == nil && w > 0 {
			width = w
		}
		if get("sidebarPosition") == "right" {
			c.Right = width
		} else {
			c.Left = width
		}
	}
	return c
}

// body 把窗格比例和分隔条修正换成点。正文不覆盖 Rex 自己的描边、标题或拖动区。
func (c chrome) body(p placement, win rect) rect {
	top := c.Top
	if p.floating {
		top = c.floatTop
	}
	w, h := win.W-c.Left-c.Right, win.H-top-c.Bottom
	r, g := p.Rect, p.gapOffset
	x := win.X + c.Left + r.X*w + g.X*c.Gap
	y := win.Y + top + r.Y*h + g.Y*c.Gap
	right := x + r.W*w + g.W*c.Gap
	bottom := y + r.H*h + g.H*c.Gap
	border := 0.0
	if c.Radius > 0 || p.floating {
		border = 1
	}
	// 分别取整四条边，避免先舍入起点再舍入宽度导致右边越过原生描边。
	x, y = math.Round(x)+border, math.Round(y)+max(c.Header, border)
	return rect{x, y, math.Round(right) - border - x, math.Round(bottom) - border - y}
}

// shownSession 是 Rex App 眼下显示的会话，从它的设置 Workspace.lastFocusedSession 里读，读不出来是空的。
func shownSession(pref func(name string) (string, bool)) string {
	raw, ok := pref("lastFocusedSession")
	if !ok {
		return ""
	}
	var v struct {
		SessionID string `json:"sessionID"`
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return ""
	}
	return v.SessionID
}

// ---- 按键 ----

// 修饰键写在前面，固定按这个顺序；pane_darwin.m 里的 rexwv_chord 也是这个顺序。
var chordMods = []string{"ctrl", "alt", "shift", "cmd"}

var (
	modAlias = map[string]string{
		"cmd": "cmd", "command": "cmd", "super": "cmd",
		"shift": "shift",
		"alt":   "alt", "opt": "alt", "option": "alt",
		"ctrl": "ctrl", "control": "ctrl",
	}
	keyAlias = map[string]string{"esc": "escape", "return": "enter", "del": "delete"}
)

// normChord 把 Rex 写的一个键（比如 Shift+Cmd+P）换成固定的写法 shift+cmd+p。
// 认不出来的返回 false：带方括号的物理键（cmd+[Digit1]）、某个模式里的键（resize/left）。
func normChord(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "+" {
		return s, true
	}
	// 把最后的加号当作键：cmd++ 是 Command 加上加号，不是缺了一个键的 cmd+。
	modsText, key, _ := strings.CutLast(s, "+")
	if key == "" && strings.HasSuffix(s, "++") {
		modsText, key = strings.TrimSuffix(s, "++"), "+"
	} else if !strings.Contains(s, "+") {
		modsText, key = "", s
	}
	if key == "" || modAlias[key] != "" || strings.ContainsAny(key, " \t\r\n") || (len(key) > 1 && strings.ContainsAny(key, "/[]")) {
		return "", false
	}
	var mods []string
	if modsText != "" {
		for part := range strings.SplitSeq(modsText, "+") {
			m, ok := modAlias[part]
			if !ok || slices.Contains(mods, m) {
				return "", false
			}
			mods = append(mods, m)
		}
	} else if strings.HasPrefix(s, "+") {
		return "", false
	}
	if a, ok := keyAlias[key]; ok {
		key = a
	}
	out := make([]string, 0, len(mods)+1)
	for _, m := range chordMods {
		if slices.Contains(mods, m) {
			out = append(out, m)
		}
	}
	return strings.Join(append(out, key), "+"), true
}

// binding 是 Rex 里绑了动作的一串键。
type binding struct {
	Keys   []string
	Action string
	Args   any
}

// defaultBindings 是 Rex for Mac 出厂的快捷键里，焦点在网页应用上时还用得着的那些（摘自 Rex 文档的 Mac Actions）。
// App 不公布它的快捷键，rex keymap 只有配置文件里绑的，所以只能抄一份；在 App 的设置里改过的这里不知道。
var defaultBindings = []binding{
	{Keys: []string{"shift+cmd+p"}, Action: "client.palette.toggle"},
	{Keys: []string{"cmd+k"}, Action: "session.switch"},
	{Keys: []string{"cmd+p"}, Action: "pane.go_to"},
	{Keys: []string{"cmd+t"}, Action: "client.tab.new"},
	{Keys: []string{"ctrl+tab"}, Action: "client.tab.next"},
	{Keys: []string{"ctrl+shift+tab"}, Action: "client.tab.previous"},
	{Keys: []string{"alt+cmd+left"}, Action: "pane.focus.left"},
	{Keys: []string{"alt+cmd+right"}, Action: "pane.focus.right"},
	{Keys: []string{"alt+cmd+up"}, Action: "pane.focus.up"},
	{Keys: []string{"alt+cmd+down"}, Action: "pane.focus.down"},
	{Keys: []string{"cmd+]"}, Action: "pane.focus_next"},
	{Keys: []string{"cmd+["}, Action: "pane.focus_previous"},
	{Keys: []string{"cmd+d"}, Action: "pane.split.right"},
	{Keys: []string{"shift+cmd+d"}, Action: "pane.split.down"},
	{Keys: []string{"shift+cmd+enter"}, Action: "pane.zoom"},
	{Keys: []string{"cmd+/"}, Action: "client.sidebar.toggle"},
}

func reservedChord(chord string, keys []string) bool {
	return chord == "cmd+w" || chord == "cmd+q" || slices.Contains(keys, chord)
}

// 只转发有修饰键开头的绑定。只要序列里出现应用保留键，连前缀也应留给页面。
func forwardable(keys, reservedKeys []string) bool {
	if len(keys) == 0 {
		return false
	}
	first := keys[0]
	if !strings.Contains(first, "cmd+") && !strings.Contains(first, "ctrl+") && !strings.Contains(first, "alt+") {
		return false
	}
	return !slices.ContainsFunc(keys, func(key string) bool { return reservedChord(key, reservedKeys) })
}

// parseBindings 从 rex keymap --json 的结果里取出要转发的绑定，后面接上 defaultBindings；
// 同一串键配置文件里绑了的在前，先被找到。
func parseBindings(keymap []byte, reservedKeys []string) []binding {
	var res struct {
		Bindings []struct {
			Keys   []string `json:"keys"`
			Action string   `json:"action"`
			Args   any      `json:"args"`
		} `json:"bindings"`
	}
	_ = json.Unmarshal(keymap, &res)
	var out []binding
	for _, b := range res.Bindings {
		// pane.send_key 是把键打进终端里，对网页没有意义
		if b.Action == "" || b.Action == "pane.send_key" {
			continue
		}
		keys := make([]string, 0, len(b.Keys))
		for _, k := range b.Keys {
			if c, ok := normChord(k); ok {
				keys = append(keys, c)
			}
		}
		if len(keys) != len(b.Keys) || !forwardable(keys, reservedKeys) {
			continue
		}
		out = append(out, binding{Keys: keys, Action: b.Action, Args: b.Args})
	}
	for _, b := range defaultBindings {
		if forwardable(b.Keys, reservedKeys) {
			out = append(out, b)
		}
	}
	return out
}

// matchKeys 拿已经按下的一串键去找绑定：找到了返回它；还只是某个绑定的前半截时 more 是 true。
func matchKeys(bindings []binding, typed []string) (hit *binding, more bool) {
	for i := range bindings {
		keys := bindings[i].Keys
		if slices.Equal(keys, typed) {
			return &bindings[i], false
		}
		if len(keys) > len(typed) && slices.Equal(keys[:len(typed)], typed) {
			more = true
		}
	}
	return nil, more
}
