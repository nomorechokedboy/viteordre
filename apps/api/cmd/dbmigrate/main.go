// Command dbmigrate is a thin wrapper around golang-migrate for viteordre's Turso databases:
// the control-plane database and the per-merchant databases. It runs on its own, so rolling
// out or rolling back a schema is never tied to starting the API server.
//
//	go run ./cmd/dbmigrate control up
//	go run ./cmd/dbmigrate tenant --all-tenants up
//	go run ./cmd/dbmigrate tenant --merchant pho-24 down 1
//
// There is deliberately no drop command.
package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/golang-migrate/migrate/v4"

	"encore.app/internal/db"
	"encore.app/internal/dbmigrate"
	"encore.app/migrations"
)

const usageText = `dbmigrate runs golang-migrate against the control-plane and per-merchant Turso databases.

Usage:
  dbmigrate <control|tenant> [flags] <command> [args]

Commands:
  up [N]          apply every pending migration, or only the next N
  down <N|all>    roll back the last N migrations, or all of them
  goto <V>        migrate up or down to version V
  force <V>       set the version to V without running anything and clear the dirty flag
                  (V may be -1, write "force -1" or "force -- -1")
  version         print the current and the latest version
  create <name>   write an empty up and down file pair into the migrations directory

Targets:
  control         the control-plane database
  tenant          merchant databases, pick them with --db, --merchant or --all-tenants

Flags:
`

var negativeInt = regexp.MustCompile(`^-\d+$`)

type options struct {
	dataDir      string
	controlDB    string
	source       string
	dbFile       string
	merchants    string
	allTenants   bool
	parallel     int
	busyTimeout  time.Duration
	experimental string
	createDB     bool
	yes          bool
	verbose      bool
}

// command is a parsed and validated command line, free of any I/O.
type command struct {
	name  string // up, down, goto, force, version
	steps int    // up and down: number of steps, ignored when all is set
	all   bool   // up with no count, down all
	ver   int    // goto and force
}

func (c command) mutating() bool { return c.name != "version" }

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func newFlagSet(o *options, out io.Writer) *flag.FlagSet {
	fset := flag.NewFlagSet("dbmigrate", flag.ContinueOnError)
	fset.SetOutput(out)
	fset.StringVar(&o.dataDir, "data-dir", envOr("DB_DATA_DIR", "./data"),
		"directory holding the database files, relative database paths are joined onto it (env DB_DATA_DIR)")
	fset.StringVar(&o.controlDB, "control-db", envOr("DB_CONTROL_FILE", "control.db"),
		"control-plane database file name inside --data-dir (env DB_CONTROL_FILE)")
	fset.StringVar(&o.source, "source", "",
		"directory holding the control and tenant migration folders, default is the set embedded in the binary")
	fset.StringVar(&o.dbFile, "db", "",
		"tenant: path of one database file, the control plane is not consulted or updated")
	fset.StringVar(&o.merchants, "merchant", "",
		"tenant: comma-separated merchant ids or slugs, looked up in the control plane")
	fset.BoolVar(&o.allTenants, "all-tenants", false,
		"tenant: every ready, failed or interrupted tenant database in the control plane")
	fset.IntVar(&o.parallel, "parallel", 1, "tenant: how many databases to migrate at once")
	fset.DurationVar(&o.busyTimeout, "busy-timeout", 30*time.Second,
		"how long to wait for a lock held by another connection or process")
	fset.StringVar(&o.experimental, "experimental", envOr("DB_EXPERIMENTAL", ""),
		"comma-separated Turso experimental features, for example multiprocess_wal (env DB_EXPERIMENTAL)")
	fset.BoolVar(&o.createDB, "create-db", false, "tenant with --db: create the file if it does not exist")
	fset.BoolVar(&o.yes, "yes", false, "skip the confirmation for destructive commands")
	fset.BoolVar(&o.verbose, "verbose", false, "print golang-migrate's own log")
	return fset
}

// parseArgs accepts flags before and after the positional words, which the standard flag
// package does not do on its own.
func parseArgs(args []string, out io.Writer) (*options, []string, error) {
	o := &options{}
	fset := newFlagSet(o, out)
	fset.Usage = func() {
		fmt.Fprint(out, usageText)
		fset.PrintDefaults()
	}

	var pos []string
	for len(args) > 0 {
		if negativeInt.MatchString(args[0]) {
			pos = append(pos, args[0])
			args = args[1:]
			continue
		}
		if err := fset.Parse(args); err != nil {
			return nil, nil, err
		}
		rest := fset.Args()
		if len(rest) == 0 {
			break
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
	return o, pos, nil
}

func parseCommand(name string, args []string) (command, error) {
	need := func(n int) error {
		if len(args) != n {
			return fmt.Errorf("%s takes %d argument(s), got %d", name, n, len(args))
		}
		return nil
	}
	switch name {
	case "up":
		switch len(args) {
		case 0:
			return command{name: "up", all: true}, nil
		case 1:
			n, err := strconv.Atoi(args[0])
			if err != nil || n < 1 {
				return command{}, fmt.Errorf("up expects a positive number of steps, got %q", args[0])
			}
			return command{name: "up", steps: n}, nil
		}
		return command{}, errors.New("up takes at most one argument")
	case "down":
		if err := need(1); err != nil {
			return command{}, fmt.Errorf("%w, use a number of steps or all", err)
		}
		if args[0] == "all" {
			return command{name: "down", all: true}, nil
		}
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 1 {
			return command{}, fmt.Errorf("down expects a positive number of steps or all, got %q", args[0])
		}
		return command{name: "down", steps: n}, nil
	case "goto":
		if err := need(1); err != nil {
			return command{}, err
		}
		v, err := strconv.Atoi(args[0])
		if err != nil || v < 1 {
			return command{}, fmt.Errorf("goto expects a version of 1 or more, got %q (use down all to roll back everything)", args[0])
		}
		return command{name: "goto", ver: v}, nil
	case "force":
		if err := need(1); err != nil {
			return command{}, err
		}
		v, err := strconv.Atoi(args[0])
		if err != nil || v < -1 {
			return command{}, fmt.Errorf("force expects a version of -1 or more, got %q", args[0])
		}
		return command{name: "force", ver: v}, nil
	case "version":
		if err := need(0); err != nil {
			return command{}, err
		}
		return command{name: "version"}, nil
	}
	return command{}, fmt.Errorf("unknown command %q", name)
}

// target is one database to operate on.
type target struct {
	label    string
	path     string
	merchant string // control-plane merchant id, empty when the control plane is not updated
}

type result struct {
	label   string
	version int // -1 when no migration is applied
	dirty   bool
	latest  uint
	msg     string
	err     error
}

type app struct {
	o       *options
	kind    dbmigrate.Kind
	fsys    fs.FS
	out     io.Writer
	errw    io.Writer
	confirm func(prompt string) (bool, error)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, out, errw io.Writer) int {
	o, pos, err := parseArgs(args, errw)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		return 2
	}
	if len(pos) < 2 {
		fmt.Fprint(errw, usageText)
		return 2
	}
	kind, err := dbmigrate.ParseKind(pos[0])
	if err != nil {
		fmt.Fprintln(errw, "error:", err)
		return 2
	}

	a := &app{o: o, kind: kind, out: out, errw: errw, confirm: o.interactiveConfirm(errw)}
	if o.source != "" {
		a.fsys = os.DirFS(o.source)
	} else {
		a.fsys = migrations.FS
	}

	if pos[1] == "create" {
		return a.create(pos[2:])
	}

	cmd, err := parseCommand(pos[1], pos[2:])
	if err != nil {
		fmt.Fprintln(errw, "error:", err)
		return 2
	}
	if err := a.validateSelection(cmd); err != nil {
		fmt.Fprintln(errw, "error:", err)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return a.execute(ctx, cmd)
}

func (a *app) validateSelection(cmd command) error {
	if a.o.parallel < 1 {
		return errors.New("--parallel must be at least 1")
	}
	if a.kind == dbmigrate.Control {
		if a.o.dbFile != "" || a.o.merchants != "" || a.o.allTenants {
			return errors.New("--db, --merchant and --all-tenants only apply to the tenant target")
		}
		return nil
	}
	chosen := 0
	for _, set := range []bool{a.o.dbFile != "", a.o.merchants != "", a.o.allTenants} {
		if set {
			chosen++
		}
	}
	if chosen != 1 {
		return errors.New("tenant needs exactly one of --db, --merchant or --all-tenants")
	}
	if a.o.createDB && a.o.dbFile == "" {
		return errors.New("--create-db only applies with --db")
	}
	return nil
}

func (a *app) create(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.errw, "error: create takes exactly one name")
		return 2
	}
	dir := a.o.source
	if dir == "" {
		dir = "migrations"
	}
	up, down, err := dbmigrate.CreateFiles(dir, a.kind, args[0])
	if err != nil {
		fmt.Fprintln(a.errw, "error:", err)
		return 1
	}
	fmt.Fprintln(a.out, "created", up)
	fmt.Fprintln(a.out, "created", down)
	if a.o.source == "" {
		fmt.Fprintln(a.out, "rebuild the binary to embed the new files, or run with --source", dir)
	}
	return 0
}

func (a *app) execute(ctx context.Context, cmd command) int {
	if cmd.name == "down" && (cmd.all || a.o.allTenants) {
		prompt := "Roll back ALL migrations?"
		if a.o.allTenants {
			prompt = "Roll back migrations on EVERY tenant database?"
		}
		ok, err := a.confirm(prompt)
		if err != nil {
			fmt.Fprintln(a.errw, "error:", err)
			return 2
		}
		if !ok {
			fmt.Fprintln(a.errw, "aborted")
			return 1
		}
	}

	if cmd.mutating() && !strings.Contains(a.o.experimental, "multiprocess_wal") {
		fmt.Fprintln(a.errw, "warning: stop the API server first. A Turso database file allows one process at a time unless multiprocess_wal is enabled on every process.")
	}

	latest, err := dbmigrate.Latest(a.fsys, a.kind)
	if err != nil {
		fmt.Fprintln(a.errw, "error:", err)
		return 1
	}

	var control *sql.DB
	targets, err := a.resolveTargets(ctx, &control)
	if control != nil {
		defer control.Close()
	}
	if err != nil {
		fmt.Fprintln(a.errw, "error:", err)
		return 1
	}
	if len(targets) == 0 {
		fmt.Fprintln(a.out, "no tenant databases to process")
		return 0
	}

	results := make([]result, len(targets))
	sem := make(chan struct{}, a.o.parallel)
	var wg sync.WaitGroup
	for i, t := range targets {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() != nil {
				results[i] = result{label: t.label, version: -1, latest: latest, msg: "skipped, interrupted", err: ctx.Err()}
				return
			}
			results[i] = a.runTarget(ctx, t, cmd, latest, control)
		}()
	}
	wg.Wait()

	return a.report(cmd, results)
}

// resolveTargets works out which database files to touch. It opens the control plane when the
// tenants are chosen by merchant, and hands it back so outcomes can be recorded in it.
func (a *app) resolveTargets(ctx context.Context, control **sql.DB) ([]target, error) {
	if a.kind == dbmigrate.Control {
		return []target{{label: "control", path: db.ResolvePath(a.o.dataDir, a.o.controlDB)}}, nil
	}
	if a.o.dbFile != "" {
		return []target{{label: a.o.dbFile, path: a.o.dbFile}}, nil
	}

	path := db.ResolvePath(a.o.dataDir, a.o.controlDB)
	c, err := db.Open(ctx, path, a.dbOptions(true))
	if err != nil {
		return nil, fmt.Errorf("open control-plane database: %w", err)
	}
	*control = c

	var selectors []string
	if a.o.merchants != "" {
		selectors = strings.Split(a.o.merchants, ",")
	}
	tenants, err := dbmigrate.ListTenants(ctx, c, selectors, a.o.allTenants)
	if err != nil {
		return nil, err
	}
	out := make([]target, 0, len(tenants))
	for _, t := range tenants {
		out = append(out, target{label: t.Label(), path: db.ResolvePath(a.o.dataDir, t.DBPath), merchant: t.MerchantID})
	}
	return out, nil
}

func (a *app) dbOptions(mustExist bool) db.Options {
	o := db.Options{BusyTimeout: a.o.busyTimeout, MaxOpenConns: 1, MustExist: mustExist}
	for _, f := range strings.Split(a.o.experimental, ",") {
		if f = strings.TrimSpace(f); f != "" {
			o.Experimental = append(o.Experimental, f)
		}
	}
	return o
}

func (a *app) runTarget(ctx context.Context, t target, cmd command, latest uint, control *sql.DB) (res result) {
	res = result{label: t.label, version: -1, latest: latest}
	// Outcomes are recorded even after Ctrl-C, the migration in flight is allowed to finish.
	bookCtx := context.WithoutCancel(ctx)
	tracked := control != nil && t.merchant != ""

	mustExist := a.kind == dbmigrate.Tenant && !a.o.createDB
	// Failing to open the file or to prepare it leaves the recorded schema version alone,
	// only the status and the error are updated.
	fail := func(err error) result {
		res.err = err
		if tracked && cmd.mutating() {
			_ = dbmigrate.MarkFailed(bookCtx, control, t.merchant, err)
		}
		return res
	}

	sqlDB, err := db.Open(ctx, t.path, a.dbOptions(mustExist))
	if err != nil {
		return fail(err)
	}
	m, err := dbmigrate.New(a.fsys, a.kind, sqlDB)
	if err != nil {
		_ = sqlDB.Close()
		return fail(err)
	}
	if a.o.verbose {
		m.Log = verboseLogger{w: a.errw, prefix: t.label}
	}

	if tracked && cmd.mutating() {
		if err := dbmigrate.MarkMigrating(bookCtx, control, t.merchant); err != nil {
			fmt.Fprintf(a.errw, "warning: %s: could not mark migrating: %v\n", t.label, err)
		}
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			select {
			case m.GracefulStop <- true:
			default:
			}
		case <-done:
		}
	}()

	opErr := a.apply(m, cmd)
	close(done)

	switch {
	case errors.Is(opErr, migrate.ErrNoChange):
		res.msg, opErr = "no change", nil
	case opErr == nil && cmd.name != "version":
		res.msg = "ok"
	}
	ver, dirty, verr := m.Version()
	current := int(ver)
	switch {
	case errors.Is(verr, migrate.ErrNilVersion):
		current = -1
	case verr != nil:
		current = -1
		if opErr == nil {
			opErr = verr
		}
	}
	res.version, res.dirty, res.err = current, dirty, opErr

	if srcErr, dbErr := m.Close(); res.err == nil && (srcErr != nil || dbErr != nil) {
		res.err = errors.Join(srcErr, dbErr)
	}

	if tracked && cmd.mutating() {
		recorded := res.version
		if recorded < 0 {
			recorded = 0
		}
		if err := dbmigrate.MarkResult(bookCtx, control, t.merchant, recorded, res.dirty, res.err); err != nil {
			fmt.Fprintf(a.errw, "warning: %s: could not record the result in the control plane: %v\n", t.label, err)
		}
	}
	return res
}

func (a *app) apply(m *migrate.Migrate, cmd command) error {
	switch cmd.name {
	case "up":
		if cmd.all {
			return m.Up()
		}
		return m.Steps(cmd.steps)
	case "down":
		if cmd.all {
			return m.Down()
		}
		return m.Steps(-cmd.steps)
	case "goto":
		return m.Migrate(uint(cmd.ver))
	case "force":
		return m.Force(cmd.ver)
	case "version":
		return nil
	}
	return fmt.Errorf("unknown command %q", cmd.name)
}

func (a *app) report(cmd command, results []result) int {
	failed := 0
	for _, r := range results {
		if r.err != nil {
			failed++
		}
	}

	tw := tabwriter.NewWriter(a.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "DATABASE\tVERSION\tLATEST\tSTATE")
	for _, r := range results {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", r.label, versionText(r.version), r.latest, stateText(cmd, r))
	}
	_ = tw.Flush()

	if len(results) > 1 {
		fmt.Fprintf(a.out, "\n%d succeeded, %d failed\n", len(results)-failed, failed)
	}
	for _, r := range results {
		if r.err != nil {
			fmt.Fprintf(a.errw, "error: %s: %v\n", r.label, r.err)
		}
	}
	if failed > 0 {
		return 1
	}
	return 0
}

func versionText(v int) string {
	if v < 0 {
		return "-"
	}
	return strconv.Itoa(v)
}

func stateText(cmd command, r result) string {
	switch {
	case r.err != nil:
		return "FAILED"
	case r.dirty:
		return "DIRTY, fix the failed migration then run force"
	case cmd.mutating() && r.msg != "":
		return r.msg
	case r.version < 0:
		return "no migrations applied"
	case uint(r.version) == r.latest:
		return "up to date"
	case uint(r.version) < r.latest:
		return "pending migrations"
	default:
		return "ahead of this binary"
	}
}

type verboseLogger struct {
	w      io.Writer
	prefix string
}

func (l verboseLogger) Printf(format string, v ...interface{}) {
	fmt.Fprintf(l.w, "[%s] %s", l.prefix, fmt.Sprintf(format, v...))
}

func (verboseLogger) Verbose() bool { return true }

// interactiveConfirm returns the confirmation used for destructive commands: --yes skips it,
// a terminal gets a y/N prompt, and a non-interactive run is refused.
func (o *options) interactiveConfirm(errw io.Writer) func(string) (bool, error) {
	return func(prompt string) (bool, error) {
		if o.yes {
			return true, nil
		}
		fi, err := os.Stdin.Stat()
		if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
			return false, errors.New("refusing a destructive command without a terminal, pass --yes to confirm")
		}
		fmt.Fprintf(errw, "%s [y/N] ", prompt)
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return false, nil
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		return answer == "y" || answer == "yes", nil
	}
}
