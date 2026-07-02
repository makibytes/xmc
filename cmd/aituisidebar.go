package cmd

import (
	"fmt"
	"sort"
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

	// Determine which window is focused (if any; -1 when chat is focused).
	focusedIdx := -1
	if m.focus > focusChat && int(m.focus)-1 < n {
		focusedIdx = int(m.focus) - 1
	}

	// Natural row count per window (body rows only, ignoring header/underline/margin).
	nat := make([]int, n)
	for i := range m.objTypes {
		nat[i] = m.windowNaturalRows(i)
	}

	// Per-window height cost helpers.
	// collapsedCost: 1 (title) + 1 if there is a window below (margin).
	collapsedCost := func(i int) int {
		if i < n-1 {
			return 2 // title + margin
		}
		return 1 // title only (last window, no margin)
	}
	// expandedCost: 2 (title+underline) + body + 1 if there is a window below.
	expandedCost := func(i, body int) int {
		if i < n-1 {
			return 3 + body // title + underline + body + margin
		}
		return 2 + body // no margin for last window
	}
	floor := func(i int) int {
		if nat[i] < 3 {
			return nat[i]
		}
		return 3
	}

	// Planner output: per-window body allocation and whether it is collapsed.
	bodyAlloc := make([]int, n)
	autoCollapsed := make([]bool, n) // auto-collapsed by the planner (not user-set)

	// Stage 1: check if all non-user-collapsed windows fit at natural height.
	total := 0
	for i := range m.objTypes {
		if m.objTypes[i].collapsed {
			total += collapsedCost(i)
		} else {
			total += expandedCost(i, nat[i])
		}
	}

	if total <= height {
		// Natural fit: assign each window its full content.
		for i := range m.objTypes {
			bodyAlloc[i] = nat[i]
		}
	} else {
		// Stage 2: shrink all expanded windows to floor rows.
		floorTotal := 0
		for i := range m.objTypes {
			if m.objTypes[i].collapsed {
				floorTotal += collapsedCost(i)
			} else {
				floorTotal += expandedCost(i, floor(i))
			}
		}

		if floorTotal <= height {
			// Fits at floor. Assign floors, then distribute surplus to windows
			// starting with the focused window.
			surplus := height - floorTotal
			for i := range m.objTypes {
				bodyAlloc[i] = floor(i)
			}
			// Give focused window extra first.
			if focusedIdx >= 0 && !m.objTypes[focusedIdx].collapsed {
				add := nat[focusedIdx] - floor(focusedIdx)
				if add > surplus {
					add = surplus
				}
				bodyAlloc[focusedIdx] += add
				surplus -= add
			}
			// Then distribute to others in index order.
			for i := range m.objTypes {
				if i == focusedIdx || m.objTypes[i].collapsed || surplus <= 0 {
					continue
				}
				add := nat[i] - floor(i)
				if add > surplus {
					add = surplus
				}
				bodyAlloc[i] += add
				surplus -= add
			}
		} else {
			// Stage 3: auto-collapse non-focused windows until everything fits.
			// Start from the window farthest from the focused index.
			order := make([]int, 0, n)
			if focusedIdx < 0 {
				// No focused window — collapse from the bottom up.
				for i := n - 1; i >= 0; i-- {
					order = append(order, i)
				}
			} else {
				// Interleave outward from focused index: bottom, top, alternating.
				lo, hi := focusedIdx-1, focusedIdx+1
				for lo >= 0 || hi < n {
					if hi < n {
						order = append(order, hi)
						hi++
					}
					if lo >= 0 {
						order = append(order, lo)
						lo--
					}
				}
			}

			// Initialise at floor.
			for i := range m.objTypes {
				bodyAlloc[i] = floor(i)
			}
			// Auto-collapse in collapse order until we fit.
			for _, i := range order {
				if i == focusedIdx || m.objTypes[i].collapsed {
					continue
				}
				autoCollapsed[i] = true
				bodyAlloc[i] = 0

				// Recalculate total.
				cur := 0
				for j := range m.objTypes {
					if m.objTypes[j].collapsed || autoCollapsed[j] {
						cur += collapsedCost(j)
					} else {
						cur += expandedCost(j, bodyAlloc[j])
					}
				}
				if cur <= height {
					break
				}
			}

			// Distribute remaining surplus to focused window.
			cur := 0
			for j := range m.objTypes {
				if m.objTypes[j].collapsed || autoCollapsed[j] {
					cur += collapsedCost(j)
				} else {
					cur += expandedCost(j, bodyAlloc[j])
				}
			}
			surplus := height - cur
			if focusedIdx >= 0 && !m.objTypes[focusedIdx].collapsed && surplus > 0 {
				add := nat[focusedIdx] - bodyAlloc[focusedIdx]
				if add > surplus {
					add = surplus
				}
				if add > 0 {
					bodyAlloc[focusedIdx] += add
				}
			}
		}
	}

	// Render each window. Junction rows are only recorded for expanded windows
	// (collapsed windows have no underline so need no ├ junction).
	junctionRows := make([]int, 0, n)
	for i := range m.objTypes {
		isCollapsed := m.objTypes[i].collapsed || autoCollapsed[i]
		if isCollapsed {
			junctionRows = append(junctionRows, -1) // no junction
		} else {
			junctionRows = append(junctionRows, lines+1) // underline is at lines+1
		}
		lines += m.writeObjectSection(&b, width, bodyAlloc[i], i, isCollapsed)
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

// writeObjectSection renders one object-type window and returns lines written.
// collapsed=true renders only the title line (no underline, no body rows).
func (m aiTUIModel) writeObjectSection(b *strings.Builder, width, bodyLines, idx int, collapsed bool) int {
	// Dispatch to the process-window renderer for the dedicated Processes pane.
	if m.objTypes[idx].kind == objWindowProcs {
		return m.writeProcessSection(b, width, bodyLines, collapsed)
	}

	lines := 0
	w := m.objTypes[idx]
	focused := int(m.focus)-1 == idx
	items := m.getFilteredSortedNodes(idx)

	// Disclosure glyph: ▸ when collapsed, ▾ when expanded.
	glyph := "▾ "
	if collapsed {
		glyph = "▸ "
	}

	// Header.
	headerText := glyph + fmt.Sprintf("%s (%d)", w.label, len(w.nodes))
	if m.loadingObjects && w.nodes == nil {
		headerText = glyph + w.label + " (…)"
	}
	if w.filter != "" {
		if m.loadingObjects && w.nodes == nil {
			headerText = glyph + w.label + fmt.Sprintf(" (%d/…)", len(items))
		} else {
			headerText = glyph + fmt.Sprintf("%s (%d/%d)", w.label, len(items), len(w.nodes))
		}
	}
	if w.sortIdx != sortByName && len(w.nodes) > 0 {
		metrics := firstMetrics(w.nodes)
		headerText += " ↕" + sortLabel(w.sortIdx, metrics)
	}
	if focused {
		pad := width - lipgloss.Width(headerText) - 4
		if pad < 0 {
			pad = 0
		}
		b.WriteString(sidebarFocusStyle.Render(headerText + strings.Repeat(" ", pad) + "◂"))
	} else {
		b.WriteString(histTitleStyle.Render(headerText))
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

	if m.loadingObjects && w.nodes == nil {
		b.WriteString(dimStyle.Render("  loading…") + "\n")
		return lines + 1
	}

	if w.err != nil {
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
		b.WriteString(sidebarErrStyle.Render(msg) + "\n")
		return lines + 1
	}

	if len(items) == 0 {
		b.WriteString(dimStyle.Render("  (none)") + "\n")
		return lines + 1
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

	selRow := w.sel

	start, end := computeWindow(len(rows), selRow, bodyLines)

	if start > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  ▲ %d more", start)) + "\n")
		lines++
		start++
		if start > selRow {
			start = selRow
		}
	}

	showBottomHint := end < len(rows)
	limit := end
	if showBottomHint {
		limit = end - 1
		if limit < start {
			limit = start
		}
	}

	for ri := start; ri < limit; ri++ {
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

		if focused && ri == w.sel && r.indent {
			marker := "▸ └ "
			pad := width - len(marker) - 2 - len(name) - len(metricStr)
			if pad < 1 {
				pad = 1
			}
			b.WriteString(sidebarSelStyle.Render(fmt.Sprintf("%s%s%s%s", marker, name, strings.Repeat(" ", pad), metricStr)))
		} else if focused && ri == w.sel {
			pad := width - 4 - len(name) - len(metricStr)
			if pad < 1 {
				pad = 1
			}
			b.WriteString(sidebarSelStyle.Render(fmt.Sprintf("▸ %s%s%s", name, strings.Repeat(" ", pad), metricStr)))
		} else {
			pad := width - len(prefix) - len(name) - len(metricStr) - 1
			if pad < 1 {
				pad = 1
			}
			if metricStr != "" {
				b.WriteString(fmt.Sprintf("%s%s%s%s", prefix, name, strings.Repeat(" ", pad), dimStyle.Render(metricStr)))
			} else {
				b.WriteString(prefix + name)
			}
		}
		b.WriteString("\n")
		lines++
	}

	if showBottomHint {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  ▼ %d more", len(rows)-limit)) + "\n")
		lines++
	}

	// Filter indicator.
	if focused && m.filtering {
		b.WriteString(statusKeyStyle.Render("/") + w.filter + "▍\n")
		lines++
	}

	return lines
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

// getFilteredSortedNodes returns the node list for window idx, filtered and sorted.
func (m aiTUIModel) getFilteredSortedNodes(idx int) []backends.ObjectNode {
	if idx < 0 || idx >= len(m.objTypes) {
		return nil
	}
	w := m.objTypes[idx]
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
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	} else {
		metricIdx := int(w.sortIdx) - 1
		sort.Slice(sorted, func(i, j int) bool {
			vi, vj := int64(0), int64(0)
			if metricIdx < len(sorted[i].Metrics) {
				vi = sorted[i].Metrics[metricIdx].Value
			}
			if metricIdx < len(sorted[j].Metrics) {
				vj = sorted[j].Metrics[metricIdx].Value
			}
			return vi > vj
		})
	}
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

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
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
