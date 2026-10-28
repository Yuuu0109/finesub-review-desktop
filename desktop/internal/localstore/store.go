// Package localstore stores non-secret desktop settings and drafts in the
// application's data directory. Authentication tokens belong in Secrets.
package localstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// Store uses one JSON file per key. Keys can contain server URLs and entry IDs;
// hashing them prevents either of those from becoming a filesystem path.
type Store struct {
	dir string
	mu  sync.Mutex
}

func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("本地数据目录为空")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("创建本地数据目录: %w", err)
	}
	return &Store{dir: abs}, nil
}

func (s *Store) filename(key string) string {
	hash := sha256.Sum256([]byte(key))
	return filepath.Join(s.dir, hex.EncodeToString(hash[:])+".json")
}

// Get returns os.ErrNotExist when a setting or draft has not been saved.
// Malformed data is reported, never silently replaced with an empty draft.
func (s *Store) Get(key string, out any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.filename(key))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("读取本地数据: %w", err)
	}
	return nil
}

// Set syncs a complete temporary file before replacing the previous value.
// An interrupted or failed save leaves the previous draft intact.
func (s *Store) Set(key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("序列化本地数据: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.CreateTemp(s.dir, ".saving-*")
	if err != nil {
		return fmt.Errorf("保存本地数据: %w", err)
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := ReplaceFile(name, s.filename(key)); err != nil {
		return fmt.Errorf("替换本地数据: %w", err)
	}
	return nil
}

func (s *Store) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(s.filename(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
