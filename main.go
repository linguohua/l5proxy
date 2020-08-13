package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"

	log "github.com/sirupsen/logrus"

	"lproxy-go/server"
)

var (
	listenAddr = ""
	listenPort = ""
	wsPath     = ""
	daemon     = ""
)

const (
	defaultListenAddr = "127.0.0.1"
	defaultPath       = "/tun"
	defaultListenPort = "8020"
)

func init() {
	flag.StringVar(&listenPort, "lp", defaultListenPort, "specify the listen port")
	flag.StringVar(&listenAddr, "l", defaultListenAddr, "specify the listen address")
	flag.StringVar(&wsPath, "p", defaultPath, "specify websocket path")
	flag.StringVar(&daemon, "d", "yes", "specify daemon mode")
}

// getVersion get version
func getVersion() string {
	return "0.1.0"
}

func main() {
	// only one thread
	runtime.GOMAXPROCS(1)

	version := flag.Bool("v", false, "show version")

	flag.Parse()

	if *version {
		fmt.Printf("%s\n", getVersion())
		os.Exit(0)
	}

	if listenPort == defaultListenPort {
		eListenPort := os.Getenv("PORT")
		if len(eListenPort) > 0 {
			log.Println("use env PORT instead of commandline:", eListenPort)
			listenPort = eListenPort
		}
	}

	if listenAddr == defaultListenAddr {
		eListenAddr := os.Getenv("LPROXY_GO_LADDR")
		if len(eListenAddr) > 0 {
			log.Println("use env LPROXY_GO_LADDR instead of commandline:", eListenAddr)
			listenAddr = eListenAddr
		}
	}

	if wsPath == defaultPath {
		ePath := os.Getenv("LPROXY_GO_PATH")
		if len(ePath) > 0 {
			log.Println("use env LPROXY_GO_PATH instead of commandline:", ePath)
			wsPath = ePath
		}
	}

	log.Println("try to start  linproxy server, version:", getVersion())

	// start http server
	go server.CreateHTTPServer(listenAddr+":"+listenPort, wsPath)
	log.Println("start linproxy server ok!")

	if daemon == "yes" {
		waitForSignal()
	} else {
		waitInput()
	}
	return
}

func waitInput() {
	var cmd string
	for {
		_, err := fmt.Scanf("%s\n", &cmd)
		if err != nil {
			//log.Println("Scanf err:", err)
			continue
		}

		switch cmd {
		case "exit", "quit":
			log.Println("exit by user")
			return
		case "gr":
			log.Println("current goroutine count:", runtime.NumGoroutine())
			break
		case "gd":
			pprof.Lookup("goroutine").WriteTo(os.Stdout, 1)
			break
		default:
			break
		}
	}
}

func dumpGoRoutinesInfo() {
	log.Println("current goroutine count:", runtime.NumGoroutine())
	// use DEBUG=2, to dump stack like golang dying due to an unrecovered panic.
	pprof.Lookup("goroutine").WriteTo(os.Stdout, 2)
}
