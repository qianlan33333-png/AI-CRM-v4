// Command recover-access-password is a Linux root maintenance entrypoint.
// Password and a real authenticated operator session are accepted only on stdin.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/maintenance"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type secrets struct {
	Password     string `json:"password"`
	SessionToken string `json:"session_token"`
}

func readSecrets(reader io.Reader) (secrets, error) {
	var input secrets
	body, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1)) // Existing Access HTTP request size.
	if err != nil || len(body) > 1<<20 {
		return secrets{}, errors.New("invalid recovery stdin")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return secrets{}, errors.New("invalid recovery stdin")
	}
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return secrets{}, errors.New("invalid recovery stdin")
		}
		seen[name] = true
		var value string
		if decoder.Decode(&value) != nil {
			return secrets{}, errors.New("invalid recovery stdin")
		}
		switch name {
		case "password":
			input.Password = value
		case "session_token":
			input.SessionToken = value
		default:
			return secrets{}, errors.New("invalid recovery stdin")
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return secrets{}, errors.New("invalid recovery stdin")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || input.Password == "" || input.SessionToken == "" {
		return secrets{}, errors.New("invalid recovery stdin")
	}
	return input, nil
}
func run() error {
	mode := flag.String("mode", "inspect", "inspect or apply")
	id := flag.Int64("target-id", 0, "exact existing super administrator ID")
	username := flag.String("username", "", "exact existing username")
	version := flag.Int64("expected-version", 0, "session version from inspect; required for apply")
	ref := flag.String("authorization-reference", "", "nonsecret human authorization reference; required for apply")
	flag.Parse()
	if flag.NArg() != 0 || (*mode != "inspect" && *mode != "apply") || *id < 1 || *username == "" {
		return errors.New("invalid recovery flags")
	}
	authority, err := maintenance.HostAuthority()
	if err != nil {
		return err
	}
	var sensitive secrets
	if *mode == "apply" {
		if *version < 1 || *ref == "" {
			return errors.New("apply requires expected version and authorization reference")
		}
		sensitive, err = readSecrets(os.Stdin)
		if err != nil {
			return err
		}
	}
	dsn, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: dsn, MaxConnections: 2})
	if err != nil {
		return errors.New("recovery database connection failed")
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	repository := accessstore.NewPostgreSQL()
	passwords := credential.PasswordHasher{}
	// Authentication here verifies an existing session; it never logs in or
	// issues a session. The standard verifier still requires a valid dummy hash.
	dummy, err := passwords.Hash("recovery-session-verification-only")
	if err != nil {
		return errors.New("password verifier unavailable")
	}
	auth, err := accessapp.NewAuthentication(repository, uow, passwords, accessapp.AuthenticationConfig{DummyPHCHash: dummy})
	if err != nil {
		return err
	}
	service, err := maintenance.New(repository, uow, passwords, auth)
	if err != nil {
		return err
	}
	var result maintenance.Target
	if *mode == "inspect" {
		result, err = service.Inspect(ctx, authority, *id, *username)
	} else {
		result, err = service.Recover(ctx, authority, maintenance.Input{Target: maintenance.Target{ID: *id, Username: *username, SessionVersion: *version}, Password: sensitive.Password, SessionToken: sensitive.SessionToken, AuthorizationReference: *ref})
	}
	if err != nil {
		return errors.New("recovery rejected or transaction failed; inspect and verify login before retrying")
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
