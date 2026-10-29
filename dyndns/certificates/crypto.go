package certificates

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
)

// LoadKey persists a separate encryption key with the database backup, never in the image.
func LoadKey(path, configured string) ([]byte, error) {
	if configured != "" {
		key, e := hex.DecodeString(configured)
		if e != nil || len(key) != 32 {
			return nil, fmt.Errorf("DDNS_CERT_KEY must be 64 hexadecimal characters")
		}
		return key, nil
	}
	key, e := os.ReadFile(path)
	if e == nil {
		if len(key) != 32 {
			return nil, fmt.Errorf("invalid certificate key file")
		}
		return key, os.Chmod(path, 0600)
	}
	if !os.IsNotExist(e) {
		return nil, e
	}
	key = make([]byte, 32)
	if _, e = rand.Read(key); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return nil, e
	}
	_, e = f.Write(key)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	return key, e
}
func seal(key, data []byte, identity string) ([]byte, error) {
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, g.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	return g.Seal(nonce, nonce, data, []byte(identity)), nil
}
func unseal(key, data []byte, identity string) ([]byte, error) {
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	if len(data) < g.NonceSize() {
		return nil, fmt.Errorf("invalid encrypted key")
	}
	return g.Open(nil, data[:g.NonceSize()], data[g.NonceSize():], []byte(identity))
}
