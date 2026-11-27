package rexwebview

import (
	"math"
	"time"
)

// floatDrag 保存标题栏按下时的正文位置。拖动时直接用鼠标位移移动窗口，
// 不等待 Rex 的 session.view，也不重新绘制网页。松手后等布局确认再交还普通跟随。
type floatDrag struct {
	layerID        string
	frame          rect
	mouseX, mouseY float64
	released       time.Time
	releaseSlot    rect   // 松手时已经知道的位置；拖动中的中间位置不能算落点确认
	releaseSeq     uint64 // 只接受松手之后开始的布局查询
}

const floatDragReleaseGrace = 500 * time.Millisecond

func (f *follower) beginFloatDrag(x, y float64, frame rect) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.drag = nil
	if f.place == nil || !f.place.floating || (f.shown != "" && f.shown != f.session) {
		return false
	}
	// 留出标题栏上沿和两侧的缩放边缘；点击网页或缩放边缘仍由各自的宿主处理。
	const edge = 7
	header := f.chrome.Header
	if header <= edge || frame.W <= 2*edge || frame.H <= 0 ||
		x < frame.X+edge || x >= frame.maxX()-edge || y < frame.Y-header+edge || y >= frame.Y {
		return false
	}
	f.drag = &floatDrag{layerID: f.place.layerID, frame: frame, mouseX: x, mouseY: y}
	return true
}

func (f *follower) moveFloatDrag(x, y float64, released bool) (rect, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.holdFloatDragLocked() || !f.drag.released.IsZero() {
		return rect{}, false
	}
	d := f.drag
	frame := d.frame
	frame.X += math.Round(x - d.mouseX)
	frame.Y += math.Round(y - d.mouseY)
	if released {
		d.released = f.now()
		d.releaseSlot = f.place.Rect
		d.releaseSeq = f.refreshSeq + 1
		// 只安排一次后台刷新。最后的 mouse-up 位置也先显示，不能等布局响应。
		f.refreshSoon()
	}
	return frame, true
}

// holdFloatDrag 在拖动和等待落点时跳过定时跟随，免得旧布局把网页拉回原处。
func (f *follower) holdFloatDrag() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.holdFloatDragLocked()
}

func (f *follower) holdFloatDragLocked() bool {
	d := f.drag
	if d == nil {
		return false
	}
	if f.place == nil || !f.place.floating || f.place.layerID != d.layerID || (f.shown != "" && f.shown != f.session) ||
		(!d.released.IsZero() && ((f.appliedSeq >= d.releaseSeq && f.place.Rect != d.releaseSlot) || !f.now().Before(d.released.Add(floatDragReleaseGrace)))) {
		f.drag = nil
		return false
	}
	return true
}

func (f *follower) cancelFloatDrag() {
	f.mu.Lock()
	f.drag = nil
	f.mu.Unlock()
}
