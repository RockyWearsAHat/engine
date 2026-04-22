//go:build !darwin && !windows

package remote

import "fmt"

// osKeychainSet is a stub for platforms without native keychain support.
// The encrypted file fallback in KeychainStore is used automatically.
func osKeychainSet(service, account, password string) error {
	return fmt.Errorf("OS keychain not supported on this platform; using file fallback")
}

func osKeychainGet(service, account string) (string, error) {
	return "", fmt.Errorf("OS keychain not supported on this platform")
}

func osKeychainDelete(service, account string) error {
	return fmt.Errorf("OS keychain not supported on this platform")
}
