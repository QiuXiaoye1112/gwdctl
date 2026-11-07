package handles

import (
	"crypto/subtle"
	"net"
	"sync"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/setting"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

var setupMutex sync.Mutex

// isDirectLoopback checks if the request connection originated from loopback (127.0.0.1 or ::1)
// and has no forwarded proxy headers.
func isDirectLoopback(c *gin.Context) bool {
	if c.GetHeader("X-Forwarded-For") != "" || c.GetHeader("X-Real-IP") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil {
		host = c.Request.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isValidSetupToken checks if the provided token matches the configured setup token
// or the system's private token, using constant-time comparison.
func isValidSetupToken(token string) bool {
	if token == "" {
		return false
	}
	if conf.Conf != nil && conf.Conf.SetupToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(conf.Conf.SetupToken)) == 1 {
		return true
	}
	if db.GetDb() != nil {
		cfgToken := setting.GetStr(conf.Token)
		if cfgToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(cfgToken)) == 1 {
			return true
		}
	}
	return false
}

// InitStatus 返回系统是否已完成初始化（即是否已存在管理员账号）。
// 前端据此判断是否跳转到安装向导。
func InitStatus(c *gin.Context) {
	if _, err := op.GetAdmin(); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.SuccessResp(c, gin.H{"initialized": false})
			return
		}
		common.ErrorResp(c, err, 500, true)
		return
	}
	common.SuccessResp(c, gin.H{"initialized": true})
}

// InitSetupReq 系统初始化请求体
type InitSetupReq struct {
	Username  string `json:"username" binding:"required"`
	Password  string `json:"password" binding:"required"`
	SiteTitle string `json:"site_title"`
	Token     string `json:"token"`
}

// InitSetup 执行系统初始化：创建管理员账号并设置站点名称等初始参数。
// Web 初始化统一要求有效初始化凭据（通过 Authorization/X-Setup-Token 头、query 参数或请求体传递），
// 杜绝本地反向代理场景下的未授权初始化；原子互斥防止并发创建多个管理员。
func InitSetup(c *gin.Context) {
	var req InitSetupReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}

	token := c.GetHeader("Authorization")
	if token == "" {
		token = c.GetHeader("X-Setup-Token")
	}
	if token == "" {
		token = c.Query("token")
	}
	if token == "" && req.Token != "" {
		token = req.Token
	}

	if !isValidSetupToken(token) {
		common.ErrorStrResp(c, "setup requires a valid setup token", 403)
		return
	}

	setupMutex.Lock()
	defer setupMutex.Unlock()

	// 锁内原子二次检查：已初始化则拒绝
	if _, err := op.GetAdmin(); err == nil {
		common.ErrorStrResp(c, "system has already been initialized", 400)
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		common.ErrorResp(c, err, 500, true)
		return
	}

	// 密码最小长度校验
	if len(req.Password) < 4 {
		common.ErrorStrResp(c, "password must be at least 4 characters", 400)
		return
	}
	admin := &model.User{
		Username:   req.Username,
		Role:       model.ADMIN,
		BasePath:   "/",
		Authn:      "[]",
		Permission: 0x71FF,
	}
	admin.SetPassword(req.Password)
	if err := op.CreateUser(admin); err != nil {
		common.ErrorResp(c, err, 500, true)
		return
	}
	// 设置站点名称（如果提供）
	if req.SiteTitle != "" {
		item := model.SettingItem{
			Key:   conf.SiteTitle,
			Value: req.SiteTitle,
			Type:  conf.TypeString,
			Group: model.SITE,
			Flag:  model.PUBLIC,
		}
		if err := op.SaveSettingItem(&item); err != nil {
			common.ErrorResp(c, err, 500, true)
			return
		}
	}
	_ = op.DelUserCache(admin.Username)
	common.SuccessResp(c)
}
