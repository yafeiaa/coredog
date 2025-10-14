package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/DomineCore/coredog/internal/agent"
	"github.com/DomineCore/coredog/internal/webhook"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func main() {
	root := cobra.Command{}

	watcherBootstrap := cobra.Command{
		Use: "watcher",
		RunE: func(cmd *cobra.Command, args []string) error {
			agent.Run()
			return nil
		},
		Long: "start a watcher agent on host to watch corefile created.",
	}

	webhookBootstrap := cobra.Command{
		Use: "webhook",
		RunE: func(cmd *cobra.Command, args []string) error {
			certFile := os.Getenv("WEBHOOK_CERT_FILE")
			if certFile == "" {
				certFile = "/etc/webhook/certs/tls.crt"
			}
			keyFile := os.Getenv("WEBHOOK_KEY_FILE")
			if keyFile == "" {
				keyFile = "/etc/webhook/certs/tls.key"
			}
			port := 8443

			server := webhook.NewServer(certFile, keyFile, port)

			// Handle graceful shutdown
			stop := make(chan os.Signal, 1)
			signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

			go func() {
				if err := server.Start(); err != nil {
					logrus.Fatalf("Webhook server failed: %v", err)
				}
			}()

			<-stop
			logrus.Info("Received shutdown signal")

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			return server.Shutdown(ctx)
		},
		Long: "start a mutating admission webhook server to inject corefile volume.",
	}

	root.AddCommand(&watcherBootstrap)
	root.AddCommand(&webhookBootstrap)
	root.Execute()
}
