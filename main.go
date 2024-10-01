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
	listenAddr      = ""
	listenPort      = ""
	wsPath          = ""
	daemon          = ""
	accountFilePath = ""
	defaultLogLevel = log.InfoLevel
)

const (
	defaultListenAddr = ""
	defaultPath       = "/l5proxy"
	defaultListenPort = "8050"
)

func init() {
	flag.StringVar(&listenPort, "lp", defaultListenPort, "specify the listen port")
	flag.StringVar(&listenAddr, "l", defaultListenAddr, "specify the listen address")
	flag.StringVar(&accountFilePath, "c", "", "specify account file path")
	flag.StringVar(&wsPath, "p", defaultPath, "specify websocket path")
	flag.StringVar(&daemon, "d", "yes", "specify daemon mode")
}

// getVersion get version
func getVersion() string {
	return "0.1.0"
}

func main() {
	logLevel := os.Getenv("L5PROXY_LOG_LEVEL")
	if len(logLevel) > 0 {
		log.Infof("use env L5PROXY_LOG_LEVEL:%s", logLevel)
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
	log.SetLevel(defaultLogLevel)

	version := flag.Bool("v", false, "show version")
	flag.Parse()

	if *version {
		fmt.Printf("%s\n", getVersion())
		os.Exit(0)
	}

	if listenPort == defaultListenPort {
		eListenPort := os.Getenv("PORT")
		if len(eListenPort) > 0 {
			log.Infof("use env PORT instead of commandline:%s", eListenPort)
			listenPort = eListenPort
		}
	}

	if listenAddr == defaultListenAddr {
		eListenAddr := os.Getenv("L5PROXY_GO_LADDR")
		if len(eListenAddr) > 0 {
			log.Infof("use env L5PROXY_GO_LADDR instead of commandline:%s", eListenAddr)
			listenAddr = eListenAddr
		}
	}

	if wsPath == defaultPath {
		ePath := os.Getenv("L5PROXY_GO_PATH")
		if len(ePath) > 0 {
			log.Infof("use env L5PROXY_GO_PATH instead of commandline:%s", ePath)
			wsPath = ePath
		}
	}

	if accountFilePath == "" {
		ePath := os.Getenv("L5PROXY_GO_ACCF_PATH")
		if len(ePath) > 0 {
			log.Infof("use env L5PROXY_GO_ACCF_PATH instead of commandline:%s", ePath)
			accountFilePath = ePath
		} else {
			log.Panic("please use -c to specify account file path of L5PROXY_GO_ACCF_PATH env")
		}
	}

	log.Infof("try to start  linproxy server, version:%s", getVersion())

	// start http server
	go server.CreateHTTPServer(listenAddr+":"+listenPort, wsPath, accountFilePath)
	log.Info("start linproxy server ok!")

	if daemon == "yes" {
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
