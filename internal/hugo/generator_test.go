package hugo

import (
	"obsidian-hugo-sync/internal/vault"
	"strings"
	"testing"
	"time"
)

func TestGenerateContent(t *testing.T) {
	generator := NewGenerator("/vault", "content/docs", "relref", "text")
	
	note := &vault.Note{
		Path:    "/vault/guides/test.md",
		UID:     "test-uid-123",
		Title:   "Test Note",
		Content: "This is test content with [[Another Note]] link.",
		Published: true,
	}
	
	hugoContent, err := generator.GenerateContent(note, 100)
	if err != nil {
		t.Fatalf("Failed to generate content: %v", err)
	}
	
	if hugoContent.Title != "Test Note" {
		t.Errorf("Expected title 'Test Note', got '%s'", hugoContent.Title)
	}
	
	if hugoContent.Weight != 100 {
		t.Errorf("Expected weight 100, got %d", hugoContent.Weight)
	}
	
	if hugoContent.NoteUID != "test-uid-123" {
		t.Errorf("Expected noteUID 'test-uid-123', got '%s'", hugoContent.NoteUID)
	}
}

func TestCreateSlug(t *testing.T) {
	generator := NewGenerator("/vault", "content/docs", "relref", "text")
	
	tests := []struct {
		filename string
		noteUID  string
		expected string
	}{
		{"Simple File.md", "uid123", "simple-file.md"},
		{"File with Spaces & Special!.md", "uid123", "file-with-spaces-special.md"},
		{"VeryLongFileNameThatExceedsFiftyCharactersAndShouldBeTruncated.md", "uid12345", "verylongfilenamethatexceedsfiftycharacters-uid12345.md"},
		{".md", "uid123", "untitled.md"},
	}
	
	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			result := generator.createSlug(tt.filename, tt.noteUID)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestProcessWikiLinks(t *testing.T) {
	generator := NewGenerator("/vault", "content/docs", "relref", "text")
	
	// Set up slug map for testing
	generator.slugMap = map[string]string{
		"Published Note": "guides/published-note",
		"Another Note":   "posts/another-note",
	}
	
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{
			name:     "published link",
			content:  "This has [[Published Note]] link.",
			expected: "This has [Published Note]({{< relref \"guides/published-note\" >}}) link.",
		},
		{
			name:     "unpublished link",
			content:  "This has [[Unpublished Note]] link.",
			expected: "This has Unpublished Note link.",
		},
		{
			name:     "custom display text",
			content:  "This has [[Published Note|Custom Text]] link.",
			expected: "This has [Custom Text]({{< relref \"guides/published-note\" >}}) link.",
		},
		{
			name:     "code blocks preserved",
			content:  "Normal [[Published Note]] and `[[Not A Link]]` and ```\n[[Also Not A Link]]\n```",
			expected: "Normal [Published Note]({{< relref \"guides/published-note\" >}}) and __INLINE_CODE_0__ and __CODE_BLOCK_0__",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generator.processWikiLinks(tt.content)
			if result != tt.expected {
				t.Errorf("Expected:\n%s\nGot:\n%s", tt.expected, result)
			}
		})
	}
}

func TestCreateHugoLink(t *testing.T) {
	tests := []struct {
		linkFormat   string
		hugoPath     string
		displayText  string
		expected     string
	}{
		{
			linkFormat:  "relref",
			hugoPath:    "guides/test-note",
			displayText: "Test Note",
			expected:    "[Test Note]({{< relref \"guides/test-note\" >}})",
		},
		{
			linkFormat:  "md",
			hugoPath:    "guides/test-note",
			displayText: "Test Note",
			expected:    "[Test Note](/guides/test-note/)",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.linkFormat, func(t *testing.T) {
			generator := NewGenerator("/vault", "content/docs", tt.linkFormat, "text")
			result := generator.createHugoLink(tt.hugoPath, tt.displayText)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestGenerateIndexFile(t *testing.T) {
	generator := NewGenerator("/vault", "content/docs", "relref", "text")
	
	indexContent := generator.GenerateIndexFile("content/docs/guides", 200)
	
	if indexContent.Title != "Guides" {
		t.Errorf("Expected title 'Guides', got '%s'", indexContent.Title)
	}
	
	if indexContent.Weight != 200 {
		t.Errorf("Expected weight 200, got %d", indexContent.Weight)
	}
	
	if !strings.Contains(indexContent.Path, "_index.md") {
		t.Error("Expected path to contain '_index.md'")
	}
	
	if indexContent.Content != "" {
		t.Error("Expected content to be empty (front-matter only)")
	}
}

func TestCalculateWeights(t *testing.T) {
	tests := []struct {
		folderPath string
		expected   int
	}{
		{"content/docs", 200},
		{"content/docs/guides", 300},
		{"content/docs/guides/advanced", 400},
	}
	
	for _, tt := range tests {
		t.Run(tt.folderPath, func(t *testing.T) {
			result := CalculateFolderWeight(tt.folderPath)
			if result != tt.expected {
				t.Errorf("Expected %d, got %d", tt.expected, result)
			}
		})
	}
	
	// Test note weight calculation
	noteWeight := CalculateNoteWeight(200, 3)
	expected := 230 // 200 + (10 * 3)
	if noteWeight != expected {
		t.Errorf("Expected note weight %d, got %d", expected, noteWeight)
	}
}

func TestHugoContentSerialization(t *testing.T) {
	content := &HugoContent{
		Path:        "content/docs/test.md",
		Title:       "Test Note",
		Content:     "This is test content.",
		Weight:      100,
		NoteUID:     "test-uid-123",
		LastUpdated: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
	}
	
	serialized := content.Serialize()
	
	// Check that front-matter is properly formatted
	if !strings.Contains(serialized, "title: \"Test Note\"") {
		t.Error("Expected title in front-matter")
	}
	
	if !strings.Contains(serialized, "weight: 100") {
		t.Error("Expected weight in front-matter")
	}
	
	if !strings.Contains(serialized, "noteUid: \"test-uid-123\"") {
		t.Error("Expected noteUid in front-matter")
	}
	
	if !strings.Contains(serialized, "This is test content.") {
		t.Error("Expected content after front-matter")
	}
	
	// Check front-matter delimiters
	if !strings.HasPrefix(serialized, "---\n") {
		t.Error("Expected front-matter to start with ---")
	}
	
	if !strings.Contains(serialized, "\n---\n\n") {
		t.Error("Expected front-matter to end with --- followed by content")
	}
} 