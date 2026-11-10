package middlewares

import (
	"crypto/subtle"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/setting"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// RequireOwner is a middleware that strictly enforces the caller is the unique owner
func RequireOwner(c *gin.Context) {
	token := c.GetHeader("Authorization")
	apiToken := setting.GetStr(conf.Token)
	if apiToken != "" && token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(apiToken)) == 1 {
		admin, err := op.GetAdmin()
		if err != nil {
			common.ErrorResp(c, err, 500)
			c.Abort()
			return
		}
		if admin.Disabled {
			common.ErrorStrResp(c, "Current user is disabled, replace please", 403)
			c.Abort()
			return
		}
		common.GinAppendValues(c, conf.UserKey, admin)
		log.Debugf("use admin token for owner: %+v", admin)
		c.Next()
		return
	}
	if token == "" {
		common.ErrorStrResp(c, "Please login first", 401)
		c.Abort()
		return
	}
	userClaims, err := common.ParseToken(token)
	if err != nil {
		common.ErrorResp(c, err, 401)
		c.Abort()
		return
	}
	owner, err := op.GetAdmin()
	if err != nil {
		common.ErrorResp(c, err, 500)
		c.Abort()
		return
	}
	// Stable owner identity verification
	if userClaims.Username != owner.Username || userClaims.PwdTS != owner.PwdTS {
		common.ErrorStrResp(c, "Invalid or expired token, login please", 401)
		c.Abort()
		return
	}
	if owner.Disabled {
		common.ErrorStrResp(c, "Current user is disabled, replace please", 403)
		c.Abort()
		return
	}
	common.GinAppendValues(c, conf.UserKey, owner)
	log.Debugf("use owner token: %+v", owner)
	c.Next()
}

// ShareOrOwner permits anonymous share requests; split handlers reject any
// anonymous request that does not target a share.
func ShareOrOwner() func(c *gin.Context) {
	return func(c *gin.Context) {
		token := c.GetHeader("Authorization")
		apiToken := setting.GetStr(conf.Token)
		if apiToken != "" && token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(apiToken)) == 1 {
			admin, err := op.GetAdmin()
			if err != nil {
				common.ErrorResp(c, err, 500)
				c.Abort()
				return
			}
			if admin.Disabled {
				common.ErrorStrResp(c, "Current user is disabled, replace please", 403)
				c.Abort()
				return
			}
			common.GinAppendValues(c, conf.UserKey, admin)
			log.Debugf("use admin token: %+v", admin)
			c.Next()
			return
		}
		if token == "" {
			c.Next()
			return
		}
		userClaims, err := common.ParseToken(token)
		if err != nil {
			common.ErrorResp(c, err, 401)
			c.Abort()
			return
		}
		owner, err := op.GetAdmin()
		if err != nil {
			common.ErrorResp(c, err, 401)
			c.Abort()
			return
		}
		// Validate against the single owner account
		if userClaims.Username != owner.Username || userClaims.PwdTS != owner.PwdTS {
			common.ErrorStrResp(c, "Invalid or expired token, login please", 401)
			c.Abort()
			return
		}
		if owner.Disabled {
			common.ErrorStrResp(c, "Current user is disabled, replace please", 401)
			c.Abort()
			return
		}
		common.GinAppendValues(c, conf.UserKey, owner)
		log.Debugf("use login token: %+v", owner)
		c.Next()
	}
}

func AuthNotGuest(c *gin.Context) {
	if u, ok := c.Request.Context().Value(conf.UserKey).(*model.User); ok && u.IsAdmin() && !u.Disabled {
		c.Next()
		return
	}
	RequireOwner(c)
}

func AuthAdmin(c *gin.Context) {
	if u, ok := c.Request.Context().Value(conf.UserKey).(*model.User); ok && u.IsAdmin() && !u.Disabled {
		c.Next()
		return
	}
	RequireOwner(c)
}
