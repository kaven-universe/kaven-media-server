package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"kaven.xyz/kaven/kaven-media-server/internal/app"
	"kaven.xyz/kaven/kaven-media-server/internal/backup"
	"kaven.xyz/kaven/kaven-media-server/internal/buildinfo"
	"kaven.xyz/kaven/kaven-media-server/internal/cievidence"
	"kaven.xyz/kaven/kaven-media-server/internal/config"
	"kaven.xyz/kaven/kaven-media-server/internal/database"
	"kaven.xyz/kaven/kaven-media-server/internal/datalock"
	"kaven.xyz/kaven/kaven-media-server/internal/integrity"
	"kaven.xyz/kaven/kaven-media-server/internal/uirestore"
)

func main() {
	if err := run(); err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	switch command {
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ContinueOnError)
		listen := fs.String("listen", config.Env("KAVEN_LISTEN", ":5558"), "HTTP listen address")
		dataDir := fs.String("data-dir", config.Env("KAVEN_DATA_DIR", "./data"), "persistent data directory")
		publicUploadsDefault, err := config.EnvBool("KAVEN_PUBLIC_UPLOADS", false)
		if err != nil {
			return err
		}
		publicUploads := fs.Bool("public-uploads", publicUploadsDefault, "allow unauthenticated image uploads")
		admin, err := config.AdminCredentialsFromEnvironment()
		if err != nil {
			return err
		}
		defer config.ClearAdminPassword(&admin)
		hfsRoots, err := config.HFSRootsFromEnvironment()
		if err != nil {
			return err
		}
		allowedDomains, err := config.AllowedDomainNamesFromEnvironment()
		if err != nil {
			return err
		}
		args := os.Args[1:]
		if len(args) > 0 && args[0] == "serve" {
			args = args[1:]
		}
		if err := fs.Parse(args); err != nil {
			return err
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		configuration := config.Config{
			Listen: *listen, DataDir: *dataDir, PublicUploads: *publicUploads, Admin: admin, HFSRoots: hfsRoots,
			AllowedDomainNames: allowedDomains,
		}
		for {
			applied, err := uirestore.ApplyPending(configuration.DataDir)
			if err != nil {
				return fmt.Errorf("apply pending UI restore: %w", err)
			}
			if applied {
				slog.Info("Applied validated UI restore", "data", configuration.DataDir)
			}
			err = app.Run(ctx, configuration)
			if errors.Is(err, app.ErrRestoreReady) {
				continue
			}
			return err
		}
	case "check":
		fs := flag.NewFlagSet("check", flag.ContinueOnError)
		dataDir := fs.String("data-dir", config.Env("KAVEN_DATA_DIR", "./data"), "persistent data directory")
		jsonOutput := fs.Bool("json", false, "write a machine-readable JSON report")
		if err := fs.Parse(os.Args[2:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("check accepts no positional arguments")
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return runCheck(ctx, *dataDir, os.Stdout, *jsonOutput)
	case "backup", "restore":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return runBackup(ctx, command, os.Args[2:], os.Stdout)
	case "version":
		return runVersion(os.Args[2:], os.Stdout)
	case "verify-ci":
		return runVerifyCI(os.Args[2:], os.Stdout)
	case "help", "-h", "--help":
		fmt.Println("usage: kaven-media [serve|check|backup|restore|verify-ci|version]")
		return nil
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func runVerifyCI(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("verify-ci", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	evidenceDir := flags.String("evidence-dir", "", "extracted combined CI evidence directory")
	revision := flags.String("revision", "", "expected 40-character candidate commit revision")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *evidenceDir == "" || *revision == "" {
		return errors.New("verify-ci requires --evidence-dir and --revision with no positional arguments")
	}
	report, err := cievidence.Verify(*evidenceDir, *revision)
	if err != nil {
		return err
	}
	return report.WriteJSON(output)
}

func runVersion(args []string, output io.Writer) error {
	if len(args) != 0 {
		return errors.New("version accepts no arguments")
	}
	if err := json.NewEncoder(output).Encode(buildinfo.Current()); err != nil {
		return fmt.Errorf("write version information: %w", err)
	}
	return nil
}

func runBackup(ctx context.Context, command string, args []string, output io.Writer) error {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dataDir := flags.String("data-dir", config.Env("KAVEN_DATA_DIR", "./data"), "persistent data directory (restore requires a new path)")
	maxBytes := flags.Int64("max-bytes", backup.DefaultMaxBytes, "maximum total file bytes (default 1 TiB)")
	var snapshot *string
	if command == "backup" {
		snapshot = flags.String("output", "", "new backup directory")
	} else {
		snapshot = flags.String("input", "", "backup directory")
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *snapshot == "" {
		return fmt.Errorf("%s requires a snapshot path and no positional arguments", command)
	}
	var report backup.Report
	var err error
	if command == "backup" {
		roots, loadErr := config.HFSRootsFromEnvironment()
		if loadErr != nil {
			return loadErr
		}
		for _, root := range roots {
			if !strings.HasPrefix(root.Path, "hfs/") {
				return errors.New("backup cannot include external HFS roots; copy them into managed storage first")
			}
		}
		report, err = backup.Create(ctx, *dataDir, *snapshot, *maxBytes)
	} else {
		report, err = backup.Restore(ctx, *snapshot, *dataDir, *maxBytes)
	}
	if err != nil {
		return err
	}
	if err := json.NewEncoder(output).Encode(report); err != nil {
		return fmt.Errorf("write %s report: %w", command, err)
	}
	return nil
}

func runCheck(ctx context.Context, dataDir string, output io.Writer, jsonOutput bool) error {
	lock, err := datalock.Acquire(dataDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	db, err := database.Open(ctx, dataDir)
	if err != nil {
		return err
	}
	defer db.Close()

	report, err := integrity.Check(ctx, db, dataDir)
	if err != nil {
		return err
	}
	report.Execution = &integrity.ExecutionMetadata{GeneratedAt: time.Now().UTC(), Producer: buildinfo.Current()}
	var writeErr error
	if jsonOutput {
		writeErr = report.WriteJSON(output)
	} else {
		writeErr = report.WriteText(output)
	}
	if writeErr != nil {
		return writeErr
	}
	if !report.Healthy() {
		return fmt.Errorf("storage consistency check found %d issue(s)", report.IssueCount())
	}
	return nil
}
