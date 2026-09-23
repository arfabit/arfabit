// Command arfabit watches a disc drive and turns what you put in it into
// something your Apple TV plays.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/arfabit/arfabit/internal/config"
	"github.com/arfabit/arfabit/internal/disc/makemkv"
	"github.com/arfabit/arfabit/internal/doctor"
	"github.com/arfabit/arfabit/internal/pipeline"
	"github.com/arfabit/arfabit/internal/store"
	"github.com/arfabit/arfabit/internal/web"
)

func main() {
	var (
		configPath = flag.String("config", config.DefaultLocalPath(), "settings file for this computer")
		addr       = flag.String("addr", "", "address to serve on, overriding the settings file")
		noOpen     = flag.Bool("no-open", false, "do not open the browser")
		checkOnly  = flag.Bool("check", false, "run the checks and exit")
	)
	flag.Parse()

	if err := run(*configPath, *addr, *noOpen, *checkOnly); err != nil {
		fmt.Fprintf(os.Stderr, "\nARFABIT could not start.\n%v\n\n", err)
		os.Exit(1)
	}
}

func run(configPath, addr string, noOpen, checkOnly bool) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if addr != "" {
		cfg.Server.Addr = addr
	}

	if checkOnly {
		return printChecks(cfg)
	}

	st, err := store.New(cfg.Paths.Data, cfg.Node.ID)
	if err != nil {
		return err
	}

	calibration, err := loadCalibration(st, cfg.Node.ID)
	if err != nil {
		return err
	}

	backend := &makemkv.Backend{MinLength: cfg.Profile.MinTitleLength}

	runner := &pipeline.Runner{
		Config:      cfg,
		Store:       st,
		Backend:     backend,
		Calibration: calibration,
	}

	server, err := web.New(cfg, st, runner, backend)
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", cfg.Server.Addr)
	if err != nil {
		return fmt.Errorf("ARFABIT could not listen on %s: %w", cfg.Server.Addr, err)
	}

	url := friendlyURL(listener.Addr())
	fmt.Printf("\nARFABIT is running.\n\n  Open %s\n\n", url)
	fmt.Printf("  Movies go in   %s\n", cfg.Paths.Library)
	fmt.Printf("  Disc copies in %s\n\n", cfg.Paths.Masters)
	fmt.Printf("Press Ctrl+C to stop.\n\n")

	if !noOpen {
		openBrowser(url)
	}

	httpServer := &http.Server{
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Ctrl+C stops the web server but never interrupts a rip mid-write: the
	// shutdown waits for in-flight requests, and the job record on disk is
	// always complete because every write is atomic.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errs := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}

	fmt.Println("\nStopping.")
	saveCalibration(st, cfg.Node.ID, calibration)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}

// printChecks runs Doctor on the command line, for anyone who would rather not
// open a browser to find out what is missing.
func printChecks(cfg config.Config) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	report := doctor.Run(ctx, cfg)
	fmt.Println()
	for _, c := range report.Checks {
		mark := map[doctor.Status]string{
			doctor.StatusOK:      "  ok ",
			doctor.StatusWarn:    "  ·  ",
			doctor.StatusMissing: "  →  ",
		}[c.Status]

		fmt.Printf("%s %s\n", mark, c.Message)
		if c.Fix != "" {
			fmt.Printf("       %s\n", c.Fix)
		}
		if c.Command != "" {
			fmt.Printf("       %s\n", c.Command)
		}
	}
	fmt.Println()

	if !report.Ready() {
		return errors.New("something needs installing first")
	}
	return nil
}

// calibrationPath is where this node keeps what it has learned about its own
// speed.
func calibrationPath(st *store.Store, nodeID string) string {
	return filepath.Join(st.Root, "nodes", nodeID, "calibration.json")
}

func loadCalibration(st *store.Store, nodeID string) (*pipeline.Calibration, error) {
	data, err := os.ReadFile(calibrationPath(st, nodeID))
	if os.IsNotExist(err) {
		return pipeline.NewCalibration(), nil
	}
	if err != nil {
		return nil, err
	}

	c := pipeline.NewCalibration()
	if err := json.Unmarshal(data, c); err != nil {
		// Calibration is only ever an optimisation, so a damaged file starts
		// over rather than stopping ARFABIT from running.
		return pipeline.NewCalibration(), nil
	}
	if c.Drives == nil {
		c.Drives = map[string]*pipeline.DriveStats{}
	}
	if c.Encode == nil {
		c.Encode = map[string]*pipeline.EncodeStats{}
	}
	return c, nil
}

func saveCalibration(st *store.Store, nodeID string, c *pipeline.Calibration) {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(calibrationPath(st, nodeID), append(data, '\n'), 0o644)
}

// friendlyURL turns a listen address into one a person can type.
func friendlyURL(addr net.Addr) string {
	host, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return "http://localhost:7847"
	}
	// Listening on every interface prints as :: or 0.0.0.0, which nobody can
	// type into a browser.
	if host == "" || host == "::" || host == "0.0.0.0" {
		host = "localhost"
	}
	return fmt.Sprintf("http://%s:%s", host, port)
}

// openBrowser opens the page, and says nothing if it cannot.
func openBrowser(url string) {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		cmd = "xdg-open"
	}

	_ = exec.Command(cmd, append(args, url)...).Start()
}
