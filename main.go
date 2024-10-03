package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"strings"

	log "github.com/sirupsen/logrus"

	"l5proxy/server"
)

var (
	configFilePath  = ""
	defaultLogLevel = log.InfoLevel
)

func init() {
	flag.StringVar(&configFilePath, "c", "", "specify account file path")
}

// getVersion get version
func getVersion() string {
	return "0.1.0"
}

func toLogLevel(logLevel string) log.Level {
	ll := defaultLogLevel
	if len(logLevel) > 0 {
		switch strings.ToLower(logLevel) {
		case "debug":
			defaultLogLevel = log.DebugLevel
		case "info":
			defaultLogLevel = log.InfoLevel
		case "warn":
			defaultLogLevel = log.WarnLevel
		case "error":
			defaultLogLevel = log.ErrorLevel
		}
	}

	return ll
}

func main() {
	logLevel := os.Getenv("L5PROXY_LOG_LEVEL")

	log.SetLevel(toLogLevel(logLevel))

	version := flag.Bool("v", false, "show version")
	flag.Parse()

	if *version {
		fmt.Printf("%s\n", getVersion())
		os.Exit(0)
	}

	if configFilePath == "" {
		ePath := os.Getenv("L5PROXY_CONFIG_PATH")
		if len(ePath) > 0 {
			log.Infof("use env L5PROXY_CONFIG_PATH instead of commandline:%s", ePath)
			configFilePath = ePath
		} else {
			log.Panic("please use -c to specify account file path or use L5PROXY_CONFIG_PATH env")
		}
	}

	cfg, err := server.ParseConfig(configFilePath)
	if err != nil {
		log.Panicf("parse config file %s failed:%s", configFilePath, err)
	}

	if len(cfg.Server.LogLevel) > 0 {
		log.SetLevel(toLogLevel(cfg.Server.LogLevel))
	}

	if len(cfg.Server.Address) == 0 {
		cfg.Server.Address = ":8085"
	}

	if len(cfg.Server.WebsocketPath) == 0 {
		cfg.Server.WebsocketPath = "/l5proxy"
	}

	log.Infof("try to start  l5proxy server, version:%s", getVersion())

	// start http server
	go server.CreateHTTPServer(cfg)
	log.Info("start l5proxy server ok!")

	if cfg.Server.Daemon {
		waitForSignal()
	} else {
		waitInput()
	}

}

func waitInput() {
	var cmd string
	for {
		_, err := fmt.Scanf("%s\n", &cmd)
		if err != nil {
			//log.Infof("Scanf err:%s", err)
			continue
		}

		switch cmd {
		case "exit", "quit":
			log.Infof("exit by user")
			return
		case "gr":
			log.Infof("current goroutine count:%d", runtime.NumGoroutine())

		case "gd":
			pprof.Lookup("goroutine").WriteTo(os.Stdout, 1)

		default:

		}
	}
}

func dumpGoRoutinesInfo() {
	log.Infof("current goroutine count:%d", runtime.NumGoroutine())
	// use DEBUG=2, to dump stack like golang dying due to an unrecovered panic.
	pprof.Lookup("goroutine").WriteTo(os.Stdout, 2)
}
