package sync

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/tawanorg/claude-sync/internal/config"
)

// DesktopEntries maps a CLI session id to the Claude desktop app's raw sidebar
// entry for it. Raw JSON preserves fields this tool doesn't know about.
type DesktopEntries map[string]json.RawMessage

// DesktopPushResult describes the outcome of pushing desktop chat entries.
type DesktopPushResult struct {
	Pushed       int
	Generated    []string // titles of entries built from VS Code/desktop transcripts
	Unchanged    bool
	NoDesktopDir bool
}

// DesktopPullResult describes the outcome of pulling desktop chat entries.
type DesktopPullResult struct {
	Added        []string // titles of chats added to the sidebar
	Generated    []string // titles of entries built from VS Code/desktop transcripts
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
// Local VS Code/desktop transcripts without an entry get one first, so they
// show up here right away and other devices receive the finished entry.
func (s *Syncer) PushDesktop(ctx context.Context) (*DesktopPushResult, error) {
	dir := s.cfg.GetDesktopSessionsDir()
	if dir == "" {
		return &DesktopPushResult{NoDesktopDir: true}, nil
	}
	_, writeDir, err := ReadDesktopEntries(dir)
	if err != nil {
		return nil, err
	}
	var generated []string
	if writeDir != "" {
		if generated, err = s.adoptTranscripts(writeDir); err != nil {
			return nil, err
		}
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
		s.state.markDesktopSeen(id)
		// Chats started in the CLI stay out of every sidebar
		if s.isCLIChat(id) {
			delete(local, id)
			continue
		}
		merged[id] = s.paths.NormalizeContent(desktopContentPath, raw)
	}

	// encoding/json sorts map keys, so equal sets marshal identically
	before, _ := json.Marshal(remote)
	data, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize desktop entries: %w", err)
	}
	result := &DesktopPushResult{Pushed: len(local), Generated: generated}
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

	// No remote yet still adopts local VS Code/desktop transcripts below
	result := &DesktopPullResult{NoRemote: remote == nil}
	for _, id := range ids {
		if _, ok := local[id]; ok || s.state.isDesktopSeen(id) || !desktopCLIIDRe.MatchString(id) {
			continue
		}
		if !s.hasTranscript(id) {
			result.Waiting++
			continue
		}
		// Older builds pushed every entry, CLI chats included
		if s.isCLIChat(id) {
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

	if result.Generated, err = s.adoptTranscripts(writeDir); err != nil {
		return nil, err
	}

	if err := s.state.Save(); err != nil {
		return nil, fmt.Errorf("failed to save state: %w", err)
	}
	return result, nil
}

// sidebarEntrypoints are the apps whose sessions belong in the desktop sidebar.
// CLI and SDK sessions ("cli", "sdk-cli", ...) are left out, even when later
// resumed in the desktop app: what counts is where the chat started.
var sidebarEntrypoints = map[string]bool{"claude-desktop": true, "claude-vscode": true}

// sidebarStart reports whether a chat whose first user message carries these
// fields belongs in the sidebar. Programs that run `claude` from inside a
// desktop chat inherit its entrypoint, but their turns are marked "sdk".
func sidebarStart(entrypoint, turnOrigin string) bool {
	return sidebarEntrypoints[entrypoint] && turnOrigin != "sdk"
}

// isCLIChat reports whether id's local transcript was started outside the
// desktop app and VS Code, or by a program rather than a person. Without a
// local transcript it can't tell: false.
func (s *Syncer) isCLIChat(id string) bool {
	m, _ := filepath.Glob(filepath.Join(s.claudeDir, "projects", "*", id+".jsonl"))
	if len(m) == 0 {
		return false
	}
	f, err := os.Open(m[0])
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		var l struct {
			Type       string `json:"type"`
			Entrypoint string `json:"entrypoint"`
			TurnOrigin string `json:"turnOrigin"`
		}
		if json.Unmarshal(line, &l) == nil && l.Type == "user" && l.Entrypoint != "" {
			return !sidebarStart(l.Entrypoint, l.TurnOrigin)
		}
		if err != nil {
			return false
		}
	}
}

// Run-specific fields of the template entry that must not carry over.
var desktopTemplateDrop = []string{"postTurnSummary", "postTurnSummaryFor", "pregeneratedId", "interruptedAwaitingPerson", "latestUserFrameAt", "lastSpawnRootDetected", "lastAssistantUuid"}

// adoptTranscripts gives a sidebar entry to every local transcript started in
// the desktop app or VS Code that has none, e.g. VS Code chats (which never
// get an entry of their own) or desktop chats from a device that hasn't pushed
// its entries. New entries copy the newest local entry's settings.
func (s *Syncer) adoptTranscripts(writeDir string) (generated []string, err error) {
	local, _, err := ReadDesktopEntries(filepath.Dir(filepath.Dir(writeDir)))
	if err != nil {
		return nil, err
	}
	var template map[string]json.RawMessage
	var newest time.Time
	files, _ := filepath.Glob(filepath.Join(writeDir, "local_*.json"))
	for _, f := range files {
		fi, err := os.Stat(f)
		if err != nil || !fi.ModTime().After(newest) {
			continue
		}
		data, _ := os.ReadFile(f)
		var m map[string]json.RawMessage
		if json.Unmarshal(data, &m) == nil {
			template, newest = m, fi.ModTime()
		}
	}
	if template == nil {
		return nil, nil
	}

	transcripts, _ := filepath.Glob(filepath.Join(s.claudeDir, "projects", "*", "*.jsonl"))
	sort.Strings(transcripts)
	for _, tr := range transcripts {
		id := strings.TrimSuffix(filepath.Base(tr), ".jsonl")
		if _, ok := local[id]; ok || s.state.isDesktopSeen(id) || !desktopCLIIDRe.MatchString(id) {
			continue
		}
		// shortcut: rereads unadopted CLI transcripts on every push/pull; cache by mtime if they get slow
		info, err := readTranscriptInfo(tr)
		if err != nil {
			return nil, err
		}
		if !info.sidebar || info.cwd == "" || info.title == "" {
			continue
		}

		entry := make(map[string]json.RawMessage, len(template))
		for k, v := range template {
			entry[k] = v
		}
		for _, k := range desktopTemplateDrop {
			delete(entry, k)
		}
		sessionID := "local_" + newUUID()
		set := func(k string, v any) { entry[k], _ = json.Marshal(v) }
		set("sessionId", sessionID)
		set("cliSessionId", id)
		set("cwd", info.cwd)
		set("originCwd", info.cwd)
		set("gitAnchors", []any{}) // the template's anchors belong to its own repo
		set("createdAt", info.first.UnixMilli())
		for _, k := range []string{"lastActivityAt", "lastTurnEdgeAt", "lastFocusedAt"} {
			set(k, info.last.UnixMilli())
		}
		set("title", info.title)
		set("titleSource", "auto")
		set("isArchived", false)
		if info.lastAssistant != "" {
			set("lastAssistantUuid", info.lastAssistant)
		}
		data, err := json.Marshal(entry)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(writeDir, sessionID+".json"), data, 0600); err != nil {
			return nil, fmt.Errorf("failed to write desktop entry: %w", err)
		}
		s.state.markDesktopSeen(id)
		generated = append(generated, info.title)
	}
	return generated, nil
}

type transcriptInfo struct {
	sidebar       bool
	cwd, title    string
	lastAssistant string
	first, last   time.Time
}

// readTranscriptInfo extracts what a sidebar entry needs from a transcript.
func readTranscriptInfo(path string) (transcriptInfo, error) {
	var info transcriptInfo
	f, err := os.Open(path)
	if err != nil {
		return info, err
	}
	defer func() { _ = f.Close() }()

	var aiTitle, userTitle string
	var seenEntrypoint bool
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var l struct {
				Entrypoint string `json:"entrypoint"`
				TurnOrigin string `json:"turnOrigin"`
				Type       string `json:"type"`
				Cwd        string `json:"cwd"`
				Timestamp  string `json:"timestamp"`
				UUID       string `json:"uuid"`
				AITitle    string `json:"aiTitle"`
				Message    struct {
					Content json.RawMessage `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &l) == nil {
				if !seenEntrypoint && l.Type == "user" && l.Entrypoint != "" {
					seenEntrypoint, info.sidebar = true, sidebarStart(l.Entrypoint, l.TurnOrigin)
				}
				if info.cwd == "" {
					info.cwd = l.Cwd
				}
				if t, err := time.Parse(time.RFC3339Nano, l.Timestamp); err == nil {
					if info.first.IsZero() || t.Before(info.first) {
						info.first = t
					}
					if t.After(info.last) {
						info.last = t
					}
				}
				switch l.Type {
				case "ai-title":
					aiTitle = l.AITitle
				case "assistant":
					info.lastAssistant = l.UUID
				case "user":
					if userTitle == "" {
						userTitle = firstRealText(l.Message.Content)
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
	info.title = aiTitle
	if info.title == "" {
		info.title = userTitle
	}
	if info.title = strings.Join(strings.Fields(info.title), " "); len([]rune(info.title)) > 60 {
		info.title = string([]rune(info.title)[:60])
	}
	return info, nil
}

// firstRealText returns the first text the user typed, skipping IDE/command
// wrappers ("<...>"), interruption notices ("[...]") and skill preambles.
func firstRealText(content json.RawMessage) string {
	real := func(t string) bool {
		t = strings.TrimSpace(t)
		return t != "" && !strings.HasPrefix(t, "<") && !strings.HasPrefix(t, "[") && !strings.HasPrefix(t, "Base directory for this skill")
	}
	var str string
	if json.Unmarshal(content, &str) == nil {
		if real(str) {
			return str
		}
		return ""
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(content, &blocks)
	for _, b := range blocks {
		if b.Type == "text" && real(b.Text) {
			return b.Text
		}
	}
	return ""
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
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
