package runtime

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/strongo/cli-helpers/daemonlifecycle"

	"github.com/openvaultdb/ovdb/internal/paths"
)

// ErrAlreadyRunning reports that another server holds this home's lock.
var ErrAlreadyRunning = errors.New("another OVDB server holds the home lock")

// Instance is the server side of the runtime directory: it holds home.lock
// for the server's lifetime and owns this run's instance id and secret.
type Instance struct {
	Dirs       paths.Dirs
	InstanceID string
	Secret     string
	Version    string

	lock *os.File
}

// Acquire takes this home's lock and writes a fresh instance secret,
// replacing any stale server.json and secret a crashed server left behind
// (REQ:single-server-home-lock, REQ:stale-runtime-state). Warnings about
// directories accessible to other users are returned even on success.
func Acquire(dirs paths.Dirs, version string) (*Instance, []string, error) {
	warnings, dirErr := PrepareDirs(dirs)
	if dirErr != nil {
		return nil, warnings, dirErr
	}
	lock, err := lockHome(dirs.Runtime)
	if err != nil {
		return nil, warnings, err
	}
	instance := &Instance{Dirs: dirs, Version: version, lock: lock}
	if instance.InstanceID, err = randomHex(16); err == nil {
		instance.Secret, err = randomHex(32)
	}
	if err == nil {
		removeRuntimeFiles(dirs.Runtime)
		err = paths.WriteFilePrivate(filepath.Join(dirs.Runtime, SecretFile), []byte(instance.Secret))
	}
	if err != nil {
		instance.Release()
		return nil, warnings, err
	}
	return instance, warnings, nil
}

// lockHome opens and exclusively locks home.lock without waiting.
func lockHome(runtimeDir string) (*os.File, error) {
	path := filepath.Join(runtimeDir, LockFile)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	locked, err := daemonlifecycle.TryLock(file)
	if err == nil && !locked {
		err = ErrAlreadyRunning
	}
	if err == nil {
		// Protected after locking, so a racing loser never changes a file
		// the winner already validated.
		if err = daemonlifecycle.ProtectOwnerOnlyFile(file); err == nil {
			err = daemonlifecycle.ValidateOwnerOnlyFile(file)
		}
		if err != nil {
			_ = daemonlifecycle.Unlock(file)
		}
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// WithHomeLock runs fn while holding home.lock, for the rare writes that
// happen with no server running. It returns ErrAlreadyRunning when a server
// holds the lock; the caller then goes through that server instead.
func WithHomeLock(dirs paths.Dirs, fn func() error) error {
	if _, dirErr := PrepareDirs(dirs); dirErr != nil {
		return dirErr
	}
	lock, err := lockHome(dirs.Runtime)
	if err != nil {
		return err
	}
	defer func() {
		_ = daemonlifecycle.Unlock(lock)
		_ = lock.Close()
	}()
	return fn()
}

// NewRecord describes this process serving port since startedAt.
func (i *Instance) NewRecord(port int, startedAt time.Time) (Record, error) {
	pid := os.Getpid()
	identity, err := daemonlifecycle.ProcessIdentity(pid)
	if err != nil {
		return Record{}, fmt.Errorf("record process identity: %w", err)
	}
	return Record{
		Schema: 1, InstanceID: i.InstanceID, Home: i.Dirs.Home, PID: pid, ProcessIdentity: identity,
		Port: port, Version: i.Version, StartedAt: startedAt.UTC().Truncate(time.Second),
	}, nil
}

// Publish writes server.json once the server is listening. Clients treat
// its appearance, together with an authenticated whoami, as readiness.
func (i *Instance) Publish(record Record) error {
	return writeRecord(i.Dirs.Runtime, record)
}

// Release removes server.json and the secret and unlocks the home. It is
// safe to call more than once.
func (i *Instance) Release() {
	if i.lock == nil {
		return
	}
	removeRuntimeFiles(i.Dirs.Runtime)
	_ = daemonlifecycle.Unlock(i.lock)
	_ = i.lock.Close()
	i.lock = nil
}

func randomHex(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
