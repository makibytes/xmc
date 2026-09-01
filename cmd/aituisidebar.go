package cmd

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/makibytes/xmc/broker/backends"
)

// sortLabel returns a human-readable label for the current sort mode.
func sortLabel(s sortMode, metrics []backends.Metric) string {
	if s == sortByName || int(s)-1 >= len(metrics) {
		return "name"
	}
	return metrics[int(s)-1].Label
}

// sidebarPlan computes the per-window body-row allocation and collapse state
// for renderSidebar's height budget, in three stages: natural fit, floor fit
// with surplus distributed to the focused window, or auto-collapse (starting
// from the window farthest from focus) until everything fits.
type sidebarPlan struct {
	m          *aiTUIModel
	n          int
	height     int
	focusedIdx int   // -1 when chat is focused
	nat        []int // natural (full) body row count per window

	bodyAlloc     []int  // solve()'s output: body rows to render per window
	autoCollapsed []bool // auto-collapsed by this plan (not user-set via Space)
}

func newSidebarPlan(m *aiTUIModel, height int) *sidebarPlan {
	n := len(m.objTypes)
	focusedIdx := -1
	if m.focus > focusChat && int(m.focus)-1 < n {
		focusedIdx = int(m.focus) - 1
	}
	nat := make([]int, n)
	for i := range m.objTypes {
		nat[i] = m.windowNaturalRows(i)
	}
	return &sidebarPlan{
		m: m, n: n, height: height, focusedIdx: focusedIdx, nat: nat,
		bodyAlloc: make([]int, n), autoCollapsed: make([]bool, n),
	}
}

// collapsedCost: 1 (title) + 1 if there is a window below (margin).
func (p *sidebarPlan) collapsedCost(i int) int {
	if i < p.n-1 {
		return 2
	}
	return 1
}

// expandedCost: 2 (title+underline) + body + 1 if there is a window below.
func (p *sidebarPlan) expandedCost(i, body int) int {
	if i < p.n-1 {
		return 3 + body
	}
	return 2 + body
}

func (p *sidebarPlan) floor(i int) int {
	if p.nat[i] < 3 {
		return p.nat[i]
	}
	return 3
}

// isCollapsed reports whether window i renders collapsed right now — either
// user-collapsed (Space) or auto-collapsed by this plan's stage 3.
func (p *sidebarPlan) isCollapsed(i int) bool {
	return p.m.objTypes[i].collapsed || p.autoCollapsed[i]
}

// total sums every window's current cost, using bodyAt(i) as the body row
// count for an expanded window. Called with p.nat/p.floor/p.current at the
// three points the original code recomputed this sum by hand.
func (p *sidebarPlan) total(bodyAt func(i int) int) int {
	sum := 0
	for i := 0; i < p.n; i++ {
		if p.isCollapsed(i) {
			sum += p.collapsedCost(i)
		} else {
			sum += p.expandedCost(i, bodyAt(i))
		}
	}
	return sum
}

func (p *sidebarPlan) natural(i int) int { return p.nat[i] }
func (p *sidebarPlan) atFloor(i int) int { return p.floor(i) }
func (p *sidebarPlan) current(i int) int { return p.bodyAlloc[i] }

// solve fills bodyAlloc/autoCollapsed for the plan's height budget.
func (p *sidebarPlan) solve() {
	if p.total(p.natural) <= p.height {
		for i := 0; i < p.n; i++ {
			p.bodyAlloc[i] = p.nat[i]
		}
		return
	}
	if p.total(p.atFloor) <= p.height {
		p.fitAtFloor()
		return
	}
	p.autoCollapseToFit()
}

// fitAtFloor shrinks every expanded window to its floor and distributes the
// remaining surplus to the focused window first, then the rest in order.
func (p *sidebarPlan) fitAtFloor() {
	surplus := p.height - p.total(p.atFloor)
	for i := 0; i < p.n; i++ {
		p.bodyAlloc[i] = p.floor(i)
	}
	if p.focusedIdx >= 0 && !p.m.objTypes[p.focusedIdx].collapsed {
		add := min(p.nat[p.focusedIdx]-p.floor(p.focusedIdx), surplus)
		p.bodyAlloc[p.focusedIdx] += add
		surplus -= add
	}
	for i := 0; i < p.n; i++ {
		if i == p.focusedIdx || p.m.objTypes[i].collapsed || surplus <= 0 {
			continue
		}
		add := min(p.nat[i]-p.floor(i), surplus)
		p.bodyAlloc[i] += add
		surplus -= add
	}
}

// autoCollapseToFit collapses non-focused windows, farthest from focus
// first, until the total fits, then gives any remaining surplus to the
// focused window.
func (p *sidebarPlan) autoCollapseToFit() {
	order := make([]int, 0, p.n)
	if p.focusedIdx < 0 {
		// No focused window — collapse from the bottom up.
		for i := p.n - 1; i >= 0; i-- {
			order = append(order, i)
		}
	} else {
		// Interleave outward from focused index: bottom, top, alternating.
		lo, hi := p.focusedIdx-1, p.focusedIdx+1
		for lo >= 0 || hi < p.n {
			if hi < p.n {
				order = append(order, hi)
				hi++
			}
			if lo >= 0 {
				order = append(order, lo)
				lo--
			}
		}
	}

	for i := 0; i < p.n; i++ {
		p.bodyAlloc[i] = p.floor(i)
	}
	for _, i := range order {
		if i == p.focusedIdx || p.m.objTypes[i].collapsed {
			continue
		}
		p.autoCollapsed[i] = true
		p.bodyAlloc[i] = 0
		if p.total(p.current) <= p.height {
			break
		}
	}

	surplus := p.height - p.total(p.current)
	if p.focusedIdx >= 0 && !p.m.objTypes[p.focusedIdx].collapsed && surplus > 0 {
		add := p.nat[p.focusedIdx] - p.bodyAlloc[p.focusedIdx]
		if add > surplus {
			add = surplus
		}
		if add > 0 {
			p.bodyAlloc[p.focusedIdx] += add
		}
	}
}

func (m aiTUIModel) renderSidebar(width, height int) (string, []int) {
	var b strings.Builder
	lines := 0
	n := len(m.objTypes)

	if n == 0 {
		if m.loadingObjects {
			b.WriteString(m.spinner.View() + " " + dimStyle.Render("Loading…") + "\n")
			lines++
		} else {
			b.WriteString(dimStyle.Render("(no management API)") + "\n")
			lines++
		}
		for lines < height {
			b.WriteString("\n")
			lines++
		}
		return b.String(), nil
	}

	plan := newSidebarPlan(&m, height)
	plan.solve()

	// Render each window. Junction rows are only recorded for expanded windows
	// (collapsed windows have no underline so need no ├ junction).
	junctionRows := make([]int, 0, n)
	for i := range m.objTypes {
		isCollapsed := plan.isCollapsed(i)
		if isCollapsed {
			junctionRows = append(junctionRows, -1) // no junction
		} else {
			junctionRows = append(junctionRows, lines+1) // underline is at lines+1
		}
		lines += m.writeObjectSection(&b, width, plan.bodyAlloc[i], i, isCollapsed)
		// Add blank margin row after every window except the last.
		if i < n-1 {
			b.WriteString("\n")
			lines++
		}
	}

	// Pad remaining height.
	for lines < height {
		b.WriteString("\n")
		lines++
	}

	return b.String(), junctionRows
}

// windowNaturalRows returns the number of body rows a window would show at full height
// (not counting the 2-line title+underline header).
func (m aiTUIModel) windowNaturalRows(idx int) int {
	w := m.objTypes[idx]
	if w.kind == objWindowProcs {
		if len(m.procs) == 0 {
			return 1 // "(none)" line
		}
		return len(m.procs)
	}
	// Object window.
	if m.loadingObjects && w.nodes == nil {
		return 1 // "loading…" line
	}
	items := m.getFilteredSortedNodes(idx)
	if len(items) == 0 {
		return 1 // "(none)" line
	}
	if w.treeView && w.hierarchical {
		rows := 0
		for _, node := range items {
			rows++ // parent row
			rows += len(node.Children)
		}
		return rows
	}
	return len(items)
}

// writeWindow renders one sidebar window's shared chrome — disclosure glyph,
// header (with focus styling), collapse, underline, "(none)"/scrolling
// N-more — and delegates everything that differs between an object window
// and the Processes window to its callers: extra (non-empty short-circuits
// the row list entirely, for the loading/error states only object windows
// have), and renderRow (the actual per-row formatting, which differs enough
// — display fields, truncation budget, selection marker shape — that sharing
// it would cost more clarity than the shared chrome saves). trailer, if
// non-nil, appends one more line after the row list (object windows' filter
// indicator).
func (m aiTUIModel) writeWindow(
	b *strings.Builder, width, bodyLines int, collapsed bool,
	headerText string, focused bool,
	extra string,
	nRows, sel int, renderRow func(ri int, selected bool) string,
	trailer func() (string, bool),
) int {
	lines := 0

	glyph := "▾ "
	if collapsed {
		glyph = "▸ "
	}
	full := glyph + headerText
	if focused {
		pad := width - lipgloss.Width(full) - 4
		if pad < 0 {
			pad = 0
		}
		b.WriteString(m.theme().focusHeader().Render(full + strings.Repeat(" ", pad) + "◂"))
	} else {
		b.WriteString(histTitleStyle.Render(full))
	}
	b.WriteString("\n")
	lines++

	// When collapsed, stop here — no underline or body rows.
	if collapsed {
		return lines
	}

	b.WriteString(dimStyle.Render(strings.Repeat("─", width-1)))
	b.WriteString("\n")
	lines++

	if extra != "" {
		b.WriteString(extra + "\n")
		return lines + 1
	}

	if nRows == 0 {
		b.WriteString(dimStyle.Render("  (none)") + "\n")
		return lines + 1
	}

	start, end := computeWindow(nRows, sel, bodyLines)

	if start > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  ▲ %d more", start)) + "\n")
		lines++
		start++
		if start > sel {
			start = sel
		}
	}

	showBottomHint := end < nRows
	limit := end
	if showBottomHint {
		limit = end - 1
		if limit < start {
			limit = start
		}
	}

	for ri := start; ri < limit; ri++ {
		b.WriteString(renderRow(ri, focused && ri == sel))
		b.WriteString("\n")
		lines++
	}

	if showBottomHint {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  ▼ %d more", nRows-limit)) + "\n")
		lines++
	}

	if trailer != nil {
		if line, ok := trailer(); ok {
			b.WriteString(line)
			lines++
		}
	}

	return lines
}

// writeObjectSection renders one object-type window and returns lines written.
// collapsed=true renders only the title line (no underline, no body rows).
func (m aiTUIModel) writeObjectSection(b *strings.Builder, width, bodyLines, idx int, collapsed bool) int {
	// Dispatch to the process-window renderer for the dedicated Processes pane.
	if m.objTypes[idx].kind == objWindowProcs {
		return m.writeProcessSection(b, width, bodyLines, collapsed)
	}

	w := m.objTypes[idx]
	focused := int(m.focus)-1 == idx
	items := m.getFilteredSortedNodes(idx)

	// Header text.
	headerText := fmt.Sprintf("%s (%d)", w.label, len(w.nodes))
	if m.loadingObjects && w.nodes == nil {
		headerText = w.label + " (…)"
	}
	if w.filter != "" {
		if m.loadingObjects && w.nodes == nil {
			headerText = w.label + fmt.Sprintf(" (%d/…)", len(items))
		} else {
			headerText = fmt.Sprintf("%s (%d/%d)", w.label, len(items), len(w.nodes))
		}
	}
	if w.sortIdx != sortByName && len(w.nodes) > 0 {
		metrics := firstMetrics(w.nodes)
		headerText += " ↕" + sortLabel(w.sortIdx, metrics)
	}

	// Loading/error short-circuit the row list entirely (see writeWindow's extra).
	var extra string
	switch {
	case m.loadingObjects && w.nodes == nil:
		extra = dimStyle.Render("  loading…")
	case w.err != nil:
		// Surface List() errors visibly rather than showing a silent "(none)".
		// Common for cloud brokers on auth/permission failures.
		msg := "⚠ " + w.err.Error()
		maxLen := width - 3
		if maxLen < 5 {
			maxLen = 5
		}
		if len([]rune(msg)) > maxLen {
			msg = string([]rune(msg)[:maxLen-1]) + "…"
		}
		extra = sidebarErrStyle.Render(msg)
	}

	// Build display rows from the same flattened list used for selection
	// (sidebarRows) — rendering order and selection order can never drift
	// apart, and sel is a direct row index, no item↔row translation needed.
	sRows := m.sidebarRows(idx)
	type displayRow struct {
		name   string
		metric string
		indent bool
	}
	rows := make([]displayRow, len(sRows))
	for i, r := range sRows {
		if r.parentName == "" {
			rows[i] = displayRow{name: r.node.Name, metric: fmtNodeDetail(r.node)}
			continue
		}
		label := r.node.Name
		if r.node.Kind != "" {
			label = r.node.Kind + " " + r.node.Name
		}
		rows[i] = displayRow{name: label, metric: fmtNodeMetric(r.node), indent: true}
	}

	renderRow := func(ri int, selected bool) string {
		r := rows[ri]
		prefix := "  "
		if r.indent {
			prefix = "  └ "
		}

		name := r.name
		metricStr := r.metric
		maxName := width - len(metricStr) - len(prefix) - 2
		if maxName < 5 {
			maxName = 5
		}
		if nameRunes := []rune(name); len(nameRunes) > maxName {
			name = string(nameRunes[:maxName-1]) + "…"
		}

		if selected && r.indent {
			marker := "▸ └ "
			pad := width - len(marker) - 2 - len(name) - len(metricStr)
			if pad < 1 {
				pad = 1
			}
			return sidebarSelStyle.Render(fmt.Sprintf("%s%s%s%s", marker, name, strings.Repeat(" ", pad), metricStr))
		}
		if selected {
			pad := width - 4 - len(name) - len(metricStr)
			if pad < 1 {
				pad = 1
			}
			return sidebarSelStyle.Render(fmt.Sprintf("▸ %s%s%s", name, strings.Repeat(" ", pad), metricStr))
		}
		pad := width - len(prefix) - len(name) - len(metricStr) - 1
		if pad < 1 {
			pad = 1
		}
		if metricStr != "" {
			return fmt.Sprintf("%s%s%s%s", prefix, name, strings.Repeat(" ", pad), dimStyle.Render(metricStr))
		}
		return prefix + name
	}

	trailer := func() (string, bool) {
		if focused && m.filtering {
			return statusKeyStyle.Render("/") + w.filter + "▍", true
		}
		return "", false
	}

	return m.writeWindow(b, width, bodyLines, collapsed, headerText, focused, extra, len(rows), w.sel, renderRow, trailer)
}

// fmtNodeMetric formats the first metric of a node for compact sidebar display.
func fmtNodeMetric(n backends.ObjectNode) string {
	if len(n.Metrics) == 0 {
		return ""
	}
	return fmtCount(n.Metrics[0].Value)
}

// fmtNodeDetail formats a top-level node's type (Kind) and first metric for the
// sidebar, e.g. "fanout" or "limits 42".
func fmtNodeDetail(n backends.ObjectNode) string {
	return strings.TrimSpace(n.Kind + " " + fmtNodeMetric(n))
}

// firstMetrics returns the metrics from the first node with metrics, or nil.
func firstMetrics(nodes []backends.ObjectNode) []backends.Metric {
	for _, n := range nodes {
		if len(n.Metrics) > 0 {
			return n.Metrics
		}
	}
	return nil
}

// getFilteredSortedNodes returns the node list for window idx, filtered and
// sorted. Memoized on the window (see objWindow.filteredCache): View() reruns
// on every spinner tick and every status-bar hotkey resolution calls this too
// (via sidebarRows), so without caching this filter+copy+sort work — cheap
// once, wasteful dozens of times a second while idle — reran on every one of
// them. The cache is invalidated by a filter edit, a sort cycle, or a data
// refresh (dataGen); it deliberately does not depend on treeView, which
// sidebarRows applies on top of this result, not before it.
func (m aiTUIModel) getFilteredSortedNodes(idx int) []backends.ObjectNode {
	if idx < 0 || idx >= len(m.objTypes) {
		return nil
	}
	w := &m.objTypes[idx]
	key := filterSortCacheKey{filter: w.filter, sortIdx: w.sortIdx, dataGen: w.dataGen}
	if w.filteredCacheValid && w.filteredCacheKey == key {
		return w.filteredCache
	}

	items := w.nodes
	if w.filter != "" {
		lower := strings.ToLower(w.filter)
		var filtered []backends.ObjectNode
		for _, n := range items {
			if strings.Contains(strings.ToLower(n.Name), lower) {
				filtered = append(filtered, n)
			}
		}
		items = filtered
	}
	sorted := make([]backends.ObjectNode, len(items))
	copy(sorted, items)
	if w.sortIdx == sortByName {
		slices.SortFunc(sorted, func(a, b backends.ObjectNode) int { return strings.Compare(a.Name, b.Name) })
	} else {
		metricIdx := int(w.sortIdx) - 1
		slices.SortFunc(sorted, func(a, b backends.ObjectNode) int {
			va, vb := int64(0), int64(0)
			if metricIdx < len(a.Metrics) {
				va = a.Metrics[metricIdx].Value
			}
			if metricIdx < len(b.Metrics) {
				vb = b.Metrics[metricIdx].Value
			}
			return -cmp.Compare(va, vb) // descending
		})
	}

	w.filteredCache = sorted
	w.filteredCacheKey = key
	w.filteredCacheValid = true
	return sorted
}

// computeWindow returns the visible [start, end) range for a windowed list,
// centering the selection in the window.
func computeWindow(total, selected, visible int) (start, end int) {
	if total <= visible {
		return 0, total
	}
	start = selected - visible/2
	if start < 0 {
		start = 0
	}
	end = start + visible
	if end > total {
		end = total
		start = end - visible
		if start < 0 {
			start = 0
		}
	}
	return start, end
}

// fmtCount formats a message count for compact display.
func fmtCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// collapseBlankLines tidies streamed reasoning output: solitary blank lines are
// removed, and runs of 2+ blank lines are collapsed to a single blank line.
func collapseBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	blankRun := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			blankRun++
			continue
		}
		if blankRun >= 2 {
			out = append(out, "")
		}
		blankRun = 0
		out = append(out, line)
	}
	// Preserve a trailing blank line only if the run was ≥2.
	if blankRun >= 2 {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}
