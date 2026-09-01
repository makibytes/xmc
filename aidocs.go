// Command xmc is the unified message-broker CLI; this file embeds the
// broker-specific AI system-prompt reference docs from docs/*.md.
package main

import (
	"embed"

	"github.com/makibytes/xmc/broker"
)

//go:embed docs/[a-z]*.md
var aiDocsFS embed.FS

func init() {
	broker.RegisterAIDocs(aiDocsFS)
}
