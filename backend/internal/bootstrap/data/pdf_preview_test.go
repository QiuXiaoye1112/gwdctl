package data

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestStripPDFJSPreviewPreservesOtherProviders(t *testing.T) {
	input := `{"pdf":{"PDF.js":"https://example.test/viewer","Other":"https://other.test/viewer"},"epub":{"EPUB.js":"https://example.test/epub"},"doc,docx":{"Microsoft":"https://example.test/office"}}`
	updated, changed := stripPDFJSPreview(input)
	if !changed {
		t.Fatal("PDF.js entry was not removed")
	}
	var got map[string]map[string]string
	if err := json.Unmarshal([]byte(updated), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]string{"pdf": {"Other": "https://other.test/viewer"}, "epub": {"EPUB.js": "https://example.test/epub"}, "doc,docx": {"Microsoft": "https://example.test/office"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected settings: %v", got)
	}
	if again, changed := stripPDFJSPreview(updated); changed || again != updated {
		t.Fatal("migration is not idempotent")
	}
	if _, changed := stripPDFJSPreview("not-json"); changed {
		t.Fatal("invalid settings must be preserved")
	}
}
