package config

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

func init() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP)

	go func() {
		for _ = range c {
			if err := updateConfig(); err != nil {
				log.Printf("Failed to reload config: %s", err)
			} else {
				log.Print("Successfully reloaded config")
			}
			if err := updateChannels(); err != nil {
				log.Printf("Failed to reload channels: %s", err)
			} else {
				log.Print("Successfully reloaded channels")
			}
		}
	}()
}
