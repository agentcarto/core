// Package scan provides helpers a plugin uses for incremental scanning: skipping
// re-parsing by reusing the previous result. The fingerprint (a hash of
// path:size:mtime) computation and the reuse/skip/dead decisions all happen
// inside the plugin process, so no per-path round trip across the subprocess
// boundary is needed.
package scan

import (
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentcarto/core/domain"
)

// Cache is used only during a single Scan. It holds the previous snapshot
// (warm/dead) and, in the scan loop, callers invoke Reuse/Skip/Dead/Stamp to
// avoid re-parsing. It is not safe for concurrent use: a plugin that scans
// paths from multiple goroutines must guard the Cache with its own mutex.
type Cache struct {
	warm map[string]domain.Session // path -> previous Session
	dead map[string]string         // previous negative cache (path -> fingerprint)
	pv   string                    // ParserVersion
	out  map[string]string         // this run's negative cache (collected and returned by DeadOut)
	fpc  map[string]string         // fingerprint memo (limits lstat of the same path to once)
}

// New builds a Cache from the ScanInput data (warm/dead) and the plugin's
// ParserVersion.
func New(warm []domain.Session, dead map[string]string, parserVersion string) *Cache {
	w := make(map[string]domain.Session, len(warm))
	for _, s := range warm {
		if s.SourceRef.Source == "" {
			continue
		}
		// Clear the volatile fields (Status/PermissionWait); DetectActive
		// recomputes them.
		s.Status = ""
		s.PermissionWait = false
		w[s.SourceRef.Source] = s
	}
	if dead == nil {
		dead = map[string]string{}
	}
	return &Cache{warm: w, dead: dead, pv: parserVersion, out: map[string]string{}, fpc: map[string]string{}}
}

func (c *Cache) fp(path string) string {
	if v, ok := c.fpc[path]; ok {
		return v
	}
	v := Fingerprint(path)
	c.fpc[path] = v
	return v
}

// Reuse returns the previous Session for path (skipping a parse) when one exists
// and both its ParserVersion and Fingerprint still match.
func (c *Cache) Reuse(path string) (domain.Session, bool) {
	s, ok := c.warm[path]
	if !ok || s.ParserVersion != c.pv {
		return domain.Session{}, false
	}
	if s.Fingerprint != c.fp(path) {
		return domain.Session{}, false
	}
	return s, true
}

// Skip returns true (skipping a parse) when a path that previously parsed into no
// session is unchanged. It also records the path in out so the entry is carried
// forward.
func (c *Cache) Skip(path string) bool {
	v, ok := c.dead[path]
	if !ok || v != c.fp(path) {
		return false
	}
	c.out[path] = v
	return true
}

// Dead records in the negative cache a path that was parsed but produced no
// session.
func (c *Cache) Dead(path string) { c.out[path] = c.fp(path) }

// Stamp fills in ParserVersion/Fingerprint on a newly parsed Session. A Session
// returned by Reuse already has them set, so there is no need to call Stamp on it.
func (c *Cache) Stamp(s *domain.Session) {
	if s.ParserVersion == "" {
		s.ParserVersion = c.pv
		s.Fingerprint = c.fp(s.SourceRef.Source)
	}
}

// DeadOut returns the negative cache collected during this run (to be placed in
// ScanOutput.Dead).
func (c *Cache) DeadOut() map[string]string { return c.out }

// Fingerprint is the FNV hash of path's "path:size:mtime". For file-based
// sessions (claude/codex/copilot) it takes a single lstat; for directory-based
// sessions (grok), or on error, it walks the whole tree. Because it relies only
// on lstat, it yields the same value whether computed by the host or the plugin.
func Fingerprint(path string) string {
	if _, e := os.Lstat(path); e != nil {
		if base, _, ok := strings.Cut(path, "#"); ok {
			if _, be := os.Lstat(base); be == nil {
				path = base
			}
		}
	}
	h := fnv.New64a()
	if st, e := os.Lstat(path); e == nil && !st.IsDir() {
		_, _ = io.WriteString(h, path)
		_, _ = io.WriteString(h, fmt.Sprintf(":%d:%d", st.Size(), st.ModTime().UnixNano()))
		return fmt.Sprintf("%x", h.Sum64())
	}
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return nil
		}
		st, e := d.Info()
		if e != nil {
			return nil
		}
		_, _ = io.WriteString(h, p)
		_, _ = io.WriteString(h, fmt.Sprintf(":%d:%d", st.Size(), st.ModTime().UnixNano()))
		return nil
	})
	return fmt.Sprintf("%x", h.Sum64())
}
