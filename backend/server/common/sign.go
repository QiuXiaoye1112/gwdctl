package common

import (
	stdpath "path"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/sign"
)

func Sign(obj model.Obj, parent string, encrypt bool) string {
	path := stdpath.Join(parent, obj.GetName())
	if obj.IsDir() || !RequiresDownloadSign(path, encrypt) {
		return ""
	}
	return sign.Sign(path)
}

// RequiresDownloadSign matches the non-metadata checks in the download guard.
func RequiresDownloadSign(path string, encrypt bool) bool {
	// Private files always require a signature when accessed without the owner token.
	return true
}
