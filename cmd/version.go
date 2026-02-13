package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// version is set at build time via ldflags:
//
//	go build -ldflags="-X 'github.com/makibytes/xmc/cmd.version=1.0.0'"
var version = "dev"

// NewVersionCommand creates a version subcommand
func NewVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version number",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(version)
		},
	}
}
