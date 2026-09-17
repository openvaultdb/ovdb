package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/strongo/cli-helpers/daemonlifecycle"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/paths"
)

// ErrAlreadyRunning reports that another server holds this home's lock.
var ErrAlreadyRunning = errors.New("another OVDB server holds the home lock")

// errStartBusy reports that another start or locked write kept start.lock
// for the whole wait.
var errStartBusy = errors.New("another OVDB server start is still in progress")

// homeLockWait bounds how long a new server waits for home.lock, which a
// stopping server or a locked config write may hold for a moment.
const homeLockWait = 3 * time.Second

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
	warnings, dirErr := PrepareDirs(dirs, uicopy.T("server.start.failed", nil))
	if dirErr != nil {
		return nil, warnings, dirErr
	}
	lock, err := lockFile(context.Background(), filepath.Join(dirs.Runtime, LockFile), homeLockWait)
	if errors.Is(err, errLocked) {
		err = ErrAlreadyRunning
	}
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

var errLocked = errors.New("lock is held")

// lockFile opens path and takes an exclusive advisory lock, waiting up to
// wait (trying once when wait is zero). It returns errLocked when the wait
// ends without the lock.
func lockFile(ctx context.Context, path string, wait time.Duration) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	locked, err := daemonlifecycle.TryLock(file)
	if err == nil && !locked && wait > 0 {
		waitCtx, cancel := context.WithTimeout(ctx, wait)
		err = daemonlifecycle.Lock(waitCtx, file, 25*time.Millisecond)
		cancel()
		locked = err == nil
		if errors.Is(err, context.DeadlineExceeded) {
			err = nil
		}
	}
	if err == nil && !locked {
		err = errLocked
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

func unlockFile(file *os.File) {
	_ = daemonlifecycle.Unlock(file)
	_ = file.Close()
}

// lockStart serializes everything that may become this home's writer:
// starts, and writes made while no server runs. Holding it, a caller that
// finds no running server knows no sibling is about to publish one.
func lockStart(ctx context.Context, runtimeDir string, wait time.Duration) (*os.File, error) {
	lock, err := lockFile(ctx, filepath.Join(runtimeDir, StartLockFile), wait)
	if errors.Is(err, errLocked) {
		return nil, errStartBusy
	}
	return lock, err
}

// WithHomeLock runs fn as the home's single writer while no server runs. It
// returns ErrAlreadyRunning when a server holds home.lock; the caller then
// goes through that server instead. failure is the message of a directory
// error; warnings about open directories are returned in every case.
func WithHomeLock(ctx context.Context, dirs paths.Dirs, failure string, fn func() error) ([]string, error) {
	warnings, dirErr := PrepareDirs(dirs, failure)
	if dirErr != nil {
		return warnings, dirErr
	}
	start, err := lockStart(ctx, dirs.Runtime, DefaultTimeout)
	if err != nil {
		return warnings, err
	}
	defer unlockFile(start)
	home, err := lockFile(ctx, filepath.Join(dirs.Runtime, LockFile), homeLockWait)
	if errors.Is(err, errLocked) {
		return warnings, ErrAlreadyRunning
	}
	if err != nil {
		return warnings, err
	}
	defer unlockFile(home)
	return warnings, fn()
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
	unlockFile(i.lock)
	i.lock = nil
}

func randomHex(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
