package main

import (
	"embed"

	"github.com/makibytes/xmc/broker"
)

//go:embed docs/*.md
var aiDocsFS embed.FS

func init() {
	broker.RegisterAIDocs(aiDocsFS)
}
