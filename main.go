//go:build linux && amd64

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"vibemonitor/internal/agent"
	"vibemonitor/internal/server"
)

const Version = "1.0.0-lite"

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func printHelp() {
	fmt.Printf(`VibeMonitor (%s) - A safe, ultra-lightweight server monitoring solution.

Usage:
  vibemonitor [command] [options]

Commands:
  server    Start the monitoring master server (default)
  agent     Start the lightweight probe agent
  version   Show version information
  help      Show this help message

Server Options:
  --listen, -l            Address to listen on (default: 0.0.0.0:1314, env: VIBEMONITOR_LISTEN)
  --data, -d              Path to data storage file (default: vibemonitor-data.json, env: VIBEMONITOR_DATA)
  --admin-password, -p    Initial admin password (first run only, env: VIBEMONITOR_ADMIN_PASSWORD)

Agent Options:
  --server, -s            VibeMonitor server URL (required, e.g. http://127.0.0.1:1314, env: VIBEMONITOR_SERVER)
  --token, -t             Client communication token (required, env: VIBEMONITOR_TOKEN)
  --interval, -i          Metrics reporting interval (default: 3s, env: VIBEMONITOR_INTERVAL)

Examples:
  # Start server
  vibemonitor server --listen 0.0.0.0:1314

  # Start agent probe
  vibemonitor agent --server http://1.2.3.4:1314 --token YOUR_TOKEN
`, Version)
}

func runServer(args []string) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	listen := fs.String("listen", getEnv("VIBEMONITOR_LISTEN", "0.0.0.0:1314"), "Address to listen on")
	fs.StringVar(listen, "l", *listen, "Address to listen on (shorthand)")
	data := fs.String("data", getEnv("VIBEMONITOR_DATA", "vibemonitor-data.json"), "Path to data file")
	fs.StringVar(data, "d", *data, "Path to data file (shorthand)")
	adminPass := fs.String("admin-password", getEnv("VIBEMONITOR_ADMIN_PASSWORD", ""), "Initial admin password (first run only)")
	fs.StringVar(adminPass, "p", *adminPass, "Admin password (shorthand)")

	_ = fs.Parse(args)

	srv, err := server.New(server.Options{
		ListenAddr:    *listen,
		DataFile:      *data,
		AdminPassword: *adminPass,
	})
	if err != nil {
		log.Fatalf("Failed to initialize server: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := srv.Run(ctx); err != nil {
		log.Fatalf("Server stopped with error: %v", err)
	}
}

func runAgent(args []string) {
	fs := flag.NewFlagSet("agent", flag.ExitOnError)
	srvURL := fs.String("server", getEnv("VIBEMONITOR_SERVER", ""), "Server URL")
	fs.StringVar(srvURL, "s", *srvURL, "Server URL (shorthand)")
	token := fs.String("token", getEnv("VIBEMONITOR_TOKEN", ""), "Agent token")
	fs.StringVar(token, "t", *token, "Agent token (shorthand)")
	intervalStr := fs.String("interval", getEnv("VIBEMONITOR_INTERVAL", "3s"), "Report interval")
	fs.StringVar(intervalStr, "i", *intervalStr, "Report interval (shorthand)")

	_ = fs.Parse(args)

	if *srvURL == "" || *token == "" {
		fmt.Println("[-] Error: --server and --token are required to run in agent mode.")
		fs.Usage()
		os.Exit(1)
	}

	interval, err := time.ParseDuration(*intervalStr)
	if err != nil || interval <= 0 {
		interval = 3 * time.Second
	}

	client := agent.New(agent.Options{
		ServerURL: *srvURL,
		Token:     *token,
		Interval:  interval,
	})

	if err := client.Run(context.Background()); err != nil {
		log.Fatalf("Agent stopped with error: %v", err)
	}
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		runServer(nil)
		return
	}

	cmd := args[0]
	if strings.HasPrefix(cmd, "-") {
		// e.g. vibemonitor --listen ...
		runServer(args)
		return
	}

	switch cmd {
	case "server":
		runServer(args[1:])
	case "agent":
		runAgent(args[1:])
	case "version", "-v", "--version":
		fmt.Printf("VibeMonitor version %s\n", Version)
	case "help", "-h", "--help":
		printHelp()
	default:
		fmt.Printf("Unknown command: %s\n\n", cmd)
		printHelp()
		os.Exit(1)
	}
}
