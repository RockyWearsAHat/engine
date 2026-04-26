//go:build windows

package remote

import "fmt"

// osKeychainSet stores a credential using the Windows Credential Manager.
// Not yet implemented; the encrypted file fallback is used automatically.
func osKeychainSet(service, account, password string) error {
	return fmt.Errorf("Windows Credential Manager not yet implemented; using file fallback")
}

func osKeychainGet(service, account string) (string, error) {
	return "", fmt.Errorf("Windows Credential Manager not yet implemented")
}

func osKeychainDelete(service, account string) error {
	return fmt.Errorf("Windows Credential Manager not yet implemented")
}
