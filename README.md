<div align="center">

<img src="assets/banner.svg" alt="Claude Sync" width="100%">

<br>

*Encrypted with [age](https://github.com/FiloSottile/age) • R2 / S3 / GCS / WebDAV supported*

[![Release](https://img.shields.io/github/v/release/tawanorg/claude-sync)](https://github.com/tawanorg/claude-sync/releases)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![npm](https://img.shields.io/npm/v/@tawandotorg/claude-sync)](https://www.npmjs.com/package/@tawandotorg/claude-sync)
[![Socket Badge](https://badge.socket.dev/npm/package/@tawandotorg/claude-sync/1.11.1)](https://badge.socket.dev/npm/package/@tawandotorg/claude-sync/1.11.1)

[Quick Start](#quick-start) • [Setup Guide](#setup-guide) • [Commands](#commands) • [How Sync Works](#how-sync-works) • [Security](#security)

</div>

---

> **This is a fork** of [tawanorg/claude-sync](https://github.com/tawanorg/claude-sync). It adds Claude desktop app chat sync (including deleted chats), keeps CLI- and program-started chats out of the desktop sidebar, fixes false conflicts, and makes `diff` readable. Install it from source: see [This fork (build from source)](#this-fork-build-from-source). The badges above describe the original project.

## Features

- **Cross-device sync**: Continue Claude Code conversations on any computer (macOS, Linux, Windows)
- **Claude desktop app chats**: Chats from the desktop app and VS Code appear in the desktop sidebar on every device; chats you delete there are deleted everywhere
- **Multi-provider storage**: Cloudflare R2, AWS S3, Google Cloud Storage, S3-compatible (Backblaze B2, MinIO, Wasabi), or WebDAV (Nextcloud, ownCloud)
- **End-to-end encryption**: All files encrypted with age before upload
- **Passphrase-based keys**: Same passphrase = same key on any device (no file copying)
- **Portable paths**: Sessions resume on devices with a different username or folder layout
- **Selective sync**: Choose `--scope sessions` to sync only conversation data, or add your own exclude filters
- **MCP server sync** (optional): Merge global MCP server configs from `~/.claude.json` across devices
- **Auto-sync**: `claude-sync auto enable` pulls when a Claude Code session starts and pushes when it stops
- **Readable diff**: `diff` groups changes by what push and pull will do, and names chats by title
- **Compression**: Gzip compression before encryption for faster syncs

<div align="center">
<img src="assets/claude-sync.gif" alt="Claude Sync Demo" width="100%">
</div>

## Quick Start

Install claude-sync on each device first: see [This fork (build from source)](#this-fork-build-from-source).

### First Device

```bash
# Set up (interactive wizard)
claude-sync init

# Push your sessions
claude-sync push

# Optional: pull/push automatically with every Claude Code session
claude-sync auto enable
```

### Second Device

```bash
# Set up with SAME storage credentials
claude-sync init
# Select same provider (R2/S3/GCS/WebDAV)
# Enter same bucket name and credentials
# Choose "Passphrase" for encryption
# Enter the SAME passphrase as first device
# ✓ Encryption key verified  <-- confirms passphrase matches!

# Preview what would be synced
claude-sync pull --dry-run

# Pull sessions (offers a backup if you have existing files)
claude-sync pull

# Optional: auto-sync here too
claude-sync auto enable
```

**Same passphrase = same encryption key.** The init verifies your passphrase can decrypt remote files before completing.

## Setup Guide

### Step 1: Choose a Storage Provider

| Provider | Free Tier | Best For |
|----------|-----------|----------|
| **Cloudflare R2** | 10GB storage | Personal use (recommended) |
| **AWS S3** | 5GB (12 months) | AWS users |
| **Google Cloud Storage** | 5GB | GCP users |
| **S3-compatible** | varies | Backblaze B2, MinIO, Wasabi, DigitalOcean Spaces, self-hosted |
| **WebDAV** | Self-hosted (unlimited) | Nextcloud/ownCloud users |

### Step 2: Create a Bucket

<details>
<summary><b>Cloudflare R2</b> (recommended)</summary>

1. Go to [Cloudflare Dashboard](https://dash.cloudflare.com/) → R2 Object Storage
2. Click "Create bucket" → name it `claude-sync`
3. Go to "Manage R2 API Tokens" → "Create API Token"
4. Select **Object Read & Write** permission → Create

You'll need: Account ID, Access Key ID, Secret Access Key
</details>

<details>
<summary><b>AWS S3</b></summary>

1. Go to [S3 Console](https://s3.console.aws.amazon.com/s3/bucket/create) → Create bucket
2. Go to [IAM Security Credentials](https://console.aws.amazon.com/iam/home#/security_credentials)
3. Create Access Keys

You'll need: Access Key ID, Secret Access Key, Region
</details>

<details>
<summary><b>Google Cloud Storage</b></summary>

1. Go to [Cloud Storage](https://console.cloud.google.com/storage/create-bucket) → Create bucket
2. Go to [Service Accounts](https://console.cloud.google.com/iam-admin/serviceaccounts) → Create service account
3. Grant "Storage Object Admin" role → Create JSON key

You'll need: Project ID, Service Account JSON file (or use `gcloud auth application-default login`)
</details>

<details>
<summary><b>S3-compatible</b> (Backblaze B2, MinIO, Wasabi, DigitalOcean Spaces, ...)</summary>

Any provider exposing an S3-compatible API works through the **S3-compatible (custom endpoint)** option. Create a bucket and an application key with your provider, then supply its S3 endpoint URL.

Example (Backblaze B2):

```bash
claude-sync init --provider s3-compatible --endpoint https://s3.us-west-004.backblazeb2.com
```

You'll need: Endpoint URL, Access Key ID, Secret Access Key, Bucket. The signing region is auto-detected from the endpoint (e.g. `us-west-004`); for providers that ignore it, `auto` is used.

For servers that don't resolve buckets as subdomains (e.g. Ceph RGW, or MinIO without wildcard DNS), add `--use-path-style` to address objects as `endpoint/bucket/key` instead of `bucket.endpoint/key`:

```bash
claude-sync init --provider s3-compatible --endpoint https://ceph.example.com --use-path-style
```

It's off by default and unnecessary for Backblaze B2, Wasabi, and DigitalOcean Spaces, which all support virtual-hosted addressing.

> Custom endpoints automatically relax the AWS SDK's default integrity-checksum headers, which some S3-compatible providers reject. AWS S3 behavior is unchanged.
</details>

<details>
<summary><b>WebDAV (Nextcloud, ownCloud, etc.)</b></summary>

No bucket to create — just point at your existing WebDAV server.

1. **Nextcloud**: Go to Settings → Security → Devices & sessions → Create app password
2. Note your WebDAV URL: `https://your-server/remote.php/dav/files/USERNAME/`

You'll need: WebDAV URL, Username, App password

Files go in a `claude-sync` subdirectory (change it with `--webdav-path-prefix`).
</details>

### Step 3: Run Init

```bash
claude-sync init
```

The interactive wizard will guide you through:

1. **Select storage provider** (R2, S3, GCS, S3-compatible, or WebDAV)
2. **Enter credentials** (provider-specific)
3. **Choose encryption method**:
   - **Passphrase** (recommended, at least 12 characters) - same passphrase on all devices = same key
   - **Random key** - must copy `~/.claude-sync/age-key.txt` to other devices
4. **Test the connection** to verify everything works

Every wizard answer can also be given as a flag (`--provider`, `--bucket`, `--access-key`, ...); see `claude-sync init --help`.

### Step 4: Push and Pull

```bash
# Upload local changes
claude-sync push

# Download remote changes
claude-sync pull
```

Or let Claude Code do it for you with [auto-sync](#auto-sync).

## What Gets Synced

| Path | Content |
|------|---------|
| `~/.claude/projects/` | Session files, auto-memory |
| `~/.claude/plans/` | Implementation plans from plan mode |
| `~/.claude/tasks/` | Task tracking state |
| `~/.claude/history.jsonl` | Command history |
| `~/.claude/agents/` | Custom agents |
| `~/.claude/commands/` | Custom slash commands |
| `~/.claude/skills/` | Custom skills |
| `~/.claude/plugins/` | Installed plugins and their settings |
| `~/.claude/rules/` | Custom rules |
| `~/.claude/workflows/` | Workflows |
| `~/.claude/settings.json` | Settings |
| `~/.claude/settings.local.json` | Local settings |
| `~/.claude/CLAUDE.md` | Global instructions |

Also synced, outside `~/.claude/`:

- **Claude desktop app chat list** (on by default), see [Claude Desktop App Chats](#claude-desktop-app-chats)
- **MCP servers from `~/.claude.json`** (off by default), see [MCP Server Sync](#mcp-server-sync)

Never synced:

- `plugins/marketplaces/` (built in): plugin catalogs. Claude Code downloads them itself from the sources in `plugins/known_marketplaces.json`, and each refresh rewrites hundreds of files.
- `plugins/synced/` (built in): plugins from your claude.ai account, which the Claude app downloads and updates on each device itself.

Installed plugins (`settings.json`, `plugins/installed_plugins.json`, `plugins/cache/`) still sync.
- `.conflict.<timestamp>` copies: they stay on the device that made them.
- Symlinks inside `~/.claude/`.
- Anything matching your own [exclude filters](#choosing-what-syncs).

### Sync scope

`init` asks whether to sync everything or just conversation data; you can also set it with `--scope`:

| Scope | Syncs | Use when |
|-------|-------|----------|
| `full` (default) | everything in the table above | you want settings, skills, agents, and plugins mirrored too |
| `sessions` | `projects/`, `history.jsonl`, `tasks/`, `plans/` only | you just want `claude --resume` to work across machines |

```bash
claude-sync init --scope sessions
```

**Why `sessions` exists:** `full` includes `plugins/`, whose plugin caches can bundle `node_modules` and Python `.venv` trees — large, machine-/arch-specific files that are regenerated on demand. `sessions` skips them, keeping syncs small, fast, and portable. The scope is saved in `~/.claude-sync/config.yaml` and applies to every `push`/`pull`.

### Choosing what syncs

Fine-tune the synced set per device with `paths`:

```bash
claude-sync paths list                          # Show sync paths and exclude filters
claude-sync paths remove plugins                # Stop syncing a whole path
claude-sync paths add plugins                   # Sync it again
claude-sync paths exclude "projects/*/node_modules/**"   # Skip files inside a synced path
claude-sync paths unexclude "projects/*/node_modules/**"
claude-sync paths reset                         # Back to the defaults
```

Exclude filters use glob syntax (`dir/*`, `dir/**`, `**/*.ext`, or a plain `*.tmp` to match file names anywhere), matched against paths relative to `~/.claude`. They're saved in `~/.claude-sync/config.yaml`, which is not synced, so set them on each device:

```yaml
exclude:
  - "*.tmp"
  - "projects/*/node_modules/**"
```

When you exclude files that were synced before, your next push deletes them from the cloud. Local copies are never touched.

## Cross-Device Path Mapping

Claude Code indexes project sessions by **absolute filesystem path**:

```
/Users/alice/my-app → ~/.claude/projects/-Users-alice-my-app/
/Users/bob/my-app   → ~/.claude/projects/-Users-bob-my-app/
```

Synced verbatim, those would be **different projects** and `claude --resume` on the second machine would never find the first machine's sessions. claude-sync solves this by translating paths during sync:

- **Home directories are mapped automatically.** Sessions are stored remotely under a portable `${HOME}` token (in both remote keys and transcript content), then rewritten to each device's real home on pull. Different usernames across machines just work.
- **Other layout differences are configurable.** If one machine keeps projects in `~/work` and another in `~/Projects`, point both at the same token in `~/.claude-sync/config.yaml`:

  ```yaml
  # machine 1
  path_map:
    ~/work: WORK
  ```

  ```yaml
  # machine 2
  path_map:
    ~/Projects: WORK
  ```

  Sessions under either directory sync to the shared `${WORK}` namespace and resume correctly on both machines.

Projects outside your home folder and any `path_map` entry (for example on another drive) only line up if they live at the same absolute path on every device.

**Upgrading from an older version?** Run `claude-sync migrate` once on each device to convert existing remote data to portable keys. Paths the current device doesn't own are left for the other device's migrate run.

## How Sync Works

claude-sync keeps a record of every file it synced (`~/.claude-sync/state.json`) and compares both sides against it.

**Push** uploads files that changed on this device since they were last synced, and deletes from the cloud files you deleted on this device.

**Pull** downloads files that are new or changed in the cloud:

- A file that changed only in the cloud replaces your copy.
- A file that changed on both sides is a **conflict**: your copy is kept and the cloud copy is saved next to it as `<file>.conflict.<timestamp>` (see [Conflict Resolution](#conflict-resolution)). Chats are checked first: they only grow, so if one side already has every message of the other, pull keeps that side and there is no conflict. A chat only conflicts when you continued it on two devices without syncing in between. The session-duration and cost line Claude Code rewrites on its own is ignored. Likewise `plugins/known_marketplaces.json` copies that differ only in their `lastUpdated` times (Claude Code sets them each time it checks for plugin updates) take the cloud copy without a conflict.
- A file you deleted here but haven't pushed yet is downloaded again. Push first if you want the deletion to stick. Desktop app chats are the exception, see below.

Whether the cloud copy changed is decided from the cloud's own timestamps, so a device whose clock is off doesn't cause false conflicts.

### Reading `diff`

`claude-sync diff` compares this device with the cloud and groups everything by what will happen:

```
Push will upload from this device (2)
  changed chat "Installation instructions"  (Documents-my-app)
  new     settings.json

Push will delete from the cloud (chat deleted in the desktop app) (1)
  deleted chat "Old experiment"  (Documents-my-app)

Deleted on this device: push deletes it from the cloud, pull brings it back (1)
  deleted agents/old-agent.md

Pull will download to this device (1)
  new     chat "Trip planning"  (Documents-notes)

2031 files in sync, 5 waiting for push or pull
```

If you exclude something that was synced before, push deletes it from the cloud (this device keeps it), and `diff` warns first, one line per folder:

```
Excluded now: push deletes it from the cloud, this device keeps it (1218)
  deleted plugins/plugin-catalog-cache.json
  deleted plugins/synced/ (1217 files)
```

Two more sections appear when needed: **Changed on both sides** (pull keeps yours and saves the cloud copy as `.conflict`) and **Different, but no sync record says which side changed**.

Chats are shown by the name you gave them, or else by their title or first message. A deleted chat's name is read from the copy still in the cloud; `(name unknown)` means no copy is left anywhere. `claude-sync status` lists only what push would do, by file path.

## Claude Desktop App Chats

The Claude desktop app keeps its own sidebar list of chats. claude-sync syncs that list too, so a chat you start in the desktop app (or in VS Code) shows up in the desktop sidebar on your other devices.

- **On by default** as part of every push and pull. Devices without the desktop app skip it. Turn it off with `claude-sync desktop disable` (or for one run, back on with `--include-desktop`).
- **Chats started in the terminal or by other programs** (CLI, SDK) still sync, so `claude --resume` finds them, but they stay out of the desktop sidebar.
- **Deleting a chat** in the desktop app deletes it everywhere: on your other devices it leaves the sidebar, and their copy of its files is removed at their next pull. A file changed on that device since its last sync is kept, so nothing written only there is lost. Archiving a chat doesn't delete it.
- After a pull adds or removes chats, restart the desktop app to see the change.

```bash
claude-sync desktop push     # Push this device's chat list now
claude-sync desktop pull     # Add chats from other devices now
claude-sync desktop disable  # Stop syncing the chat list
claude-sync desktop enable
```

Supported app locations: Windows (`%LOCALAPPDATA%\Claude-Data\claude_*` for the Microsoft Store app, or `%APPDATA%\Claude`), macOS (`~/Library/Application Support/Claude`), Linux (`~/.config/Claude`).

## MCP Server Sync

Global MCP servers live in `~/.claude.json`, outside `~/.claude/`. MCP sync is off by default; when on, push and pull merge the server list across devices (a server changed differently on two devices keeps this device's version), and paths inside server commands are mapped between home folders.

```bash
claude-sync mcp enable    # Sync MCP servers on every push/pull (syncs now too)
claude-sync mcp disable
claude-sync mcp status    # Show settings and local servers
claude-sync mcp list      # List local MCP servers
claude-sync mcp push      # One-off push / pull
claude-sync mcp pull
claude-sync push --include-mcp   # Include MCP for one run only
```

## Auto-sync

Let Claude Code run claude-sync for you:

```bash
claude-sync auto enable    # Pull when a session starts, push when it stops
claude-sync auto status
claude-sync auto disable
```

This adds `claude-sync pull -q` (on `SessionStart`) and `claude-sync push -q` (on `Stop`) hooks to `~/.claude/settings.json`.

<details>
<summary>Shell integration (alternative)</summary>

Add to `~/.zshrc` or `~/.bashrc` to sync when a terminal opens and closes instead:

```bash
# Auto-pull on shell start
if command -v claude-sync &> /dev/null; then
  # Run in a subshell so the job is detached from the parent shell's
  # job table — avoids interactive `[1] 12345` / `[1] + done` noise.
  (claude-sync pull -q &) >/dev/null 2>&1
fi

# Auto-push on shell exit
trap 'claude-sync push -q' EXIT
```

</details>

## Commands

```bash
claude-sync init             # Set up configuration (interactive wizard)
claude-sync push             # Upload local changes to cloud storage
claude-sync pull             # Download remote changes from cloud storage
claude-sync status           # Show what push would upload or delete
claude-sync diff             # Show what push and pull would do, by chat name
claude-sync conflicts        # List and resolve conflicts
claude-sync desktop          # Desktop app chat list sync (enable/disable/push/pull)
claude-sync mcp              # MCP server sync (enable/disable/status/list/push/pull)
claude-sync auto             # Auto-sync hooks for Claude Code (enable/disable/status)
claude-sync paths            # Choose what syncs (list/add/remove/exclude/unexclude/reset)
claude-sync rebuild-history  # Rebuild ~/.claude/history.jsonl from session files
claude-sync migrate          # Convert legacy remote keys to portable path-mapped keys
claude-sync reset            # Reset configuration (forgot passphrase)
claude-sync update           # Update to the latest ORIGINAL release (see below)
claude-sync changelog        # Show the original project's release history
claude-sync --version        # Show the installed version
claude-sync --help           # Show all commands
```

`-q` / `--quiet` works with every command and prints nothing (for scripts).

### Push and Pull Options

```bash
claude-sync pull                    # Normal pull (prompts if existing files)
claude-sync pull --dry-run          # Preview what would change
claude-sync pull --force            # Skip confirmation prompts
claude-sync pull --rebuild-history  # Also rebuild history.jsonl after pulling
claude-sync push --include-mcp      # Also sync MCP servers this time
claude-sync pull --include-desktop  # Sync the desktop chat list even if disabled
```

### Rebuilding Prompt History

`history.jsonl` is synced as a single file, so pushes from two devices are
last-writer-wins and one device's prompt-history entries can be lost — which
breaks the `/resume` session picker. Session files sync cleanly (one file per
session), so the history can always be reconstructed from them:

```bash
claude-sync rebuild-history         # One-off rebuild
claude-sync pull --rebuild-history  # Rebuild automatically after a pull
```

Every existing entry is preserved, recovered prompts are merged in and sorted by
timestamp, and the previous file is kept as `history.jsonl.bak`.

### Init Options

```bash
claude-sync init              # Full setup wizard
claude-sync init --passphrase # Re-enter passphrase only (keeps storage config)
claude-sync init --force      # Reset everything, start fresh
claude-sync init --scope sessions  # Sync conversation data only
```

### Updates and Changelog

`claude-sync update` and `claude-sync changelog` read the **original project's** GitHub releases. On this fork, don't run `update`: it would replace your build with the original version. Update by rebuilding instead, see [This fork (build from source)](#this-fork-build-from-source).

```bash
claude-sync changelog            # Show recent original releases
claude-sync changelog --limit 5  # Show last 5
```

## Pulling with Existing Files

When you first pull on a device that already has `~/.claude` files, claude-sync will:

1. **Show what would change** - files that would be overwritten, kept, or downloaded
2. **Ask how to proceed** - back up then pull (recommended), overwrite without backup, or abort
3. **Back up** - the backup option copies your existing files to `~/.claude.backup.<timestamp>`

```bash
# Preview first
claude-sync pull --dry-run

# Pull with prompts
claude-sync pull

# Skip prompts (for scripts)
claude-sync pull --force
```

## Conflict Resolution

When a file changed both here and in the cloud, pull keeps your version and saves the cloud version next to it as `<file>.conflict.<timestamp>`. Conflict copies stay on this device; they are never uploaded.

```bash
claude-sync conflicts                # Interactive resolution
claude-sync conflicts --list         # Just list conflicts
claude-sync conflicts --keep local   # Keep all local versions
claude-sync conflicts --keep remote  # Keep all remote versions
```

Interactive options:
- **[l]** Keep local (delete conflict file)
- **[r]** Keep remote (replace local)
- **[d]** Show diff
- **[s]** Skip
- **[q]** Quit

## Wrong Passphrase?

If you entered the wrong passphrase on a new device:

```bash
# Re-enter passphrase (keeps your storage config)
claude-sync init --passphrase
```

The init will verify your passphrase can decrypt remote files before completing.

## Forgot Passphrase?

The passphrase is **never stored**. If you forget it:

1. Your encrypted files cannot be recovered
2. Reset and start fresh:

```bash
claude-sync reset --remote   # Delete remote files and local config
claude-sync init             # Set up again with new passphrase
claude-sync push             # Re-upload from this device
```

`reset` on its own clears only the local config; add `--local` to also clear the local sync record.

## Security

- Files compressed with gzip, then encrypted with [age](https://github.com/FiloSottile/age) (X25519 + ChaCha20-Poly1305) before upload
- Passphrase-derived keys use Argon2id (memory-hard KDF). The salt is fixed on purpose, so the same passphrase gives the same key on every device; that's why passphrases must be at least 12 characters
- Passphrase is never stored - only the derived key at `~/.claude-sync/age-key.txt`
- Cloud storage is private (API key/IAM auth)
- On macOS and Linux, config files and downloads are stored with 0600/0700 permissions (user-only); Windows uses your user folder's permissions
- Backward compatible: can read both compressed and uncompressed remote files

## Cost

Claude sessions typically use < 50MB. Syncing is effectively **free** on any provider:

| Provider | Free Tier |
|----------|-----------|
| **Cloudflare R2** | 10GB storage, 1M writes, 10M reads/month |
| **AWS S3** | 5GB for 12 months (then ~$0.023/GB) |
| **Google Cloud Storage** | 5GB, 5K writes, 50K reads/month |
| **WebDAV** | Self-hosted — no limits, no cost beyond your own server |

## Installation Options

### This fork (build from source)

The npm packages, release binaries, `go install …@latest` and `claude-sync update` all install the original [tawanorg/claude-sync](https://github.com/tawanorg/claude-sync), without this fork's changes. To use this fork, build it.

**Prerequisite:** Go 1.24+, with Go's bin folder (`go env GOPATH`/bin) on your PATH

Install:

```bash
git clone https://github.com/Karonan/claude-sync
cd claude-sync
go install -ldflags "-X main.version=dev" ./cmd/claude-sync
claude-sync --version   # claude-sync version dev
```

Update (run in the `claude-sync` folder):

```bash
git pull
go install -ldflags "-X main.version=dev" ./cmd/claude-sync
```

Don't run `claude-sync update`: it replaces this build with the original version. Update every device, so they all run the same version.

### Original project

These install [tawanorg/claude-sync](https://github.com/tawanorg/claude-sync) without this fork's changes.

```bash
# npm (Node.js 14+, downloads a pre-compiled binary)
npm install -g @tawandotorg/claude-sync

# Go 1.24+
go install github.com/tawanorg/claude-sync/cmd/claude-sync@latest
```

Pre-built binaries: [GitHub Releases](https://github.com/tawanorg/claude-sync/releases).

## Development

```bash
make test          # Run tests
make fmt           # Format code
make check         # Run all pre-commit checks
make build         # Build ./bin/claude-sync
make build-all     # Build for macOS and Linux (arm64/amd64)
make setup-hooks   # Enable git pre-commit hooks
```

On Windows, a few tests check Unix file permissions and fail, and one `cmd/claude-sync` test copies your real `~/.claude` folder to `~/.claude.backup.<timestamp>`. Run `go test ./internal/...` there instead.

## License

MIT
