package handles

import (
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"testing"
)

type nativeDoc struct{ model.Object }

func (*nativeDoc) GetNativeMimeType() string { return "application/vnd.google-apps.document" }

func TestGoogleDocumentIsTextPreview(t *testing.T) {
	doc := &nativeDoc{Object: model.Object{Name: "document without extension"}}
	if got := objPreviewType(doc); got != conf.TEXT {
		t.Fatalf("native document type = %d, want text", got)
	}
	wrapped := &model.ObjWrapName{Name: "renamed", Obj: doc}
	if got := objPreviewType(wrapped); got != conf.TEXT {
		t.Fatalf("wrapped native document type = %d, want text", got)
	}
}
