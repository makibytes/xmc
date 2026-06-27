package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/chzyer/readline"
	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
	"github.com/muesli/reflow/wordwrap"
	"github.com/muesli/reflow/wrap"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// ---------- TUI state machine ----------

type tuiState int

const (
	tuiIdle      tuiState = iota // waiting for user input
	tuiThinking                  // AI request in flight, streaming tokens
	tuiProposing                 // showing proposed command for confirmation
	tuiEditing                   // editing a proposed command before accepting
	tuiExecuting                 // running the confirmed command
	tuiPicking                   // interactive picker overlay (model, effort)
)

// ---------- Focus model ----------

// focusTarget identifies which pane has keyboard focus.
// focusChat = text input; values > 0 index into objTypes (1-based so 0 is chat).
type focusTarget int

const focusChat focusTarget = 0 // text input (default)

// ---------- Sort modes ----------

type sortMode int

const (
	sortByName sortMode = iota // sort by name ascending
	// Metric-based sorts: sortByMetric0, sortByMetric1, … are encoded as
	// sortByName + 1 + metricIndex. cycleSort generates them dynamically.
)

// sortLabel returns a human-readable label for the current sort mode.
func sortLabel(s sortMode, metrics []backends.Metric) string {
	if s == sortByName || int(s)-1 >= len(metrics) {
		return "name"
	}
	return metrics[int(s)-1].Label
}

// ---------- Bubble Tea messages ----------

type tokenMsg struct{ text string }                              // streamed token from AI
type aiDoneMsg struct{ text string; usage TokenUsage; err error } // AI request completed
type execDoneMsg struct{ err error; stdout, stderr string }       // command finished
type setCancelMsg struct{ cancel context.CancelFunc }             // pass cancel from bg goroutine to model
type modelsMsg struct{ models []string; err error }               // model listing result
type objectsMsg struct {                                         // broker objects fetch result
	windows [][]backends.ObjectNode // one slice per ObjectType
	errs    []error                // per-ObjectType errors
}

type connMsg struct{ err error }              // connection probe result
type reconnectTickMsg struct{}                // 500ms ticker for reconnect countdown + blink
type reconnectProbeMsg struct{ err error }    // result of a reconnect probe attempt
type refreshTickMsg struct{}                  // time for the next periodic sidebar fetch
type refreshWatchdogMsg struct{ gen int }     // fired after refreshWatchdogTimeout if a fetch is still in-flight

// errNoManageAPI is returned by startLoadObjects when the broker has no ManageSpec.
var errNoManageAPI = errors.New("no management API")

const (
	// maxInputLines is the maximum number of rows the textarea grows to before it
	// scrolls instead of expanding.
	maxInputLines = 5

	// baseRefreshPeriod is the default minimum interval between periodic sidebar fetches.
	// Can be overridden at runtime via /refresh <dur> or in config (ai.refresh-interval).
	baseRefreshPeriod = 5 * time.Second
	// minRefreshInterval is the hard lower bound for any user-provided refresh interval.
	minRefreshInterval = 1 * time.Second
	// refreshFactor scales the last fetch duration to compute the next interval
	// (next = max(refreshPeriod, refreshFactor * lastDuration)). This limits
	// load on the broker's management backend proportionally.
	refreshFactor = 3
	// refreshWatchdogTimeout is the maximum time to wait for an in-flight fetch
	// before re-issuing the request (assumed wedged).
	refreshWatchdogTimeout = 120 * time.Second
)

// ---------- Input mode ----------

type inputMode int

const (
	modeAI  inputMode = iota // natural-language prompt to the AI
	modeCmd                  // direct xmc command entry (shell-like)
)

// ---------- Styles ----------

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("#1a7f8a"))

	titleDimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#b0d4d8")).
			Background(lipgloss.Color("#1a7f8a"))

	titleServerOKStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("15")).
				Background(lipgloss.Color("#1a7f8a"))

	titleServerErrStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("9")).
				Background(lipgloss.Color("#1a7f8a"))

	// titleServerBusyStyle: yellow + ANSI blink — shown while auto-reconnect is active.
	titleServerBusyStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#f5a623")).
				Blink(true).
				Background(lipgloss.Color("#1a7f8a"))

	userStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("6")) // cyan

	cmdStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("2")) // green

	warnStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("1")) // red

	infoStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("3")) // yellow

	dimStyle = lipgloss.NewStyle().
			Faint(true)

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("7"))

	statusKeyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15"))

	histTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("5"))

	histCmdStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("14"))

	histOkStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2"))

	sidebarFocusStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("15")).
				Background(lipgloss.Color("#1a7f8a")).
				PaddingLeft(1).PaddingRight(1)

	sidebarSelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15"))

	// sidebarErrStyle renders fetch errors inside a sidebar window (red, non-bold
	// to distinguish from the header, no background so it blends with the pane).
	sidebarErrStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("9")) // bright red

	// promptRuleStyle draws the horizontal rules that bracket the input area
	// (same teal as the title-bar background).
	promptRuleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#1a7f8a"))

	// shimmerHighStyle is the bright "lit" rune style used in the proposal shimmer.
	shimmerHighStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("15")) // bright white

	// msgBorderStyle colours the left border of message payload blocks.
	msgBorderStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6")) // cyan

	// msgBodyStyle renders message payload text (italic, light cyan).
	msgBodyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")).
			Italic(true)

	// copyHintStyle renders the ⧉ clipboard marker.
	copyHintStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#5fa8d3"))
)

// ---------- Sidebar object window ----------

// objWindow holds the state for one object-type window in the sidebar.
type objWindow struct {
	kind         objWindowKind         // window type (default 0 = broker objects, 1 = processes)
	label        string                // header title: "Queues", "Exchanges", …
	hierarchical bool                  // true → expand hotkey reveals Children
	listFn       func() ([]backends.ObjectNode, error) // data source
	nodes        []backends.ObjectNode // current data (nil = not yet loaded)
	err          error                 // last fetch error
	sel          int                   // selected index (in filtered+sorted view)
	filter       string               // active case-insensitive substring filter
	sortIdx      sortMode             // current sort mode
	treeView  bool // hierarchical children shown (x to toggle)
	collapsed bool // window collapsed to title only (Space to toggle)
}

// ---------- Model ----------

type aiTUIModel struct {
	// Bubble Tea components
	viewport viewport.Model
	input    textarea.Model
	spinner  spinner.Model

	// State
	state        tuiState
	transcript   *strings.Builder // full transcript (pointer: Builder must not be copied)
	streamBuf    *strings.Builder // tokens streamed so far (pointer: Builder must not be copied)
	proposedCmd        string
	proposedDestructive bool // true when the proposed command is flagged as destructive
	shimmerPhase       int  // animation frame counter for the proposal shimmer
	execCancel     context.CancelFunc
	quitting       bool
	exitAll        bool // /exit: quit xmc entirely
	fetchingModels bool // spinner while /model fetches the list
	fixAttempts    int  // auto-fix retry counter (reset on success or new user prompt)
	follow         bool // auto-scroll to bottom on new content (false when user scrolls up)
	width        int
	height       int

	// AI session
	ai      *aiSession
	session *shellSession
	rootCmd *cobra.Command

	// Display info
	binaryName string
	server     string

	// Connection state
	connChecked bool  // true after the initial probe completes
	connErr     error // non-nil if the broker is unreachable

	// Auto-reconnect state
	reconnecting      bool          // true while backoff is ticking or a probe is in-flight
	reconnectAt       time.Time     // wall-clock time the next probe fires
	reconnectBackoff  time.Duration // current wait interval (doubles each failure, capped at 3 min)
	reconnectDisabled bool          // user ran /disconnect — stop auto-retry
	reconnectBlink    bool          // toggled every 500ms for the title-bar yellow blink
	reconnectStatus   string        // one-line countdown shown below the viewport

	// Statistics
	totalIn  int
	totalOut int
	turnIn   int
	turnOut  int

	// Sidebar object windows (from ManageSpec.Objects)
	objTypes       []objWindow
	loadingObjects bool // true during the first (visible) load; false for periodic refreshes

	// Input area height (grows as the user types, up to maxInputLines).
	inputLines int

	// Clipboard — copyable items accumulated this session (commands + payloads).
	// Each item is registered with a ⧉N marker in the transcript; index N refers
	// back to this slice. wrappedContentLines caches the post-wrap content lines
	// so that mouse-click copy can resolve which line was clicked.
	copyItems           []string
	wrappedContentLines []string

	// Token coalescing: tracks the last time setViewportContent was called
	// during streaming so we throttle re-renders to ~50ms intervals.
	lastStreamRender time.Time

	// Periodic sidebar refresh state
	refreshing      bool          // true while a background fetch is in-flight
	refreshStart    time.Time     // wall-clock time the current fetch started
	lastFetchDur    time.Duration // duration of the most recent fetch (drives adaptive interval)
	refreshGen      int           // generation counter — guards against stale watchdog messages
	refreshPeriod   time.Duration // effective base interval (floor before adaptive scaling)
	refreshEnabled  bool          // whether the periodic refresh loop is active

	// Focus (0 = chat, 1..N = objTypes index + 1)
	focus     focusTarget
	filtering bool // filter mode active in focused pane

	// Background process management (cmd/aiproc.go).
	procs      []*bgProcess // active and finished background processes
	procNextID int          // monotone counter for bgProcess.id
	procWinIdx int          // index of the Processes window in objTypes (-1 if absent)
	procSel    int          // selected row in the Processes window

	// Saved prompt state while Processes pane is focused (enterProcessView / exitProcessView).
	savedMode  inputMode
	savedInput string
	savedHist  int
	inProcView bool // true while the process pane owns prompt save/restore

	// Input mode: AI prompt vs direct xmc command
	mode       inputMode
	completer  *readline.PrefixCompleter // verb/flag autocomplete (from shell)
	cmdHistory []string                  // shared command history (loaded from shell history file)
	askHistory []string                  // in-memory AI prompt history
	histIdx    int                       // current position in history (-1 = draft)
	histDraft  string                    // draft text before history navigation started

	// Interactive picker state (for /model, /effort)
	picker *pickerState

	// Program reference (double pointer for Bubble Tea value copy)
	program **tea.Program
}

// pickerState holds the state for an interactive selection list (Claude Code-style).
type pickerState struct {
	title    string   // heading shown above the list
	items    []string // selectable labels
	sel      int      // currently highlighted index
	current  int      // index of the active item (-1 if none matches)
	onSelect func(m *aiTUIModel, idx int) // called on Enter with the chosen index
}

func newAITUIModel(ai *aiSession, session *shellSession, rootCmd *cobra.Command, binaryName, server string) aiTUIModel {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		w, h = 80, 24
	}

	ta := textarea.New()
	// Show the prompt label only on the first visual line; wrapped lines get
	// indent spaces of the same width so all content aligns at one margin.
	// SetPromptFunc must be called before SetWidth so promptWidth is known.
	const initPrompt = "ask> "
	applyPromptFunc(&ta, initPrompt)
	promptLabelSty := lipgloss.NewStyle().Foreground(lipgloss.Color("#1a7f8a")).Bold(true)
	ta.FocusedStyle.Prompt = promptLabelSty
	ta.BlurredStyle.Prompt = promptLabelSty
	ta.Placeholder = "Ask anything..."
	ta.Focus()
	ta.CharLimit = 2000
	ta.SetHeight(1)
	ta.SetWidth(w - 4)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(false)

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))

	vp := viewport.New(w-4, h-6)
	vp.MouseWheelEnabled = true // explicit; it defaults to true but be certain

	// Build sidebar windows from ManageSpec.Objects.
	var windows []objWindow
	if session != nil && session.spec.ManageSpec != nil {
		for _, ot := range session.spec.ManageSpec.Objects {
			windows = append(windows, objWindow{
				label:        ot.Label,
				hierarchical: ot.Hierarchical,
				listFn:       ot.List,
			})
		}
	}

	// Build autocomplete tree (same as the regular shell).
	var comp *readline.PrefixCompleter
	if rootCmd != nil {
		var aliases map[string]string
		if ai != nil && session != nil {
			aliases = session.aliases
		}
		comp = newShellCompleter(rootCmd, aliases)
	}

	period := baseRefreshPeriod
	refreshOn := true
	if ai != nil {
		period = ai.refreshPeriod
		refreshOn = ai.refreshEnabled
	}

	return aiTUIModel{
		viewport:        vp,
		input:           ta,
		spinner:         sp,
		state:           tuiIdle,
		width:           w,
		height:          h,
		transcript:      &strings.Builder{},
		streamBuf:       &strings.Builder{},
		ai:              ai,
		session:         session,
		rootCmd:         rootCmd,
		binaryName:      binaryName,
		server:          server,
		follow:          true,
		objTypes:        windows,
		loadingObjects:  len(windows) > 0,
		inputLines:      1,
		refreshPeriod:   period,
		refreshEnabled:  refreshOn,
		completer:       comp,
		cmdHistory:      loadShellHistory(),
		askHistory:      loadAskHistory(),
		histIdx:         -1,
		procWinIdx:      -1,
	}
}

func (m aiTUIModel) Init() tea.Cmd {
	cmds := []tea.Cmd{textarea.Blink, m.spinner.Tick}
	if len(m.objTypes) > 0 {
		cmds = append(cmds, m.startLoadObjects())
		if m.autoUpdateEnabled() {
			// Schedule the first periodic tick; beginRefresh() runs inside
			// Update() where model mutations are preserved.
			cmds = append(cmds, tea.Tick(m.refreshPeriod, func(time.Time) tea.Msg { return refreshTickMsg{} }))
		}
	}
	if m.session != nil && m.session.spec.Ping != nil {
		cmds = append(cmds, m.startProbeConnection())
	}
	return tea.Batch(cmds...)
}

func (m aiTUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		(&m).updateInputHeight()
		return m, nil

	case tea.KeyMsg:
		// Scroll keys work in all states (don't conflict with any input).
		switch msg.Type {
		case tea.KeyPgUp:
			m.viewport.HalfPageUp()
			m.follow = m.viewport.AtBottom()
			return m, nil
		case tea.KeyPgDown:
			m.viewport.HalfPageDown()
			m.follow = m.viewport.AtBottom()
			return m, nil
		case tea.KeyHome:
			m.viewport.GotoTop()
			m.follow = false
			return m, nil
		case tea.KeyEnd:
			m.viewport.GotoBottom()
			m.follow = true
			return m, nil
		}
		return m.handleKey(msg)

	case tea.MouseMsg:
		// Check for left-click on a ⧉ copy marker before passing to viewport.
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			viewportStartY := 2 // title bar (1) + blank line (1)
			vRow := msg.Y - viewportStartY
			if vRow >= 0 && vRow < m.viewport.Height {
				contentLine := m.viewport.YOffset + vRow
				if idx := m.copyIdxForLine(contentLine); idx >= 0 && idx < len(m.copyItems) {
					_ = clipboard.WriteAll(m.copyItems[idx])
					m.appendTranscript(copyHintStyle.Render("(copied to clipboard)") + "\n\n")
					return m, nil
				}
			}
		}
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		m.follow = m.viewport.AtBottom()
		return m, cmd

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
		// Advance the shimmer while a proposal is open.
		if m.state == tuiProposing || m.state == tuiEditing {
			m.shimmerPhase++
			m.setViewportContent()
		}

	case tokenMsg:
		m.streamBuf.WriteString(msg.text)
		// Throttle viewport rebuilds to ~50 ms to avoid O(n²) reflow per token.
		if time.Since(m.lastStreamRender) >= 50*time.Millisecond {
			m.lastStreamRender = time.Now()
			m.setViewportContent()
		}
		return m, nil

	case aiDoneMsg:
		return m.handleAIDone(msg)

	case execDoneMsg:
		return m.handleExecDone(msg)

	case setCancelMsg:
		m.execCancel = msg.cancel
		return m, nil

	case procCancelMsg:
		return m.handleProcCancelMsg(msg)

	case procDoneMsg:
		return m.handleProcDoneMsg(msg)

	case modelsMsg:
		return m.handleModelsDone(msg)

	case objectsMsg:
		return m.handleObjectsDone(msg)

	case connMsg:
		return m.handleConnDone(msg)

	case reconnectTickMsg:
		return m.handleReconnectTick()

	case reconnectProbeMsg:
		return m.handleReconnectProbe(msg)

	case refreshTickMsg:
		if !m.refreshing && len(m.objTypes) > 0 && m.autoUpdateEnabled() {
			return m, (&m).beginRefresh()
		}
		return m, nil

	case refreshWatchdogMsg:
		// Re-issue the fetch only if the wedged generation matches.
		if m.refreshing && msg.gen == m.refreshGen && len(m.objTypes) > 0 {
			log.Verbose("sidebar refresh watchdog: fetch gen %d timed out, re-issuing", msg.gen)
			return m, (&m).beginRefresh()
		}
		return m, nil
	}

	return m, tea.Batch(cmds...)
}

func (m aiTUIModel) View() string {
	if m.quitting {
		return ""
	}

	w := m.width

	// ── Title bar ──
	modelInfo := m.modelInfo()
	titleText := " XMC AI "
	binaryText := " " + m.binaryName + " "
	serverInfo := m.serverInfo()
	padLen := w - lipgloss.Width(titleText) - lipgloss.Width(binaryText) - lipgloss.Width(modelInfo) - lipgloss.Width(serverInfo)
	if padLen < 1 {
		padLen = 1
	}
	titleBar := titleStyle.Render(titleText) +
		titleDimStyle.Render(binaryText) +
		titleDimStyle.Render(strings.Repeat("─", padLen)) +
		titleDimStyle.Render(modelInfo) +
		serverInfo

	// ── Main content area ──
	mainContent := m.renderMainContent()

	// ── Input area ──
	inputView := m.input.View()

	// ── Status bar ──
	statusBar := m.renderStatusBar()

	// ── Horizontal rules bracketing the prompt (same teal as title bar) ──
	rule := promptRuleStyle.Render(strings.Repeat("─", w))

	return lipgloss.JoinVertical(lipgloss.Left,
		titleBar,
		"",
		mainContent,
		rule,
		inputView,
		rule,
		statusBar,
	)
}

func (m aiTUIModel) modelInfo() string {
	if m.ai == nil {
		return ""
	}
	if m.ai.modelName == "" && m.ai.providerName == "" {
		return ""
	}
	return fmt.Sprintf(" %s · %s ", m.ai.modelName, m.ai.providerName)
}

// serverInfo renders the broker URL for the title bar, coloured by connection state:
//   - yellow blink: reconnecting (probe in-flight or waiting for backoff)
//   - red: confirmed unreachable and auto-reconnect stopped/failed
//   - white: connected (or not yet checked)
func (m aiTUIModel) serverInfo() string {
	if m.server == "" {
		return ""
	}
	text := " " + m.server + " "
	switch {
	case m.reconnecting:
		return titleServerBusyStyle.Render(text)
	case m.connChecked && m.connErr != nil:
		return titleServerErrStyle.Render(text)
	default:
		return titleServerOKStyle.Render(text)
	}
}

// paneWidths computes the conversation and sidebar widths based on the
// terminal size and whether sidebar windows are configured.
func (m aiTUIModel) paneWidths() (convWidth, sideWidth int) {
	if m.width >= 90 && len(m.objTypes) > 0 {
		sideWidth = m.width / 3
		if sideWidth < 32 {
			sideWidth = 32
		}
		if sideWidth > 40 {
			sideWidth = 40
		}
	}
	convWidth = m.width
	if sideWidth > 0 {
		convWidth = m.width - sideWidth - 3
	}
	return
}

func (m aiTUIModel) renderMainContent() string {
	convWidth, sideWidth := m.paneWidths()
	_ = convWidth // viewport.Width already set by recalcLayout

	convPane := m.viewport.View()

	// Show reconnect countdown as a one-line status below the transcript.
	if m.reconnectStatus != "" {
		convPane = lipgloss.JoinVertical(lipgloss.Left,
			convPane,
			infoStyle.Render(m.reconnectStatus),
		)
	}

	if sideWidth == 0 {
		return convPane
	}

	// Sidebar (N object windows) + which rows carry window underlines.
	sidePane, junctionRows := m.renderSidebar(sideWidth, m.viewport.Height)

	// Build a full-height vertical separator. Use the same grey (dimStyle) as
	// the sidebar window underlines. On rows that align with a window underline,
	// emit ├ to visually connect the vertical bar to the horizontal rule.
	h := m.viewport.Height
	if h < 1 {
		h = 1
	}
	junctionSet := make(map[int]bool, len(junctionRows))
	for _, r := range junctionRows {
		junctionSet[r] = true
	}
	sepLines := make([]string, h)
	for row := 0; row < h; row++ {
		ch := "│"
		if junctionSet[row] {
			ch = "├"
		}
		sepLines[row] = " " + dimStyle.Render(ch)
	}
	sep := strings.Join(sepLines, "\n")

	return lipgloss.JoinHorizontal(lipgloss.Top,
		convPane,
		sep,
		sidePane,
	)
}

// renderSidebar renders the right-column sidebar with N object-type windows.
// The focused window expands; others collapse.
// It also returns the row indices (0-based from the top of the sidebar) at which
// each window's horizontal underline is drawn, so the caller can place ├ junction
// characters on the vertical separator to visually connect the two.
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
	if w.filter != "" {
		headerText = glyph + fmt.Sprintf("%s (%d/%d)", w.label, len(items), len(w.nodes))
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

	// Build display rows. In expanded hierarchical mode, flatten parent+children.
	type displayRow struct {
		name    string
		metric  string
		indent  bool
		itemIdx int // index in the items slice (-1 for children)
	}
	var rows []displayRow
	if w.treeView && w.hierarchical {
		for i, node := range items {
			rows = append(rows, displayRow{name: node.Name, metric: fmtNodeDetail(node), itemIdx: i})
			for _, child := range node.Children {
				label := child.Name
				if child.Kind != "" {
					label = child.Kind + " " + child.Name
				}
				rows = append(rows, displayRow{name: label, metric: fmtNodeMetric(child), indent: true, itemIdx: -1})
			}
		}
	} else {
		for i, node := range items {
			rows = append(rows, displayRow{name: node.Name, metric: fmtNodeDetail(node), itemIdx: i})
		}
	}

	// Windowed rendering over rows. Selection applies to items, not rows.
	// Map selection to row index.
	selRow := 0
	if !w.treeView || !w.hierarchical {
		selRow = w.sel
	} else {
		for ri, r := range rows {
			if r.itemIdx == w.sel {
				selRow = ri
				break
			}
		}
	}

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
		if len(name) > maxName {
			name = name[:maxName-1] + "…"
		}

		if focused && !r.indent && r.itemIdx == w.sel {
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

func (m aiTUIModel) renderStatusBar() string {
	var left string
	switch m.state {
	case tuiIdle:
		if m.fetchingModels {
			left = m.spinner.View() + " " + statusStyle.Render("Fetching models…")
		} else if m.filtering {
			filter := ""
			if wi := int(m.focus) - 1; wi >= 0 && wi < len(m.objTypes) {
				filter = m.objTypes[wi].filter
			}
			left = statusKeyStyle.Render("/") + statusStyle.Render(filter+"▍") +
				statusStyle.Render("  ") +
				statusKeyStyle.Render("Enter") + statusStyle.Render(" apply") +
				statusStyle.Render("  ") +
				statusKeyStyle.Render("Esc") + statusStyle.Render(" clear")
		} else if wi := int(m.focus) - 1; wi >= 0 && wi < len(m.objTypes) && m.objTypes[wi].kind == objWindowProcs {
			left = statusKeyStyle.Render("↑↓") + statusStyle.Render(" browse") +
				statusStyle.Render("  ") +
				statusKeyStyle.Render("Enter") + statusStyle.Render(" view output") +
				statusStyle.Render("  ") +
				statusKeyStyle.Render("d") + statusStyle.Render(" remove") +
				statusStyle.Render("  ") +
				statusKeyStyle.Render("k") + statusStyle.Render(" kill") +
				statusStyle.Render("  ") +
				statusKeyStyle.Render("p") + statusStyle.Render(" purge done") +
				statusStyle.Render("  ") +
				statusKeyStyle.Render("D") + statusStyle.Render(" kill all") +
				statusStyle.Render("  ") +
				statusKeyStyle.Render("Space") + statusStyle.Render(" collapse") +
				statusStyle.Render("  ") +
				statusKeyStyle.Render("Tab") + statusStyle.Render("/") +
				statusKeyStyle.Render("Shift+Tab") + statusStyle.Render(" next/prev") +
				statusStyle.Render("  ") +
				statusKeyStyle.Render("Esc") + statusStyle.Render(" chat")
		} else if m.focus != focusChat {
			treeHint := ""
			if wi := int(m.focus) - 1; wi >= 0 && wi < len(m.objTypes) && m.objTypes[wi].hierarchical {
				treeHint = statusStyle.Render("  ") +
					statusKeyStyle.Render("x") + statusStyle.Render(" tree")
			}
			left = statusKeyStyle.Render("↑↓") + statusStyle.Render(" move") +
				statusStyle.Render("  ") +
				statusKeyStyle.Render("/") + statusStyle.Render(" filter") +
				statusStyle.Render("  ") +
				statusKeyStyle.Render("s") + statusStyle.Render(" sort") +
				treeHint +
				statusStyle.Render("  ") +
				statusKeyStyle.Render("Space") + statusStyle.Render(" collapse") +
				statusStyle.Render("  ") +
				statusKeyStyle.Render("Enter") + statusStyle.Render(" use") +
				statusStyle.Render("  ") +
				statusKeyStyle.Render("Tab") + statusStyle.Render("/") +
				statusKeyStyle.Render("Shift+Tab") + statusStyle.Render(" next/prev") +
				statusStyle.Render("  ") +
				statusKeyStyle.Render("Esc") + statusStyle.Render(" chat")
		} else {
			browseHint := ""
			if len(m.objTypes) > 0 {
				if m.mode == modeCmd {
					browseHint = statusStyle.Render("  ") +
						statusKeyStyle.Render("Shift+Tab") + statusStyle.Render(" browse")
				} else {
					browseHint = statusStyle.Render("  ") +
						statusKeyStyle.Render("Tab") + statusStyle.Render("/") +
						statusKeyStyle.Render("Shift+Tab") + statusStyle.Render(" browse")
				}
			}
			modeHint := "cmd"
			if m.mode == modeCmd {
				modeHint = "ask"
			}
			tabHint := ""
			if m.mode == modeCmd {
				tabHint = statusStyle.Render("  ") +
					statusKeyStyle.Render("Tab") + statusStyle.Render(" complete")
			}
			scrollHint := ""
			canScroll := m.viewport.TotalLineCount() > m.viewport.Height
			if canScroll {
				scrollHint = statusStyle.Render("  ") +
					statusKeyStyle.Render("PgUp/PgDn") + statusStyle.Render(" scroll")
				if !m.follow {
					scrollHint += statusStyle.Render("  ") +
						statusKeyStyle.Render("End") + statusStyle.Render(" ↓bottom")
				}
			}
			left = statusKeyStyle.Render("Enter") + statusStyle.Render(" send") +
				tabHint +
				browseHint +
				scrollHint +
				statusStyle.Render("  ") +
				statusKeyStyle.Render("Esc") + statusStyle.Render(" "+modeHint) +
				statusStyle.Render("  ") +
				statusKeyStyle.Render("/help") + statusStyle.Render(" help")
		}
	case tuiThinking:
		left = m.spinner.View() + " " +
			statusStyle.Render("Thinking…") +
			statusStyle.Render("  ") +
			statusKeyStyle.Render("Esc") + statusStyle.Render(" cancel")
	case tuiProposing:
		left = statusKeyStyle.Render("Enter") + statusStyle.Render(" run") +
			statusStyle.Render("  ") +
			statusKeyStyle.Render("e") + statusStyle.Render(" edit") +
			statusStyle.Render("  ") +
			statusKeyStyle.Render("c") + statusStyle.Render(" chat") +
			statusStyle.Render("  ") +
			statusKeyStyle.Render("Esc") + statusStyle.Render(" discard")
	case tuiEditing:
		left = statusKeyStyle.Render("Enter") + statusStyle.Render(" accept") +
			statusStyle.Render("  ") +
			statusKeyStyle.Render("Esc") + statusStyle.Render(" cancel")
	case tuiExecuting:
		left = m.spinner.View() + " " +
			statusStyle.Render("Running…") +
			statusStyle.Render("  ") +
			statusKeyStyle.Render("Esc") + statusStyle.Render(" cancel")
	case tuiPicking:
		left = statusKeyStyle.Render("↑/↓") + statusStyle.Render(" select") +
			statusStyle.Render("  ") +
			statusKeyStyle.Render("Enter") + statusStyle.Render(" confirm") +
			statusStyle.Render("  ") +
			statusKeyStyle.Render("Esc") + statusStyle.Render(" cancel")
	}

	right := ""
	if m.totalIn > 0 || m.totalOut > 0 {
		right = dimStyle.Render(fmt.Sprintf("%s↓ %s↑", fmtTokens(m.totalIn), fmtTokens(m.totalOut)))
	}

	pad := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 1 {
		pad = 1
	}

	return left + strings.Repeat(" ", pad) + right
}

func fmtTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// ---------- Key handling ----------

func (m aiTUIModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.state {
	case tuiIdle:
		return m.handleKeyIdle(msg)
	case tuiThinking:
		return m.handleKeyThinking(msg)
	case tuiProposing:
		return m.handleKeyProposing(msg)
	case tuiEditing:
		return m.handleKeyEditing(msg)
	case tuiExecuting:
		return m.handleKeyExecuting(msg)
	case tuiPicking:
		return m.handleKeyPicking(msg)
	}
	return m, nil
}

func (m aiTUIModel) handleKeyPicking(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.picker
	if p == nil {
		m.state = tuiIdle
		m.input.Focus()
		return m, nil
	}
	switch msg.Type {
	case tea.KeyUp:
		if p.sel > 0 {
			p.sel--
			m.setViewportContent()
		}
		return m, nil
	case tea.KeyDown:
		if p.sel < len(p.items)-1 {
			p.sel++
			m.setViewportContent()
		}
		return m, nil
	case tea.KeyEnter:
		p.onSelect(&m, p.sel)
		m.picker = nil
		m.state = tuiIdle
		m.input.Focus()
		m.setViewportContent()
		return m, nil
	case tea.KeyEsc, tea.KeyCtrlC:
		m.appendTranscript(dimStyle.Render("(cancelled)") + "\n\n")
		m.picker = nil
		m.state = tuiIdle
		m.input.Focus()
		return m, nil
	}
	return m, nil
}

func (m aiTUIModel) handleKeyIdle(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Shift+Tab cycles focus backward (chat → last window → … → first window → chat).
	if msg.Type == tea.KeyShiftTab {
		m.cycleFocus(false)
		return m, nil
	}

	// When a sidebar pane is focused, delegate.
	if m.focus != focusChat {
		if m.filtering {
			return m.handleKeyFilter(msg)
		}
		// Route process pane keys to the dedicated handler.
		if wi := int(m.focus) - 1; wi >= 0 && wi < len(m.objTypes) && m.objTypes[wi].kind == objWindowProcs {
			return m.handleKeyProcessPane(msg)
		}
		return m.handleKeyPane(msg)
	}

	switch msg.Type {
	case tea.KeyCtrlC:
		(&m).killAllProcs()
		m.quitting = true
		return m, tea.Quit

	case tea.KeyEsc:
		// Esc in chat toggles between AI and command mode.
		m.toggleInputMode()
		return m, nil

	case tea.KeyTab:
		if m.mode == modeCmd {
			m.doAutocomplete()
		} else {
			// Tab in AI mode → cycle sidebar focus forward.
			m.cycleFocus(true)
		}
		return m, nil

	case tea.KeyUp:
		m.historyPrev()
		(&m).updateInputHeight()
		return m, nil

	case tea.KeyDown:
		m.historyNext()
		(&m).updateInputHeight()
		return m, nil

	case tea.KeyEnter:
		prompt := strings.TrimSpace(m.input.Value())
		if prompt == "" {
			return m, nil
		}

		// Well-formedness check: reject a trailing bare '&' (use --for instead).
		// Check BEFORE resetting the input so the user's text is preserved on rejection.
		if m.mode == modeCmd && !strings.HasPrefix(prompt, "/") && !commandWellFormed(prompt) {
			m.appendTranscript(warnStyle.Render("✗ trailing & is not allowed — use --for <duration> to run in the background") + "\n\n")
			return m, nil
		}

		m.input.Reset()
		m.histIdx = -1
		// Unconditional height reset (fixes stale inputLines glitch after multi-line input).
		m.inputLines = 1
		m.input.SetHeight(1)
		m.recalcLayout()

		// Slash commands (work in both modes).
		if strings.HasPrefix(prompt, "/") {
			return m.handleSlashCommand(prompt)
		}

		if m.mode == modeCmd {
			// Direct command execution (shell-like).
			if prompt == "exit" || prompt == "quit" {
				(&m).killAllProcs()
				m.exitAll = true
				m.quitting = true
				return m, tea.Quit
			}
			m.cmdHistory = append(m.cmdHistory, prompt)
			m.appendTranscript(cmdStyle.Render(m.binaryName+"> ") + prompt + "\n\n")
			m.proposedCmd = prompt
			// Commands with --for become managed background processes.
			if commandHasFor(prompt) {
				return m.startBackgroundProcess(prompt)
			}
			return m.startExecution(prompt)
		}

		// AI mode: send to the model.
		m.askHistory = append(m.askHistory, prompt)
		appendAskHistory(prompt)
		m.appendTranscript(userStyle.Render("you: ") + prompt + "\n\n")

		m.ai.history = append(m.ai.history, aiMessage{Role: "user", Content: prompt})
		trimHistory(&m.ai.history, maxHistory)

		m.fixAttempts = 0
		m.state = tuiThinking
		m.streamBuf.Reset()
		m.turnIn = 0
		m.turnOut = 0

		return m, m.startAIRequest()

	case tea.KeyRunes:
		// Fall through to textarea update.
		fallthrough

	default:
		// Pre-size BEFORE forwarding the key: resize the textarea so that
		// repositionView() inside Update() sees the correct height and doesn't
		// scroll content out of view when the text wraps to a new visual row.
		// predictValue avoids calling Update() twice (which caused double-insertion
		// due to shared backing arrays in the textarea's [][]rune value).
		if n := (&m).computeInputLines(predictValue(m.input.Value(), msg)); n != m.inputLines {
			m.inputLines = n
			m.input.SetHeight(n)
			m.recalcLayout()
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		(&m).updateInputHeight()
		return m, cmd
	}
}

func (m aiTUIModel) handleSlashCommand(input string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(input)
	cmd := strings.ToLower(parts[0])
	arg := ""
	if len(parts) > 1 {
		arg = strings.Join(parts[1:], " ")
	}

	switch cmd {
	case "/help":
		m.appendTranscript(dimStyle.Render(
			"/model               pick a model from the provider\n"+
				"/model <name>        switch to a model directly\n"+
				"/effort              pick reasoning effort (temperature)\n"+
				"/effort low|med|high set effort directly\n"+
				"/refresh             reload broker objects now\n"+
				"/refresh off         disable periodic refresh\n"+
				"/refresh <dur>       set refresh interval (e.g. 3s, 3m; min 1s)\n"+
				"/connect             reconnect to the broker (enables auto-reconnect)\n"+
				"/disconnect          stop auto-reconnect\n"+
				"/reset               reset conversation history\n"+
				"/clear               clear the display\n"+
				"/exit                quit xmc\n"+
				"/help                show this help\n"+
				"\n"+
				"Esc          toggle between ask> (AI) and "+m.binaryName+"> (command) mode\n"+
				"Tab          autocomplete (command mode) · browse sidebar forward (AI mode)\n"+
				"Shift+Tab    browse sidebar backward\n"+
				"Up/Down      recall history\n"+
				"Space        collapse/expand selected sidebar window\n"+
				"x            toggle hierarchical tree-view for broker objects\n"+
				"PgUp/PgDn    scroll conversation · mouse wheel also works\n"+
				"Home/End     jump to top/bottom\n"+
				"             click ⧉ in the transcript to copy any item\n") + "\n")

	case "/exit":
		(&m).killAllProcs()
		m.exitAll = true
		m.quitting = true
		return m, tea.Quit

	case "/reset":
		m.ai.resetHistory()
		m.transcript.Reset()
		m.totalIn = 0
		m.totalOut = 0
		m.turnIn = 0
		m.turnOut = 0
		m.setViewportContent()
		m.appendTranscript(dimStyle.Render("(conversation reset)") + "\n\n")

	case "/refresh":
		if arg != "" {
			// /refresh <interval> — change periodic refresh interval.
			d, enabled, err := parseRefreshInterval(arg)
			if err != nil {
				m.appendTranscript(warnStyle.Render(err.Error()) + "\n\n")
				return m, nil
			}
			wasEnabled := m.refreshEnabled
			m.refreshEnabled = enabled
			if enabled {
				m.refreshPeriod = d
			}
			// Persist to config.
			persistVal := formatRefreshInterval(d, enabled)
			msg := "periodic refresh → " + persistVal
			if err := saveRefreshInterval(persistVal); err != nil {
				msg += fmt.Sprintf(" (save failed: %s)", err)
			}
			m.appendTranscript(dimStyle.Render(msg) + "\n\n")
			// If turned on and not already refreshing, kick off the loop.
			if enabled && !wasEnabled && !m.refreshing && len(m.objTypes) > 0 {
				return m, (&m).beginRefresh()
			}
			return m, nil
		}
		// No arg: manual one-shot reload.
		if len(m.objTypes) > 0 {
			m.appendTranscript(dimStyle.Render("refreshing…") + "\n\n")
			m.loadingObjects = true
			return m, m.startLoadObjects()
		}
		m.appendTranscript(dimStyle.Render("(no management API)") + "\n\n")

	case "/disconnect":
		if m.reconnecting {
			m.reconnecting = false
			m.reconnectDisabled = true
			m.reconnectStatus = ""
			m.appendTranscript(dimStyle.Render("Auto-reconnect disabled. Use /connect to reconnect manually.") + "\n\n")
		} else {
			m.appendTranscript(dimStyle.Render("Not currently reconnecting.") + "\n\n")
		}

	case "/connect":
		if m.session == nil || m.session.spec.Ping == nil {
			m.appendTranscript(warnStyle.Render("No connection probe available for this broker.") + "\n\n")
			return m, nil
		}
		if m.connErr == nil && !m.reconnecting {
			m.appendTranscript(dimStyle.Render("Already connected.") + "\n\n")
			return m, nil
		}
		m.reconnectDisabled = false
		m.reconnecting = true
		m.reconnectAt = time.Now() // fire probe immediately
		m.reconnectStatus = "↻ connecting…"
		m.appendTranscript(dimStyle.Render("Connecting…") + "\n\n")
		return m, m.startReconnectProbe()

	case "/clear":
		m.transcript.Reset()
		m.setViewportContent()

	case "/model":
		if arg == "" {
			m.appendTranscript(dimStyle.Render(fmt.Sprintf("current: %s · %s", m.ai.modelName, m.ai.providerName)) + "\n")
			m.appendTranscript(dimStyle.Render("fetching models…") + "\n\n")
			m.fetchingModels = true
			return m, m.startListModels()
		}
		m.applyModel(arg)
		msg := "model → " + arg
		if err := saveAIModel(arg); err != nil {
			msg += fmt.Sprintf(" (save failed: %s)", err)
		}
		m.appendTranscript(dimStyle.Render(msg) + "\n\n")

	case "/effort":
		if arg == "" {
			// Open interactive effort picker.
			effortLevels := []string{"low", "medium", "high"}
			currentIdx := -1
			if setter, ok := m.ai.client.(modelSettable); ok {
				switch t := setter.Temperature(); {
				case t <= 0:
					currentIdx = 0
				case t <= 0.4:
					currentIdx = 1
				default:
					currentIdx = 2
				}
			}
			startSel := currentIdx
			if startSel < 0 {
				startSel = 0
			}
			m.picker = &pickerState{
				title:   "Select effort level:",
				items:   effortLevels,
				sel:     startSel,
				current: currentIdx,
				onSelect: func(model *aiTUIModel, idx int) {
					temps := []float64{0, 0.3, 0.7}
					if setter, ok := model.ai.client.(modelSettable); ok {
						setter.SetTemperature(temps[idx])
					}
					model.appendTranscript(dimStyle.Render(fmt.Sprintf("effort → %s (temperature %.1f)", effortLevels[idx], temps[idx])) + "\n\n")
				},
			}
			m.state = tuiPicking
			m.input.Blur()
			m.setViewportContent()
			return m, nil
		}
		m.applyEffort(arg)



	default:
		m.appendTranscript(warnStyle.Render("unknown command: "+cmd+" (type /help)") + "\n\n")
	}

	return m, nil
}

// applyEffort parses an effort level string and sets the temperature.
// Returns false if the argument is invalid.
func (m *aiTUIModel) applyEffort(arg string) bool {
	var temp float64
	switch strings.ToLower(arg) {
	case "low", "l":
		temp = 0
	case "medium", "med", "m":
		temp = 0.3
	case "high", "h":
		temp = 0.7
	default:
		m.appendTranscript(warnStyle.Render("effort must be low, medium, or high") + "\n\n")
		return false
	}
	if setter, ok := m.ai.client.(modelSettable); ok {
		setter.SetTemperature(temp)
	}
	m.appendTranscript(dimStyle.Render(fmt.Sprintf("effort → %s (temperature %.1f)", strings.ToLower(arg), temp)) + "\n\n")
	return true
}

// applyModel switches the AI client to a new model (in-memory only).
func (m *aiTUIModel) applyModel(name string) {
	if setter, ok := m.ai.client.(modelSettable); ok {
		setter.SetModel(name)
	}
	m.ai.modelName = name
	m.ai.rebuildPrompt()
}

func (m aiTUIModel) handleKeyThinking(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyEsc || msg.Type == tea.KeyCtrlC {
		if m.execCancel != nil {
			m.execCancel()
		}
		m.appendTranscript(dimStyle.Render("(cancelled)") + "\n\n")
		m.state = tuiIdle
		if len(m.ai.history) > 0 && m.ai.history[len(m.ai.history)-1].Role == "user" {
			m.ai.history = m.ai.history[:len(m.ai.history)-1]
		}
		m.input.Focus()
		return m, nil
	}
	return m, nil
}

func (m aiTUIModel) handleKeyProposing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		// Freeze with a grey ✗ marker.
		m.appendTranscript(freezeProposal(m.proposedCmd, "✗", true, false))
		m.ai.history = append(m.ai.history, aiMessage{Role: "assistant", Content: m.proposedCmd})
		m.ai.history = append(m.ai.history, aiMessage{Role: "user", Content: "[user discarded the command]"})
		m.state = tuiIdle
		m.input.Focus()
		return m, nil

	case tea.KeyEnter:
		// Freeze with a green ✓ marker, then execute (or background if --for).
		m.appendTranscript(freezeProposal(m.proposedCmd, "✓", false, m.proposedDestructive))
		if commandHasFor(m.proposedCmd) {
			return m.startBackgroundProcess(m.proposedCmd)
		}
		return m.startExecution(m.proposedCmd)

	case tea.KeyRunes:
		switch msg.String() {
		case "e":
			// Switch to editing sub-state — shimmer continues while the user types.
			m.input.SetValue(m.proposedCmd)
			m.state = tuiEditing
			m.input.Focus()
			(&m).updateInputHeight()
			return m, nil
		case "c":
			// Freeze with a yellow ? marker to mark it as a follow-up topic.
			m.appendTranscript(freezeProposal(m.proposedCmd, "?", false, m.proposedDestructive))
			m.ai.history = append(m.ai.history, aiMessage{Role: "assistant", Content: m.proposedCmd})
			m.ai.history = append(m.ai.history, aiMessage{Role: "user", Content: "[command NOT executed — user wants to discuss or refine it before running]"})
			m.input.SetValue("")
			m.input.Placeholder = "Chat about the suggestion..."
			m.state = tuiIdle
			m.input.Focus()
			(&m).updateInputHeight()
			return m, nil
		}
	}
	return m, nil
}

// handleKeyEditing handles keyboard events in tuiEditing state: the user is
// refining the proposed command. Enter accepts and runs it; Esc cancels.
// Any other key is forwarded to the textarea so the user can edit freely.
func (m aiTUIModel) handleKeyEditing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		cmd := strings.TrimSpace(m.input.Value())
		if cmd == "" {
			return m, nil
		}
		m.proposedCmd = cmd
		m.proposedDestructive = anyCommand(cmd, isDestructive)
		// Freeze with a green ✓ and run (or background if --for).
		m.appendTranscript(freezeProposal(cmd, "✓", false, m.proposedDestructive))
		m.input.SetValue("")
		m.inputLines = 1
		m.input.SetHeight(1)
		m.recalcLayout()
		if commandHasFor(cmd) {
			return m.startBackgroundProcess(cmd)
		}
		return m.startExecution(cmd)

	case tea.KeyEsc, tea.KeyCtrlC:
		// Freeze with a grey ✗ marker and return to idle.
		m.appendTranscript(freezeProposal(m.proposedCmd, "✗", true, false))
		m.input.SetValue("")
		m.inputLines = 1
		m.input.SetHeight(1)
		m.state = tuiIdle
		m.input.Focus()
		m.recalcLayout()
		return m, nil

	default:
		if n := (&m).computeInputLines(predictValue(m.input.Value(), msg)); n != m.inputLines {
			m.inputLines = n
			m.input.SetHeight(n)
			m.recalcLayout()
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		(&m).updateInputHeight()
		return m, cmd
	}
}

// handleKeyPane processes keys when a sidebar pane has focus.
func (m aiTUIModel) handleKeyPane(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	wi := int(m.focus) - 1
	if wi < 0 || wi >= len(m.objTypes) {
		return m, nil
	}

	switch msg.Type {
	case tea.KeyEsc:
		m.focus = focusChat
		m.input.Focus()
		return m, nil
	case tea.KeyUp:
		m.moveSel(-1)
		return m, nil
	case tea.KeyDown:
		m.moveSel(1)
		return m, nil
	case tea.KeyEnter:
		name := m.selectedName()
		if name != "" {
			m.input.SetValue(name)
			m.focus = focusChat
			m.input.Focus()
			(&m).updateInputHeight()
		}
		return m, nil
	case tea.KeySpace:
		// Space collapses/expands the whole window (title only vs. full content).
		m.objTypes[wi].collapsed = !m.objTypes[wi].collapsed
		return m, nil
	case tea.KeyTab:
		m.cycleFocus(true)
		return m, nil
	case tea.KeyShiftTab:
		m.cycleFocus(false)
		return m, nil
	case tea.KeyRunes:
		switch msg.String() {
		case "j":
			m.moveSel(1)
			return m, nil
		case "k":
			m.moveSel(-1)
			return m, nil
		case "/":
			m.filtering = true
			return m, nil
		case "s":
			m.cycleSort()
			return m, nil
		case "r":
			m.loadingObjects = true
			return m, m.startLoadObjects()
		case "x":
			// x toggles the hierarchical tree-view (show/hide children).
			if m.objTypes[wi].hierarchical {
				m.objTypes[wi].treeView = !m.objTypes[wi].treeView
			}
			return m, nil
		}
	}
	return m, nil
}

// handleKeyFilter processes keys while the inline filter is active.
func (m aiTUIModel) handleKeyFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	wi := int(m.focus) - 1
	if wi < 0 || wi >= len(m.objTypes) {
		return m, nil
	}

	switch msg.Type {
	case tea.KeyEsc:
		m.filtering = false
		m.objTypes[wi].filter = ""
		m.objTypes[wi].sel = 0
		return m, nil
	case tea.KeyEnter:
		m.filtering = false
		return m, nil
	case tea.KeyBackspace:
		if len(m.objTypes[wi].filter) > 0 {
			m.objTypes[wi].filter = m.objTypes[wi].filter[:len(m.objTypes[wi].filter)-1]
			m.objTypes[wi].sel = 0
		}
		return m, nil
	case tea.KeyRunes:
		m.objTypes[wi].filter += msg.String()
		m.objTypes[wi].sel = 0
		return m, nil
	}
	return m, nil
}

func (m aiTUIModel) handleKeyExecuting(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyEsc || msg.Type == tea.KeyCtrlC {
		if m.execCancel != nil {
			m.execCancel()
		}
		return m, nil
	}
	return m, nil
}

// ---------- AI request ----------

func (m aiTUIModel) startAIRequest() tea.Cmd {
	ai := m.ai
	pptr := m.program

	return func() tea.Msg {
		if err := ai.init(); err != nil {
			return aiDoneMsg{err: err}
		}

		if ai.topology == "" && len(ai.history) <= 1 {
			ai.refreshTopology()
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		prog := derefProgram(pptr)

		if prog != nil {
			prog.Send(setCancelMsg{cancel: cancel})
		}

		var onToken func(string)
		if prog != nil {
			onToken = func(token string) {
				prog.Send(tokenMsg{text: token})
			}
		}

		text, usage, err := ai.client.Complete(ctx, ai.sysPrompt, ai.history, onToken)
		return aiDoneMsg{text: text, usage: usage, err: err}
	}
}

func (m aiTUIModel) startListModels() tea.Cmd {
	ai := m.ai
	return func() tea.Msg {
		if err := ai.init(); err != nil {
			return modelsMsg{err: err}
		}
		lister, ok := ai.client.(modelLister)
		if !ok {
			return modelsMsg{err: fmt.Errorf("model listing not supported for %s", ai.providerName)}
		}
		models, err := lister.ListModels(context.Background())
		return modelsMsg{models: models, err: err}
	}
}

func (m aiTUIModel) handleModelsDone(msg modelsMsg) (tea.Model, tea.Cmd) {
	m.fetchingModels = false
	if msg.err != nil {
		m.appendTranscript(warnStyle.Render("error listing models: "+msg.err.Error()) + "\n\n")
		return m, nil
	}
	if len(msg.models) == 0 {
		m.appendTranscript(dimStyle.Render("(no models returned)") + "\n\n")
		return m, nil
	}

	// Open an interactive picker with the fetched models.
	currentIdx := -1
	for i, id := range msg.models {
		if id == m.ai.modelName {
			currentIdx = i
			break
		}
	}
	startSel := currentIdx
	if startSel < 0 {
		startSel = 0
	}
	models := msg.models // capture for closure
	m.picker = &pickerState{
		title:   fmt.Sprintf("Select model (%s):", m.ai.providerName),
		items:   models,
		sel:     startSel,
		current: currentIdx,
		onSelect: func(model *aiTUIModel, idx int) {
			name := models[idx]
			model.applyModel(name)
			info := "model → " + name
			if err := saveAIModel(name); err != nil {
				info += fmt.Sprintf(" (save failed: %s)", err)
			}
			model.appendTranscript(dimStyle.Render(info) + "\n\n")
		},
	}
	m.state = tuiPicking
	m.input.Blur()
	m.setViewportContent()
	return m, nil
}

func (m aiTUIModel) handleAIDone(msg aiDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		if strings.Contains(msg.err.Error(), "context canceled") {
			return m, nil
		}
		m.appendTranscript(warnStyle.Render("error: "+msg.err.Error()) + "\n\n")
		if len(m.ai.history) > 0 && m.ai.history[len(m.ai.history)-1].Role == "user" {
			m.ai.history = m.ai.history[:len(m.ai.history)-1]
		}
		m.state = tuiIdle
		m.input.Focus()
		return m, nil
	}

	m.turnIn = msg.usage.InputTokens
	m.turnOut = msg.usage.OutputTokens
	m.totalIn += msg.usage.InputTokens
	m.totalOut += msg.usage.OutputTokens

	if msg.usage.InputTokens > 0 || msg.usage.OutputTokens > 0 {
		log.Verbose("AI tokens: %d input, %d output", msg.usage.InputTokens, msg.usage.OutputTokens)
	}

	command := extractCommandWithVerbs(msg.text, m.ai.verbSet)
	if command == "" {
		raw := strings.TrimSpace(msg.text)
		if raw != "" {
			// The model replied with prose instead of a command — show it.
			m.appendTranscript(dimStyle.Render(raw) + "\n\n")
		} else {
			m.appendTranscript(dimStyle.Render(fmt.Sprintf("(empty response from %s — try rephrasing)", m.ai.modelName)) + "\n\n")
		}
		if len(m.ai.history) > 0 && m.ai.history[len(m.ai.history)-1].Role == "user" {
			m.ai.history = m.ai.history[:len(m.ai.history)-1]
		}
		m.state = tuiIdle
		m.input.Focus()
		return m, nil
	}

	if question, ok := strings.CutPrefix(command, "# ask:"); ok {
		m.appendTranscript(infoStyle.Render("? "+strings.TrimSpace(question)) + "\n\n")
		m.ai.history = append(m.ai.history, aiMessage{Role: "assistant", Content: command})
		m.fixAttempts = 0
		m.state = tuiIdle
		m.input.Focus()
		return m, nil
	}

	if reason, ok := strings.CutPrefix(command, "# cannot:"); ok {
		m.appendTranscript(infoStyle.Render("✗ "+strings.TrimSpace(reason)) + "\n\n")
		m.ai.history = append(m.ai.history, aiMessage{Role: "assistant", Content: command})
		m.fixAttempts = 0
		m.state = tuiIdle
		m.input.Focus()
		return m, nil
	}

	// Normalize: collapse embedded newlines (xmc commands are single logical lines).
	command = strings.Join(strings.FieldsFunc(command, func(r rune) bool {
		return r == '\n' || r == '\r'
	}), " ")
	command = strings.TrimSpace(command)

	m.proposedCmd = command
	m.proposedDestructive = anyCommand(command, isDestructive)
	m.shimmerPhase = 0
	// Switch to tuiProposing BEFORE calling setViewportContent so that the
	// state guard in setViewportContent renders the live proposal overlay.
	// (The proposal is NOT written to the transcript yet — it is frozen there
	// only when the user accepts, rejects, edits, or chats.)
	m.state = tuiProposing
	m.input.Blur()
	m.setViewportContent()
	return m, nil
}

// ---------- Command execution ----------

func (m aiTUIModel) startExecution(command string) (tea.Model, tea.Cmd) {
	m.state = tuiExecuting
	m.input.Blur()

	m.ai.history = append(m.ai.history, aiMessage{Role: "assistant", Content: command})

	ai := m.ai
	sess := m.session
	rootCmd := m.rootCmd
	pptr := m.program

	return m, func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		prog := derefProgram(pptr)

		if prog != nil {
			prog.Send(setCancelMsg{cancel: cancel})
		}

		var capBuf cappedBuffer
		capBuf.max = maxCapture
		var errBuf cappedBuffer
		errBuf.max = maxCapture

		execErr := sess.executePipelineIO(ctx, command, rootCmd, strings.NewReader(""), &capBuf, &errBuf)

		refresh := anyCommand(command, isManageList) ||
			(ai.autoUpdateObjects && anyCommand(command, mutatesObjects)) ||
			(ai.autoUpdateMessages && anyCommand(command, mutatesMessages))
		if refresh {
			ai.refreshTopology()
		}

		stdout, stderr := capBuf.String(), errBuf.String()
		feedback := buildFeedback(execErr, stdout, stderr)
		ai.history = append(ai.history, aiMessage{Role: "user", Content: feedback})
		trimHistory(&ai.history, maxHistory)

		return execDoneMsg{err: execErr, stdout: stdout, stderr: stderr}
	}
}

func (m aiTUIModel) handleExecDone(msg execDoneMsg) (tea.Model, tea.Cmd) {
	var result strings.Builder

	// Register the executed command as a copyable item.
	m.copyItems = append(m.copyItems, m.proposedCmd)
	result.WriteString(histCmdStyle.Render("▶ ran: "+m.proposedCmd) +
		copyHintStyle.Render(" ⧉") + "\n")

	if msg.stdout != "" {
		trimmed := strings.TrimRight(msg.stdout, "\n")
		if isMessageReadCommand(m.proposedCmd) {
			// Style message payloads with a left border and italic text.
			result.WriteString(renderMessagePayload(trimmed))
			// Register the payload as a copyable item.
			m.copyItems = append(m.copyItems, trimmed)
			result.WriteString(copyHintStyle.Render("  ⧉") + "\n")
		} else {
			result.WriteString(dimStyle.Render(trimmed) + "\n")
		}
	}
	if msg.err != nil {
		result.WriteString(warnStyle.Render("✗ "+msg.err.Error()) + "\n")
	} else {
		result.WriteString(histOkStyle.Render("✓ ok") + "\n")
		m.fixAttempts = 0
	}
	result.WriteString("\n")

	appendShellHistory(m.proposedCmd)
	m.appendTranscript(result.String())

	// Auto-fix: on error, ask the AI to correct the command (up to maxFixAttempts).
	if msg.err != nil && m.fixAttempts < maxFixAttempts && m.mode == modeAI {
		m.fixAttempts++
		m.appendTranscript(dimStyle.Render("↻ auto-fixing…") + "\n\n")
		m.state = tuiThinking
		m.streamBuf.Reset()
		return m, m.startAIRequest()
	}

	m.state = tuiIdle
	m.input.Focus()
	return m, nil
}

// appendShellHistory appends a command to the shared shell history file.
func appendShellHistory(command string) {
	path, err := shellHistoryPath()
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, command)
}

// ---------- Input prompt helpers ----------

// applyPromptFunc configures ta so that only the first visual line shows the
// prompt label; all wrapped continuation lines receive indent spaces of the
// same width. Must be called before SetWidth so the textarea can account for
// promptWidth in its layout.
func applyPromptFunc(ta *textarea.Model, text string) {
	w := len([]rune(text))
	indent := strings.Repeat(" ", w)
	ta.SetPromptFunc(w, func(lineIdx int) string {
		if lineIdx == 0 {
			return text
		}
		return indent
	})
}

// predictValue estimates the textarea value after applying msg without calling
// Update() — used for pre-sizing the textarea height before the actual Update
// so the resize happens in the right order. Cursor position is not modelled
// precisely; only total length matters for row-count calculation.
func predictValue(current string, msg tea.KeyMsg) string {
	switch msg.Type {
	case tea.KeyRunes:
		return current + string(msg.Runes)
	case tea.KeyBackspace, tea.KeyDelete:
		runes := []rune(current)
		if len(runes) > 0 {
			return string(runes[:len(runes)-1])
		}
	}
	return current
}

// ---------- Layout helpers ----------

func (m *aiTUIModel) recalcLayout() {
	headerHeight := 2
	// Use the textarea's authoritative height so the budget always matches what
	// View() actually renders (textarea.View() always emits exactly m.input.Height() lines).
	inputLines := m.input.Height()
	if inputLines < 1 {
		inputLines = 1
	}
	inputHeight := 2 + inputLines // top rule + textarea rows + bottom rule
	statusHeight := 1
	slack := 1
	// Account for the extra reconnect-status line that renderMainContent appends.
	reconnectLine := 0
	if m.reconnectStatus != "" {
		reconnectLine = 1
	}
	vpHeight := m.height - headerHeight - inputHeight - statusHeight - slack - reconnectLine
	if vpHeight < 3 {
		vpHeight = 3
	}
	convWidth, _ := m.paneWidths()
	m.viewport.Width = convWidth - 2
	m.viewport.Height = vpHeight
	m.input.SetWidth(m.width - 4)
	m.setViewportContent()
}

// updateInputHeight recomputes how many visual rows the current input text
// needs (up to maxInputLines), resizes the textarea if needed, and always
// calls recalcLayout so that viewport height tracks any input-area changes.
// computeInputLines returns the number of visual rows needed to display value in
// the textarea, clamped to [1, maxInputLines]. It uses m.input.Width() which
// reflects the true inner text width that the textarea computed after SetWidth,
// so measurements are always in sync with the widget's own layout.
func (m *aiTUIModel) computeInputLines(value string) int {
	w := m.input.Width() // inner content width (after prompt and reserved margins)
	if w < 1 {
		w = 1
	}
	runes := []rune(value)
	n := 1
	if len(runes) > 0 {
		n = (len(runes) + w - 1) / w
	}
	if n < 1 {
		n = 1
	}
	if n > maxInputLines {
		n = maxInputLines
	}
	return n
}

func (m *aiTUIModel) updateInputHeight() {
	n := m.computeInputLines(m.input.Value())
	if n != m.inputLines {
		m.inputLines = n
		m.input.SetHeight(n)
	}
	m.recalcLayout()
}

// maxTranscriptBytes is the soft cap on transcript memory. When exceeded, the
// oldest content is dropped and a trim marker is prepended.
const maxTranscriptBytes = 200 * 1024 // 200 KB

func (m *aiTUIModel) appendTranscript(text string) {
	m.transcript.WriteString(text)
	// Transcript cap: drop the oldest content when we exceed the budget.
	if m.transcript.Len() > maxTranscriptBytes {
		raw := m.transcript.String()
		// Keep the last maxTranscriptBytes bytes, aligning to a newline boundary.
		keep := raw[m.transcript.Len()-maxTranscriptBytes:]
		if nl := strings.Index(keep, "\n"); nl >= 0 {
			keep = keep[nl+1:]
		}
		m.transcript.Reset()
		m.transcript.WriteString(dimStyle.Render("… earlier output trimmed …") + "\n\n")
		m.transcript.WriteString(keep)
	}
	m.setViewportContent()
}

// setViewportContent rebuilds the viewport content from the transcript (and
// any in-flight streaming tokens), soft-wraps it to the pane width, and
// conditionally scrolls to the bottom when follow mode is active.
func (m *aiTUIModel) setViewportContent() {
	content := m.transcript.String()
	if m.state == tuiThinking && m.streamBuf.Len() > 0 {
		content += dimStyle.Render(collapseBlankLines(m.streamBuf.String()))
	}
	if m.state == tuiPicking && m.picker != nil {
		content += m.renderPicker()
	}
	if m.state == tuiProposing || m.state == tuiEditing {
		text := m.proposedCmd
		if m.state == tuiEditing {
			text = m.input.Value() // shimmer tracks edits in real time
		}
		// Bug A fix: wrap the plain "▶ <cmd>" text first so width measurement is
		// accurate (no ANSI codes), then apply the shimmer band per rune on the
		// pre-wrapped lines. This prevents double-wrapping when the sidebar is shown.
		w := m.viewport.Width
		if w < 1 {
			w = 80
		}
		plainLine := "▶ " + text
		wrapped := wordwrap.String(plainLine, w)
		lines := strings.Split(wrapped, "\n")
		// Count total runes across all wrapped lines to compute shimmer period.
		totalRunes := 0
		for _, l := range lines {
			totalRunes += len([]rune(l))
		}
		period := totalRunes + shimmerBand
		if period < 1 {
			period = 1
		}
		pos := m.shimmerPhase % period // left edge of the bright band
		var shimmed strings.Builder
		globalIdx := 0 // continuous rune index across all wrapped lines
		for li, line := range lines {
			for _, r := range []rune(line) {
				inBand := globalIdx >= pos && globalIdx < pos+shimmerBand
				ch := string(r)
				if inBand {
					shimmed.WriteString(shimmerHighStyle.Render(ch))
				} else {
					shimmed.WriteString(cmdStyle.Render(ch))
				}
				globalIdx++
			}
			if li < len(lines)-1 {
				shimmed.WriteString("\n")
			}
		}
		content += shimmed.String()
		if m.proposedDestructive {
			content += "\n" + warnStyle.Render("  ⚠ destructive — review carefully")
		}
		content += "\n\n"
	}
	if w := m.viewport.Width; w > 0 {
		content = wrap.String(wordwrap.String(content, w), w)
	}
	// Cache the wrapped lines for click-to-copy ⧉ detection.
	m.wrappedContentLines = strings.Split(content, "\n")
	m.viewport.SetContent(content)
	if m.follow {
		m.viewport.GotoBottom()
	}
}

// pickerSelectedStyle is the highlighted item in the picker.
var pickerSelectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))

// renderPicker renders the interactive picker list.
func (m *aiTUIModel) renderPicker() string {
	p := m.picker
	if p == nil {
		return ""
	}
	var b strings.Builder
	if p.title != "" {
		b.WriteString(infoStyle.Render(p.title) + "\n")
	}
	for i, item := range p.items {
		label := item
		if i == p.current {
			label += " (current)"
		}
		if i == p.sel {
			b.WriteString(pickerSelectedStyle.Render("▸ " + label) + "\n")
		} else {
			b.WriteString(dimStyle.Render("  " + label) + "\n")
		}
	}
	b.WriteString(dimStyle.Render("\n↑/↓ select · Enter confirm · Esc cancel") + "\n")
	return b.String()
}

// ---------- Proposal shimmer ----------

// shimmerBand is the number of "lit" runes in the shimmer highlight band.
const shimmerBand = 4

// ---------- Copy-to-clipboard helpers ----------

const copyMarker = "⧉"

// lineHasCopyMarker reports whether the (potentially ANSI-coloured) line
// contains a ⧉ clipboard marker.
func lineHasCopyMarker(line string) bool {
	return strings.Contains(xansi.Strip(line), copyMarker)
}

// copyIdxForLine returns the 0-based index into m.copyItems for a click on
// wrappedContentLines[clickedLine].  It counts the number of ⧉ markers that
// appear at or before clickedLine (the Nth marker → copyItems[N-1]).
// Returns -1 if the clicked line has no marker.
func (m aiTUIModel) copyIdxForLine(clickedLine int) int {
	if clickedLine < 0 || clickedLine >= len(m.wrappedContentLines) {
		return -1
	}
	if !lineHasCopyMarker(m.wrappedContentLines[clickedLine]) {
		return -1
	}
	count := 0
	for i := 0; i <= clickedLine; i++ {
		if lineHasCopyMarker(m.wrappedContentLines[i]) {
			count++
		}
	}
	return count - 1 // 0-based
}

// isMessageReadCommand reports whether cmd is one that consumes / peeks
// messages and therefore produces payload output on stdout.
func isMessageReadCommand(cmd string) bool {
	verbs := []string{"receive ", "receive\n", "peek ", "peek\n", "subscribe ", "subscribe\n"}
	cmd = strings.TrimSpace(cmd)
	for _, v := range verbs {
		if strings.HasPrefix(cmd, strings.TrimSpace(v)) {
			return true
		}
	}
	// Also match bare verb (no args).
	switch cmd {
	case "receive", "peek", "subscribe":
		return true
	}
	return false
}

// renderMessagePayload renders a message payload (or multiple NDJSON records)
// with a left border and italic cyan body, mimicking a blockquote.
func renderMessagePayload(content string) string {
	lines := strings.Split(content, "\n")
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(msgBorderStyle.Render("│") + " " + msgBodyStyle.Render(line) + "\n")
	}
	return b.String()
}

// ---------- Proposal rendering ----------

// freezeProposal builds the static transcript line written when the user
// resolves a proposed command. The marker (✓, ✗, ?) is appended with the
// appropriate style. When dim is true the command text is rendered in dimStyle.
func freezeProposal(cmd, marker string, dim, destructive bool) string {
	var prefix string
	if dim {
		prefix = dimStyle.Render("▶ " + cmd)
	} else {
		prefix = cmdStyle.Render("▶ " + cmd)
	}
	var result string
	switch marker {
	case "✓":
		result = prefix + " " + cmdStyle.Render("✓")
	case "✗":
		result = prefix + " " + warnStyle.Render("✗")
	case "?":
		result = prefix + " " + infoStyle.Render("?")
	default:
		result = prefix
	}
	if destructive && marker != "✗" {
		result += "\n" + warnStyle.Render("  ⚠ destructive — review carefully")
	}
	return result + "\n\n"
}

// ---------- Broker objects ----------

func (m aiTUIModel) startLoadObjects() tea.Cmd {
	// Capture the list functions so they can be called from the background.
	type listFn struct {
		fn func() ([]backends.ObjectNode, error)
	}
	fns := make([]listFn, len(m.objTypes))
	for i, w := range m.objTypes {
		fns[i] = listFn{fn: w.listFn}
	}
	return func() tea.Msg {
		if len(fns) == 0 {
			return objectsMsg{errs: []error{errNoManageAPI}}
		}
		msg := objectsMsg{
			windows: make([][]backends.ObjectNode, len(fns)),
			errs:    make([]error, len(fns)),
		}
		for i, f := range fns {
			if f.fn != nil {
				nodes, err := f.fn()
				msg.windows[i] = nodes
				msg.errs[i] = err
			}
		}
		return msg
	}
}

// autoUpdateEnabled returns true when periodic sidebar refresh is enabled.
func (m aiTUIModel) autoUpdateEnabled() bool {
	return m.ai != nil && m.refreshEnabled
}

// beginRefresh starts a background sidebar fetch, stamps the start time, bumps
// the generation counter, and arms a watchdog. Must be called as a pointer
// receiver so the state mutation is visible to the caller.
func (m *aiTUIModel) beginRefresh() tea.Cmd {
	m.refreshing = true
	m.refreshStart = time.Now()
	m.refreshGen++
	gen := m.refreshGen
	return tea.Batch(
		m.startLoadObjects(),
		tea.Tick(refreshWatchdogTimeout, func(time.Time) tea.Msg { return refreshWatchdogMsg{gen: gen} }),
	)
}

func (m aiTUIModel) handleObjectsDone(msg objectsMsg) (tea.Model, tea.Cmd) {
	m.loadingObjects = false
	for i := range m.objTypes {
		// Never overwrite the Processes window with broker-objects data.
		if m.objTypes[i].kind == objWindowProcs {
			continue
		}
		if i < len(msg.windows) {
			m.objTypes[i].nodes = msg.windows[i]
		}
		if i < len(msg.errs) {
			m.objTypes[i].err = msg.errs[i]
			if msg.errs[i] != nil && !errors.Is(msg.errs[i], errNoManageAPI) {
				log.Verbose("objects fetch [%s]: %s", m.objTypes[i].label, msg.errs[i])
			}
		}
		// Clamp selection.
		filtered := m.getFilteredSortedNodes(i)
		if m.objTypes[i].sel >= len(filtered) {
			m.objTypes[i].sel = max(0, len(filtered)-1)
		}
	}

	// Schedule the next periodic refresh if this was a background fetch.
	if m.refreshing {
		m.refreshing = false
		m.lastFetchDur = time.Since(m.refreshStart)
		if m.autoUpdateEnabled() {
			next := time.Duration(refreshFactor) * m.lastFetchDur
			if next < m.refreshPeriod {
				next = m.refreshPeriod
			}
			return m, tea.Tick(next, func(time.Time) tea.Msg { return refreshTickMsg{} })
		}
	}
	return m, nil
}

// ---------- Sidebar helpers ----------

// cycleFocus moves focus to the next (forward=true) or previous available pane.
func (m *aiTUIModel) cycleFocus(forward bool) {
	// Targets: focusChat (0), then 1..len(objTypes) for each window.
	n := len(m.objTypes) + 1
	if n <= 1 {
		return
	}
	prevFocus := m.focus
	cur := int(m.focus)
	if forward {
		cur = (cur + 1) % n
	} else {
		cur = (cur - 1 + n) % n
	}
	m.focus = focusTarget(cur)

	// Exit process view when leaving the process pane.
	if prevWi := int(prevFocus) - 1; prevWi >= 0 && prevWi < len(m.objTypes) &&
		m.objTypes[prevWi].kind == objWindowProcs {
		m.exitProcessView()
	}

	if m.focus == focusChat {
		m.input.Focus()
	} else {
		m.input.Blur()
		// Enter process view when arriving at the process pane.
		if wi := int(m.focus) - 1; wi >= 0 && wi < len(m.objTypes) &&
			m.objTypes[wi].kind == objWindowProcs {
			m.enterProcessView()
		}
	}
}

// moveSel moves the selection in the focused pane by delta.
func (m *aiTUIModel) moveSel(delta int) {
	wi := int(m.focus) - 1
	if wi < 0 || wi >= len(m.objTypes) {
		return
	}
	items := m.getFilteredSortedNodes(wi)
	if len(items) == 0 {
		return
	}
	m.objTypes[wi].sel = clampInt(m.objTypes[wi].sel+delta, 0, len(items)-1)
}

// selectedName returns the name of the currently selected item in the focused pane.
func (m *aiTUIModel) selectedName() string {
	wi := int(m.focus) - 1
	if wi < 0 || wi >= len(m.objTypes) {
		return ""
	}
	items := m.getFilteredSortedNodes(wi)
	if m.objTypes[wi].sel < len(items) {
		return items[m.objTypes[wi].sel].Name
	}
	return ""
}

// cycleSort advances the sort mode for the focused pane.
func (m *aiTUIModel) cycleSort() {
	wi := int(m.focus) - 1
	if wi < 0 || wi >= len(m.objTypes) {
		return
	}
	w := &m.objTypes[wi]
	metrics := firstMetrics(w.nodes)
	nModes := 1 + len(metrics) // name + one per metric
	w.sortIdx = sortMode((int(w.sortIdx) + 1) % nModes)
	w.sel = 0
}

// getFilteredSortedNodes returns the node list for window idx, filtered and sorted.
func (m *aiTUIModel) getFilteredSortedNodes(idx int) []backends.ObjectNode {
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

// ---------- Connection probe ----------

func (m aiTUIModel) startProbeConnection() tea.Cmd {
	ping := m.session.spec.Ping
	return func() tea.Msg {
		conn, err := ping()
		if err != nil {
			return connMsg{err: err}
		}
		_ = conn.Close()
		return connMsg{}
	}
}

const (
	reconnectInitialBackoff = 2 * time.Second
	reconnectMaxBackoff     = 3 * time.Minute
)

func (m aiTUIModel) handleConnDone(msg connMsg) (tea.Model, tea.Cmd) {
	m.connChecked = true
	m.connErr = msg.err
	if msg.err == nil {
		return m, nil
	}
	m.appendTranscript(warnStyle.Render("connection error: "+msg.err.Error()) + "\n\n")
	if m.reconnectDisabled {
		return m, nil
	}
	// Start auto-reconnect with initial backoff.
	m.reconnecting = true
	m.reconnectBackoff = reconnectInitialBackoff
	m.reconnectAt = time.Now().Add(m.reconnectBackoff)
	return m, tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return reconnectTickMsg{} })
}

// startReconnectProbe fires the connection probe and returns the result as
// reconnectProbeMsg (distinct from connMsg so the two paths don't collide).
func (m aiTUIModel) startReconnectProbe() tea.Cmd {
	ping := m.session.spec.Ping
	return func() tea.Msg {
		conn, err := ping()
		if err != nil {
			return reconnectProbeMsg{err: err}
		}
		_ = conn.Close()
		return reconnectProbeMsg{}
	}
}

func (m aiTUIModel) handleReconnectTick() (tea.Model, tea.Cmd) {
	if !m.reconnecting || m.reconnectDisabled {
		m.reconnectStatus = ""
		return m, nil
	}
	m.reconnectBlink = !m.reconnectBlink

	remaining := time.Until(m.reconnectAt)
	if remaining > 0 {
		secs := int(remaining.Seconds()) + 1
		m.reconnectStatus = fmt.Sprintf("↻ reconnecting in %ds…", secs)
		return m, tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return reconnectTickMsg{} })
	}

	// Time to probe.
	m.reconnectStatus = "↻ connecting…"
	return m, m.startReconnectProbe()
}

func (m aiTUIModel) handleReconnectProbe(msg reconnectProbeMsg) (tea.Model, tea.Cmd) {
	if msg.err == nil {
		// Connected!
		m.reconnecting = false
		m.reconnectStatus = ""
		m.connErr = nil
		m.connChecked = true
		m.appendTranscript(infoStyle.Render("✓ connected") + "\n\n")
		return m, nil
	}
	// Still failing — double backoff and keep ticking.
	m.reconnectBackoff *= 2
	if m.reconnectBackoff > reconnectMaxBackoff {
		m.reconnectBackoff = reconnectMaxBackoff
	}
	m.reconnectAt = time.Now().Add(m.reconnectBackoff)
	return m, tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return reconnectTickMsg{} })
}

// ---------- Input mode & history ----------

// toggleInputMode switches between AI prompt and direct command modes.
func (m *aiTUIModel) toggleInputMode() {
	m.histIdx = -1
	if m.mode == modeAI {
		m.mode = modeCmd
		applyPromptFunc(&m.input, m.binaryName+"> ")
		m.input.Placeholder = "Type an xmc command..."
	} else {
		m.mode = modeAI
		applyPromptFunc(&m.input, "ask> ")
		m.input.Placeholder = "Ask anything..."
	}
	// Re-layout: prompt width may have changed (e.g. "ask> " vs "awsmc> ").
	// updateInputHeight recomputes inputLines for the new prompt width, then calls recalcLayout.
	m.updateInputHeight()
}

// historyPrev recalls the previous entry from the active history.
func (m *aiTUIModel) historyPrev() {
	hist := m.activeHistory()
	if len(hist) == 0 {
		return
	}
	if m.histIdx == -1 {
		// Save the current draft before navigating.
		m.histDraft = m.input.Value()
		m.histIdx = len(hist) - 1
	} else if m.histIdx > 0 {
		m.histIdx--
	}
	m.input.SetValue(hist[m.histIdx])
}

// historyNext recalls the next entry, returning to the draft at the end.
func (m *aiTUIModel) historyNext() {
	hist := m.activeHistory()
	if m.histIdx == -1 {
		return
	}
	if m.histIdx < len(hist)-1 {
		m.histIdx++
		m.input.SetValue(hist[m.histIdx])
	} else {
		m.histIdx = -1
		m.input.SetValue(m.histDraft)
	}
}

// activeHistory returns the history list for the current input mode.
func (m *aiTUIModel) activeHistory() []string {
	if m.mode == modeCmd {
		return m.cmdHistory
	}
	return m.askHistory
}

// ---------- Autocomplete ----------

// doAutocomplete performs Tab-completion on the current input line using the
// readline-compatible PrefixCompleter (same tree as the regular shell).
func (m *aiTUIModel) doAutocomplete() {
	if m.completer == nil {
		return
	}
	line := []rune(m.input.Value())
	candidates, prefixLen := m.completer.Do(line, len(line))
	if len(candidates) == 0 {
		return
	}
	if len(candidates) == 1 {
		// Single match — append it.
		m.input.SetValue(string(line) + string(candidates[0]))
		return
	}
	// Multiple matches — append longest common prefix.
	lcp := longestCommonPrefix(candidates)
	if len(lcp) > 0 && len(lcp) > int(prefixLen) {
		// Only append the portion beyond what's already typed.
		m.input.SetValue(string(line) + string(lcp))
		return
	}
	// Show candidates as a transient line in the transcript.
	var names []string
	for _, c := range candidates {
		name := strings.TrimSpace(string(c))
		if name != "" {
			names = append(names, name)
		}
	}
	if len(names) > 0 {
		m.appendTranscript(dimStyle.Render("  "+strings.Join(names, "  ")) + "\n")
	}
}

// longestCommonPrefix returns the longest common prefix among rune slices.
func longestCommonPrefix(candidates [][]rune) []rune {
	if len(candidates) == 0 {
		return nil
	}
	prefix := candidates[0]
	for _, c := range candidates[1:] {
		n := len(prefix)
		if len(c) < n {
			n = len(c)
		}
		i := 0
		for i < n && prefix[i] == c[i] {
			i++
		}
		prefix = prefix[:i]
		if len(prefix) == 0 {
			break
		}
	}
	return prefix
}

// loadShellHistory reads the shared shell history file and returns its lines.
func loadShellHistory() []string {
	path, err := shellHistoryPath()
	if err != nil {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if line := scanner.Text(); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// loadAskHistory reads the AI ask-prompt history file and returns its lines.
func loadAskHistory() []string {
	path, err := askHistoryPath()
	if err != nil {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if line := scanner.Text(); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// appendAskHistory appends an AI ask-prompt to the persistent history file.
func appendAskHistory(prompt string) {
	path, err := askHistoryPath()
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, prompt)
}

// ---------- Entry point ----------

func derefProgram(pptr **tea.Program) *tea.Program {
	if pptr == nil {
		return nil
	}
	return *pptr
}

func runAITUI(ai *aiSession, session *shellSession, rootCmd *cobra.Command, binaryName, server string) (exitAll bool, totalIn, totalOut int, err error) {
	var prog *tea.Program
	model := newAITUIModel(ai, session, rootCmd, binaryName, server)
	model.program = &prog
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	prog = p
	finalModel, runErr := p.Run()
	if m, ok := finalModel.(aiTUIModel); ok {
		totalIn = m.totalIn
		totalOut = m.totalOut
		// Cancel any still-running background processes and wait up to 2s for
		// their goroutines to exit before the shared session adapters are torn down.
		(&m).killAllProcs()
		deadline := time.After(2 * time.Second)
		for _, proc := range m.procs {
			if proc.doneCh != nil {
				select {
				case <-proc.doneCh:
				case <-deadline:
				}
			}
		}
		if m.exitAll {
			return true, totalIn, totalOut, runErr
		}
	}
	return false, totalIn, totalOut, runErr
}
