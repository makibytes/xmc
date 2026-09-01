package cmd

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/makibytes/xmc/broker/backends"
)

// sidebarAction is the single source of truth for a selection-dependent sidebar
// object hotkey ("c", "d", "p", "m", "P", "S", "R"). Both the status-bar hint
// renderer (cmd/aitui.go renderStatusBar) and the sidebar key dispatcher
// (cmd/aikeys.go handleKeyPane) call resolve for the currently focused window
// and selection: it returns the hint label to display plus a run closure to
// invoke on keypress, or ok=false when the key does nothing for the current
// selection. Because both call sites consume the same resolve function, the
// hint bar can never advertise a key that the dispatcher would then ignore
// (or vice versa) — the two were previously hand-kept in sync across two
// files and could drift.
//
// resolve itself must be side-effect-free (the hint renderer calls it purely
// to check eligibility and discards run); all mutation happens lazily inside
// the returned run closure, which only executes when the key is actually
// pressed.
type sidebarAction struct {
	key     string
	resolve func(m *aiTUIModel, wi int) (hint string, run func() (tea.Model, tea.Cmd), ok bool)
}

// sidebarActions is the ordered table of sidebar object hotkeys. Order
// determines status-bar hint order, matching the pre-refactor visual layout:
// create, delete, purge/publish, peek, metadata, send, receive. Static — the
// resolve funcs close over no per-call state — so it's built once rather
// than reallocated (with fresh resolveReadAction closures) on every call;
// renderStatusBar ranges over it on every frame.
var sidebarActions = []sidebarAction{
	{"c", resolveCreateAction},
	{"d", resolveDeleteAction},
	{"P", resolvePurgePublishAction},
	{"p", resolveReadAction("peek", "peek", false, backends.VerbosityQuiet, payloadOnlyRender)},
	{"m", resolveReadAction("metadata", "peek metadata", false, backends.VerbosityVerbose, withMetadataRender)},
	{"S", resolveSendAction},
	{"R", resolveReadAction("receive", "receive", true, backends.VerbosityQuiet, payloadOnlyRender)},
}

// lookupSidebarAction finds the action bound to key, if any.
func lookupSidebarAction(key string) (sidebarAction, bool) {
	for _, a := range sidebarActions {
		if a.key == key {
			return a, true
		}
	}
	return sidebarAction{}, false
}

// resolveCreateAction: "c" never depends on the current selection — only on
// whether the focused window declares a create action.
func resolveCreateAction(m *aiTUIModel, wi int) (string, func() (tea.Model, tea.Cmd), bool) {
	if wi < 0 || wi >= len(m.objTypes) || m.objTypes[wi].createAction == nil {
		return "", nil, false
	}
	return "create", func() (tea.Model, tea.Cmd) {
		m.startPrompt("create", wi, "")
		return *m, nil
	}, true
}

// resolveDeleteAction: "d" applies to any window with a delete action, on a
// selected top-level row (never a child row — deletion of e.g. a RabbitMQ
// binding or NATS consumer isn't modeled as a sidebar action).
func resolveDeleteAction(m *aiTUIModel, wi int) (string, func() (tea.Model, tea.Cmd), bool) {
	if wi < 0 || wi >= len(m.objTypes) || m.objTypes[wi].deleteAction == nil {
		return "", nil, false
	}
	node, ok := m.selectedTopLevelNode()
	if !ok || node.Name == "" {
		return "", nil, false
	}
	name := node.Name
	return "delete", func() (tea.Model, tea.Cmd) {
		m.startPrompt("delete", wi, name)
		return *m, nil
	}, true
}

// resolvePurgePublishAction: "P" carries three meanings depending on window
// and selection — publish on a top-level Topic, purge on a selected
// Subscription child (Azure/Google only), or purge on Queues/Streams (and
// never on Addresses/Exchanges, which have no reliable 1:1 queue mapping).
func resolvePurgePublishAction(m *aiTUIModel, wi int) (string, func() (tea.Model, tea.Cmd), bool) {
	if wi < 0 || wi >= len(m.objTypes) {
		return "", nil, false
	}
	ow := m.objTypes[wi]

	if ow.publish {
		if child, parentName, ok := m.selectedChildNode(); ok {
			if !ow.subscriptionEligible(child.Kind) || child.Name == "" {
				return "", nil, false
			}
			if m.session == nil || m.session.spec.ManageSpec == nil || m.session.spec.ManageSpec.PurgeSubscription == nil {
				return "", nil, false
			}
			childName := child.Name
			return "purge", func() (tea.Model, tea.Cmd) {
				m.promptTarget = parentName
				m.startPrompt("purge-subscription", wi, childName)
				return *m, nil
			}, true
		}
		node, ok := m.selectedTopLevelNode()
		if !ok || node.Name == "" {
			return "", nil, false
		}
		name := node.Name
		return "publish", func() (tea.Model, tea.Cmd) {
			m.promptTarget = name
			m.startPrompt("publish", wi, "")
			return *m, nil
		}, true
	}

	if !ow.sendEligible() {
		return "", nil, false
	}
	node, ok := m.selectedTopLevelNode()
	if !ok || node.Name == "" {
		return "", nil, false
	}
	if !ow.drain {
		return "", nil, false
	}
	if m.session == nil || m.session.spec.ManageSpec == nil || m.session.spec.ManageSpec.Purge == nil {
		return "", nil, false
	}
	name := node.Name
	return "purge", func() (tea.Model, tea.Cmd) {
		m.startPrompt("purge", wi, name)
		return *m, nil
	}, true
}

// resolveSendAction: "S" applies to any send-eligible window (Queues, Streams,
// Addresses, Exchanges) on a selected top-level row.
func resolveSendAction(m *aiTUIModel, wi int) (string, func() (tea.Model, tea.Cmd), bool) {
	if wi < 0 || wi >= len(m.objTypes) || !m.objTypes[wi].sendEligible() {
		return "", nil, false
	}
	node, ok := m.selectedTopLevelNode()
	if !ok || node.Name == "" {
		return "", nil, false
	}
	name, kind := node.Name, node.Kind
	return "send", func() (tea.Model, tea.Cmd) {
		m.promptTarget = name
		m.promptNodeKind = kind
		m.startPrompt("send", wi, "")
		return *m, nil
	}, true
}

// payloadOnlyRender and withMetadataRender are the two ways resolveReadAction
// turns a read message into a sideActionMsg; fmtMode is always captured by
// the caller (even payloadOnlyRender's caller) so both share one closure shape.
func payloadOnlyRender(msg *backends.Message, _ metadataFormat) sideActionMsg {
	return formatMessagePayloadForSideAction(msg)
}

func withMetadataRender(msg *backends.Message, fmtMode metadataFormat) sideActionMsg {
	return formatMessageMetadataForSideAction(msg, fmtMode)
}

// resolveReadAction returns a resolve func for "R" (receive: hint="receive",
// descVerb="receive", ack=true), "p" (peek: hint="peek", descVerb="peek",
// ack=false), or "m" (peek metadata: hint="metadata", descVerb="peek
// metadata", ack=false, verbosity=Verbose so adapters that otherwise omit
// properties/internal metadata include them). Applies to a selected
// Subscription child (a Topics-shaped window with ChildKind set — Azure/
// Google only) or a top-level row on a drain-eligible window (Queues/
// Streams — never Addresses/Exchanges, which have no reliable 1:1 queue
// mapping).
func resolveReadAction(hint, descVerb string, ack bool, verbosity backends.Verbosity, render func(*backends.Message, metadataFormat) sideActionMsg) func(m *aiTUIModel, wi int) (string, func() (tea.Model, tea.Cmd), bool) {
	return func(m *aiTUIModel, wi int) (string, func() (tea.Model, tea.Cmd), bool) {
		if wi < 0 || wi >= len(m.objTypes) {
			return "", nil, false
		}
		ow := m.objTypes[wi]

		if child, parentName, ok := m.selectedChildNode(); ok {
			if !ow.subscriptionEligible(child.Kind) || child.Name == "" {
				return "", nil, false
			}
			childName, session, fmtMode := child.Name, m.session, m.metadataFormat
			return hint, func() (tea.Model, tea.Cmd) {
				desc := fmt.Sprintf("▶ %s Subscription \"%s\"", descVerb, childName)
				m.appendTranscript(histCmdStyle.Render(desc) + "\n")
				m.state = tuiExecuting
				topic, sub := parentName, childName
				return *m, func() tea.Msg {
					ta, err := session.getTopicAdapter()
					if err != nil {
						return sideActionMsg{err: fmt.Errorf("adapter: %w", err)}
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					msg, err := ta.Subscribe(ctx, backends.SubscribeOptions{
						Topic:       topic,
						Extra:       map[string]string{"subscription": sub},
						Acknowledge: ack,
						Verbosity:   verbosity,
						Timeout:     1,
						Wait:        false,
					})
					if err != nil {
						if isNoMessage(err) {
							return sideActionMsg{action: "   └ (no messages available)"}
						}
						return sideActionMsg{err: err}
					}
					return render(msg, fmtMode)
				}
			}, true
		}

		if !ow.drain {
			return "", nil, false
		}
		node, ok := m.selectedTopLevelNode()
		if !ok || node.Name == "" {
			return "", nil, false
		}
		name, session, fmtMode := node.Name, m.session, m.metadataFormat
		return hint, func() (tea.Model, tea.Cmd) {
			desc := fmt.Sprintf("▶ %s %s \"%s\"", descVerb, ow.singularLabel(), name)
			m.appendTranscript(histCmdStyle.Render(desc) + "\n")
			m.state = tuiExecuting
			queue := name
			return *m, func() tea.Msg {
				qa, err := session.getQueueAdapter()
				if err != nil {
					return sideActionMsg{err: fmt.Errorf("adapter: %w", err)}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				msg, err := qa.Receive(ctx, backends.ReceiveOptions{
					Queue:       queue,
					Acknowledge: ack,
					Verbosity:   verbosity,
					Timeout:     1,
					Wait:        false,
				})
				if err != nil {
					if isNoMessage(err) {
						return sideActionMsg{action: "   └ (no messages available)"}
					}
					return sideActionMsg{err: err}
				}
				return render(msg, fmtMode)
			}
		}, true
	}
}
