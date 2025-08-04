# 📄 Obsidian → Hugo Sync Daemon

**Autosave for your website**: Automatically sync your Obsidian notes to a Hugo Lotus-Docs site. Simply tag notes with `publish: true` or `#publish` and they appear on your public site. Remove the tag and they disappear. All changes queue in a draft branch for review before going live.

## 🌟 Features

- **Zero manual exporting** — One workspace (Obsidian) powers both private notes and public docs
- **No broken links** — Wikilinks convert to Hugo links only when target notes are published
- **Safety net** — All changes queue in a GitHub pull request for review
- **Automatic cleanup** — Unpublish a note and its page (plus empty sections) vanishes automatically
- **Process isolation** — Only one daemon instance per vault with PID file management
- **Incremental sync** — Efficient change detection with content hashing and modification times
- **Image handling** — Automatic image copying with reference tracking and cleanup
- **Robust error handling** — Comprehensive error recovery with user-friendly messages

## 🚀 Quick Start

### Prerequisites

- Go 1.21 or later
- Git repository for your Hugo site
- Obsidian vault with markdown files

### Installation

```bash
# Clone the repository
git clone https://github.com/your-username/obsidian-hugo-sync.git
cd obsidian-hugo-sync

# Build the binary
go build -o obsidian-hugo-sync ./cmd/obsidian-hugo-sync

# Install to system PATH (optional)
sudo mv obsidian-hugo-sync /usr/local/bin/
```

### Basic Usage

```bash
# Start the daemon
obsidian-hugo-sync \
  --vault /path/to/obsidian/vault \
  --repo /path/to/hugo/repository \
  --branch draft-content

# Run in dry-run mode to preview changes
obsidian-hugo-sync \
  --vault /path/to/obsidian/vault \
  --repo /path/to/hugo/repository \
  --dry-run

# Show help
obsidian-hugo-sync --help
```

## 📋 Configuration

### Command Line Options

| Flag | Default | Description |
|------|---------|-------------|
| `--vault` | — | Path to Obsidian vault (required) |
| `--repo` | — | Path to Hugo repository clone (required) |
| `--content-dir` | `content/docs` | Target docs directory in Hugo repo |
| `--branch` | `draft-content` | Git branch for syncing changes |
| `--auto-weight` | `true` | Auto-assign weights to notes and folders |
| `--link-format` | `relref` | Link format: `relref` or `md` |
| `--unpublished-link` | `text` | Handle unpublished links: `text` or `hash` |
| `--interval` | `30s` | Scan interval when fsnotify unavailable |
| `--log-level` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `--dry-run` | `false` | Preview changes without committing |
| `--git-token` | — | Git authentication token |

### Configuration File

Create `~/.config/obsidian-hugo-sync/config.toml`:

```toml
[default]
vault = "/path/to/vault"
repo = "/path/to/repo"
content_dir = "content/docs"
branch = "draft-content"
auto_weight = true
link_format = "relref"
log_level = "info"
```

### Environment Variables

- `GIT_AUTH_TOKEN` — Git authentication token
- `OBSIDIAN_VAULT` — Vault path (overridden by CLI flag)
- `HUGO_REPO` — Repository path (overridden by CLI flag)

## 📝 Publishing Notes

### Mark Notes for Publishing

Add to front-matter:
```yaml
---
title: "My Note"
publish: true
---
```

Or use tags:
```yaml
---
title: "My Note"
tags: ["documentation", "#publish"]
---
```

### File and Path Mapping

**Vault:** `Guides/SEO Basics.md`  
**Hugo:** `content/docs/guides/seo-basics.md`  
**URL:** `/docs/guides/seo-basics/`

Root-level notes fall back to `content/docs/posts/`.

### Wikilink Conversion

| Obsidian | Hugo (relref) | Hugo (md) |
|----------|---------------|-----------|
| `[[Note]]` | `[Note]({{< relref "folder/note" >}})` | `[Note](/docs/folder/note/)` |
| `[[Note\|Custom]]` | `[Custom]({{< relref "folder/note" >}})` | `[Custom](/docs/folder/note/)` |
| `[[Unpublished]]` | `Unpublished` (plain text) | `Unpublished` (plain text) |

## 🔧 Git Authentication

The daemon supports multiple authentication methods:

### SSH Key (Recommended)
```bash
# Ensure your SSH key works
ssh -T git@github.com

# Set custom key if needed
export GIT_SSH_COMMAND="ssh -i /path/to/key"
```

### Personal Access Token
```bash
# Via environment variable
export GIT_AUTH_TOKEN="ghp_your_token_here"

# Or via command line
obsidian-hugo-sync --git-token "ghp_your_token_here" ...
```

### System Git Config
If `git push` works in your shell, the daemon will inherit those credentials.

## 🖼️ Image Handling

Images are automatically copied when referenced in published notes:

- **Markdown format:** `![alt text](path/to/image.png)`
- **Wiki format:** `![[image.png]]`
- **Supported formats:** `.png`, `.jpg`, `.jpeg`, `.gif`, `.svg`, `.webp`
- **Grace period:** 24h before cleanup of unused images
- **Reference tracking:** Maintains image→notes mapping

## 🔍 Monitoring and Debugging

### Log Levels

```bash
# Debug mode - very verbose
obsidian-hugo-sync --log-level debug ...

# Info mode - normal operation (default)
obsidian-hugo-sync --log-level info ...

# Warning and error only
obsidian-hugo-sync --log-level warn ...
```

### Dry Run Mode

Test your configuration without making changes:

```bash
obsidian-hugo-sync --dry-run \
  --vault /path/to/vault \
  --repo /path/to/repo
```

### State and Cache

- **State location:** `~/.cache/obsidian-hugo-sync/{vault-hash}/state.json`
- **Config location:** `~/.config/obsidian-hugo-sync/config.toml`
- **Lock file:** `{vault}/.obsidian-hugo-sync.lock`

## 🛠️ Development

### Building from Source

```bash
# Clone and build
git clone https://github.com/your-username/obsidian-hugo-sync.git
cd obsidian-hugo-sync
go build ./cmd/obsidian-hugo-sync

# Run tests
go test ./...

# Run specific tests
go test ./internal/vault -v
go test ./internal/hugo -v
```

### Project Structure

```
obsidian-hugo-sync/
├── cmd/obsidian-hugo-sync/    # Main application
├── internal/
│   ├── config/                # Configuration management
│   ├── daemon/                # Main orchestrator
│   ├── errors/                # Error handling
│   ├── git/                   # Git operations
│   ├── hugo/                  # Hugo content generation
│   ├── images/                # Image processing
│   ├── logging/               # Structured logging
│   ├── process/               # Process isolation
│   ├── state/                 # State management
│   ├── vault/                 # Obsidian note parsing
│   └── watcher/               # File system watching
├── go.mod
├── go.sum
└── README.md
```

## 🚨 Troubleshooting

### Common Issues

**Another instance running:**
```bash
# Check for lock file
ls /path/to/vault/.obsidian-hugo-sync.lock

# Remove if stale
rm /path/to/vault/.obsidian-hugo-sync.lock
```

**Git authentication failed:**
```bash
# Test SSH
ssh -T git@github.com

# Or check token
curl -H "Authorization: token $GIT_AUTH_TOKEN" https://api.github.com/user
```

**Vault not found:**
```bash
# Check path exists and is readable
ls -la /path/to/vault
```

**Permission denied:**
```bash
# Check file permissions
ls -la /path/to/vault
ls -la /path/to/hugo/repo
```

### Error Categories

The daemon provides helpful error messages with suggestions:

- **Configuration errors** — Check paths and settings
- **Vault errors** — Verify markdown and YAML syntax
- **Git errors** — Check authentication and network
- **Process errors** — Handle lock files and permissions

## 📊 Performance

Target performance metrics:

- **Startup time:** <2s for 1000 notes
- **Incremental sync:** <300ms per note change
- **Memory usage:** <30MB idle, <100MB during full scan
- **Batch processing:** 50 notes/second

## 🤝 Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Make your changes
4. Add tests for new functionality
5. Ensure all tests pass (`go test ./...`)
6. Commit your changes (`git commit -am 'Add amazing feature'`)
7. Push to the branch (`git push origin feature/amazing-feature`)
8. Open a Pull Request

## 📜 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🙏 Acknowledgments

- [Obsidian](https://obsidian.md/) for the amazing note-taking experience
- [Hugo](https://gohugo.io/) for the powerful static site generator
- [Lotus Docs](https://lotusdocs.dev/) for the beautiful documentation theme
- The Go community for excellent libraries and tools

---

**Made with ❤️ for the Obsidian and Hugo communities** 