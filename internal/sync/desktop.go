package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/tawanorg/claude-sync/internal/config"
)

// DesktopEntries maps a CLI session id to the Claude desktop app's raw sidebar
// entry for it. Raw JSON preserves fields this tool doesn't know about.
type DesktopEntries map[string]json.RawMessage

// DesktopPushResult describes the outcome of pushing desktop chat entries.
type DesktopPushResult struct {
	Pushed       int
	Unchanged    bool
	NoDesktopDir bool
}

// DesktopPullResult describes the outcome of pulling desktop chat entries.
type DesktopPullResult struct {
	Added        []string // titles of chats added to the sidebar
	Waiting      int      // entries whose transcript hasn't been pulled yet
	NoRemote     bool
	NoDesktopDir bool
}

// desktopContentPath gives path rewriting the JSON escaping rules.
const desktopContentPath = "desktop-sessions.json"

// Ids become file names and glob patterns, so only safe characters pass.
var (
	desktopCLIIDRe     = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
	desktopSessionIDRe = regexp.MustCompile(`^local_[A-Za-z0-9-]+$`)
)

type desktopEntryHeader struct {
	SessionID    string `json:"sessionId"`
	CLISessionID string `json:"cliSessionId"`
	Title        string `json:"title"`
}

// ReadDesktopEntries reads every sidebar entry under dir/<account>/<org>/.
// writeDir is the folder holding the most recently modified entry, which is
// where new entries should go; it is empty when there are no entries yet.
func ReadDesktopEntries(dir string) (entries DesktopEntries, writeDir string, err error) {
	files, err := filepath.Glob(filepath.Join(dir, "*", "*", "local_*.json"))
	if err != nil {
		return nil, "", err
	}
	entries = make(DesktopEntries)
	var newest time.Time
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, "", fmt.Errorf("failed to read %s: %w", f, err)
		}
		var h desktopEntryHeader
		if json.Unmarshal(data, &h) != nil || !desktopCLIIDRe.MatchString(h.CLISessionID) {
			continue // not a chat entry we understand; leave it alone
		}
		entries[h.CLISessionID] = data
		if fi, err := os.Stat(f); err == nil && fi.ModTime().After(newest) {
			newest, writeDir = fi.ModTime(), filepath.Dir(f)
		}
	}
	return entries, writeDir, nil
}

// downloadDesktopEntries returns the remote entries, or nil when none were
// pushed yet. A failed lookup is an error, never "empty", so push can't wipe
// another device's entries over a network hiccup.
func (s *Syncer) downloadDesktopEntries(ctx context.Context) (DesktopEntries, error) {
	key := config.DesktopRemoteKey + ".age"
	objs, err := s.storage.List(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to check remote desktop entries: %w", err)
	}
	found := false
	for _, o := range objs {
		found = found || o.Key == key
	}
	if !found {
		return nil, nil
	}
	data, err := s.decryptRemote(ctx, key)
	if err != nil {
		return nil, err
	}
	var remote DesktopEntries
	if err := json.Unmarshal(data, &remote); err != nil {
		return nil, fmt.Errorf("failed to parse remote desktop entries: %w", err)
	}
	return remote, nil
}

func (s *Syncer) decryptRemote(ctx context.Context, key string) ([]byte, error) {
	encrypted, err := s.storage.Download(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to download %s: %w", key, err)
	}
	data, err := s.encryptor.Decrypt(encrypted)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt %s: %w", key, err)
	}
	if isGzipped(data) {
		if data, err = gzipDecompress(data); err != nil {
			return nil, fmt.Errorf("failed to decompress %s: %w", key, err)
		}
	}
	return data, nil
}

// PushDesktop merges this device's desktop chat entries into the remote set.
// Entries from other devices are kept, so devices never overwrite each other.
func (s *Syncer) PushDesktop(ctx context.Context) (*DesktopPushResult, error) {
	dir := s.cfg.GetDesktopSessionsDir()
	if dir == "" {
		return &DesktopPushResult{NoDesktopDir: true}, nil
	}
	local, _, err := ReadDesktopEntries(dir)
	if err != nil {
		return nil, err
	}
	remote, err := s.downloadDesktopEntries(ctx)
	if err != nil {
		return nil, err
	}

	merged := make(DesktopEntries, len(remote)+len(local))
	for id, raw := range remote {
		merged[id] = raw
	}
	for id, raw := range local {
		merged[id] = s.paths.NormalizeContent(desktopContentPath, raw)
		s.state.markDesktopSeen(id)
	}

	// encoding/json sorts map keys, so equal sets marshal identically
	before, _ := json.Marshal(remote)
	data, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize desktop entries: %w", err)
	}
	result := &DesktopPushResult{Pushed: len(local)}
	if remote != nil && string(before) == string(data) {
		result.Unchanged = true
	} else {
		compressed, err := gzipCompress(data)
		if err != nil {
			return nil, fmt.Errorf("failed to compress: %w", err)
		}
		encrypted, err := s.encryptor.Encrypt(compressed)
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt: %w", err)
		}
		if err := s.storage.Upload(ctx, config.DesktopRemoteKey+".age", encrypted); err != nil {
			return nil, fmt.Errorf("failed to upload desktop entries: %w", err)
		}
	}

	if err := s.state.Save(); err != nil {
		return nil, fmt.Errorf("failed to save state: %w", err)
	}
	return result, nil
}

// PullDesktop adds sidebar entries for remote desktop chats whose transcript
// is present locally. It only creates files: existing entries are never
// changed, and chats this device has seen before (then deleted) are skipped.
func (s *Syncer) PullDesktop(ctx context.Context) (*DesktopPullResult, error) {
	dir := s.cfg.GetDesktopSessionsDir()
	if dir == "" {
		return &DesktopPullResult{NoDesktopDir: true}, nil
	}
	remote, err := s.downloadDesktopEntries(ctx)
	if err != nil {
		return nil, err
	}
	if remote == nil {
		return &DesktopPullResult{NoRemote: true}, nil
	}
	local, writeDir, err := ReadDesktopEntries(dir)
	if err != nil {
		return nil, err
	}
	// The <account>/<org> folder only exists once the app has saved a chat
	if writeDir == "" {
		return &DesktopPullResult{NoDesktopDir: true}, nil
	}

	ids := make([]string, 0, len(remote))
	for id := range remote {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	result := &DesktopPullResult{}
	for _, id := range ids {
		if _, ok := local[id]; ok || s.state.isDesktopSeen(id) || !desktopCLIIDRe.MatchString(id) {
			continue
		}
		if !s.hasTranscript(id) {
			result.Waiting++
			continue
		}
		raw := s.paths.ResolveContent(desktopContentPath, remote[id])
		var h desktopEntryHeader
		if json.Unmarshal(raw, &h) != nil || h.CLISessionID != id || !desktopSessionIDRe.MatchString(h.SessionID) {
			continue
		}
		f, err := os.OpenFile(filepath.Join(writeDir, h.SessionID+".json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if os.IsExist(err) {
			s.state.markDesktopSeen(id)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("failed to create desktop entry: %w", err)
		}
		_, werr := f.Write(raw)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return nil, fmt.Errorf("failed to write desktop entry: %w", werr)
		}
		s.state.markDesktopSeen(id)
		result.Added = append(result.Added, h.Title)
	}

	if err := s.state.Save(); err != nil {
		return nil, fmt.Errorf("failed to save state: %w", err)
	}
	return result, nil
}

// hasTranscript reports whether ~/.claude/projects/*/<id>.jsonl exists.
func (s *Syncer) hasTranscript(id string) bool {
	m, _ := filepath.Glob(filepath.Join(s.claudeDir, "projects", "*", id+".jsonl"))
	return len(m) > 0
}

func (s *SyncState) markDesktopSeen(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := sort.SearchStrings(s.DesktopSeen, id)
	if i < len(s.DesktopSeen) && s.DesktopSeen[i] == id {
		return
	}
	s.DesktopSeen = append(s.DesktopSeen, "")
	copy(s.DesktopSeen[i+1:], s.DesktopSeen[i:])
	s.DesktopSeen[i] = id
}

func (s *SyncState) isDesktopSeen(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := sort.SearchStrings(s.DesktopSeen, id)
	return i < len(s.DesktopSeen) && s.DesktopSeen[i] == id
}
