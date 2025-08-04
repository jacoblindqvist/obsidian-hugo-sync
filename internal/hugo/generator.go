package hugo

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"obsidian-hugo-sync/internal/vault"
)

// Generator handles conversion from Obsidian notes to Hugo format
type Generator struct {
	vaultPath       string
	contentDir      string
	linkFormat      string
	unpublishedLink string
	slugMap         map[string]string // target -> hugo_path for link resolution
	protectedContent map[string]string // placeholder -> original content for restoration
}

// NewGenerator creates a new Hugo content generator
func NewGenerator(vaultPath, contentDir, linkFormat, unpublishedLink string) *Generator {
	return &Generator{
		vaultPath:        vaultPath,
		contentDir:       contentDir,
		linkFormat:       linkFormat,
		unpublishedLink:  unpublishedLink,
		slugMap:          make(map[string]string),
		protectedContent: make(map[string]string),
	}
}

// GenerateContent converts an Obsidian note to Hugo format
func (g *Generator) GenerateContent(note *vault.Note, weight int) (*HugoContent, error) {
	hugoPath := g.generateHugoPath(note.Path, note.UID)
	
	// Process wikilinks in content
	processedContent := g.processWikiLinks(note.Content)
	
	// Escape Hugo shortcodes with placeholder text
	processedContent = g.escapeExampleShortcodes(processedContent)
	
	content := &HugoContent{
		Path:        hugoPath,
		Title:       note.Title,
		Content:     processedContent,
		Weight:      weight,
		NoteUID:     note.UID,
		LastUpdated: time.Now(),
	}
	
	return content, nil
}

// HugoContent represents processed content ready for Hugo
type HugoContent struct {
	Path        string
	Title       string
	Content     string
	Weight      int
	NoteUID     string
	LastUpdated time.Time
}

// Serialize returns the complete Hugo content with front-matter
func (hc *HugoContent) Serialize() string {
	var sb strings.Builder
	
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("title: %q\n", hc.Title))
	sb.WriteString(fmt.Sprintf("weight: %d\n", hc.Weight))
	sb.WriteString(fmt.Sprintf("noteUid: %q\n", hc.NoteUID))
	sb.WriteString(fmt.Sprintf("lastUpdated: %s\n", hc.LastUpdated.Format(time.RFC3339)))
	sb.WriteString("---\n\n")
	sb.WriteString(hc.Content)
	
	return sb.String()
}

// generateHugoPath creates the Hugo content path for a note
func (g *Generator) generateHugoPath(notePath, noteUID string) string {
	// Get relative path from vault root
	relPath, err := filepath.Rel(g.vaultPath, notePath)
	if err != nil {
		// Fallback to using the full path if relative calculation fails
		relPath = filepath.Clean(notePath)
	}
	
	// Convert to Hugo path structure
	dir := filepath.Dir(relPath)
	filename := filepath.Base(relPath)
	
	// Create slug from filename
	slug := g.createSlug(filename, noteUID)
	
	// Handle root level notes
	if dir == "." || dir == "/" {
		return filepath.Join(g.contentDir, "posts", slug)
	}
	
	// Convert folder structure to Hugo path
	hugoDirs := strings.Split(dir, string(filepath.Separator))
	hugoPath := append([]string{g.contentDir}, hugoDirs...)
	hugoPath = append(hugoPath, slug)
	
	return filepath.Join(hugoPath...)
}

// createSlug creates a URL-friendly slug from a filename
func (g *Generator) createSlug(filename, noteUID string) string {
	// Remove .md extension
	name := strings.TrimSuffix(filename, ".md")
	
	// Convert to lowercase and replace spaces/special chars with hyphens
	slug := strings.ToLower(name)
	slug = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	
	// Handle edge cases
	if slug == "" {
		slug = "untitled"
	}
	
	// Truncate if too long and append UID
	if len(slug) > 50 {
		slug = slug[:42] + "-" + noteUID[:8]
	}
	
	return slug + ".md"
}

// UpdateSlugMap updates the internal mapping of note targets to Hugo paths
func (g *Generator) UpdateSlugMap(publishedNotes map[string]*vault.Note) {
	g.slugMap = make(map[string]string)
	
	for _, note := range publishedNotes {
		if note.Published {
			// Map by filename (without path and extension)
			filename := strings.TrimSuffix(filepath.Base(note.Path), ".md")
			hugoPath := g.generateHugoPath(note.Path, note.UID)
			
			// Store relative path for Hugo relref (strip content/ but keep subdirs like docs/)
			relPath := hugoPath
			if strings.HasPrefix(relPath, "content/") {
				relPath = strings.TrimPrefix(relPath, "content/")
			} else if strings.HasPrefix(relPath, "content\\") {
				relPath = strings.TrimPrefix(relPath, "content\\")
			}
			relPath = strings.ReplaceAll(relPath, "\\", "/")
			relPath = g.convertToHugoURL(relPath)
			relPath = strings.TrimSuffix(relPath, ".md")
			
			g.slugMap[filename] = relPath
			
			// Also map by full title if different
			if note.Title != filename {
				g.slugMap[note.Title] = relPath
			}
		}
	}
}

// processWikiLinks converts wikilinks to Hugo links
func (g *Generator) processWikiLinks(content string) string {
	// Regex to match wikilinks while avoiding code blocks
	wikiLinkRegex := regexp.MustCompile(`\[\[([^\]|]+)(?:\|([^\]]+))?\]\]`)
	
	// First, protect code blocks and inline code
	protectedContent := g.protectCodeSections(content)
	
	// Process wikilinks
	result := wikiLinkRegex.ReplaceAllStringFunc(protectedContent, func(match string) string {
		return g.convertWikiLink(match)
	})
	
	// Restore code sections
	return g.restoreCodeSections(result)
}

// convertWikiLink converts a single wikilink to Hugo format
func (g *Generator) convertWikiLink(wikilink string) string {
	// Extract target and display text
	wikiLinkRegex := regexp.MustCompile(`\[\[([^\]|]+)(?:\|([^\]]+))?\]\]`)
	matches := wikiLinkRegex.FindStringSubmatch(wikilink)
	
	if len(matches) < 2 {
		return wikilink // Return unchanged if parsing fails
	}
	
	target := strings.TrimSpace(matches[1])
	displayText := target
	
	if len(matches) > 2 && matches[2] != "" {
		displayText = strings.TrimSpace(matches[2])
	}
	
	// Remove section reference for target lookup
	targetForLookup := target
	if idx := strings.Index(target, "#"); idx >= 0 {
		targetForLookup = target[:idx]
	}
	
	// Look up target in slug map
	if hugoPath, exists := g.slugMap[targetForLookup]; exists {
		// Target is published, create proper link
		return g.createHugoLink(hugoPath, displayText)
	}
	
	// Target not published, handle based on configuration
	switch g.unpublishedLink {
	case "hash":
		return fmt.Sprintf("[%s](#)", displayText)
	default: // "text"
		return displayText
	}
}

// createHugoLink creates a Hugo link based on the configured format
func (g *Generator) createHugoLink(hugoPath, displayText string) string {
	switch g.linkFormat {
	case "md":
		// Generate static markdown link
		url := "/" + strings.ReplaceAll(hugoPath, "\\", "/")
		if !strings.HasSuffix(url, "/") {
			url += "/"
		}
		return fmt.Sprintf("[%s](%s)", displayText, url)
	default: // "relref"
		// Hugo relref expects path relative to content root (content/), not contentDir (content/docs)
		// So we need to strip only "content/" prefix, keeping the docs/ part
		relrefPath := hugoPath
		
		// Strip "content/" prefix if present (but keep subdirectories like "docs/")
		if strings.HasPrefix(relrefPath, "content/") {
			relrefPath = strings.TrimPrefix(relrefPath, "content/")
		} else if strings.HasPrefix(relrefPath, "content\\") {
			relrefPath = strings.TrimPrefix(relrefPath, "content\\")
		}
		
		// Convert backslashes to forward slashes for Hugo
		relrefPath = strings.ReplaceAll(relrefPath, "\\", "/")
		
		// Convert to Hugo URL format (lowercase, spaces to hyphens)
		relrefPath = g.convertToHugoURL(relrefPath)
		
		return fmt.Sprintf("[%s]({{< relref \"%s\" >}})", displayText, relrefPath)
	}
}

// convertToHugoURL converts a file path to Hugo's URL format (lowercase, spaces to hyphens)
func (g *Generator) convertToHugoURL(path string) string {
	// Split path into components
	parts := strings.Split(path, "/")
	
	// Convert each part (except the final filename) to Hugo URL format
	for i, part := range parts {
		if i == len(parts)-1 {
			// Last part is filename, keep as-is (already slugified)
			continue
		}
		
		// Convert folder names to Hugo format: lowercase, spaces to hyphens
		converted := strings.ToLower(part)
		converted = strings.ReplaceAll(converted, " ", "-")
		// Also handle other common characters
		converted = strings.ReplaceAll(converted, "_", "-")
		parts[i] = converted
	}
	
	return strings.Join(parts, "/")
}

// protectCodeSections replaces code blocks, inline code, and markdown links with placeholders
func (g *Generator) protectCodeSections(content string) string {
	// Clear previous protected content
	g.protectedContent = make(map[string]string)
	protected := content
	
	// Protect markdown links first (to avoid processing wikilinks inside them)
	markdownLinkRegex := regexp.MustCompile(`\[([^\]]*)\]\(([^)]*)\)`)
	markdownLinks := markdownLinkRegex.FindAllString(protected, -1)
	
	for i, link := range markdownLinks {
		placeholder := fmt.Sprintf("__MARKDOWN_LINK_%d__", i)
		g.protectedContent[placeholder] = link
		protected = strings.Replace(protected, link, placeholder, 1)
	}
	
	// Protect code blocks
	codeBlockRegex := regexp.MustCompile("(?s)```[^`]*```")
	codeBlocks := codeBlockRegex.FindAllString(protected, -1)
	
	for i, block := range codeBlocks {
		placeholder := fmt.Sprintf("__CODE_BLOCK_%d__", i)
		g.protectedContent[placeholder] = block
		protected = strings.Replace(protected, block, placeholder, 1)
	}
	
	// Protect inline code
	inlineCodeRegex := regexp.MustCompile("`[^`]*`")
	inlineCodes := inlineCodeRegex.FindAllString(protected, -1)
	
	for i, code := range inlineCodes {
		placeholder := fmt.Sprintf("__INLINE_CODE_%d__", i)
		g.protectedContent[placeholder] = code
		protected = strings.Replace(protected, code, placeholder, 1)
	}
	
	return protected
}

// restoreCodeSections restores code blocks, inline code, and markdown links from placeholders
func (g *Generator) restoreCodeSections(content string) string {
	restored := content
	
	// Restore all protected content
	for placeholder, original := range g.protectedContent {
		restored = strings.Replace(restored, placeholder, original, -1)
	}
	
	return restored
}

// escapeExampleShortcodes escapes Hugo shortcodes that contain placeholder/example text
func (g *Generator) escapeExampleShortcodes(content string) string {
	// Pattern to match Hugo shortcodes like {{< relref "path" >}}
	shortcodeRegex := regexp.MustCompile(`\{\{<\s*(\w+)\s+"([^"]+)"\s*>\}\}`)
	
	// Common placeholder patterns that should be escaped
	placeholderPatterns := []string{
		"folder/slug",
		"folder/note", 
		"path",
		"folder/path",
		"docs/path",
	}
	
	return shortcodeRegex.ReplaceAllStringFunc(content, func(match string) string {
		// Extract the path from the shortcode
		matches := shortcodeRegex.FindStringSubmatch(match)
		if len(matches) < 3 {
			return match
		}
		
		shortcodeType := matches[1]
		path := matches[2]
		
		// Check if this looks like a placeholder/example
		for _, placeholder := range placeholderPatterns {
			if path == placeholder {
				// Escape the shortcode so Hugo displays it as literal text
				return fmt.Sprintf("{{</* %s \"%s\" */>}}", shortcodeType, path)
			}
		}
		
		// Not a placeholder, leave unchanged
		return match
	})
}

// GenerateIndexFile creates an _index.md file for a directory
func (g *Generator) GenerateIndexFile(dirPath string, weight int) *HugoContent {
	// Extract directory name for title
	dirName := filepath.Base(dirPath)
	title := strings.ReplaceAll(dirName, "-", " ")
	title = strings.Title(title)
	
	indexPath := filepath.Join(dirPath, "_index.md")
	
	return &HugoContent{
		Path:        indexPath,
		Title:       title,
		Content:     "", // No content, just front-matter
		Weight:      weight,
		NoteUID:     "", // Index files don't have UIDs
		LastUpdated: time.Now(),
	}
}

// CalculateFolderWeight calculates weight for a folder based on depth
func CalculateFolderWeight(folderPath string) int {
	depth := strings.Count(folderPath, string(filepath.Separator))
	return 100 * (depth + 1)
}

// CalculateNoteWeight calculates weight for a note within its folder
func CalculateNoteWeight(folderWeight int, alphabeticalIndex int) int {
	return folderWeight + (10 * alphabeticalIndex)
} 