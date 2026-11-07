package common

import (
	stdpath "path"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/setting"
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
	if encrypt || setting.GetBool(conf.SignAll) || IsStorageSignEnabled(path) {
		return true
	}
	guest, err := op.GetGuest()
	return err == nil && guest.Disabled
}
