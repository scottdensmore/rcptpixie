package rename

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

var (
	ErrTargetExists = errors.New("target already exists")
	ErrUnsafeName   = errors.New("refusing unsafe generated name")
)

// Apply renames it.OldPath to dir/it.NewName without ever overwriting anything.
func Apply(ctx context.Context, dir string, it Item, j *Journal) (newPath string, err error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !IsSafeBase(it.NewName) {
		return "", fmt.Errorf("%w: %q", ErrUnsafeName, it.NewName)
	}
	target := filepath.Join(dir, it.NewName)
	// Assertion, not a repair: unreachable if the sanitizer is correct.
	if filepath.Dir(target) != filepath.Clean(dir) || filepath.Base(target) != it.NewName {
		return "", fmt.Errorf("%w: %q", ErrUnsafeName, it.NewName)
	}
	if target == it.OldPath {
		return target, nil
	}

	oldBase := filepath.Base(it.OldPath)
	// A case-only rename on a case-INSENSITIVE filesystem would collide with the
	// source file itself, so it has to skip the claim. Matching on the names
	// alone is not enough to know that: on a case-SENSITIVE filesystem
	// "receipt.pdf" and "RECEIPT.pdf" are two different files, and skipping the
	// claim there lets os.Rename silently destroy the second one. Only an
	// identity check on the target proves it is really the source.
	if !sameFileOnDisk(it.OldPath, target) {
		// os.Rename silently destroys the destination on POSIX and Stat-then-Rename
		// is a TOCTOU race; an O_EXCL claim closes both holes.
		f, cerr := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if cerr != nil {
			if errors.Is(cerr, fs.ErrExist) {
				return "", fmt.Errorf("%w: %s", ErrTargetExists, target)
			}
			return "", cerr
		}
		f.Close()
		defer func() {
			if err != nil {
				os.Remove(target)
			}
		}()
	}

	if err = os.Rename(it.OldPath, target); err != nil {
		return "", err
	}

	// Journalled only after the rename lands. Writing ahead would leave a record
	// of a rename that never happened, and a later undo would follow it onto
	// whatever file happens to hold that name by then. The window this loses is
	// the microseconds between the rename and the append; the window it closes is
	// permanent.
	if jerr := j.Append(Entry{Time: time.Now(), Old: oldBase, New: it.NewName}); jerr != nil {
		j.logger().Warn("could not record rename for undo", "journal", j.Path(), "err", jerr)
	}
	return target, nil
}

// sameFileOnDisk reports whether target already resolves to the very same file
// as oldPath, which is what a case-only rename looks like on a case-insensitive
// filesystem. A missing target, or one that is a genuinely different file,
// reports false so the caller still stakes its O_EXCL claim.
func sameFileOnDisk(oldPath, target string) bool {
	ti, err := os.Lstat(target)
	if err != nil {
		return false
	}
	si, err := os.Lstat(oldPath)
	if err != nil {
		return false
	}
	return os.SameFile(si, ti)
}
