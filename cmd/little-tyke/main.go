package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	log "github.com/sirupsen/logrus"

	"github.com/Blink-Build-Studios/little-tyke/cmd/little-tyke/cmd"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := cmd.RootCmd.ExecuteContext(ctx); err != nil {
		log.WithError(err).Fatal("fatal error")
		os.Exit(1)
	}
}
