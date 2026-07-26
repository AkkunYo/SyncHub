package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

var ErrLocked = errors.New("config is locked by another process")

var processLocks = struct {
	sync.Mutex
	paths map[string]struct{}
}{paths: make(map[string]struct{})}

type Store struct {
	path     string
	lockPath string
	lockFile *os.File

	updateMu sync.Mutex
	mu       sync.RWMutex
	config   Config
	closed   bool
}

func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(io.LimitReader(file, 8<<20))
	decoder.KnownFields(true)
	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if err := Validate(&cfg); err != nil {
		return Config{}, fmt.Errorf("validate config: %w", err)
	}
	return cfg, nil
}

func Open(path string) (*Store, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}
	absolutePath = filepath.Clean(absolutePath)
	if !claimProcessLock(absolutePath) {
		return nil, ErrLocked
	}
	releaseClaim := true
	defer func() {
		if releaseClaim {
			releaseProcessLock(absolutePath)
		}
	}()

	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o700); err != nil {
		return nil, fmt.Errorf("create config directory: %w", err)
	}
	lockPath := absolutePath + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open config lock: %w", err)
	}
	if err := lockFile.Chmod(0o600); err != nil {
		_ = lockFile.Close()
		return nil, fmt.Errorf("secure config lock: %w", err)
	}
	if err := lockConfigFile(lockFile); err != nil {
		_ = lockFile.Close()
		if errors.Is(err, ErrLocked) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("lock config: %w", err)
	}
	releaseFile := true
	defer func() {
		if releaseFile {
			_ = closeConfigLock(lockFile)
		}
	}()

	store := &Store{path: absolutePath, lockPath: lockPath, lockFile: lockFile}
	cfg, err := Load(absolutePath)
	if errors.Is(err, os.ErrNotExist) {
		cfg = Default()
		if writeErr := writeAtomic(absolutePath, cfg); writeErr != nil {
			return nil, writeErr
		}
	} else if err != nil {
		return nil, err
	}
	if err := os.Chmod(absolutePath, 0o600); err != nil {
		return nil, fmt.Errorf("secure config file: %w", err)
	}
	store.config = deepCopy(cfg)
	releaseFile = false
	releaseClaim = false
	return store, nil
}

func (s *Store) Snapshot() Config {
	if s == nil {
		return Config{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return deepCopy(s.config)
}

func (s *Store) Update(ctx context.Context, mutate func(*Config) error) error {
	if s == nil {
		return errors.New("config store is nil")
	}
	if mutate == nil {
		return errors.New("config mutator is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return errors.New("config store is closed")
	}
	next := deepCopy(s.config)
	s.mu.RUnlock()

	if err := mutate(&next); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := Validate(&next); err != nil {
		return err
	}
	if err := writeAtomic(s.path, next); err != nil {
		return err
	}

	s.mu.Lock()
	s.config = deepCopy(next)
	s.mu.Unlock()
	return nil
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	lockFile := s.lockFile
	s.lockFile = nil
	s.mu.Unlock()

	var result error
	if lockFile != nil {
		result = closeConfigLock(lockFile)
	}
	releaseProcessLock(s.path)
	return result
}

func closeConfigLock(lockFile *os.File) error {
	var result error
	if err := unlockConfigFile(lockFile); err != nil {
		result = fmt.Errorf("unlock config: %w", err)
	}
	if err := lockFile.Close(); err != nil && result == nil {
		result = fmt.Errorf("close config lock: %w", err)
	}
	return result
}

func writeAtomic(path string, cfg Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("secure temporary config: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	if err := syncConfigDirectory(directory); err != nil {
		return fmt.Errorf("sync config directory: %w", err)
	}
	return nil
}

func claimProcessLock(path string) bool {
	processLocks.Lock()
	defer processLocks.Unlock()
	if _, exists := processLocks.paths[path]; exists {
		return false
	}
	processLocks.paths[path] = struct{}{}
	return true
}

func releaseProcessLock(path string) {
	processLocks.Lock()
	delete(processLocks.paths, path)
	processLocks.Unlock()
}
