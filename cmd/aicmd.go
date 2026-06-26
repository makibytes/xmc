package cmd

import (
	"fmt"
	"os"

	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

// NewAICommand creates the standalone AI mode command. It builds its own
// shellSession and aiSession, then runs the Bubble Tea TUI directly —
// no readline or backtab machinery needed.
func NewAICommand(spec BrokerSpec) *cobra.Command {
	return &cobra.Command{
		Use:   "ai",
		Short: "Start the AI assistant (natural-language → xmc commands)",
		Long: `Opens a full-screen AI assistant that translates natural language into
xmc commands. Describe what you want and the AI will propose the command
for you to review, edit, or run.

The sidebar shows live broker objects (queues, topics, exchanges, …).
Press Tab to browse them, Enter to insert a name into your prompt.

Commands executed in AI mode are written to the shared shell history.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAI(cmd, spec)
		},
	}
}

func runAI(cmd *cobra.Command, spec BrokerSpec) error {
	if err := ensureXMCDir(); err != nil {
		log.Error("warning: %s\n", err)
	}

	baseName := binBaseName()

	rootCmd := cmd.Root()

	// Load config early to set auto-update and alias preferences.
	cfg, cfgErr := loadConfig()
	if cfgErr != nil {
		log.Error("warning: %s\n", cfgErr)
		cfg = &xmcConfig{}
	}

	session := &shellSession{
		spec:         spec,
		queueFactory: wrapReconnectQueue(spec.Queue, ReconnectOptions{}),
		topicFactory: wrapReconnectTopic(spec.Topic, ReconnectOptions{}),
		aliases:      cfg.Aliases,
	}
	defer session.close()

	period, intervalOn := cfg.AI.refreshIntervalDuration()
	ai := &aiSession{
		session:            session,
		rootCmd:            rootCmd,
		brokerContext:      spec.AIContext,
		aliases:            cfg.Aliases,
		autoUpdateObjects:  cfg.AI.autoUpdateObjectsEnabled(),
		autoUpdateMessages: cfg.AI.autoUpdateMessagesEnabled(),
		refreshPeriod:      period,
		refreshEnabled:     intervalOn && (cfg.AI.autoUpdateObjectsEnabled() || cfg.AI.autoUpdateMessagesEnabled()),
	}

	var server string
	if f := rootCmd.PersistentFlags().Lookup("server"); f != nil {
		server = f.Value.String()
	}

	_, totalIn, totalOut, err := runAITUI(ai, session, rootCmd, baseName, server)
	if totalIn > 0 || totalOut > 0 {
		fmt.Fprintf(os.Stderr, "tokens: %s in, %s out\n", fmtTokens(totalIn), fmtTokens(totalOut))
	}
	return err
}
