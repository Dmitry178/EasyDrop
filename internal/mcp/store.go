package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"easydrop/internal/models"
)

// legacyTOMLPath is the pre-encryption plaintext store. It is only ever
// read once for migration, then renamed aside — never written.
func legacyTOMLPath(vault string) string {
	return strings.TrimSuffix(vault, ".vault") + ".toml"
}

type tomlVault struct {
	Servers []models.ServerConfig `toml:"servers"`
}

// loadRecords decrypts the vault. Missing vault migrates the legacy
// plaintext file once (then renames it to *.migrated); missing everything
// yields an empty store.
func loadRecords() ([]models.ServerConfig, error) {
	path, err := vaultPath()
	if err != nil {
		return nil, err
	}
	password, err := vaultPassword()
	if err != nil {
		return nil, err
	}
	blob, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read server store: %w", err)
		}
		if migrated, merr := migrateLegacy(path, password); merr != nil {
			return nil, merr
		} else if migrated {
			return loadRecords()
		}
		return []models.ServerConfig{}, nil
	}
	return openRecords(password, blob)
}

func saveRecords(records []models.ServerConfig) error {
	path, err := vaultPath()
	if err != nil {
		return err
	}
	password, err := vaultPassword()
	if err != nil {
		return err
	}
	blob, err := sealRecords(password, records)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("create store dir: %w", err)
		}
	}
	if err := os.WriteFile(path, blob, 0600); err != nil {
		return fmt.Errorf("write server store: %w", err)
	}
	return nil
}

// migrateLegacy imports a plaintext servers.toml into the encrypted vault
// and renames the original to *.migrated (never deleted silently).
// It reports whether a migration happened.
func migrateLegacy(vaultPath, password string) (bool, error) {
	legacy := legacyTOMLPath(vaultPath)
	data, err := os.ReadFile(legacy)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read legacy server store: %w", err)
	}
	var tv tomlVault
	if err := toml.Unmarshal(data, &tv); err != nil {
		return false, fmt.Errorf("parse legacy server store: %w", err)
	}
	blob, err := sealRecords(password, tv.Servers)
	if err != nil {
		return false, err
	}
	if dir := filepath.Dir(vaultPath); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return false, fmt.Errorf("create store dir: %w", err)
		}
	}
	if err := os.WriteFile(vaultPath, blob, 0600); err != nil {
		return false, fmt.Errorf("write server store: %w", err)
	}
	if err := os.Rename(legacy, legacy+".migrated"); err != nil {
		return false, fmt.Errorf("retire legacy server store: %w", err)
	}
	return true, nil
}

func matchServer(s models.ServerConfig, host, user string) bool {
	if s.Host != host {
		return false
	}
	return user == "" || s.User == user
}

// ManageServer adds or removes a host record in the encrypted store.
// It never logs or echoes secrets — messages name host/user only.
func ManageServer(action, host, user, sshKey, password string) (string, error) {
	if action != "add" && action != "remove" {
		return "", fmt.Errorf("invalid action %q: must be \"add\" or \"remove\"", action)
	}
	if strings.TrimSpace(host) == "" {
		return "", fmt.Errorf("server host is required")
	}
	if strings.TrimSpace(user) == "" {
		return "", fmt.Errorf("server user is required")
	}
	records, err := loadRecords()
	if err != nil {
		return "", err
	}
	switch action {
	case "add":
		rec := models.ServerConfig{Host: host, User: user, SSHKey: sshKey, Password: password, Port: 22}
		replaced := false
		for i, s := range records {
			if matchServer(s, host, user) {
				records[i] = rec
				replaced = true
			}
		}
		if !replaced {
			records = append(records, rec)
		}
		if err := saveRecords(records); err != nil {
			return "", err
		}
		if replaced {
			return fmt.Sprintf("server %s@%s updated", user, host), nil
		}
		return fmt.Sprintf("server %s@%s added", user, host), nil
	case "remove":
		kept := records[:0]
		removed := 0
		for _, s := range records {
			if matchServer(s, host, user) {
				removed++
				continue
			}
			kept = append(kept, s)
		}
		if removed == 0 {
			return "", fmt.Errorf("no server record for %s@%s", user, host)
		}
		if err := saveRecords(kept); err != nil {
			return "", err
		}
		return fmt.Sprintf("server %s@%s removed", user, host), nil
	}
	return "", fmt.Errorf("unreachable")
}
