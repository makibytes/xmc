package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/chzyer/readline"
	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
	runewidth "github.com/mattn/go-runewidth"
	"github.com/muesli/reflow/wordwrap"
	"github.com/rivo/uniseg"
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

// ---------- Bubble Tea messages ----------

type tokenMsg struct{ text string }                              // streamed token from AI
type aiDoneMsg struct{ text string; usage TokenUsage; err error } // AI request completed
type execDoneMsg struct{ err error; stdout, stderr string }       // command finished
type sideActionMsg struct{ action string; err error }             // sidebar create/delete finished
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
	treeView     bool                  // hierarchical children shown (x to toggle)
	collapsed    bool                  // window collapsed to title only (Space to toggle)
	createAction *ManageAction         // optional: 'c' hotkey creates this object type
	deleteAction *ManageAction         // optional: 'd' hotkey deletes selected
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

	// Connection and auto-reconnect state
	conn connState

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

	// Sidebar inline prompt for create/delete hotkeys
	promptActive  bool   // true when awaiting input for create/delete
	promptKind    string // "create" or "delete"
	promptObjIdx  int    // index into objTypes
	promptName    string // typed name (create) or object name (delete)

	// Program reference (double pointer for Bubble Tea value copy)
	program **tea.Program
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
				c, d := session.spec.ManageSpec.SidebarActions(ot.Label)
				windows = append(windows, objWindow{
					label:        ot.Label,
					hierarchical: ot.Hierarchical,
					listFn:       ot.List,
					createAction: c,
					deleteAction: d,
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
		cmdHistory:      shellHistory.Load(),
		askHistory:      askHistory.Load(),
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
			// Guard on X too: with the sidebar shown, a click there can land on a
			// row that happens to align with a marked transcript line — without
			// the X check it would copy that unrelated transcript item.
			if vRow >= 0 && vRow < m.viewport.Height && msg.X >= 0 && msg.X < m.viewport.Width {
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

	case sideActionMsg:
		if msg.err != nil {
			m.appendTranscript(warnStyle.Render("✗ "+msg.err.Error()) + "\n\n")
		} else if msg.action != "" {
			m.appendTranscript(msg.action + "\n\n")
		} else {
			m.appendTranscript(histOkStyle.Render("✓ ok") + "\n\n")
		}
		if m.ai != nil && m.ai.autoUpdateObjects {
			m.loadingObjects = true
			cmds = append(cmds, m.startLoadObjects())
		}
		m.state = tuiIdle
		m.input.Focus()
		return m, tea.Batch(cmds...)

	case setCancelMsg:
		m.execCancel = msg.cancel
		return m, nil

	case procCancelMsg:
		return m.handleProcCancelMsg(msg)

	case procDoneMsg:
		return m.handleProcDoneMsg(msg)

	case procStderrMsg:
		// State already updated by the writer goroutine (bgProcess.stderrSeen);
		// the message only forces a repaint so the red highlight shows now.
		return m, nil

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

	// ── Sidebar create/delete prompt overlay ──
	if m.promptActive {
		inputView = m.renderPromptLine()
	}

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
	case m.conn.reconnecting:
		return titleServerBusyStyle.Render(text)
	case m.conn.checked && m.conn.err != nil:
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

// renderPromptLine renders the inline prompt for sidebar create/delete hotkeys.
func (m aiTUIModel) renderPromptLine() string {
	wi := m.promptObjIdx
	if wi < 0 || wi >= len(m.objTypes) {
		return ""
	}
	label := m.objTypes[wi].label
	if m.promptKind == "create" {
		prompt := fmt.Sprintf("Enter name for new %s: %s█", singular(label), m.promptName)
		return infoStyle.Render(prompt)
	}
	prompt := fmt.Sprintf("Delete \"%s\"?  (Enter=confirm, Esc=cancel)", m.promptName)
	return warnStyle.Render(prompt)
}

func (m aiTUIModel) renderMainContent() string {
	convWidth, sideWidth := m.paneWidths()
	_ = convWidth // viewport.Width already set by recalcLayout

	convPane := m.viewport.View()

	// Show reconnect countdown as a one-line status below the transcript.
	if m.conn.reconnectStatus != "" {
		convPane = lipgloss.JoinVertical(lipgloss.Left,
			convPane,
			infoStyle.Render(m.conn.reconnectStatus),
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
			wi := int(m.focus) - 1
			treeHint := ""
			if wi >= 0 && wi < len(m.objTypes) && m.objTypes[wi].hierarchical {
				treeHint = statusStyle.Render("  ") +
					statusKeyStyle.Render("x") + statusStyle.Render(" tree")
			}
			createHint := ""
			if wi >= 0 && wi < len(m.objTypes) && m.objTypes[wi].createAction != nil {
				createHint = statusStyle.Render("  ") +
					statusKeyStyle.Render("c") + statusStyle.Render(" create")
			}
			deleteHint := ""
			if wi >= 0 && wi < len(m.objTypes) && m.objTypes[wi].deleteAction != nil {
				deleteHint = statusStyle.Render("  ") +
					statusKeyStyle.Render("d") + statusStyle.Render(" delete")
			}
			left = statusKeyStyle.Render("↑↓") + statusStyle.Render(" move") +
				statusStyle.Render("  ") +
				statusKeyStyle.Render("/") + statusStyle.Render(" filter") +
				statusStyle.Render("  ") +
				statusKeyStyle.Render("s") + statusStyle.Render(" sort") +
				treeHint +
				createHint +
				deleteHint +
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
	if m.conn.reconnectStatus != "" {
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

// computeInputLines returns the number of visual rows needed to display value in
// the textarea, clamped to [1, maxInputLines]. It uses m.input.Width() which
// reflects the true inner text width that the textarea computed after SetWidth,
// and textareaWrap, which replicates the widget's own word-wrap, so the count
// always matches what the widget renders. A plain ceil(runes/width) estimate
// undercounts as soon as a word soft-wraps, which made the textarea scroll its
// first line out of view before the height caught up.
func (m *aiTUIModel) computeInputLines(value string) int {
	w := m.input.Width() // inner content width (after prompt and reserved margins)
	if w < 1 {
		w = 1
	}
	n := 0
	for _, line := range strings.Split(value, "\n") {
		n += len(textareaWrap([]rune(line), w))
	}
	if n < 1 {
		n = 1
	}
	if n > maxInputLines {
		n = maxInputLines
	}
	return n
}

// textareaWrap is a verbatim copy of the soft-wrap algorithm inside
// charmbracelet/bubbles v1.0.0 textarea (wrap(), MIT license). The input area
// grows with its content instead of scrolling, so the height budget must agree
// exactly with the widget's internal wrapping — including its quirks (whole
// words move to the next row; a line that is exactly full gets a trailing
// empty row for the cursor). Keep in sync when upgrading bubbles.
func textareaWrap(runes []rune, width int) [][]rune {
	var (
		lines  = [][]rune{{}}
		word   = []rune{}
		row    int
		spaces int
	)

	for _, r := range runes {
		if unicode.IsSpace(r) {
			spaces++
		} else {
			word = append(word, r)
		}

		if spaces > 0 {
			if uniseg.StringWidth(string(lines[row]))+uniseg.StringWidth(string(word))+spaces > width {
				row++
				lines = append(lines, []rune{})
				lines[row] = append(lines[row], word...)
				lines[row] = append(lines[row], repeatSpaces(spaces)...)
			} else {
				lines[row] = append(lines[row], word...)
				lines[row] = append(lines[row], repeatSpaces(spaces)...)
			}
			spaces = 0
			word = nil
		} else {
			// A trailing double-width rune may not fit on this line.
			lastCharLen := runewidth.RuneWidth(word[len(word)-1])
			if uniseg.StringWidth(string(word))+lastCharLen > width {
				// If the current line has any content, move to the next line
				// because the current word fills up the entire line.
				if len(lines[row]) > 0 {
					row++
					lines = append(lines, []rune{})
				}
				lines[row] = append(lines[row], word...)
				word = nil
			}
		}
	}

	if uniseg.StringWidth(string(lines[row]))+uniseg.StringWidth(string(word))+spaces >= width {
		lines = append(lines, []rune{})
		lines[row+1] = append(lines[row+1], word...)
		// The extra space accounts for the trailing space at the end of the
		// previous soft-wrapped lines (cursor navigation consistency).
		spaces++
		lines[row+1] = append(lines[row+1], repeatSpaces(spaces)...)
	} else {
		lines[row] = append(lines[row], word...)
		spaces++
		lines[row] = append(lines[row], repeatSpaces(spaces)...)
	}

	return lines
}

func repeatSpaces(n int) []rune {
	return []rune(strings.Repeat(" ", n))
}

// updateInputHeight recomputes how many visual rows the current input text
// needs (up to maxInputLines), resizes the textarea if needed, and always
// calls recalcLayout so that viewport height tracks any input-area changes.
func (m *aiTUIModel) updateInputHeight() {
	n := m.computeInputLines(m.input.Value())
	if n != m.inputLines {
		grew := n > m.inputLines
		m.inputLines = n
		m.input.SetHeight(n)
		if grew {
			m.resetInputScroll()
		}
	}
	m.recalcLayout()
}

// resetInputScroll clears the textarea's internal viewport offset after the
// input area has grown. When a keystroke wraps the text to a new visual row
// while the widget still has its old (smaller) height, the textarea scrolls
// down to keep the cursor visible — and growing the height afterwards does not
// scroll back, leaving the first input line hidden. Rebuilding the value goes
// through Reset(), which is the only exported path to viewport.GotoTop();
// the cursor column is restored afterwards.
func (m *aiTUIModel) resetInputScroll() {
	if m.input.LineCount() != 1 {
		return // cursor row is not restorable across SetValue; skip the heal
	}
	li := m.input.LineInfo()
	col := li.StartColumn + li.ColumnOffset
	m.input.SetValue(m.input.Value())
	m.input.SetCursor(col)
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
		content = wordwrap.String(content, w)
	}
	// Cache the wrapped lines for click-to-copy ⧉ detection.
	m.wrappedContentLines = strings.Split(content, "\n")
	m.viewport.SetContent(content)
	if m.follow {
		m.viewport.GotoBottom()
	}
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
