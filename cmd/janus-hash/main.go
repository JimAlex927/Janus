// Command janus-hash generates bcrypt hashes for the Janus admin password.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"
)

func main() {
	flags := flag.NewFlagSet("janus-hash", flag.ExitOnError)
	cost := flags.Int("cost", bcrypt.DefaultCost, "bcrypt cost (4-31)")
	passwordStdin := flags.Bool("password-stdin", false, "read the password from stdin without echoing it")
	_ = flags.Parse(os.Args[1:])

	password, err := readPassword(*passwordStdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "janus-hash: %v\n", err)
		os.Exit(1)
	}
	if err := validatePassword(password); err != nil {
		fmt.Fprintf(os.Stderr, "janus-hash: %v\n", err)
		os.Exit(1)
	}
	if *cost < bcrypt.MinCost || *cost > bcrypt.MaxCost {
		fmt.Fprintf(os.Stderr, "janus-hash: cost must be between %d and %d\n", bcrypt.MinCost, bcrypt.MaxCost)
		os.Exit(2)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), *cost)
	if err != nil {
		fmt.Fprintf(os.Stderr, "janus-hash: generate hash: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(hash))
}

func readPassword(fromStdin bool) (string, error) {
	if fromStdin {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read password from stdin: %w", err)
		}
		return strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r"), nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("stdin is not a terminal; use -password-stdin for non-interactive use")
	}

	fmt.Fprint(os.Stderr, "Password: ")
	password, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	fmt.Fprint(os.Stderr, "Confirm password: ")
	confirmation, confirmErr := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if confirmErr != nil {
		return "", fmt.Errorf("read password confirmation: %w", confirmErr)
	}
	if string(password) != string(confirmation) {
		return "", errors.New("passwords do not match")
	}
	return string(password), nil
}

func validatePassword(password string) error {
	if password == "" {
		return errors.New("password must not be empty")
	}
	if len([]byte(password)) > 72 {
		return errors.New("password must be at most 72 bytes for bcrypt")
	}
	return nil
}
