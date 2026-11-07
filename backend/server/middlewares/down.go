package middlewares

import (
	"crypto/subtle"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/setting"

	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

func PathParse(c *gin.Context) {
	rawPath := parsePath(c.Param("path"))
	common.GinAppendValues(c, conf.PathKey, rawPath)
	c.Next()
}

func isOwnerRequest(c *gin.Context) bool {
	token := c.GetHeader("Authorization")
	if token == "" {
		token = c.Query("token")
	}
	apiToken := setting.GetStr(conf.Token)
	if apiToken != "" && token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(apiToken)) == 1 {
		return true
	}
	if token != "" {
		claims, err := common.ParseToken(token)
		if err == nil && claims != nil {
			admin, err := op.GetAdmin()
			if err == nil && admin != nil && !admin.Disabled && claims.Username == admin.Username && claims.PwdTS == admin.PwdTS {
				return true
			}
		}
	}
	return false
}

func Down(verifyFunc func(string, string) error) func(c *gin.Context) {
	return func(c *gin.Context) {
		rawPath := c.Request.Context().Value(conf.PathKey).(string)
		meta, err := op.GetNearestMeta(rawPath)
		if err != nil && !errors.Is(errors.Cause(err), errs.MetaNotFound) {
			common.ErrorPage(c, err, 500, true)
			return
		}
		common.GinAppendValues(c, conf.MetaKey, meta)

		// Owner has direct access via header/token
		if isOwnerRequest(c) {
			c.Next()
			return
		}

		// verify sign
		if needSign(meta, rawPath) {
			s := c.Query("sign")
			err = verifyFunc(rawPath, strings.TrimSuffix(s, "/"))
			if err != nil {
				common.ErrorPage(c, err, 401)
				c.Abort()
				return
			}
		}
		c.Next()
	}
}

// TODO: implement
// path maybe contains # ? etc.
func parsePath(path string) string {
	return utils.FixAndCleanPath(path)
}

func needSign(meta *model.Meta, path string) bool {
	if setting.GetBool(conf.SignAll) {
		return true
	}
	if common.IsStorageSignEnabled(path) {
		return true
	}
	guest, err := op.GetGuest()
	if err == nil && guest.Disabled {
		return true
	}
	if meta == nil || meta.Password == "" {
		return false
	}
	if !meta.PSub && !common.MetaCoversPath(meta.Path, path, false) {
		return false
	}
	return true
}
