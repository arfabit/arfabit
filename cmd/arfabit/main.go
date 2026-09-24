// Command arfabit watches a disc drive and turns what you put in it into
// something your Apple TV plays.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/arfabit/arfabit/internal/config"
	"github.com/arfabit/arfabit/internal/disc/makemkv"
	"github.com/arfabit/arfabit/internal/doctor"
	"github.com/arfabit/arfabit/internal/meta"
	"github.com/arfabit/arfabit/internal/pipeline"
	"github.com/arfabit/arfabit/internal/profiles"
	"github.com/arfabit/arfabit/internal/restart"
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
		fmt.Fprintf(os.Stderr, "\n%v\n\n", err)
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

	// Create the folders now, while ARFABIT is visibly starting.
	//
	// On macOS this is what prompts for access to the Downloads folder, and
	// that question makes sense here. Left until later it arrives minutes
	// afterwards, next to whatever the person happened to be doing, looking
	// like a consequence of it.
	prepareFolders(cfg)

	st, err := store.New(cfg.Paths.Data, cfg.Node.ID)
	if err != nil {
		return err
	}

	calibration, err := loadCalibration(st, cfg.Node.ID)
	if err != nil {
		return err
	}

	backend := &makemkv.Backend{
		MinLength: cfg.Profile.MinTitleLength,
		CacheMB:   cfg.Profile.ReadCacheMB,
	}

	// A missing film list is not a problem: without it a disc's own name is
	// used, which on many discs is already right.
	index, err := meta.LoadIndex(cfg.Paths.Data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "The film list could not be read, so disc names will be used: %v\n", err)
	}

	runner := &pipeline.Runner{
		Config:      cfg,
		Store:       st,
		Backend:     backend,
		Calibration: calibration,
		Index:       index,
		Slots:       pipeline.NewSlots(cfg.Profile.MaxConversions),
	}

	// Anything left running by a restart is settled before the page opens, so
	// nobody sees a job that claims to be working when nothing is.
	if interrupted, err := runner.Reconcile(); err == nil && len(interrupted) > 0 {
		fmt.Printf("\n%d job%s %s interrupted when ARFABIT last stopped.\n",
			len(interrupted),
			map[bool]string{true: "", false: "s"}[len(interrupted) == 1],
			map[bool]string{true: "was", false: "were"}[len(interrupted) == 1])
	}

	// Profiles made here, as opposed to written in the settings file.
	profileStore, err := profiles.Open(cfg.Paths.Data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "The saved profiles could not be read, so only the ones in your settings file are available: %v\n", err)
	}

	server, err := web.New(cfg, st, runner, backend)
	if err != nil {
		return err
	}
	server.Profiles = profileStore

	// A disc gets whichever profile is currently the default, which can be
	// changed on the page while ARFABIT runs.
	runner.DefaultProfile = func() config.Profile { return profileStore.DefaultProfile(cfg) }

	// Stopping from the page matters for anyone whose computer starts ARFABIT
	// on its own: they have no terminal to press Ctrl+C in.
	// One shutdown, one announcement. The reason is carried to the single
	// place that prints it, rather than announced here and again below.
	quit := make(chan struct{})
	var quitReason atomic.Pointer[string]

	server.Quit = func(reason string) {
		quitReason.CompareAndSwap(nil, &reason)
		close(quit)
	}

	// Restarting is how most small problems get cleared, so the page offers
	// it. Everything is put away first, exactly as Ctrl+C would.
	server.Restart = func() error {
		fmt.Println("\nRestarting.")
		saveCalibration(st, cfg.Node.ID, calibration)
		server.Close()
		return restart.Exec()
	}

	listener, err := net.Listen("tcp", cfg.Server.Addr)
	if err != nil {
		// Another copy already has the port. Ask it to stand down and take
		// over: somebody who has just started ARFABIT wants the copy they
		// started, which is very often one they have only now rebuilt.
		if url, ok := alreadyRunning(cfg.Server.Addr); ok {
			fmt.Printf("\nARFABIT was already running at %s. Taking over.\n", url)

			if err := askToQuit(url, "a newer copy of ARFABIT was started"); err != nil {
				return fmt.Errorf(
					"ARFABIT is already running at %s and would not stand down.\n"+
						"Stop it from that page, or run:\n\n  lsof -ti :%s -sTCP:LISTEN | xargs kill\n\n"+
						"The underlying message was: %v",
					url, portOf(cfg.Server.Addr), err)
			}

			listener, err = waitForPort(cfg.Server.Addr, 10*time.Second)
			if err != nil {
				return explainListenFailure(cfg.Server.Addr, err)
			}
		} else {
			return explainListenFailure(cfg.Server.Addr, err)
		}
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

	// Watching the drive is how the page knows a disc has been put in without
	// anyone having to ask it to look.
	watchCtx, stopWatching := context.WithCancel(context.Background())
	defer stopWatching()
	go server.WatchDrives(watchCtx)

	errs := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	select {
	case err := <-errs:
		return err
	case <-quit:
	case <-ctx.Done():
	}

	if reason := quitReason.Load(); reason != nil {
		fmt.Printf("\nStopping: %s.\n", *reason)
	} else {
		fmt.Println("\nStopping.")
	}
	saveCalibration(st, cfg.Node.ID, calibration)

	// Pages hold their update connection open for as long as they are on
	// screen, so those are closed first: waiting for them would wait forever.
	server.Close()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		// Something is still holding on. Close it outright rather than leave
		// the port occupied, which would stop ARFABIT starting again.
		_ = httpServer.Close()
	}
	return nil
}

// alreadyRunning reports whether the occupied port is ARFABIT itself.
//
// Asking is better than assuming: something else may have the port, and saying
// "already running" about another program would be a guess (§15).
func alreadyRunning(addr string) (string, bool) {
	url := friendlyURLFor(addr)

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url + "/api/state")
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", false
	}

	// The reply must look like ARFABIT's own, not merely be a web server.
	var state struct {
		NodeName string `json:"node_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&state); err != nil {
		return "", false
	}
	if state.NodeName == "" {
		return "", false
	}

	return url, true
}

// askToQuit asks a running copy to stand down.
func askToQuit(url, reason string) error {
	client := &http.Client{Timeout: 5 * time.Second}

	body := strings.NewReader(fmt.Sprintf(`{"reason":%q}`, reason))
	resp, err := client.Post(url+"/api/quit", "application/json", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return fmt.Errorf("it declined: %s", strings.TrimSpace(string(body)))
	}
	return nil
}

// waitForPort waits for the address to become free, then claims it.
func waitForPort(addr string, within time.Duration) (net.Listener, error) {
	deadline := time.Now().Add(within)
	for {
		listener, err := net.Listen("tcp", addr)
		if err == nil {
			return listener, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func portOf(addr string) string {
	if _, port, err := net.SplitHostPort(addr); err == nil {
		return port
	}
	return addr
}

// explainListenFailure says what an occupied port actually means.
//
// "bind: address already in use" almost always means ARFABIT is already
// running, often in another window, and saying so saves a hunt.
func explainListenFailure(addr string, err error) error {
	if errors.Is(err, syscall.EADDRINUSE) {
		return fmt.Errorf(
			"Something is already using %s, but it does not answer like ARFABIT.\n"+
				"Close whatever it is, or start ARFABIT on a different address with:\n\n"+
				"  arfabit -addr :7848\n\n"+
				"The underlying message was: %v", addr, err)
	}
	return fmt.Errorf("ARFABIT could not listen on %s: %w", addr, err)
}

// prepareFolders makes the folders ARFABIT writes to.
//
// Problems are not fatal: Doctor reports them properly a moment later, and
// stopping here would mean the page never opens to explain why.
func prepareFolders(cfg config.Config) {
	for _, dir := range []string{cfg.Paths.Data, cfg.Paths.Masters, cfg.Paths.Library, cfg.Paths.Lab} {
		if dir != "" {
			_ = os.MkdirAll(dir, 0o755)
		}
	}
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

// friendlyURLFor turns a configured address into one a person can type.
func friendlyURLFor(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://localhost:7847"
	}
	if host == "" || host == "::" || host == "0.0.0.0" {
		host = "localhost"
	}
	return fmt.Sprintf("http://%s:%s", host, port)
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
