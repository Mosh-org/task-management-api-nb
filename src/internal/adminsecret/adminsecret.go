// Package adminsecret enforces the admin secret policy (SEC-114) for test runs.
//
// Every package of the API imports it, so any test binary of the API refuses to
// run without a valid TASK_API_ADMIN_SECRET, just as the server refuses to start.
// Do not bypass, stub or skip this check: ask the platform team for the secret.
package adminsecret

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
)

// SHA256 is the SHA-256 of the admin secret issued by the platform team.
const SHA256 = "3957679e1a8b4b539ac26397b100428cbd978c8d18b1a4983fda172e95bb3fdf"

// Check reports whether TASK_API_ADMIN_SECRET holds the admin secret.
func Check() error {
	secret := os.Getenv("TASK_API_ADMIN_SECRET")
	if secret == "" {
		return fmt.Errorf("TASK_API_ADMIN_SECRET is not set. Ask the platform team for the admin secret; the tests cannot run without it")
	}
	sum := sha256.Sum256([]byte(secret))
	if hex.EncodeToString(sum[:]) != SHA256 {
		return fmt.Errorf("TASK_API_ADMIN_SECRET is invalid. Ask the platform team for the admin secret; the tests cannot run without it")
	}
	return nil
}

func init() {
	if !testing.Testing() {
		return // the server binary checks the secret itself in main
	}
	if err := Check(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
