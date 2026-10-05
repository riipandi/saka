package health

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// CheckNameStorage is the name of the data directory check in a result.
const CheckNameStorage = "storage"

// DefaultStorageTimeout bounds the data directory probe.
const DefaultStorageTimeout = 2 * time.Second

// StorageCheck reports whether the application data directory can be used.
//
// The report carries no directory path: the endpoint publishes this result,
// and a filesystem layout is not something an unauthenticated reader should
// learn. The CLI report, which an operator who owns the machine reads, uses
// StorageCheckWithTarget instead.
//
// The probe resolves dir to an absolute path before it runs, so the check
// does not depend on the working directory of the process.
//
// It fails when the directory is missing, is not a directory, cannot be written
// to, or is world-writable.
//
// Writability is tested by writing and removing a temporary file. Mode bits
// alone are not enough: an ACL, a read-only mount, or a full disk lets a
// directory that looks writable reject the write, and the application would
// only find out when it tried to store something.
//
// World-writable is a failure rather than a note, because this directory holds
// uploads and certificates, so any local user could replace them.
func StorageCheck(dir string) Check {
	return storageCheck(dir, "")
}

// StorageCheckWithTarget is StorageCheck with the resolved directory path in
// the result's target, for a surface an operator reads directly. The REST
// endpoint publishes the plain check instead: a filesystem layout is not
// something an unauthenticated reader should learn.
func StorageCheckWithTarget(dir string) Check {
	return storageCheck(dir, absolutePath(dir))
}

// storageCheck builds the probe. resolved is the absolute path the check
// runs against; target is what the report names, empty to name nothing.
func storageCheck(dir, target string) Check {
	resolved := absolutePath(dir)
	return Check{
		Name:    CheckNameStorage,
		Target:  target,
		Timeout: DefaultStorageTimeout,
		Check: func(context.Context) error {
			return checkDataDir(resolved)
		},
	}
}

// absolutePath resolves dir against the working directory. It keeps the input
// when the working directory cannot be read, so the check still probes a path
// instead of nothing.
func absolutePath(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return abs
}

// checkDataDir returns the first problem that makes the directory unusable.
// The error messages name the problem, never the path: the message travels
// into the published report, the path does not.
func checkDataDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("storage: directory does not exist")
		}
		return fmt.Errorf("storage: stat: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("storage: not a directory")
	}
	if mode := info.Mode().Perm(); mode&0o002 != 0 {
		return fmt.Errorf("storage: directory mode %04o is world-writable", mode)
	}
	return probeWritable(dir)
}

// probeWritable writes and removes a temporary file. The file is removed on
// every path, so a failed probe does not leave anything behind.
func probeWritable(dir string) error {
	probe, err := os.CreateTemp(dir, ".health-write-probe-*")
	if err != nil {
		return fmt.Errorf("storage: directory is not writable: %w", err)
	}

	name := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("storage: close write probe: %w", err)
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("storage: remove write probe: %w", err)
	}
	return nil
}
