// ollama-manager: small web UI to manage local Ollama models.
//
// Usage:
//
//	ollama-manager                       # serve using ./config.json
//	ollama-manager -config /path/cfg.json
//	ollama-manager set-password <pwd>    # hash and store a new password
//	ollama-manager version
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gense/ollama-manager/internal/config"
	"github.com/gense/ollama-manager/internal/ollama"
	"github.com/gense/ollama-manager/internal/server"
	"golang.org/x/crypto/bcrypt"
)

//go:embed all:web
var webFS embed.FS

const version = "0.1.0"

func main() {
	log.SetFlags(log.LstdFlags)
	log.SetPrefix("[ollama-manager] ")

	configPath := flag.String("config", "config.json", "path to config.json")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "ollama-manager %s\n\n", version)
		fmt.Fprintf(os.Stderr, "usage:\n")
		fmt.Fprintf(os.Stderr, "  ollama-manager [-config path]\n")
		fmt.Fprintf(os.Stderr, "  ollama-manager set-password <password>\n")
		fmt.Fprintf(os.Stderr, "  ollama-manager clear-password\n")
		fmt.Fprintf(os.Stderr, "  ollama-manager version\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	switch flag.Arg(0) {
	case "version":
		fmt.Println(version)
		return
	case "set-password":
		if flag.NArg() < 2 {
			fmt.Fprintln(os.Stderr, "set-password requires a password argument")
			os.Exit(2)
		}
		runSetPassword(*configPath, flag.Arg(1))
		return
	case "clear-password":
		runClearPassword(*configPath)
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	if cfg.ExposeNetwork && !cfg.HasPassword() {
		log.Println("WARNING: expose_network=true with no password set. Anyone on your LAN can manage Ollama.")
		log.Println("         Run 'ollama-manager set-password <password>' to secure access.")
	}

	subFS, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("embed: %v", err)
	}

	client := ollama.New(cfg.OllamaURL)
	srv, err := server.New(cfg, client, subFS)
	if err != nil {
		log.Fatalf("server: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := cfg.BindAddress()
	log.Printf("listening on http://%s  (ollama: %s)", addr, cfg.OllamaURL)
	if err := srv.ListenAndServe(ctx); err != nil {
		log.Fatalf("server: %v", err)
	}
	log.Println("bye")
}

func runSetPassword(path, pwd string) {
	cfg, err := config.Load(path)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pwd), bcrypt.DefaultCost)
	if err != nil {
		log.Fatalf("bcrypt: %v", err)
	}
	cfg.PasswordHash = string(hash)
	if err := cfg.Save(); err != nil {
		log.Fatalf("save: %v", err)
	}
	fmt.Printf("password updated in %s\n", cfg.Path())
}

func runClearPassword(path string) {
	cfg, err := config.Load(path)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cfg.PasswordHash = ""
	if err := cfg.Save(); err != nil {
		log.Fatalf("save: %v", err)
	}
	fmt.Printf("password cleared in %s\n", cfg.Path())
}
