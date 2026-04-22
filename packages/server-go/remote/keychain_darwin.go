package remote

import (
	"os/exec"
	"strings"
)

// osKeychainSet stores a credential in the macOS Keychain using the security CLI.
func osKeychainSet(service, account, password string) error {
	// Try update first, then add.
	cmd := exec.Command("security", "add-generic-password",
		"-U", // update if exists
		"-s", service,
		"-a", account,
		"-w", password,
	)
	return cmd.Run()
}

func osKeychainGet(service, account string) (string, error) {
	out, err := exec.Command("security", "find-generic-password",
		"-s", service,
		"-a", account,
		"-w",
	).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func osKeychainDelete(service, account string) error {
	return exec.Command("security", "delete-generic-password",
		"-s", service,
		"-a", account,
	).Run()
}
