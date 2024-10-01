package main

import (
	"os"
	"os/signal"
	"syscall"

	log "github.com/sirupsen/logrus"
)

func waitForSignal() {
	for {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGUSR1, syscall.SIGUSR2)

		// Block until a signal is received.
		s := <-c
		log.Infof("Got signal:%s", s.String())

		if s == syscall.SIGUSR1 {
			dumpGoRoutinesInfo()
			continue
		}

		if s == syscall.SIGUSR2 {
			// servercfg.ReLoadConfigFile()
			continue
		}

		break
	}
}
