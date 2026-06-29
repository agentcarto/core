package transaction

import (
	"context"
	"fmt"
	"github.com/agentcarto/core/domain"
	"os"
	"path/filepath"
	"strings"
)

func Validate(p domain.MutationPlan) error {
	if len(p.Writes) == 0 && len(p.Moves) == 0 {
		return fmt.Errorf("empty mutation plan")
	}
	allowed := func(path string) bool {
		a, e := filepath.Abs(path)
		if e != nil {
			return false
		}
		for _, r := range p.AllowedRoots {
			rr, e := filepath.Abs(r)
			if e == nil && (a == rr || strings.HasPrefix(a, rr+string(filepath.Separator))) {
				return true
			}
		}
		return false
	}
	for _, w := range p.Writes {
		if !allowed(w.Path) {
			return fmt.Errorf("write outside allowed roots: %s", w.Path)
		}
	}
	for _, m := range p.Moves {
		if !allowed(m.From) || !allowed(m.To) {
			return fmt.Errorf("move outside allowed roots: %s -> %s", m.From, m.To)
		}
	}
	return nil
}

// backup captures the pre-write state of a path so a failed plan can be rolled
// back to it.
type backup struct {
	path    string
	data    []byte
	mode    os.FileMode
	existed bool
}

// captureBackup snapshots the current contents of path (when it exists) so it can
// be restored on rollback. A non-existent path yields a backup with existed=false.
func captureBackup(path string) (backup, error) {
	b := backup{path: path}
	st, e := os.Stat(path)
	if e != nil {
		if os.IsNotExist(e) {
			return b, nil
		}
		return b, e
	}
	b.existed = true
	b.mode = st.Mode()
	b.data, e = os.ReadFile(path)
	return b, e
}

func Apply(ctx context.Context, p domain.MutationPlan) (domain.MutationResult, error) {
	var r domain.MutationResult
	if e := Validate(p); e != nil {
		return r, e
	}
	var bs []backup
	rollback := func() {
		for i := len(bs) - 1; i >= 0; i-- {
			b := bs[i]
			if b.existed {
				_ = atomicWrite(b.path, b.data, b.mode)
			} else {
				_ = os.Remove(b.path)
			}
			r.RolledBack = append(r.RolledBack, b.path)
		}
	}
	for i, w := range p.Writes {
		select {
		case <-ctx.Done():
			rollback()
			return r, ctx.Err()
		default:
		}
		b, e := captureBackup(w.Path)
		if e != nil {
			rollback()
			return r, e
		}
		bs = append(bs, b)
		mode := os.FileMode(w.Mode)
		if mode == 0 {
			mode = 0600
		}
		if e = atomicWrite(w.Path, w.Data, mode); e != nil {
			for _, x := range p.Writes[i:] {
				r.Pending = append(r.Pending, x.Path)
			}
			rollback()
			return r, e
		}
		r.Completed = append(r.Completed, w.Path)
	}
	for i, m := range p.Moves {
		if e := moveMerge(m.From, m.To, &r); e != nil {
			for _, x := range p.Moves[i:] {
				r.Pending = append(r.Pending, x.From)
			}
			return r, e
		}
	}
	return r, nil
}
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".agentcarto-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(tmp, path)
}

// moveMerge moves from into to. If to already exists, entries are merged flatly,
// one level deep: it does not recurse, name collisions (whether file or
// directory) are skipped with a warning, and from is removed only once it is
// empty (any leftover residue is kept).
func moveMerge(from, to string, r *domain.MutationResult) error {
	if st, e := os.Stat(from); e != nil || !st.IsDir() {
		return nil // no-op if it does not exist or is not a directory
	}
	if _, e := os.Stat(to); os.IsNotExist(e) {
		if e = os.MkdirAll(filepath.Dir(to), 0700); e != nil {
			return e
		}
		if e = os.Rename(from, to); e != nil {
			return e
		}
		r.Completed = append(r.Completed, from+" -> "+to)
		return nil
	}
	entries, e := os.ReadDir(from)
	if e != nil {
		return e
	}
	for _, x := range entries {
		src, dst := filepath.Join(from, x.Name()), filepath.Join(to, x.Name())
		if _, de := os.Stat(dst); de == nil {
			r.Warnings = append(r.Warnings, "skipped (already exists): "+dst)
			continue
		}
		if e = os.Rename(src, dst); e != nil {
			return e
		}
		r.Completed = append(r.Completed, src+" -> "+dst)
	}
	_ = os.Remove(from) // remove if empty; leftover residue from skipped collisions stays (a failed rmdir is ignored)
	return nil
}
