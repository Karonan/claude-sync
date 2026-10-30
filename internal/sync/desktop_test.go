package sync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tawanorg/claude-sync/internal/config"
)

// desktopDevice is one simulated machine: its syncer, ~/.claude, and the
// desktop app's sessions folder (with the <account>/<org> layout).
type desktopDevice struct {
	syncer    *Syncer
	claudeDir string
	entryDir  string
}

func newDesktopDevice(t *testing.T, store *mockStorage, from *Syncer) *desktopDevice {
	t.Helper()
	tmp := t.TempDir()
	claudeDir := filepath.Join(tmp, ".claude")
	stateDir := filepath.Join(tmp, ".claude-sync")
	sessions := filepath.Join(tmp, "claude-code-sessions")
	entryDir := filepath.Join(sessions, "acct", "org")
	for _, d := range []string{claudeDir, stateDir, entryDir} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	state, err := LoadStateFromDir(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	enc := testEncryptor(t, stateDir)
	if from != nil {
		enc = from.encryptor
	}
	cfg := &config.Config{DesktopSessionsDir: sessions}
	return &desktopDevice{NewSyncerWith(cfg, store, enc, state, claudeDir, true), claudeDir, entryDir}
}

func (d *desktopDevice) addEntry(t *testing.T, sessionID, cliID, cwd, title string) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"sessionId": sessionID, "cliSessionId": cliID, "cwd": cwd, "title": title, "model": "m"})
	if err := os.WriteFile(filepath.Join(d.entryDir, sessionID+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func (d *desktopDevice) addTranscript(t *testing.T, cliID string) {
	t.Helper()
	createTestFile(t, d.claudeDir, filepath.Join("projects", "-proj", cliID+".jsonl"), `{"type":"user"}`+"\n")
}

func (d *desktopDevice) entries(t *testing.T) DesktopEntries {
	t.Helper()
	e, _, err := ReadDesktopEntries(filepath.Dir(filepath.Dir(d.entryDir)))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestDesktopPullAddsOnlyDesktopChats(t *testing.T) {
	ctx := context.Background()
	store := newMockStorage()
	a := newDesktopDevice(t, store, nil)
	b := newDesktopDevice(t, store, a.syncer)

	home, _ := os.UserHomeDir()
	cwd := filepath.Join(home, "proj")
	a.addEntry(t, "local_1", "cli-desktop", cwd, "Desktop chat")
	a.addEntry(t, "local_2", "cli-later", cwd, "Transcript not pulled yet")
	if r, err := a.syncer.PushDesktop(ctx); err != nil || r.Pushed != 2 {
		t.Fatalf("push: %+v %v", r, err)
	}

	b.addEntry(t, "local_own", "cli-own", cwd, "B's own chat")
	b.addTranscript(t, "cli-desktop")
	b.addTranscript(t, "cli-only") // a CLI session: no desktop entry anywhere

	r, err := b.syncer.PullDesktop(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Added) != 1 || r.Added[0] != "Desktop chat" || r.Waiting != 1 {
		t.Fatalf("pull result: %+v", r)
	}
	got := b.entries(t)
	if _, ok := got["cli-only"]; ok {
		t.Error("CLI-only session must not get a sidebar entry")
	}
	var h struct{ Cwd string }
	_ = json.Unmarshal(got["cli-desktop"], &h)
	if h.Cwd != cwd {
		t.Errorf("cwd = %q, want %q", h.Cwd, cwd)
	}

	// Transcript arrives later: the waiting entry is added on the next pull
	b.addTranscript(t, "cli-later")
	if r, _ = b.syncer.PullDesktop(ctx); len(r.Added) != 1 || r.Waiting != 0 {
		t.Fatalf("second pull: %+v", r)
	}
}

func TestDesktopPushKeepsOtherDevicesEntries(t *testing.T) {
	ctx := context.Background()
	store := newMockStorage()
	a := newDesktopDevice(t, store, nil)
	b := newDesktopDevice(t, store, a.syncer)

	a.addEntry(t, "local_a", "cli-a", "/x", "A")
	b.addEntry(t, "local_b", "cli-b", "/x", "B")
	if _, err := a.syncer.PushDesktop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := b.syncer.PushDesktop(ctx); err != nil {
		t.Fatal(err)
	}
	remote, err := a.syncer.downloadDesktopEntries(ctx)
	if err != nil || len(remote) != 2 {
		t.Fatalf("remote = %v, %v; want both devices' entries", remote, err)
	}
	if r, _ := b.syncer.PushDesktop(ctx); !r.Unchanged {
		t.Error("repeat push should be unchanged")
	}
}

func TestDesktopPullNeverResurrectsOrOverwrites(t *testing.T) {
	ctx := context.Background()
	store := newMockStorage()
	a := newDesktopDevice(t, store, nil)
	b := newDesktopDevice(t, store, a.syncer)

	a.addEntry(t, "local_1", "cli-1", "/x", "Remote title")
	a.addEntry(t, "local_2", "cli-2", "/x", "Deleted on B")
	if _, err := a.syncer.PushDesktop(ctx); err != nil {
		t.Fatal(err)
	}
	b.addTranscript(t, "cli-1")
	b.addTranscript(t, "cli-2")
	b.addEntry(t, "local_mine", "cli-1", "/x", "Local title")
	if _, err := b.syncer.PullDesktop(ctx); err != nil {
		t.Fatal(err)
	}
	// User deletes the pulled chat on B; the next pull must not bring it back
	if err := os.Remove(filepath.Join(b.entryDir, "local_2.json")); err != nil {
		t.Fatal(err)
	}
	if r, _ := b.syncer.PullDesktop(ctx); len(r.Added) != 0 {
		t.Fatalf("deleted chat came back: %+v", r)
	}
	var h desktopEntryHeader
	_ = json.Unmarshal(b.entries(t)["cli-1"], &h)
	if h.Title != "Local title" {
		t.Errorf("existing entry was changed: %q", h.Title)
	}
}

func TestDesktopPullRejectsUnsafeSessionID(t *testing.T) {
	ctx := context.Background()
	store := newMockStorage()
	a := newDesktopDevice(t, store, nil)
	b := newDesktopDevice(t, store, a.syncer)

	a.addEntry(t, "local_ok", "cli-1", "/x", "x")
	if _, err := a.syncer.PushDesktop(ctx); err != nil {
		t.Fatal(err)
	}
	// Tamper with the remote entry so its sessionId escapes the folder
	remote, _ := a.syncer.downloadDesktopEntries(ctx)
	remote["cli-1"] = json.RawMessage(`{"sessionId":"local_../../evil","cliSessionId":"cli-1"}`)
	data, _ := json.Marshal(remote)
	enc, _ := a.syncer.encryptor.Encrypt(data)
	_ = store.Upload(ctx, config.DesktopRemoteKey+".age", enc)

	b.addEntry(t, "local_seed", "cli-seed", "/x", "seed")
	b.addTranscript(t, "cli-1")
	if r, err := b.syncer.PullDesktop(ctx); err != nil || len(r.Added) != 0 {
		t.Fatalf("unsafe entry written: %+v %v", r, err)
	}
}

func TestDesktopNoAppDir(t *testing.T) {
	s, _, _ := testSyncer(t)
	s.cfg.DesktopSessionsDir = filepath.Join(t.TempDir(), "missing")
	if r, err := s.PullDesktop(context.Background()); err != nil || !r.NoDesktopDir {
		t.Fatalf("pull: %+v %v", r, err)
	}
}

func TestDesktopPullAdoptsVSCodeAndDesktopTranscripts(t *testing.T) {
	ctx := context.Background()
	d := newDesktopDevice(t, newMockStorage(), nil)
	d.addEntry(t, "local_tpl", "cli-tpl", "/x", "template")

	write := func(id, entrypoint string, extra ...string) {
		lines := `{"type":"queue-operation","timestamp":"2026-01-01T00:00:00Z"}` + "\n" +
			`{"type":"user","cwd":"/proj","entrypoint":"` + entrypoint + `","timestamp":"2026-01-02T00:00:00Z","message":{"content":[{"type":"text","text":"<ide_opened_file>x</ide_opened_file>"},{"type":"text","text":"Fix   the bug"}]}}` + "\n" +
			`{"type":"assistant","uuid":"a1","timestamp":"2026-01-03T00:00:00Z"}` + "\n"
		for _, e := range extra {
			lines += e + "\n"
		}
		createTestFile(t, d.claudeDir, filepath.Join("projects", "-proj", id+".jsonl"), lines)
	}
	write("vs", "claude-vscode")
	write("desk", "claude-desktop", `{"type":"ai-title","aiTitle":"Named by AI"}`)
	write("cli", "cli")
	write("sdk", "sdk-cli")

	r, err := d.syncer.PullDesktop(ctx)
	if err != nil || !r.NoRemote {
		t.Fatalf("pull: %+v %v", r, err)
	}
	if len(r.Generated) != 2 {
		t.Fatalf("generated = %v, want the VS Code and desktop chats", r.Generated)
	}
	got := d.entries(t)
	for _, id := range []string{"cli", "sdk"} {
		if _, ok := got[id]; ok {
			t.Errorf("%s session must not get a sidebar entry", id)
		}
	}
	var vs struct {
		SessionID, Title, Cwd, LastAssistantUUID string
		CreatedAt, LastActivityAt                int64
		GitAnchors                               []any
	}
	_ = json.Unmarshal(got["vs"], &vs)
	if vs.Title != "Fix the bug" || vs.Cwd != "/proj" || vs.LastAssistantUUID != "a1" || len(vs.GitAnchors) != 0 {
		t.Errorf("vs entry = %+v", vs)
	}
	if vs.CreatedAt >= vs.LastActivityAt || !desktopSessionIDRe.MatchString(vs.SessionID) {
		t.Errorf("bad timestamps or id: %+v", vs)
	}
	var desk desktopEntryHeader
	_ = json.Unmarshal(got["desk"], &desk)
	if desk.Title != "Named by AI" {
		t.Errorf("desk title = %q", desk.Title)
	}

	// A later VS Code chat is adopted by push, and its entry is uploaded
	write("vs2", "claude-vscode")
	pr, err := d.syncer.PushDesktop(ctx)
	if err != nil || len(pr.Generated) != 1 {
		t.Fatalf("push: %+v %v", pr, err)
	}
	remote, _ := d.syncer.downloadDesktopEntries(ctx)
	if _, ok := remote["vs2"]; !ok {
		t.Error("push should upload the adopted entry")
	}

	// Deleting an adopted chat keeps it gone
	if err := os.Remove(filepath.Join(d.entryDir, vs.SessionID+".json")); err != nil {
		t.Fatal(err)
	}
	if r, _ = d.syncer.PullDesktop(ctx); len(r.Generated) != 0 {
		t.Fatalf("adopted again: %v", r.Generated)
	}
}

// A chat started in the CLI never gets a sidebar entry through sync, even
// when it was later resumed in the desktop app or an older build pushed it.
func TestDesktopSyncLeavesOutCLIStartedChats(t *testing.T) {
	ctx := context.Background()
	store := newMockStorage()
	a := newDesktopDevice(t, store, nil)
	b := newDesktopDevice(t, store, a.syncer)
	transcript := func(d *desktopDevice, id string, entrypoints ...string) {
		var lines string
		for _, ep := range entrypoints {
			lines += `{"type":"user","entrypoint":"` + ep + `"}` + "\n"
		}
		createTestFile(t, d.claudeDir, filepath.Join("projects", "-proj", id+".jsonl"), lines)
	}
	for _, d := range []*desktopDevice{a, b} {
		transcript(d, "desk", "claude-desktop")
		transcript(d, "resumed", "cli", "claude-desktop")
		// A program ran `claude` from a desktop chat: inherited entrypoint, "sdk" turns
		createTestFile(t, d.claudeDir, filepath.Join("projects", "-proj", "spawned.jsonl"),
			`{"type":"user","entrypoint":"claude-desktop","turnOrigin":"sdk"}`+"\n")
	}

	a.addEntry(t, "local_1", "desk", "/proj", "Desktop chat")
	a.addEntry(t, "local_2", "resumed", "/proj", "CLI chat opened in desktop")
	a.addEntry(t, "local_3", "spawned", "/proj", "say hi")
	if r, err := a.syncer.PushDesktop(ctx); err != nil || r.Pushed != 1 {
		t.Fatalf("push: %+v %v", r, err)
	}
	remote, _ := a.syncer.downloadDesktopEntries(ctx)
	if _, ok := remote["resumed"]; ok {
		t.Error("push uploaded a CLI-started chat")
	}

	// As an older build would have: the CLI chat's entry is in the remote list
	remote["resumed"] = json.RawMessage(`{"sessionId":"local_2","cliSessionId":"resumed","title":"CLI chat opened in desktop"}`)
	data, _ := json.Marshal(remote)
	compressed, _ := gzipCompress(data)
	encrypted, _ := a.syncer.encryptor.Encrypt(compressed)
	if err := store.Upload(ctx, config.DesktopRemoteKey+".age", encrypted); err != nil {
		t.Fatal(err)
	}

	b.addEntry(t, "local_own", "own", "/proj", "B's own chat") // pull needs the app's folder
	r, err := b.syncer.PullDesktop(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Added) != 1 || r.Added[0] != "Desktop chat" {
		t.Fatalf("pull added %v, want only the desktop chat", r.Added)
	}
	if _, ok := b.entries(t)["resumed"]; ok {
		t.Error("pull recreated a CLI-started chat")
	}
}

func TestDesktopDeletionSyncsAcrossDevices(t *testing.T) {
	ctx := context.Background()
	store := newMockStorage()
	a := newDesktopDevice(t, store, nil)
	b := newDesktopDevice(t, store, a.syncer)
	for _, id := range []string{"keep", "gone", "moved"} {
		a.addTranscript(t, id)
	}
	a.addEntry(t, "local_1", "keep", "/proj", "Keep me")
	a.addEntry(t, "local_2", "gone", "/proj", "Delete me")
	a.addEntry(t, "local_3", "moved", "/proj", "Other note")
	if _, err := a.syncer.PushDesktop(ctx); err != nil {
		t.Fatal(err)
	}

	// The app on B deleted "gone"; its note reached A like any synced file.
	// Notes with another reason must not remove anything.
	note := func(id, reason string) {
		createTestFile(t, a.claudeDir, filepath.Join("projects", "-proj", id+".desktop-released.json"),
			`{"v":1,"releasedAt":"2026-10-30T18:11:05.410Z","reason":"`+reason+`"}`)
	}
	note("gone", "delete")
	note("moved", "archive")

	// Pushing before pulling must not keep the deleted chat in the remote list
	if r, err := a.syncer.PushDesktop(ctx); err != nil || r.Pushed != 2 {
		t.Fatalf("push: %+v %v", r, err)
	}
	r, err := a.syncer.PullDesktop(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Removed) != 1 || r.Removed[0] != "Delete me" {
		t.Fatalf("pull removed %v, want [Delete me]", r.Removed)
	}
	got := a.entries(t)
	if _, ok := got["gone"]; ok {
		t.Error("deleted chat still in the sidebar")
	}
	if _, ok := got["keep"]; !ok {
		t.Error("kept chat was removed")
	}
	if _, ok := got["moved"]; !ok {
		t.Error("a non-delete note removed a chat")
	}
	if !a.syncer.hasTranscript("gone") {
		t.Error("transcript must stay")
	}
	if r, _ := a.syncer.PullDesktop(ctx); len(r.Added)+len(r.Generated)+len(r.Removed) != 0 {
		t.Errorf("second pull changed the sidebar: %+v", r)
	}

	// A device that never had it doesn't add it from an older remote list
	b.addTranscript(t, "gone")
	b.addTranscript(t, "keep")
	createTestFile(t, b.claudeDir, filepath.Join("projects", "-proj", "gone.desktop-released.json"), `{"reason":"delete"}`)
	b.addEntry(t, "local_b", "own", "/proj", "B's own chat")
	if r, err := b.syncer.PullDesktop(ctx); err != nil || len(r.Added) != 1 || r.Added[0] != "Keep me" {
		t.Fatalf("B pull: %+v %v", r, err)
	}
}
