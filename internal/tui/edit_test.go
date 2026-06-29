package tui

import (
	"testing"

	"github.com/mickael-menu/opencode-manager/internal/module"
)

func entry(name, category, desc, label string) editEntry {
	return editEntry{
		mod:   module.Module{Name: name, Category: category, Description: desc},
		label: label,
	}
}

func TestCategoryLabel(t *testing.T) {
	cases := map[string]string{
		"source-code": "Source code",
		"tools":       "Tools",
		"cloud":       "Cloud",
		"":            "",
	}
	for in, want := range cases {
		if got := categoryLabel(in); got != want {
			t.Errorf("categoryLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEditEntryMatches(t *testing.T) {
	e := entry("aws", "cloud", "Install the AWS CLI and credentials.", "prod")
	cases := []struct {
		q    string
		want bool
	}{
		{"aws", true},         // name
		{"credentials", true}, // description
		{"cloud", true},       // category
		{"prod", true},        // instance label
		{"kube", false},
	}
	for _, c := range cases {
		if got := editEntryMatches(e, c.q); got != c.want {
			t.Errorf("editEntryMatches(%q) = %v, want %v", c.q, got, c.want)
		}
	}
}

func TestEditVisibleNavigationSkipsFiltered(t *testing.T) {
	m := model{
		editEntries: []editEntry{
			entry("aws", "cloud", "AWS CLI", ""),
			entry("git", "tools", "git identity", ""),
			entry("ssh", "tools", "ssh keys", ""),
		},
		editFilter: "tools",
	}

	// Only the two tools entries (indices 1,2) are visible.
	if m.editVisible(0) || !m.editVisible(1) || !m.editVisible(2) {
		t.Fatalf("unexpected visibility: %v %v %v", m.editVisible(0), m.editVisible(1), m.editVisible(2))
	}
	if got := m.firstVisibleEdit(); got != 1 {
		t.Fatalf("firstVisibleEdit = %d, want 1", got)
	}
	if got := m.lastVisibleEdit(); got != 2 {
		t.Fatalf("lastVisibleEdit = %d, want 2", got)
	}
	// Navigating down from the first visible row lands on the next visible one,
	// skipping the filtered-out index 0.
	if got := m.nextVisibleEdit(1); got != 2 {
		t.Fatalf("nextVisibleEdit(1) = %d, want 2", got)
	}
	if got := m.prevVisibleEdit(2); got != 1 {
		t.Fatalf("prevVisibleEdit(2) = %d, want 1", got)
	}
}

func catEntry(category string) editEntry {
	return editEntry{isCategory: true, category: category}
}

func TestEditCategoriesCollapsedByDefault(t *testing.T) {
	m := model{
		editEntries: []editEntry{
			catEntry("cloud"),
			entry("aws", "cloud", "AWS CLI", ""),
			catEntry("tools"),
			entry("git", "tools", "git identity", ""),
		},
		editCollapsed: map[string]bool{"cloud": true, "tools": true},
	}

	// With both categories collapsed, only the two headers are visible.
	if !m.editVisible(0) || m.editVisible(1) || !m.editVisible(2) || m.editVisible(3) {
		t.Fatalf("unexpected collapsed visibility: %v %v %v %v",
			m.editVisible(0), m.editVisible(1), m.editVisible(2), m.editVisible(3))
	}
	// Navigation hops header to header, skipping the hidden module rows.
	if got := m.nextVisibleEdit(0); got != 2 {
		t.Fatalf("nextVisibleEdit(0) = %d, want 2 (next header)", got)
	}

	// Expanding "cloud" reveals its module row.
	m.editCollapsed["cloud"] = false
	if !m.editVisible(1) {
		t.Fatalf("aws row should be visible once cloud is expanded")
	}
	if got := m.nextVisibleEdit(0); got != 1 {
		t.Fatalf("nextVisibleEdit(0) = %d, want 1 (expanded module row)", got)
	}
}

func TestEditFilterIgnoresCollapse(t *testing.T) {
	m := model{
		editEntries: []editEntry{
			catEntry("cloud"),
			entry("aws", "cloud", "AWS CLI", ""),
			catEntry("tools"),
			entry("git", "tools", "git identity", ""),
		},
		editCollapsed: map[string]bool{"cloud": true, "tools": true},
		editFilter:    "git",
	}

	// A filter reveals matches regardless of collapse: the tools header (it has a
	// matching child) and the git row are visible; the non-matching cloud side is not.
	if m.editVisible(0) || m.editVisible(1) {
		t.Fatalf("cloud header/row should be hidden by the filter")
	}
	if !m.editVisible(2) || !m.editVisible(3) {
		t.Fatalf("tools header and git row should be visible under the filter")
	}
}

func TestSnapEditPosMovesToVisible(t *testing.T) {
	m := model{
		editEntries: []editEntry{
			entry("aws", "cloud", "AWS CLI", ""),
			entry("git", "tools", "git identity", ""),
		},
		editFilter: "git",
		editPos:    0, // currently on a filtered-out row
	}
	m.snapEditPos()
	if m.editPos != 1 {
		t.Fatalf("snapEditPos = %d, want 1 (first visible)", m.editPos)
	}
}
