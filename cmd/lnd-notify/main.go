package main

import (
	"flag"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/Primexz/lndnotify/internal/config"
	"github.com/Primexz/lndnotify/internal/events"
	"github.com/Primexz/lndnotify/internal/lnd"
	"github.com/Primexz/lndnotify/internal/notify"
	log "github.com/sirupsen/logrus"
	prefixed "github.com/x-cray/logrus-prefixed-formatter"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func init() {
	log.SetFormatter(&prefixed.TextFormatter{
		TimestampFormat:  "2006/01/02 - 15:04:05",
		FullTimestamp:    true,
		QuoteEmptyFields: true,
		SpacePadding:     45,
	})

	log.SetReportCaller(true)

	log.SetLevel(log.DebugLevel)
}

func main() {
	log.WithFields(log.Fields{
		"commit":  commit,
		"runtime": runtime.Version(),
		"arch":    runtime.GOARCH,
	}).Infof("starting lndnotify ⚡️ %s", version)

	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	// Load configuration
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		// Try environment variables if config file fails
		cfg, err = config.LoadConfigFromEnv()
		if err != nil {
			log.Fatalf("Failed to load configuration: %v", err)
		}
	}

	// Create LND client
	lndClient := lnd.NewClient(&lnd.ClientConfig{
		Host:         cfg.LND.Host,
		Port:         cfg.LND.Port,
		TLSCertPath:  cfg.LND.TLSCertPath,
		MacaroonPath: cfg.LND.MacaroonPath,
	})

	// Connect to LND
	if err := lndClient.Connect(); err != nil {
		log.Fatalf("Failed to connect to LND: %v", err)
	}
	defer lndClient.Disconnect()

	// Create notification manager
	notifier := notify.NewManager(&notify.ManagerConfig{
		Providers: cfg.Notifications.Providers,
		Templates: cfg.Notifications.Templates,
		RateLimit: cfg.RateLimit,
	})

	// Create event processor
	processor := events.NewProcessor(&events.ProcessorConfig{
		EnabledEvents: cfg.Events,
		RateLimit:     cfg.RateLimit,
	})

	// Subscribe to events
	eventChan, err := lndClient.SubscribeEvents()
	if err != nil {
		log.Fatalf("Failed to subscribe to events: %v", err)
	}

	// Start event processing
	if err := processor.Start(); err != nil {
		log.Fatalf("Failed to start event processor: %v", err)
	}

	// Handle shutdown gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Info("started lndnotify")

	// Main event loop
	for {
		select {
		case event := <-eventChan:
			if err := processor.ProcessEvent(event); err != nil {
				log.Printf("Error processing event: %v", err)
				continue
			}

			msg, err := notifier.RenderTemplate(event.Type(), event.GetTemplateData())
			if err != nil {
				log.Printf("Error rendering template: %v", err)
				continue
			}
			notifier.Send(msg)

		case <-sigChan:
			log.Info("received shutdown signal")
			processor.Stop()
			if err := lndClient.Disconnect(); err != nil {
				log.Printf("Error disconnecting from LND: %v", err)
			}

			log.Info("shutdown complete")
			return
		}
	}
}
