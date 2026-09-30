package mcp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/scrypt"

	"easydrop/internal/models"
)

// Vault encryption parameters (locked): AES-256-GCM, key derived with
// scrypt(N=32768, r=8, p=1). The password comes from EASYDROP_VAULT_PASSWORD
// only — no fallback, no prompt (MCP is non-interactive). Fail closed.
const (
	scryptN = 32768
	scryptR = 8
	scryptP = 1
	keyLen  = 32
)

type vaultFile struct {
	V     int    `json:"v"`
	Salt  string `json:"salt"`
	Nonce string `json:"nonce"`
	Data  string `json:"data"`
}

func vaultPath() (string, error) {
	if p := os.Getenv("EASYDROP_SERVERS_FILE"); p != "" {
		if strings.HasSuffix(p, ".toml") {
			// Legacy plaintext path in tests/envs: keep the sibling name.
			return strings.TrimSuffix(p, ".toml") + ".vault", nil
		}
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".easydrop", "servers.vault"), nil
}

func vaultPassword() (string, error) {
	if pw := os.Getenv("EASYDROP_VAULT_PASSWORD"); pw != "" {
		return pw, nil
	}
	return "", fmt.Errorf("EASYDROP_VAULT_PASSWORD is not set: the server store is encrypted, export a vault password first")
}

func deriveKey(password string, salt []byte) ([]byte, error) {
	key, err := scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, keyLen)
	if err != nil {
		return nil, fmt.Errorf("derive vault key: %w", err)
	}
	return key, nil
}

func sealRecords(password string, records []models.ServerConfig) ([]byte, error) {
	plain, err := json.Marshal(records)
	if err != nil {
		return nil, fmt.Errorf("marshal vault payload: %w", err)
	}
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}
	key, err := deriveKey(password, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("init cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("init gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	out, err := json.Marshal(vaultFile{
		V:     1,
		Salt:  base64.StdEncoding.EncodeToString(salt),
		Nonce: base64.StdEncoding.EncodeToString(nonce),
		Data:  base64.StdEncoding.EncodeToString(gcm.Seal(nil, nonce, plain, nil)),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal vault file: %w", err)
	}
	return out, nil
}

func openRecords(password string, blob []byte) ([]models.ServerConfig, error) {
	var vf vaultFile
	if err := json.Unmarshal(blob, &vf); err != nil {
		return nil, fmt.Errorf("parse vault file (wrong password or corrupt file): %w", err)
	}
	if vf.V != 1 {
		return nil, fmt.Errorf("unsupported vault version %d", vf.V)
	}
	salt, err := base64.StdEncoding.DecodeString(vf.Salt)
	if err != nil {
		return nil, fmt.Errorf("decode vault salt: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(vf.Nonce)
	if err != nil {
		return nil, fmt.Errorf("decode vault nonce: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(vf.Data)
	if err != nil {
		return nil, fmt.Errorf("decode vault payload: %w", err)
	}
	key, err := deriveKey(password, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("init cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("init gcm: %w", err)
	}
	plain, err := gcm.Open(nil, nonce, data, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt vault (wrong password or corrupt file): %w", err)
	}
	var records []models.ServerConfig
	if err := json.Unmarshal(plain, &records); err != nil {
		return nil, fmt.Errorf("parse vault payload: %w", err)
	}
	return records, nil
}
