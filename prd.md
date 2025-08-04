# 📄 Obsidian → Hugo Sync Daemon (Go)

**Concise Project Requirements · Lotus Docs-ready**

## Project Overview (Non-Technical)

Imagine autosave for your website: whenever you place a special tag (`publish: true` or `#publish`) on a note in Obsidian, that note shows up—beautifully formatted—on your public Lotus-Docs site. Remove the tag and the page disappears. Nothing goes live immediately; every change waits in a "draft" branch so you can glance at it first. No command-line gymnastics, no copy-pasting between apps—just write, tag, and merge when you're happy.

### What Problems Does It Solve?

- **Zero manual exporting** — one workspace (Obsidian) powers both your private notes and your public docs
- **No broken links** — links are converted only when the target note is also published
- **Safety net** — everything queues in a GitHub pull request you approve before the site updates
- **Automatic clean-up** — un-publish a note and its page, plus any now-empty section, vanishes automatically

---

## 1. Purpose

- Keep your Obsidian vault and a Lotus-Docs Hugo site in sync
- Every note gets a stable `noteUid`
- Notes marked publishable (`publish: true` or `#publish`) are copied to Hugo
- A folder/section is published only while it contains at least one publishable note
- All changes go to `draft-content`; you merge the PR → Netlify deploys
- **Process isolation** — daemon creates `.obsidian-hugo-sync.lock` with PID; graceful shutdown removes it; startup fails if active PID found

---

## 2. High-Level Workflow

```mermaid
flowchart TD
    scan[Scan vault] --> uid[Ensure noteUid]
    uid --> detect[Detect publishable notes]
    detect --> copy[Copy to content/docs/<folder>/]
    copy --> wikilinks[Convert wikilinks]
    wikilinks --> section[Create / delete _index.md]
    section --> images[Handle image assets]
    images --> git[Commit add / edit / delete → draft-content]
    git --> push[Push & error handling]
```

---

## 3. Key Rules & Defaults

| Topic | Rule |
|-------|------|
| **Publish flag** | `publish: true` or `#publish` tag |
| **Folder publish** | Folder live ↔ at least 1 publishable note |
| **Section file** | `_index.md` auto-generated / removed (never stored in vault) |
| **Wikilinks** | `[[Note]]` → `relref` only if target publishable; else plain text |
| **Weights** | Auto-assign: folders = 100×depth, notes = folder_weight + (10×alphabetical_index) |
| **Cache** | Versioned JSON at `~/.config/obsidian-hugo-sync/{vault-hash}/state.json` (Linux), `~/Library/Application Support/...` (macOS), `%APPDATA%\...` (Windows) |
| **Change detection** | Content hashes (SHA256) + file modification times + UID tracking |
| **File tracking** | Moved/renamed files tracked by `noteUid`, not path |
| **Performance** | Optimized for efficiency with incremental processing |
| **Scope** | Content-only sync (Hugo config handled separately) |

---

## 4. State Management Schema

### Cache Structure (`state.json`)
```json
{
  "version": "1.0",
  "vault_hash": "abc123def456",
  "notes": {
    "note-uid-12345": {
      "source_path": "Guides/SEO Basics.md",
      "hugo_path": "content/docs/guides/seo-basics.md", 
      "last_modified": "2024-01-15T10:30:00Z",
      "last_sync": "2024-01-15T10:30:05Z",
      "published": true,
      "content_hash": "sha256-abc123def456"
    }
  },
  "images": {
    "guides/screenshot.png": ["note-uid-12345", "note-uid-67890"]
  }
}
```

### Change Detection Logic
- **Content changes:** Compare SHA256 hash of file content
- **Rename/move detection:** Match by `noteUid` in front-matter, update paths
- **Publish state changes:** Track `publish: true/false` and `#publish` tag changes
- **Cache invalidation:** Rebuild if version mismatch or corruption detected

---

## 5. File & Path Mapping

**Vault:** `Guides/SEO Basics.md`  
**Hugo:** `content/docs/guides/seo-basics.md`  
**URL:** `/docs/guides/seo-basics/`

*Root-level notes fall back to `content/docs/posts/`.*

### Conflict Resolution
**Slug collision:** If multiple notes generate same slug (e.g., `seo-basics.md`), append `-{noteUid[0:8]}` to duplicates.  
**Deep nesting:** Paths >5 levels deep flatten to `content/docs/{folder1}-{folder2}-{etc}/note.md`  
**Long paths:** Slugs >50 chars truncate with `-{noteUid[0:8]}` suffix

### Image Handling

**Vault:** `Guides/images/screenshot.png`  
**Hugo:** `content/docs/guides/images/screenshot.png`  
- **Copy trigger:** Images copied when `![](path)` or `![[filename]]` found in published notes
- **Cleanup logic:** 24h grace period after last published reference removed (protects against temporary unpublish)
- **Reference tracking:** Daemon maintains image→notes mapping; deletes when mapping empty
- **Supported formats:** `.png`, `.jpg`, `.jpeg`, `.gif`, `.svg`, `.webp`

---

## 6. Link Tracking & Wikilink Conversion

### Core Processing Rules
- **Slug map rebuilt every cycle** → the daemon records every current publishable note as `folder/slug`
- **During copy each `[[wikilink]]` is looked up:**
  - **Hit** → `[text]({{< relref "folder/slug" >}})` (or Markdown link if `--link-format=md`)
  - **Miss** → `text` (plain) or `text` linked to `#` when `--unpublished-link=hash`
- **Automatic downgrade/upgrade** — if a target note is later un-published, next sync converts the link back to plain text; when a target becomes publishable the link upgrades to a real URL
- **Guarantee** — A merged PR can never introduce an internal 404

### Advanced Link Patterns
```markdown
[[Note]]                    → [Note]({{< relref "folder/note" >}})
[[Note|Custom Text]]        → [Custom Text]({{< relref "folder/note" >}})
[[Note#Section]]            → [Note]({{< relref "folder/note" >}}) (Hugo handles sections)
[[../Other Folder/Note]]    → Resolve relative paths to absolute vault paths first

# Skip processing in code blocks
```markdown
[[Not processed]]
```

# Skip processing in inline code
`[[Not processed]]`
```

### Implementation Notes
- **Custom display text:** Extract `|Custom Text` and use as link text
- **Section links:** Ignore `#Section` part (Hugo's relref handles navigation)
- **Relative paths:** Normalize `../` paths relative to current note's folder
- **Code block detection:** Skip wikilinks inside ` ``` ` blocks and `` `inline` `` code

---

## 7. Front-Matter Mutations

### YAML Processing Rules
- Insert `noteUid` (UUID v4) first if missing
- Insert `weight` only when `--auto-weight` (default on) and user hasn't set it
- **Respect user weights:** If user sets `weight: 50`, preserve it (don't auto-assign)
- **YAML support:** Basic strings, numbers, booleans, arrays, multi-line (keep simple)
- **Error handling:** Malformed YAML → clear error message, skip file until fixed
- **Tag processing:** Support both `publish: true` in front-matter and `#publish` in tags array
- Nothing else is modified

### Example Processing
```yaml
---
# Before (user file)
title: "SEO Guide"
tags: [seo, #publish, marketing]
weight: 75
---

# After (daemon processing)  
noteUid: 12345678-abcd-ef90-1234-567890abcdef
title: "SEO Guide"
tags: [seo, #publish, marketing]
weight: 75  # User weight preserved
---
```

---

## 8. Configuration Management

### CLI Flags (Minimum Set)

| Flag | Default | Notes |
|------|---------|-------|
| `--vault` | — | Path to vault (required) |
| `--repo` | — | Path to Hugo repo clone (required) |
| `--content-dir` | `content/docs` | Target docs dir |
| `--branch` | `draft-content` | Sync branch |
| `--auto-weight` | `true` | Disable to manage weights yourself |
| `--link-format` | `relref` | `relref` = `{{< relref "path" >}}`, `md` = `[text](/docs/path/)` (static Hugo URLs) |
| `--unpublished-link` | `text` | `hash` to link `#` instead |
| `--interval` | `30s` | Scan interval if fsnotify off |
| `--log-level` | `info` | `debug`, `info`, `warn`, `error` |
| `--dry-run` | `false` | Preview changes without committing |

*All other advanced flags remain available; see README once built.*

### Configuration File Support
```toml
# ~/.config/obsidian-hugo-sync/config.toml
[default]
vault = "/path/to/vault"
repo = "/path/to/repo" 
content_dir = "content/docs"
branch = "draft-content"
auto_weight = true
link_format = "relref"
log_level = "info"

# Sensitive data excluded from version control
# Use environment variables or separate auth config
```

### Configuration Rules
- **File location:** Same directory as cache: `~/.config/obsidian-hugo-sync/config.toml`
- **Precedence:** CLI flags override config file values
- **Security:** Never commit auth tokens; use environment variables or separate auth file
- **MVP scope:** Single profile only (no multi-vault support initially)
- **Validation:** Clear error messages for invalid config values

---

## 9. Error Handling & Recovery

### Cache Management
- **Corruption detection** — Validate `state.json` on startup; rebuild if corrupted
- **Cache versioning** — Migrate cache format automatically for future updates
- **Fallback strategy** — Full vault rescan if cache is missing or invalid

### Git Operations
- **Network timeout** → 3 retries with 2s, 4s, 8s delays, then fail with clear offline guidance  
- **Permission denied** → Check SSH key validity, suggest `ssh -T git@github.com` test
- **Branch management:**
  - Auto-create `draft-content` branch if missing
  - Atomic commits: batch all changes into single commit per sync cycle
  - Commit messages: `sync: updated 3 notes, deleted 1, added 2 images`
- **Conflict resolution:**
  - Auto-rebase `draft-content` on `main`; if conflicts, pause daemon and alert user
  - Users can add manual content to Hugo repo on separate branches (not `draft-content`)
  - Daemon detects manual Hugo edits and warns but continues
- **Error recovery:**
  - Auto-restart after recoverable errors (network failures, temporary permission issues)
  - Corrupted Git state → attempt `git fsck` and `git reset --hard origin/draft-content`
  - Force-push detection → warn user and attempt to recover from origin

### User Feedback
- **Clear error messages** — Specific guidance when operations fail
- **Dry-run mode** — Test configuration and preview changes before execution
- **Logging levels** — Configurable verbosity for debugging

---

## 10. Git Authentication

The daemon uses go-git; it needs Git credentials to push to your `draft-content` branch.

| Method | How to Supply | Notes |
|--------|---------------|-------|
| **SSH key (recommended)** | Ensure `~/.ssh/id_ed25519` (or custom) can access the repo. Set `GIT_SSH_COMMAND="ssh -i /path/to/key"` if needed. | Works offline; no token rotation |
| **Personal Access Token** | Set env var `GIT_AUTH_TOKEN=<token>` or flag `--git-token`. Creates HTTPS URL `https://<token>@github.com/user/repo.git`. | Token needs repo scope; rotate periodically |
| **System Git config** | If `git push` works in shell, daemon inherits credential helper (e.g., macOS Keychain). | Easiest for interactive setup |

**Resolution order:** `--git-token` flag → `GIT_AUTH_TOKEN` env → SSH agent → system Git config.

---

## 11. Performance Targets & Monitoring

| Metric | Target | Notes |
|--------|---------|-------|
| **Startup time** | <2s for 1000 notes | Includes cache validation & Git status |
| **Incremental sync** | <300ms single note | File change → Git commit complete |
| **Memory baseline** | <30MB idle | <100MB during full vault scan |
| **Batch processing** | 50 notes/second | Publishing large note sets |
| **Cache rebuild** | <10s for 1000 notes | When state.json corrupted/missing |

*Note: Performance targets are guidelines; focus on stability over micro-optimizations.*

### Monitoring & Observability
- **Prometheus metrics:** Export basic stats (sync cycles, errors, processing times)
- **Structured logging:** Use Go's `log/slog` with configurable levels
- **Dry-run mode:** Show detailed Git diffs of proposed changes
- **Status reporting:** Log sync summaries: "Processed 5 files, 2 published, 1 unpublished"

---

## 12. Tech Stack & File Watching

- **Go** ≥ 1.21
- **File watching strategy:**
  - Primary: fsnotify with recursive vault monitoring
  - Fallback: 5-minute polling when fsnotify fails (network drives, >8192 files on Linux)
  - Batch processing: collect simultaneous file changes, process atomically
  - No debouncing: ignore rapid typing changes, rely on periodic scans
- **go-git/v5** for Git ops
- **yaml.v3, uuid, sha256**
- **log/slog** for structured logging

---

## 13. Testing Strategy

### Test Scope & Environment
- **Primary target:** Linux (MVP focus)
- **Unit tests:** Mock fsnotify, Git operations, and file system interactions
- **Integration tests:** Real vault and Git repository interactions
- **Test isolation:** Create temporary vaults and Git repos for each test
- **Cross-platform:** Future consideration, Linux-first approach

---

## 14. Acceptance Tests

- UIDs added everywhere, no duplicates across vault
- Folder appears when first note published; disappears when last unpublished
- Zero broken internal links after PR merge (comprehensive link validation)
- Deleting `state.json` triggers rebuild without duplicate content
- Daemon restart resumes work without duplicating commits
- Images correctly copied and cleaned up (24h grace period respected)
- All error scenarios fail gracefully with actionable messages
- Dry-run mode accurately previews all changes without side effects
- Performance targets met under normal and stress conditions

---

## 15. Post-MVP Ideas

- Auto-create GitHub PRs via API
- Netlify Deploy Preview integration
- Slack/Discord notifications for sync events
- BoltDB cache for >50MB vaults
- Cross-platform binaries & Homebrew tap
- Watch for Hugo config changes and auto-restart

---

**END – PRD v12-implementation-ready**