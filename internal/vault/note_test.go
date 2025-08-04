package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseNote(t *testing.T) {
	// Create a temporary test file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.md")
	
	content := `---
title: "Test Note"
tags: ["test", "#publish"]
noteUid: "test-uid-123"
---

# Test Content

This is a test note with [[wikilink]] and some content.

![Test Image](images/test.png)
`
	
	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	
	// Parse the note
	note, err := ParseNote(testFile)
	if err != nil {
		t.Fatalf("Failed to parse note: %v", err)
	}
	
	// Test basic properties
	if note.Title != "Test Note" {
		t.Errorf("Expected title 'Test Note', got '%s'", note.Title)
	}
	
	if note.UID != "test-uid-123" {
		t.Errorf("Expected UID 'test-uid-123', got '%s'", note.UID)
	}
	
	if !note.Published {
		t.Error("Expected note to be published (has #publish tag)")
	}
	
	if !strings.Contains(note.Content, "This is a test note") {
		t.Error("Content not parsed correctly")
	}
}

func TestEnsureUID(t *testing.T) {
	note := &Note{
		FrontMatter: make(map[string]interface{}),
	}
	
	// Test that UID is generated when missing
	changed := note.EnsureUID()
	if !changed {
		t.Error("Expected EnsureUID to return true when UID was missing")
	}
	
	if note.UID == "" {
		t.Error("Expected UID to be generated")
	}
	
	// Test that UID is not changed when already present
	oldUID := note.UID
	changed = note.EnsureUID()
	if changed {
		t.Error("Expected EnsureUID to return false when UID already exists")
	}
	
	if note.UID != oldUID {
		t.Error("Expected UID to remain unchanged")
	}
}

func TestExtractWikiLinks(t *testing.T) {
	note := &Note{
		Content: `This note has [[Simple Link]] and [[Link|Custom Text]] and [[Note#Section]].
		
Also has code: ` + "`[[Not a link]]`" + ` and:

` + "```" + `
[[Also not a link]]
` + "```" + `
`,
	}
	
	links := note.ExtractWikiLinks()
	
	expectedLinks := []string{"Simple Link", "Link", "Note"}
	if len(links) != len(expectedLinks) {
		t.Errorf("Expected %d links, got %d", len(expectedLinks), len(links))
	}
	
	for i, expected := range expectedLinks {
		if i >= len(links) || links[i].Target != expected {
			t.Errorf("Expected link %d to be '%s', got '%s'", i, expected, links[i].Target)
		}
	}
	
	// Test custom display text
	if len(links) > 1 && links[1].DisplayText != "Custom Text" {
		t.Errorf("Expected custom display text 'Custom Text', got '%s'", links[1].DisplayText)
	}
}

func TestExtractImageReferences(t *testing.T) {
	note := &Note{
		Content: `This note has ![alt text](images/test.png) and ![[embedded.jpg]].`,
		Path:    "/vault/notes/test.md",
	}
	
	images := note.ExtractImageReferences()
	
	if len(images) != 2 {
		t.Errorf("Expected 2 image references, got %d", len(images))
	}
	
	// Check first image (markdown format)
	if len(images) > 0 {
		if images[0].AltText != "alt text" {
			t.Errorf("Expected alt text 'alt text', got '%s'", images[0].AltText)
		}
	}
	
	// Check second image (wiki format)
	if len(images) > 1 {
		if images[1].AltText != "embedded.jpg" {
			t.Errorf("Expected alt text 'embedded.jpg', got '%s'", images[1].AltText)
		}
	}
}

func TestIsPublished(t *testing.T) {
	tests := []struct {
		name        string
		frontMatter map[string]interface{}
		tags        []string
		expected    bool
	}{
		{
			name:        "publish true in frontmatter",
			frontMatter: map[string]interface{}{"publish": true},
			expected:    true,
		},
		{
			name:        "publish false in frontmatter",
			frontMatter: map[string]interface{}{"publish": false},
			expected:    false,
		},
		{
			name:     "publish tag in tags",
			tags:     []string{"test", "#publish", "other"},
			expected: true,
		},
		{
			name:     "publish tag without hash",
			tags:     []string{"test", "publish", "other"},
			expected: true,
		},
		{
			name:     "no publish indicator",
			tags:     []string{"test", "other"},
			expected: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			note := &Note{
				FrontMatter: tt.frontMatter,
				Tags:        tt.tags,
			}
			if note.FrontMatter == nil {
				note.FrontMatter = make(map[string]interface{})
			}
			
			result := note.isPublished()
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
} 