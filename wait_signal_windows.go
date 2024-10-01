package main

import (
	"os"
	"os/signal"

	log "github.com/sirupsen/logrus"
)

func waitForSignal() {
	for {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, os.Kill)

		// Block until a signal is received.
		s := <-c
		log.Infof("Got signal:%s", s.String())

		if s == os.Kill {
			// how can one send a 'kill' signal to process on windows?
			dumpGoRoutinesInfo()
		}

		break
	}
}
