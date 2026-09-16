package protocol

import (
	"fmt"
	"strings"
	"time"
)

// PageInfo captures the metadata of the web page during review.
type PageInfo struct {
	SanitizedURL   string `json:"sanitizedUrl"`
	Title          string `json:"title"`
	ViewportWidth  int    `json:"viewportWidth"`
	ViewportHeight int    `json:"viewportHeight"`
	ScrollX        int    `json:"scrollX"`
	ScrollY        int    `json:"scrollY"`
	CapturedAt     string `json:"capturedAt"`
}

// FrameworkInfo captures framework-specific component and source metadata.
type FrameworkInfo struct {
	Name           string            `json:"name"`                     // "React", "Elixir Phoenix", "Astro", "Svelte", "Vue", "HTMX", "Static"
	Component      string            `json:"component,omitempty"`      // e.g. "<Button>", "OrderLive"
	SourceLocation string            `json:"sourceLocation,omitempty"` // e.g. "components/Button.tsx:42:15"
	Provenance     string            `json:"provenance"`               // "exact", "inferred", "unavailable"
	Attributes     map[string]string `json:"attributes,omitempty"`     // e.g. {"phx-click": "submit", "hx-post": "/api"}
}

// TargetInfo captures complete element inspection details.
type TargetInfo struct {
	TagName        string            `json:"tagName"`
	Role           string            `json:"role,omitempty"`           // Resolved ARIA role
	AccessibleName string            `json:"accessibleName,omitempty"` // Resolved accessible name
	Selector       string            `json:"selector"`
	ElementPath    string            `json:"elementPath"`
	FullPath       string            `json:"fullPath"`
	CSSClasses     string            `json:"cssClasses,omitempty"`
	SelectedText   string            `json:"selectedText,omitempty"`
	TextSnippet    string            `json:"textSnippet,omitempty"`
	HTMLSnippet    string            `json:"htmlSnippet,omitempty"`
	RectViewport   Rect              `json:"rectViewport"`
	RectPage       Rect              `json:"rectPage"`
	IsFixed        bool              `json:"isFixed"`
	ComputedStyles map[string]string `json:"computedStyles,omitempty"`
	Framework      FrameworkInfo     `json:"framework"`
}

// ReviewPayload captures the complete context of an element grab.
type ReviewPayload struct {
	Page           PageInfo   `json:"page"`
	Target         TargetInfo `json:"target"`
	NearbyText     []string   `json:"nearbyText,omitempty"`
	NearbyElements []string   `json:"nearbyElements,omitempty"`
	AncestorPath   []string   `json:"ancestorPath,omitempty"`
}

// ReviewNote represents a pinned review comment attached to an element.
type ReviewNote struct {
	ID        string         `json:"id"`
	Index     int            `json:"index"` // 1-indexed pin number (1, 2, 3...)
	Intent    string         `json:"intent"` // "design_fix", "bug", "clarification"
	Comment   string         `json:"comment"`
	CreatedAt time.Time      `json:"createdAt"`
	Payload   *ReviewPayload `json:"payload"`
}

// safeHTMLFence generates a Markdown code fence that cannot be terminated by backticks inside the snippet.
func safeHTMLFence(snippet string) (string, string) {
	maxRun := 0
	currentRun := 0
	for _, ch := range snippet {
		if ch == '`' {
			currentRun++
			if currentRun > maxRun {
				maxRun = currentRun
			}
		} else {
			currentRun = 0
		}
	}
	fenceLen := 3
	if maxRun >= 3 {
		fenceLen = maxRun + 1
	}
	fence := strings.Repeat("`", fenceLen)
	return fence + "html\n", "\n" + fence + "\n"
}
func sanitizeMarkdownLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.TrimSpace(s)
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return s
}

func sanitizeComment(comment string) string {
	lines := strings.Split(strings.TrimSpace(comment), "\n")
	var out []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "#") {
			trimmed = "\\" + trimmed
		}
		out = append(out, trimmed)
	}
	return strings.Join(out, "\n")
}

// FormatDesignFeedbackReport formats a collection of review notes into structured markdown for AI agents.
func FormatDesignFeedbackReport(notes []*ReviewNote, pageURL string, viewport string) string {
	if len(notes) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Design Feedback: %s\n\n", pageURL))
	sb.WriteString(fmt.Sprintf("**URL:** %s\n", pageURL))
	if viewport != "" {
		sb.WriteString(fmt.Sprintf("**Viewport:** %s\n", viewport))
	}
	sb.WriteString("\n")

	for _, note := range notes {
		if note == nil || note.Payload == nil {
			continue
		}

		target := note.Payload.Target
		fw := target.Framework

		// Title line uses the actual stored pin index, not slice position
		pinIndex := note.Index
		if pinIndex <= 0 {
			pinIndex = 1
		}
		componentLabel := target.TagName
		if target.Role != "" && target.Role != target.TagName {
			componentLabel = fmt.Sprintf("%s (%s)", componentLabel, target.Role)
		}
		if fw.Component != "" {
			componentLabel = fmt.Sprintf("%s %s", fw.Component, componentLabel)
		}
		if target.AccessibleName != "" {
			componentLabel = fmt.Sprintf("%s %q", componentLabel, sanitizeMarkdownLine(target.AccessibleName))
		} else if target.TextSnippet != "" {
			snip := sanitizeMarkdownLine(target.TextSnippet)
			if len(snip) > 40 {
				snip = snip[:40] + "..."
			}
			componentLabel = fmt.Sprintf("%s %q", componentLabel, snip)
		}

		sb.WriteString(fmt.Sprintf("### %d. %s\n", pinIndex, componentLabel))
		if note.Intent != "" {
			sb.WriteString(fmt.Sprintf("**Intent:** %s\n", sanitizeMarkdownLine(note.Intent)))
		}
		if fw.Name != "" && fw.Name != "Static" {
			fwLine := fw.Name
			if fw.Component != "" {
				fwLine = fmt.Sprintf("%s (%s)", fwLine, fw.Component)
			}
			sb.WriteString(fmt.Sprintf("**Framework:** %s\n", fwLine))
		}
		if fw.SourceLocation != "" {
			sb.WriteString(fmt.Sprintf("**Source:** %s (provenance: %s)\n", fw.SourceLocation, fw.Provenance))
		}
		sb.WriteString(fmt.Sprintf("**Selector:** %s\n", sanitizeMarkdownLine(target.Selector)))
		if target.ElementPath != "" {
			sb.WriteString(fmt.Sprintf("**Location:** %s\n", sanitizeMarkdownLine(target.ElementPath)))
		}
		sb.WriteString(fmt.Sprintf("**Bounds:** x=%.0f, y=%.0f, %.0fx%.0f\n",
			target.RectViewport.X, target.RectViewport.Y, target.RectViewport.Width, target.RectViewport.Height))
		if target.CSSClasses != "" {
			sb.WriteString(fmt.Sprintf("**Classes:** `%s`\n", target.CSSClasses))
		}
		if len(target.ComputedStyles) > 0 {
			sb.WriteString("**Computed styles:**\n")
			for k, v := range target.ComputedStyles {
				sb.WriteString(fmt.Sprintf("- %s: %s\n", k, v))
			}
		}
		if len(note.Payload.NearbyText) > 0 {
			sb.WriteString("**Nearby text:**\n")
			for _, t := range note.Payload.NearbyText {
				clean := sanitizeMarkdownLine(t)
				if clean != "" {
					sb.WriteString(fmt.Sprintf("- %s\n", clean))
				}
			}
		}
		if target.HTMLSnippet != "" {
			startFence, endFence := safeHTMLFence(target.HTMLSnippet)
			sb.WriteString("**HTML:**\n")
			sb.WriteString(startFence)
			sb.WriteString(strings.TrimSpace(target.HTMLSnippet))
			sb.WriteString(endFence)
		}
		sb.WriteString(fmt.Sprintf("**Feedback:** %s\n\n", sanitizeComment(note.Comment)))
	}

	return strings.TrimSpace(sb.String())
}
