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

// sidebarActions returns the ordered table of sidebar object hotkeys. Order
// determines status-bar hint order, matching the pre-refactor visual layout:
// create, delete, purge/publish, peek, metadata, send, receive.
func sidebarActions() []sidebarAction {
	return []sidebarAction{
		{"c", resolveCreateAction},
		{"d", resolveDeleteAction},
		{"P", resolvePurgePublishAction},
		{"p", resolvePeekAction(false)},
		{"m", resolvePeekAction(true)},
		{"S", resolveSendAction},
		{"R", resolveReceiveAction},
	}
}

// lookupSidebarAction finds the action bound to key, if any.
func lookupSidebarAction(key string) (sidebarAction, bool) {
	for _, a := range sidebarActions() {
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
	label := m.objTypes[wi].label

	if label == "Topics" {
		if child, parentName, ok := m.selectedChildNode(); ok {
			if !sidebarSubscriptionEligible(label, child.Kind) || child.Name == "" {
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

	if !sidebarWindowSupportsSRP(label) {
		return "", nil, false
	}
	node, ok := m.selectedTopLevelNode()
	if !ok || node.Name == "" {
		return "", nil, false
	}
	if !sidebarPurgeReceiveAllowed(label, node) {
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

// resolveSendAction: "S" applies to any SRP-eligible window (Queues, Streams,
// Addresses, Exchanges) on a selected top-level row.
func resolveSendAction(m *aiTUIModel, wi int) (string, func() (tea.Model, tea.Cmd), bool) {
	if wi < 0 || wi >= len(m.objTypes) || !sidebarWindowSupportsSRP(m.objTypes[wi].label) {
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

// resolveReceiveAction: "R" applies to a selected Subscription child (Topics
// window, Azure/Google only) or a top-level row on a purge/receive-eligible
// window (Queues/Streams — never Addresses/Exchanges).
func resolveReceiveAction(m *aiTUIModel, wi int) (string, func() (tea.Model, tea.Cmd), bool) {
	if wi < 0 || wi >= len(m.objTypes) {
		return "", nil, false
	}
	label := m.objTypes[wi].label

	if child, parentName, ok := m.selectedChildNode(); ok {
		if !sidebarSubscriptionEligible(label, child.Kind) || child.Name == "" {
			return "", nil, false
		}
		childName := child.Name
		return "receive", func() (tea.Model, tea.Cmd) {
			desc := fmt.Sprintf("▶ receive Subscription \"%s\"", childName)
			m.appendTranscript(histCmdStyle.Render(desc) + "\n")
			m.state = tuiExecuting
			topic, sub, session := parentName, childName, m.session
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
					Acknowledge: true,
					Timeout:     1,
					Wait:        false,
				})
				if err != nil {
					if isNoMessage(err) {
						return sideActionMsg{action: "   └ (no messages available)"}
					}
					return sideActionMsg{err: err}
				}
				return formatMessagePayloadForSideAction(msg)
			}
		}, true
	}

	if !sidebarWindowSupportsSRP(label) {
		return "", nil, false
	}
	node, ok := m.selectedTopLevelNode()
	if !ok || node.Name == "" {
		return "", nil, false
	}
	if !sidebarPurgeReceiveAllowed(label, node) {
		return "", nil, false
	}
	name := node.Name
	return "receive", func() (tea.Model, tea.Cmd) {
		desc := fmt.Sprintf("▶ receive %s \"%s\"", singular(label), name)
		m.appendTranscript(histCmdStyle.Render(desc) + "\n")
		m.state = tuiExecuting
		queue := name
		return *m, func() tea.Msg {
			qa, err := m.session.getQueueAdapter()
			if err != nil {
				return sideActionMsg{err: fmt.Errorf("adapter: %w", err)}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			msg, err := qa.Receive(ctx, backends.ReceiveOptions{
				Queue:       queue,
				Acknowledge: true,
				Timeout:     1,
				Wait:        false,
			})
			if err != nil {
				if isNoMessage(err) {
					return sideActionMsg{action: "   └ (no messages available)"}
				}
				return sideActionMsg{err: err}
			}
			return formatMessagePayloadForSideAction(msg)
		}
	}, true
}

// resolvePeekAction returns a resolve func for "p" (metadataOnly=false) or
// "m" (metadataOnly=true): peek applies to a selected Subscription child
// (Topics window, Azure/Google only) or a top-level row on Queues/Streams.
func resolvePeekAction(metadataOnly bool) func(m *aiTUIModel, wi int) (string, func() (tea.Model, tea.Cmd), bool) {
	hint := "peek"
	if metadataOnly {
		hint = "metadata"
	}
	verbosity := backends.VerbosityQuiet
	if metadataOnly {
		// Metadata peek must request full metadata from adapters, otherwise
		// some backends intentionally omit properties/internal metadata.
		verbosity = backends.VerbosityVerbose
	}

	return func(m *aiTUIModel, wi int) (string, func() (tea.Model, tea.Cmd), bool) {
		if wi < 0 || wi >= len(m.objTypes) {
			return "", nil, false
		}
		label := m.objTypes[wi].label

		if child, parentName, ok := m.selectedChildNode(); ok {
			if !sidebarSubscriptionEligible(label, child.Kind) || child.Name == "" {
				return "", nil, false
			}
			childName := child.Name
			return hint, func() (tea.Model, tea.Cmd) {
				desc := fmt.Sprintf("▶ peek Subscription \"%s\"", childName)
				if metadataOnly {
					desc = fmt.Sprintf("▶ peek metadata Subscription \"%s\"", childName)
				}
				m.appendTranscript(histCmdStyle.Render(desc) + "\n")
				m.state = tuiExecuting
				topic, sub, session, fmtMode := parentName, childName, m.session, m.metadataFormat
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
						Acknowledge: false,
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
					if metadataOnly {
						return formatMessageMetadataForSideAction(msg, fmtMode)
					}
					return formatMessagePayloadForSideAction(msg)
				}
			}, true
		}

		node, ok := m.selectedTopLevelNode()
		if !ok || node.Name == "" {
			return "", nil, false
		}
		switch label {
		case "Queues", "Streams":
		default:
			return "", nil, false
		}
		name := node.Name
		return hint, func() (tea.Model, tea.Cmd) {
			desc := fmt.Sprintf("▶ peek %s \"%s\"", singular(label), name)
			if metadataOnly {
				desc = fmt.Sprintf("▶ peek metadata %s \"%s\"", singular(label), name)
			}
			m.appendTranscript(histCmdStyle.Render(desc) + "\n")
			m.state = tuiExecuting
			queue, fmtMode := name, m.metadataFormat
			return *m, func() tea.Msg {
				qa, err := m.session.getQueueAdapter()
				if err != nil {
					return sideActionMsg{err: fmt.Errorf("adapter: %w", err)}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				msg, err := qa.Receive(ctx, backends.ReceiveOptions{
					Queue:       queue,
					Acknowledge: false,
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
				if metadataOnly {
					return formatMessageMetadataForSideAction(msg, fmtMode)
				}
				return formatMessagePayloadForSideAction(msg)
			}
		}, true
	}
}
