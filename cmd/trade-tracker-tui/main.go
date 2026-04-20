package main

import (
	"fmt"
	"os"

	"github.com/alecthomas/kong"
	tea "github.com/charmbracelet/bubbletea"

	"trade-tracker-go/internal/tui"
	"trade-tracker-go/internal/tui/client"
)

var cli struct {
	Addr string `help:"gRPC server address (plaintext, no TLS — use localhost or a trusted network)." default:"apollo:10000" env:"TRADE_TRACKER_ADDR"`
}

func main() {
	kong.Parse(&cli)
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	c, err := client.New(cli.Addr)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", cli.Addr, err)
	}
	defer func() { _ = c.Close() }()

	p := tea.NewProgram(tui.New(c, cli.Addr), tea.WithAltScreen())
	_, err = p.Run()
	return err
}
