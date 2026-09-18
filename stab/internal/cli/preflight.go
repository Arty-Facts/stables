package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// wOK is the POSIX access(2) write-permission flag. Go's syscall package
// provides Access but not the R_OK/W_OK/X_OK constants, so the flag is named
// here (W_OK == 2 on all POSIX platforms).
const wOK = 2

// homeStateDirs lists the stab-managed directories under home that
// installations need. Every one is created by stab or stables; resetting
// them never deletes user documents. Note: ~/.pi/agent also holds pi's
// provider credentials (auth.json), so a full reset means re-logging in.
func homeStateDirs(home string) []string {
	return []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".pi", "agent"),
		filepath.Join(home, ".stables"),
	}
}

// fsResult summarises one ensured directory.
type fsResult struct {
	Path   string
	Status string // "ok", "reset", or "fail"
	Detail string // what was done, or the actionable failure reason
}

// ensureHomeDirs verifies each stab-managed home dir exists and is
// writable by us. Repair recovers blocked paths without sudo, in order of
// destructiveness:
//
//  1. we own the path but lack the write bit (a stripped mode): fix the mode,
//     preserving everything inside;
//  2. the path is foreign-owned and deletable (parent writable by us): remove
//     just the stab-owned artifact and recreate it.
//
// If neither works, the caller gets the exact path and the minimal fix.
func ensureHomeDirs(home string, repair bool) []fsResult {
	var results []fsResult
	for _, dir := range homeStateDirs(home) {
		if os.MkdirAll(dir, dirMode(dir)) == nil && syscall.Access(dir, wOK) == nil {
			results = append(results, fsResult{Path: dir, Status: "ok"})
			continue
		}
		if !repair {
			results = append(results, fsResult{Path: dir, Status: "fail", Detail: ownershipHint(dir, hintTarget(home, dir))})
			continue
		}
		fixed, action, rerr := resetStabDir(home, dir)
		if rerr == nil {
			results = append(results, fsResult{Path: dir, Status: "reset", Detail: action + " " + fixed + sensitiveNote(dir)})
			continue
		}
		results = append(results, fsResult{Path: dir, Status: "fail",
			Detail: ownershipHint(dir, hintTarget(home, dir)) + "\n    repair attempt failed: " + rerr.Error()})
	}
	return results
}

// dirMode is the canonical mode for each state dir (secrets stay tight).
func dirMode(dir string) os.FileMode {
	if filepath.Base(dir) == ".stables" {
		return 0o700
	}
	return 0o755
}

// hintTarget is the specific artifact a user should delete if the blocked dir
// is not repairable: never the user's whole bin dir, only stab's binary.
func hintTarget(home, dir string) string {
	if ts := safeResetTargets(home, dir); len(ts) > 0 {
		return ts[0]
	}
	return dir
}

// safeResetTargets maps a blocked dir to the ordered deletions acceptable when
// repairing (most specific first). Only stab-owned artifacts ever go.
func safeResetTargets(home, dir string) []string {
	home = filepath.Clean(home)
	switch filepath.Clean(dir) {
	case filepath.Join(home, ".pi", "agent"):
		// ~/.pi holds only pi's agent state in this product; removing it (and
		// only it) is safe and keeps foreign files in the rest of home.
		return []string{
			filepath.Join(home, ".pi", "agent"),
			filepath.Join(home, ".pi"),
		}
	case filepath.Join(home, ".local", "bin"):
		// Never remove the user's whole bin dir — only stab's binary.
		return []string{filepath.Join(home, ".local", "bin", "stab")}
	case filepath.Join(home, ".stables"):
		return []string{dir}
	default:
		return nil
	}
}

// resetStabDir repairs a blocked dir. Order of preference:
//  1. we own the blocked dir itself and it is only mode-broken: chmod it,
//     preserving everything inside (least destructive);
//  2. the dir is foreign-owned: delete the whitelisted stab artifact(s)
//     that sit inside and recreate the dir.
//
// Returns the path touched and a short action description.
func resetStabDir(home, dir string) (fixed, action string, err error) {
	if st, serr := os.Stat(dir); serr == nil && owns(st) && syscall.Access(dir, wOK) != nil {
		if cerr := os.Chmod(dir, dirMode(dir)); cerr == nil && syscall.Access(dir, wOK) == nil {
			return dir, "repaired mode of", nil
		}
	}
	targets := safeResetTargets(home, dir)
	if len(targets) == 0 {
		return "", "", fmt.Errorf("no safe reset target for %s", dir)
	}
	var tried []string
	for _, t := range targets {
		_, sErr := os.Stat(t)
		if sErr != nil && !errors.Is(sErr, os.ErrNotExist) {
			tried = append(tried, fmt.Sprintf("%s (%v)", t, sErr))
			continue
		}
		if sErr == nil { // target exists: delete it
			if err := os.RemoveAll(t); err != nil {
				// A parent we own may be mode-broken; unblock it and retry.
				if !unblockOwnedAncestors(home, t) {
					tried = append(tried, fmt.Sprintf("%s (%v)", t, err))
					continue
				}
				if err := os.RemoveAll(t); err != nil {
					tried = append(tried, fmt.Sprintf("%s (%v after unblocking)", t, err))
					continue
				}
			}
		}
		if err := os.MkdirAll(dir, dirMode(dir)); err != nil {
			return "", "", fmt.Errorf("removed %s but cannot recreate %s: %v", t, dir, err)
		}
		return t, "removed stale", nil
	}
	return "", "", fmt.Errorf("could not touch any of: %v", tried)
}

// unblockOwnedAncestors chmods (upwards, stopping at home) any ancestor of
// path that we own but cannot write into. Reports whether anything was done.
func unblockOwnedAncestors(home, path string) bool {
	home = filepath.Clean(home)
	p := filepath.Dir(filepath.Clean(path))
	any := false
	for p != "/" && !strings.HasPrefix(p, home+string(os.PathSeparator)) {
		if st, err := os.Stat(p); err == nil && owns(st) && syscall.Access(p, wOK) != nil {
			if os.Chmod(p, 0o755) == nil {
				any = true
			}
		}
		p = filepath.Dir(p)
		if p == home {
			break
		}
	}
	return any
}

// owns reports whether the file is owned by our uid (optimistic when the
// platform does not expose ownership).
func owns(st os.FileInfo) bool {
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		return int(sys.Uid) == os.Getuid()
	}
	return true
}

// sensitiveNote flags a reset that destroys cached pi credentials, so every
// caller (install, check --repair, stables) surfaces the warning uniformly.
func sensitiveNote(dir string) string {
	slash := filepath.ToSlash(filepath.Clean(dir))
	if strings.HasSuffix(slash, ".pi/agent") || strings.HasSuffix(slash, ".pi") {
		return " [note: pi provider logins cached there must be done again]"
	}
	return ""
}

// ownershipHint explains a permission failure with the concrete no-sudo fix:
// a chmod for dirs we own, a targeted delete for foreign-owned ones (or an
// admin chown when the user cannot delete it).
func ownershipHint(path, target string) string {
	ourUID, ourGID := os.Getuid(), os.Getgid()
	if st, err := os.Stat(path); err == nil {
		if sys, ok := st.Sys().(*syscall.Stat_t); ok {
			if int(sys.Uid) == ourUID {
				return fmt.Sprintf(
					"permission denied on %s (yours, but not writable)\n"+
						"    fix without sudo:  chmod u+w '%s'\n"+
						"    then re-run this command",
					path, path)
			}
			return fmt.Sprintf(
				"permission denied on %s (owned by uid %d:%d, you are uid %d:%d)\n"+
					"    fix without sudo, if you can delete it:  rm -rf '%s'\n"+
					"    then re-run this command (stab recreates it).\n"+
					"    if you cannot delete it, an admin on that machine can:  chown %d:%d '%s'  (or: rm -rf '%s')",
				path, sys.Uid, sys.Gid, ourUID, ourGID,
				target, ourUID, ourGID, target, target)
		}
	}
	return "permission denied on " + path
}
