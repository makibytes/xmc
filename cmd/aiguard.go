package cmd

import (
	"fmt"
	"runtime/debug"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/makibytes/xmc/log"
)

// guard wraps the body of a background tea.Cmd so that a panic in it — a broker
// client library, a provider response nobody anticipated — becomes the Cmd's
// own "done" message carrying an error (fail builds it), instead of killing the
// whole program or, worse, leaving the UI waiting for a result that never comes.
// Every Cmd the AI shell starts for work that ends a UI state goes through it.
func guard(fail func(error) tea.Msg, fn func() tea.Msg) tea.Cmd {
	return func() (msg tea.Msg) {
		defer func() {
			if r := recover(); r != nil {
				log.Verbose("panic in background command: %v\n%s", r, debug.Stack())
				msg = fail(fmt.Errorf("internal error: %v", r))
			}
		}()
		return fn()
	}
}
