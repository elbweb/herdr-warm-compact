// herdr-warm-compact: a herdr plugin that compacts idle Claude Code sessions before their prompt cache
// expires. With no arguments (herdr's startup) it re-launches itself detached as `run` and exits.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/elbweb/herdr-warm-compact/internal/herdr"
	"github.com/elbweb/herdr-warm-compact/internal/model"
	"github.com/elbweb/herdr-warm-compact/internal/panel"
	"github.com/elbweb/herdr-warm-compact/internal/run"
	"github.com/elbweb/herdr-warm-compact/internal/store"
)

var errNotRunning = errors.New("Warm Compact is not running; nothing was changed")

func main() {
	if err := dispatch(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "herdr-warm-compact:", err)
		os.Exit(1)
	}
}

func dispatch(args []string) error {
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "":
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return run.Detach(exe, "run")
	case "run":
		env, err := run.EnvFromOS()
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return run.Resident(ctx, env)
	case "restart":
		env, err := run.EnvFromOS()
		if err != nil {
			return err
		}
		if err := run.Stop(env.ConfigDir); err != nil {
			return err
		}
		return run.Detach(env.Exe, "run")
	case "stop":
		// Asks the resident to quit (never a kill); run before disabling or uninstalling the plugin.
		dir, err := run.ConfigDir()
		if err != nil {
			return err
		}
		if err := run.Stop(dir); err != nil {
			return err
		}
		fmt.Println("Warm Compact stopped")
		return nil
	case "set":
		if len(args) != 2 {
			return fmt.Errorf("usage: herdr-warm-compact set <default|auto|on|off>")
		}
		if _, err := model.ParseSetting(args[1]); err != nil {
			return err
		}
		pane := os.Getenv("HERDR_PANE_ID")
		if pane == "" {
			return fmt.Errorf("HERDR_PANE_ID is not set: run this from inside a herdr pane")
		}
		dir, err := run.ConfigDir()
		if err != nil {
			return err
		}
		if !run.Running(dir) {
			return errNotRunning
		}
		if err := store.WriteRequest(store.RequestsDir(dir), store.Request{Kind: "set", Pane: pane, Value: args[1]}); err != nil {
			return err
		}
		fmt.Printf("requested: this session's compaction setting → %s (check the panel or sidebar)\n", args[1])
		return nil
	case "toggle":
		var c struct {
			Pane string `json:"focused_pane_id"`
		}
		json.Unmarshal([]byte(os.Getenv("HERDR_PLUGIN_CONTEXT_JSON")), &c)
		if c.Pane == "" {
			return fmt.Errorf("no focused pane in HERDR_PLUGIN_CONTEXT_JSON")
		}
		dir, err := run.ConfigDir()
		if err != nil {
			return err
		}
		if !run.Running(dir) {
			return errNotRunning
		}
		return store.WriteRequest(store.RequestsDir(dir), store.Request{Kind: "toggle", Pane: c.Pane})
	case "open-panel":
		return herdr.New(os.Getenv("HERDR_SOCKET_PATH")).OpenPanel(context.Background())
	case "panel":
		dir, err := run.ConfigDir()
		if err != nil {
			return err
		}
		return panel.Run(dir)
	}
	return fmt.Errorf("unknown command %q (want run, restart, stop, set, toggle, open-panel or panel)", cmd)
}
