package vault

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

// Note represents a parsed Obsidian note
type Note struct {
	Path        string
	UID         string
	Title       string
	Content     string
	FrontMatter map[string]interface{}
	Tags        []string
	Published   bool
	ModTime     time.Time
	Raw         []byte
}

// FrontMatterDelimiter is the YAML front-matter delimiter
const FrontMatterDelimiter = "---"

// PublishTag is the tag that marks a note for publishing
const PublishTag = "#publish"

var (
	// wikiLinkRegex matches [[Note]] and [[Note|Display Text]] patterns
	wikiLinkRegex = regexp.MustCompile(`\[\[([^\]|]+)(?:\|([^\]]+))?\]\]`)
	
	// imageRefRegex matches ![](path) and ![[filename]] patterns
	imageRefRegex = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)|!\[\[([^\]]+)\]\]`)
	
	// codeBlockRegex matches ``` code blocks to exclude wikilinks
	codeBlockRegex = regexp.MustCompile("(?s)```[^`]*```")
	
	// inlineCodeRegex matches `inline code` to exclude wikilinks
	inlineCodeRegex = regexp.MustCompile("`[^`]*`")
)

// ParseNote reads and parses an Obsidian note file
func ParseNote(filePath string) (*Note, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	info, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("getting file info: %w", err)
	}

	note := &Note{
		Path:    filePath,
		ModTime: info.ModTime(),
		Raw:     data,
	}

	if err := note.parse(); err != nil {
		return nil, fmt.Errorf("parsing note: %w", err)
	}

	return note, nil
}

// parse extracts front-matter and content from the note
func (n *Note) parse() error {
	content := string(n.Raw)
	
	// Initialize front-matter map
	n.FrontMatter = make(map[string]interface{})

	// Check for front-matter
	if strings.HasPrefix(content, FrontMatterDelimiter+"\n") {
		// Find the end of front-matter
		lines := strings.Split(content, "\n")
		endIndex := -1
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == FrontMatterDelimiter {
				endIndex = i
				break
			}
		}

		if endIndex > 0 {
			// Extract and parse front-matter
			frontMatterContent := strings.Join(lines[1:endIndex], "\n")
			if err := yaml.Unmarshal([]byte(frontMatterContent), &n.FrontMatter); err != nil {
				return fmt.Errorf("parsing front-matter YAML: %w", err)
			}

			// Extract content after front-matter
			n.Content = strings.Join(lines[endIndex+1:], "\n")
		} else {
			// Malformed front-matter, treat as content
			n.Content = content
		}
	} else {
		// No front-matter, entire content is body
		n.Content = content
	}

	// Extract metadata from front-matter
	if title, ok := n.FrontMatter["title"].(string); ok {
		n.Title = title
	} else {
		// Use filename as title if not specified
		n.Title = strings.TrimSuffix(filepath.Base(n.Path), ".md")
	}

	// Extract UID from front-matter
	if uid, ok := n.FrontMatter["noteUid"].(string); ok {
		n.UID = uid
	}

	// Extract tags
	if tags, ok := n.FrontMatter["tags"]; ok {
		n.Tags = extractTags(tags)
	}

	// Determine if note should be published
	n.Published = n.isPublished()

	return nil
}

// isPublished determines if the note should be published based on front-matter and tags
func (n *Note) isPublished() bool {
	// Check for publish: true in front-matter
	if publish, ok := n.FrontMatter["publish"].(bool); ok && publish {
		return true
	}

	// Check for #publish tag
	for _, tag := range n.Tags {
		if tag == "#publish" || tag == "publish" {
			return true
		}
	}

	return false
}

// EnsureUID ensures the note has a UID, generating one if necessary
func (n *Note) EnsureUID() bool {
	if n.UID != "" {
		return false // No change needed
	}

	n.UID = uuid.New().String()
	n.FrontMatter["noteUid"] = n.UID
	return true // Changed
}

// EnsureWeight ensures the note has a weight if auto-weight is enabled and user hasn't set one
func (n *Note) EnsureWeight(weight int, autoWeight bool) bool {
	if !autoWeight {
		return false
	}

	// Don't override user-set weights
	if _, exists := n.FrontMatter["weight"]; exists {
		return false
	}

	n.FrontMatter["weight"] = weight
	return true // Changed
}

// SerializeFrontMatter returns the updated front-matter as YAML
func (n *Note) SerializeFrontMatter() ([]byte, error) {
	if len(n.FrontMatter) == 0 {
		return nil, nil
	}

	var buf bytes.Buffer
	buf.WriteString(FrontMatterDelimiter + "\n")
	
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	
	if err := encoder.Encode(n.FrontMatter); err != nil {
		return nil, fmt.Errorf("encoding front-matter: %w", err)
	}
	
	encoder.Close()
	buf.WriteString(FrontMatterDelimiter + "\n")
	
	return buf.Bytes(), nil
}

// SerializeContent returns the complete note content with updated front-matter
func (n *Note) SerializeContent() ([]byte, error) {
	frontMatter, err := n.SerializeFrontMatter()
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if frontMatter != nil {
		buf.Write(frontMatter)
	}
	buf.WriteString(n.Content)

	return buf.Bytes(), nil
}

// ExtractWikiLinks finds all wikilinks in the note content
func (n *Note) ExtractWikiLinks() []WikiLink {
	// Remove code blocks and inline code to avoid processing wikilinks within them
	content := n.Content
	content = codeBlockRegex.ReplaceAllString(content, "")
	content = inlineCodeRegex.ReplaceAllString(content, "")

	matches := wikiLinkRegex.FindAllStringSubmatch(content, -1)
	links := make([]WikiLink, 0, len(matches))

	for _, match := range matches {
		link := WikiLink{
			Raw:    match[0],
			Target: strings.TrimSpace(match[1]),
		}

		// Check for custom display text
		if len(match) > 2 && match[2] != "" {
			link.DisplayText = strings.TrimSpace(match[2])
		} else {
			link.DisplayText = link.Target
		}

		// Handle section links (remove #section part for target resolution)
		if idx := strings.Index(link.Target, "#"); idx >= 0 {
			link.Section = link.Target[idx+1:]
			link.Target = link.Target[:idx]
		}

		// Resolve relative paths
		if strings.HasPrefix(link.Target, "../") {
			link.Target = n.resolveRelativePath(link.Target)
		}

		links = append(links, link)
	}

	return links
}

// ExtractImageReferences finds all image references in the note content
func (n *Note) ExtractImageReferences() []ImageRef {
	matches := imageRefRegex.FindAllStringSubmatch(n.Content, -1)
	refs := make([]ImageRef, 0, len(matches))

	for _, match := range matches {
		var ref ImageRef
		
		if match[2] != "" {
			// ![alt](path) format
			ref.AltText = match[1]
			ref.Path = match[2]
		} else if match[3] != "" {
			// ![[filename]] format
			ref.Path = match[3]
			ref.AltText = match[3]
		}

		if ref.Path != "" {
			// Resolve relative to note's directory
			if !filepath.IsAbs(ref.Path) {
				noteDir := filepath.Dir(n.Path)
				ref.Path = filepath.Join(noteDir, ref.Path)
			}
			refs = append(refs, ref)
		}
	}

	return refs
}

// resolveRelativePath resolves a relative path from the note's directory
func (n *Note) resolveRelativePath(relativePath string) string {
	noteDir := filepath.Dir(n.Path)
	resolved := filepath.Join(noteDir, relativePath)
	return filepath.Clean(resolved)
}

// WikiLink represents a wikilink found in note content
type WikiLink struct {
	Raw         string // Original [[...]] text
	Target      string // Target note name/path
	DisplayText string // Custom display text if provided
	Section     string // Section reference if provided (#section)
}

// ImageRef represents an image reference found in note content
type ImageRef struct {
	Path    string // Image file path
	AltText string // Alt text for the image
}

// extractTags converts various tag formats to a string slice
func extractTags(tags interface{}) []string {
	switch v := tags.(type) {
	case []interface{}:
		result := make([]string, 0, len(v))
		for _, tag := range v {
			if str, ok := tag.(string); ok {
				result = append(result, str)
			}
		}
		return result
	case []string:
		return v
	case string:
		// Single tag as string
		return []string{v}
	default:
		return nil
	}
}

// ScanVault recursively scans a vault directory for markdown files
func ScanVault(vaultPath string) ([]string, error) {
	var notePaths []string
	
	err := filepath.Walk(vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden directories and files (except our lock file)
		name := filepath.Base(path)
		if name[0] == '.' && name != ".obsidian-hugo-sync.lock" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Only process markdown files
		if !info.IsDir() && filepath.Ext(path) == ".md" {
			notePaths = append(notePaths, path)
		}

		return nil
	})

	return notePaths, err
} 