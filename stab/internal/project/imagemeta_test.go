package project

import (
	"path/filepath"
	"testing"
)

func TestImageMetaRoundTrip(t *testing.T) {
	root := t.TempDir()
	if v, ok := ImageTemplateVersion(root, "img:tag"); ok {
		t.Fatalf("expected unknown, got %q", v)
	}
	if err := RecordImageTemplate(root, "img:tag", "26"); err != nil {
		t.Fatal(err)
	}
	if err := RecordImageTemplate(root, "img:other", "27"); err != nil {
		t.Fatal(err)
	}
	v, ok := ImageTemplateVersion(root, "img:tag")
	if !ok || v != "26" {
		t.Fatalf("got %q (ok=%v), want 26", v, ok)
	}
	if v, _ := ImageTemplateVersion(root, "img:other"); v != "27" {
		t.Fatalf("got %q, want 27", v)
	}
}

func TestRecordImageTemplateEmptyIsNoop(t *testing.T) {
	root := t.TempDir()
	if err := RecordImageTemplate(root, "img:tag", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := filepath.Rel(root, filepath.Join(root, "images.json")); err == nil {
		if _, present := ImageTemplateVersion(root, "img:tag"); present {
			t.Fatal("empty version must not be recorded")
		}
	}
}
