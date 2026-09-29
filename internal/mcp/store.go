package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"easydrop/internal/models"
)

// serversFile resolves the vault path. EASYDROP_SERVERS_FILE overrides the
// default for tests; the directory is created 0700, the file 0600.
func serversFile() (string, error) {
	if p := os.Getenv("EASYDROP_SERVERS_FILE"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".easydrop", "servers.toml"), nil
}

type serverVault struct {
	Servers []models.ServerConfig `toml:"servers"`
}

func loadVault(path string) (*serverVault, error) {
	v := &serverVault{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return v, nil
		}
		return nil, fmt.Errorf("read server store: %w", err)
	}
	if err := toml.Unmarshal(data, v); err != nil {
		return nil, fmt.Errorf("parse server store: %w", err)
	}
	return v, nil
}

func saveVault(path string, v *serverVault) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("create store dir: %w", err)
		}
	}
	data, err := toml.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal server store: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write server store: %w", err)
	}
	return nil
}

func matchServer(s models.ServerConfig, host, user string) bool {
	if s.Host != host {
		return false
	}
	return user == "" || s.User == user
}

// ManageServer adds or removes a host record. It never logs or echoes
// secrets — messages name host/user only.
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
	path, err := serversFile()
	if err != nil {
		return "", err
	}
	v, err := loadVault(path)
	if err != nil {
		return "", err
	}
	switch action {
	case "add":
		rec := models.ServerConfig{Host: host, User: user, SSHKey: sshKey, Password: password, Port: 22}
		replaced := false
		for i, s := range v.Servers {
			if matchServer(s, host, user) {
				v.Servers[i] = rec
				replaced = true
			}
		}
		if !replaced {
			v.Servers = append(v.Servers, rec)
		}
		if err := saveVault(path, v); err != nil {
			return "", err
		}
		if replaced {
			return fmt.Sprintf("server %s@%s updated", user, host), nil
		}
		return fmt.Sprintf("server %s@%s added", user, host), nil
	case "remove":
		kept := v.Servers[:0]
		removed := 0
		for _, s := range v.Servers {
			if matchServer(s, host, user) {
				removed++
				continue
			}
			kept = append(kept, s)
		}
		if removed == 0 {
			return "", fmt.Errorf("no server record for %s@%s", user, host)
		}
		v.Servers = kept
		if err := saveVault(path, v); err != nil {
			return "", err
		}
		return fmt.Sprintf("server %s@%s removed", user, host), nil
	}
	return "", fmt.Errorf("unreachable")
}
