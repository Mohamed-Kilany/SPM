// Command backupctl creates, inspects, verifies and prunes SPM backup
// bundles. It is intentionally dependency-free so an operator can build it
// from the repository and run it on the backup host.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"spm-platform/api/internal/backup"
)

const usageText = `backupctl manages SPM backup bundles.

Usage:
  backupctl <command> [flags]

Commands:
  keygen     Create a new AES-256 bundle key.
  manifest   Build manifest.json for a staging directory.
  pack       Pack a staging directory into a tar bundle.
  unpack     Extract a bundle (plain or encrypted) into a directory.
  seal       Encrypt a tar bundle with AES-256-GCM.
  open       Decrypt a sealed bundle.
  inspect    Read the bundle manifest without hashing payload files.
  verify     Recompute every payload checksum and validate the manifest.
  retention  List or delete old bundles by count and age.
  status     Read or update the backup status file.

Run "backupctl <command> -h" for command flags.
`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "backupctl: %v\n", err)
		os.Exit(1)
	}
}

var errHelp = errors.New("help requested")

func run(args []string, stdout, stderr io.Writer) error {
	err := dispatch(args, stdout, stderr)
	if errors.Is(err, errHelp) {
		return nil
	}
	return err
}

func dispatch(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stdout, usageText)
		return nil
	}
	command := args[0]
	rest := args[1:]
	switch command {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
		return nil
	case "keygen":
		return runKeygen(rest, stdout, stderr)
	case "manifest":
		return runManifest(rest, stdout, stderr)
	case "pack":
		return runPack(rest, stdout, stderr)
	case "unpack":
		return runUnpack(rest, stdout, stderr)
	case "seal":
		return runSeal(rest, stdout, stderr)
	case "open":
		return runOpen(rest, stdout, stderr)
	case "inspect":
		return runInspect(rest, stdout, stderr)
	case "verify":
		return runVerify(rest, stdout, stderr)
	case "retention":
		return runRetention(rest, stdout, stderr)
	case "status":
		return runStatus(rest, stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q\n\n%s", command, usageText)
	}
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	return flags
}

func parseFlags(flags *flag.FlagSet, args []string) error {
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errHelp
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	return nil
}

func runKeygen(args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("keygen", stderr)
	output := flags.String("output", "", "path for the new key file")
	force := flags.Bool("force", false, "overwrite an existing key file")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if *output == "" {
		return errors.New("keygen: -output is required")
	}
	identifier, err := backup.GenerateKey(*output, *force)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "key: %s\nkey id: %s\n", *output, identifier)
	fmt.Fprintln(stdout, "Store this file separately from the backups and restrict access to the operator account.")
	return nil
}

func runManifest(args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("manifest", stderr)
	root := flags.String("root", "", "staging directory")
	output := flags.String("output", "", "manifest path (default <root>/manifest.json)")
	host := flags.String("host", defaultHostname(), "host name recorded in the manifest")
	commit := flags.String("commit", "", "source revision recorded in the manifest")
	version := flags.String("version", "", "backup tool version recorded in the manifest")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if *root == "" {
		return errors.New("manifest: -root is required")
	}
	manifest, err := backup.BuildManifest(*root, backup.BuildOptions{
		Host:        *host,
		Commit:      *commit,
		ToolVersion: *version,
	})
	if err != nil {
		return err
	}
	if *output != "" {
		if err := backup.WriteManifestFile(*output, manifest); err != nil {
			return err
		}
	} else if err := backup.WriteManifest(*root, manifest); err != nil {
		return err
	}
	printManifest(stdout, manifest)
	return nil
}

func runPack(args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("pack", stderr)
	root := flags.String("root", "", "staging directory")
	output := flags.String("output", "", "bundle tar path")
	host := flags.String("host", defaultHostname(), "host name recorded in the manifest")
	commit := flags.String("commit", "", "source revision recorded in the manifest")
	version := flags.String("version", "", "backup tool version recorded in the manifest")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if *root == "" || *output == "" {
		return errors.New("pack: -root and -output are required")
	}
	if err := ensureOutside(*root, *output); err != nil {
		return err
	}
	file, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("pack: create %s: %w", *output, err)
	}
	manifest, packErr := backup.Pack(*root, file, backup.BuildOptions{
		Host:        *host,
		Commit:      *commit,
		ToolVersion: *version,
	})
	closeErr := file.Close()
	if packErr != nil {
		_ = os.Remove(*output)
		return packErr
	}
	if closeErr != nil {
		_ = os.Remove(*output)
		return fmt.Errorf("pack: close %s: %w", *output, closeErr)
	}
	fmt.Fprintf(stdout, "bundle: %s\n", *output)
	printManifest(stdout, manifest)
	return nil
}

func runUnpack(args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("unpack", stderr)
	input := flags.String("input", "", "bundle path")
	output := flags.String("output", "", "destination directory")
	keyFile := flags.String("key-file", "", "encryption key file (required for sealed bundles)")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if *input == "" || *output == "" {
		return errors.New("unpack: -input and -output are required")
	}
	key, err := optionalKey(*keyFile)
	if err != nil {
		return err
	}
	bundle, err := backup.OpenBundle(*input, key)
	if err != nil {
		return err
	}
	defer bundle.Close()
	manifest, err := backup.Unpack(bundle, *output)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "extracted: %s\nencrypted: %t\n", *output, bundle.Encrypted)
	printManifest(stdout, manifest)
	return nil
}

func runSeal(args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("seal", stderr)
	input := flags.String("input", "", "plain tar bundle")
	output := flags.String("output", "", "encrypted bundle path")
	keyFile := flags.String("key-file", "", "encryption key file")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if *input == "" || *output == "" || *keyFile == "" {
		return errors.New("seal: -input, -output and -key-file are required")
	}
	key, err := backup.LoadKey(*keyFile)
	if err != nil {
		return err
	}
	source, err := os.Open(*input)
	if err != nil {
		return fmt.Errorf("seal: open %s: %w", *input, err)
	}
	defer source.Close()
	target, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("seal: create %s: %w", *output, err)
	}
	sealErr := backup.SealStream(target, source, key)
	closeErr := target.Close()
	if sealErr != nil {
		_ = os.Remove(*output)
		return sealErr
	}
	if closeErr != nil {
		_ = os.Remove(*output)
		return fmt.Errorf("seal: close %s: %w", *output, closeErr)
	}
	info, err := os.Stat(*output)
	if err != nil {
		return fmt.Errorf("seal: stat %s: %w", *output, err)
	}
	fmt.Fprintf(stdout, "sealed: %s\nkey id: %s\nbytes: %d\n", *output, backup.KeyID(key), info.Size())
	return nil
}

func runOpen(args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("open", stderr)
	input := flags.String("input", "", "encrypted bundle")
	output := flags.String("output", "", "plain tar bundle path")
	keyFile := flags.String("key-file", "", "encryption key file")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if *input == "" || *output == "" || *keyFile == "" {
		return errors.New("open: -input, -output and -key-file are required")
	}
	key, err := backup.LoadKey(*keyFile)
	if err != nil {
		return err
	}
	source, err := os.Open(*input)
	if err != nil {
		return fmt.Errorf("open: open %s: %w", *input, err)
	}
	defer source.Close()
	target, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open: create %s: %w", *output, err)
	}
	openErr := backup.OpenStream(target, source, key)
	closeErr := target.Close()
	if openErr != nil {
		_ = os.Remove(*output)
		return openErr
	}
	if closeErr != nil {
		_ = os.Remove(*output)
		return fmt.Errorf("open: close %s: %w", *output, closeErr)
	}
	fmt.Fprintf(stdout, "opened: %s\n", *output)
	return nil
}

func runInspect(args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("inspect", stderr)
	bundlePath := flags.String("bundle", "", "bundle path")
	keyFile := flags.String("key-file", "", "encryption key file")
	asJSON := flags.Bool("json", false, "print JSON")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if *bundlePath == "" {
		return errors.New("inspect: -bundle is required")
	}
	key, err := optionalKey(*keyFile)
	if err != nil {
		return err
	}
	report, err := backup.InspectBundle(*bundlePath, key)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(stdout, report)
	}
	fmt.Fprintf(stdout, "bundle: %s\nencrypted: %t\n", report.Path, report.Encrypted)
	printManifest(stdout, report.Manifest)
	return nil
}

func runVerify(args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("verify", stderr)
	bundlePath := flags.String("bundle", "", "bundle path")
	keyFile := flags.String("key-file", "", "encryption key file")
	asJSON := flags.Bool("json", false, "print JSON")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if *bundlePath == "" {
		return errors.New("verify: -bundle is required")
	}
	key, err := optionalKey(*keyFile)
	if err != nil {
		return err
	}
	report, err := backup.VerifyBundle(*bundlePath, key)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(stdout, report)
	}
	fmt.Fprintf(stdout, "verified: %s\nencrypted: %t\nfiles: %d\nbytes: %d\n",
		report.Path, report.Encrypted, report.Files, report.Bytes)
	printManifest(stdout, report.Manifest)
	return nil
}

func runRetention(args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("retention", stderr)
	directory := flags.String("directory", "", "backup directory")
	keepLast := flags.Int("keep-last", 7, "minimum number of newest bundles to keep")
	keepDays := flags.Int("keep-days", 30, "keep expired bundles younger than this many days")
	apply := flags.Bool("apply", false, "delete the selected bundles")
	asJSON := flags.Bool("json", false, "print JSON")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if *directory == "" {
		return errors.New("retention: -directory is required")
	}
	if *keepLast < 0 || *keepDays < 0 {
		return errors.New("retention: -keep-last and -keep-days must not be negative")
	}
	bundles, err := backup.ListBundles(*directory)
	if err != nil {
		return err
	}
	expired := backup.SelectExpired(bundles, *keepLast, time.Duration(*keepDays)*24*time.Hour, time.Now())
	var removed []backup.BundleFile
	if *apply && len(expired) > 0 {
		removed, err = backup.Prune(expired)
		if err != nil {
			return err
		}
	}
	if *asJSON {
		return printJSON(stdout, map[string]any{
			"bundles": bundles,
			"expired": expired,
			"removed": removed,
			"applied": *apply,
		})
	}
	fmt.Fprintf(stdout, "bundles: %d\nexpired: %d\n", len(bundles), len(expired))
	for _, bundle := range expired {
		action := "would remove"
		if *apply {
			action = "removed"
		}
		fmt.Fprintf(stdout, "  %s %s\n", action, bundle.Path)
	}
	return nil
}

func runStatus(args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("status", stderr)
	path := flags.String("file", "", "status file")
	set := flags.String("set", "", "success, failure, verified, drill-ok or drill-failed")
	bundlePath := flags.String("bundle", "", "bundle path for -set success")
	sha256Value := flags.String("sha256", "", "bundle SHA-256 for -set success")
	size := flags.Int64("size", 0, "bundle size for -set success")
	duration := flags.Duration("duration", 0, "attempt duration")
	encrypted := flags.Bool("encrypted", false, "bundle is encrypted")
	message := flags.String("error", "", "failure message for -set failure")
	asJSON := flags.Bool("json", false, "print JSON")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if *path == "" {
		return errors.New("status: -file is required")
	}
	status, err := backup.ReadStatus(*path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	now := time.Now().UTC()
	switch *set {
	case "":
	case "success":
		status.RecordSuccess(now, *bundlePath, *sha256Value, *size, *encrypted, *duration)
	case "failure":
		if strings.TrimSpace(*message) == "" {
			return errors.New("status: -error is required with -set failure")
		}
		status.RecordFailure(now, *message, *duration)
	case "verified":
		status.RecordVerified(now)
	case "drill-ok":
		status.RecordDrill(now, true)
	case "drill-failed":
		status.RecordDrill(now, false)
	default:
		return fmt.Errorf("status: unknown -set value %q", *set)
	}
	if *set != "" {
		if err := backup.WriteStatus(*path, status); err != nil {
			return err
		}
	}
	if *asJSON {
		return printJSON(stdout, status)
	}
	printStatus(stdout, *path, status)
	return nil
}

func optionalKey(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	return backup.LoadKey(path)
}

func ensureOutside(root, output string) error {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("pack: resolve root: %w", err)
	}
	absoluteOutput, err := filepath.Abs(output)
	if err != nil {
		return fmt.Errorf("pack: resolve output: %w", err)
	}
	relative, err := filepath.Rel(absoluteRoot, absoluteOutput)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("pack: output %s must be outside the staging root %s", output, root)
	}
	return nil
}

func printManifest(stdout io.Writer, manifest *backup.Manifest) {
	fmt.Fprintf(stdout, "created: %s\nhost: %s\n", manifest.CreatedAt.Format(time.RFC3339), manifest.Host)
	if manifest.Commit != "" {
		fmt.Fprintf(stdout, "commit: %s\n", manifest.Commit)
	}
	if len(manifest.Databases) > 0 {
		fmt.Fprintf(stdout, "databases: %s\n", strings.Join(manifest.Databases, ", "))
	}
	if len(manifest.Volumes) > 0 {
		fmt.Fprintf(stdout, "volumes: %s\n", strings.Join(manifest.Volumes, ", "))
	}
	fmt.Fprintf(stdout, "files: %d\nbytes: %d\n", len(manifest.Entries), manifest.TotalBytes)
}

func printStatus(stdout io.Writer, path string, status backup.Status) {
	fmt.Fprintf(stdout, "status: %s\n", path)
	fmt.Fprintf(stdout, "last attempt: %s\n", formatTime(status.LastAttemptAt))
	fmt.Fprintf(stdout, "last success: %s\n", formatTime(status.LastSuccessAt))
	fmt.Fprintf(stdout, "last failure: %s\n", formatTime(status.LastFailureAt))
	fmt.Fprintf(stdout, "last verified: %s\n", formatTime(status.LastVerifiedAt))
	fmt.Fprintf(stdout, "last drill: %s (ok=%t)\n", formatTime(status.LastDrillAt), status.LastDrillOK)
	fmt.Fprintf(stdout, "bundle: %s\n", status.LastBundle)
	fmt.Fprintf(stdout, "sha256: %s\n", status.LastSHA256)
	fmt.Fprintf(stdout, "size: %d bytes\n", status.LastSizeBytes)
	fmt.Fprintf(stdout, "encrypted: %t\n", status.Encrypted)
	fmt.Fprintf(stdout, "successes: %d\nfailures: %d\nconsecutive failures: %d\n",
		status.SuccessesTotal, status.FailuresTotal, status.ConsecutiveFailures)
	if status.LastError != "" {
		fmt.Fprintf(stdout, "last error: %s\n", status.LastError)
	}
}

func formatTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return "never"
	}
	return value.Format(time.RFC3339)
}

func printJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func defaultHostname() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "unknown"
	}
	return name
}
