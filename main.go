package main

import (
	"os"

	"github.com/makibytes/amc/broker"
	"github.com/makibytes/amc/log"
)

func main() {
	rootCmd := broker.GetRootCommand()
	if rootCmd == nil {
		log.Error("No broker loaded - make sure to build with a broker tag (artemis, ibmmq, kafka, mqtt, rabbitmq)")
		os.Exit(1)
	}

	if err := rootCmd.Execute(); err != nil {
		log.Error("%s", err)
		os.Exit(1)
	}
}
