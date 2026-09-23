package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"janus/internal/certutil"
)

func runCertCommand(args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(out, "Usage: janus cert <ca|server|client> [options]\nNo gateway config is read. Use 'janus cert <command> --help' for options.")
		return nil
	}
	kind := args[0]
	if kind != "ca" && kind != "server" && kind != "client" {
		return fmt.Errorf("unknown certificate command %q; expected ca, server or client", kind)
	}
	flags := flag.NewFlagSet("janus cert "+kind, flag.ContinueOnError)
	flags.SetOutput(out)
	dir := flags.String("out", "", "required output directory; existing files are never overwritten")
	defaultDays := 365
	if kind == "ca" {
		defaultDays = 3650
	}
	days := flags.Int("days", defaultDays, "validity in days; issued certificates never outlive their CA")
	var caFile, caKey, name, hosts string
	if kind != "ca" {
		flags.StringVar(&caFile, "ca", "", "required signing CA certificate (PEM)")
		flags.StringVar(&caKey, "ca-key", "", "required signing CA private key (PEM)")
	}
	if kind == "server" {
		flags.StringVar(&hosts, "hosts", "", "required comma-separated access DNS names/IPs; no scheme, port or path")
	} else {
		defaultName := ""
		if kind == "ca" {
			defaultName = "Janus private CA"
		}
		flags.StringVar(&name, "name", defaultName, "certificate display name (client: required device label, not a login or access policy)")
	}
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	var result certutil.Result
	var err error
	switch kind {
	case "ca":
		result, err = certutil.CreateCA(*dir, name, *days)
	case "server":
		result, err = certutil.IssueServer(*dir, caFile, caKey, hosts, *days)
	case "client":
		result, err = certutil.IssueClient(*dir, caFile, caKey, name, *days)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Created %s certificate: %s\nPrivate key: %s\nCertificate SHA-256: %s\nExpires: %s\n", kind, result.CertFile, result.KeyFile, result.Fingerprint, result.NotAfter.UTC().Format(time.RFC3339))
	if result.P12File != "" {
		fmt.Fprintf(out, "Device import bundle: %s\nBundle password file (keep private): %s\n", result.P12File, result.PasswordFile)
	}
	fmt.Fprintln(out, "No gateway config or OS trust was changed. Protect private keys and password files; on Windows use private NTFS ACLs.")
	return nil
}
