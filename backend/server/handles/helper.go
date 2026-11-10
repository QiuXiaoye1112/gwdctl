package handles

import (
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/setting"
	"github.com/gin-gonic/gin"
)

func Favicon(c *gin.Context) {
	c.Redirect(302, setting.GetStr(conf.Favicon))
}

func Robots(c *gin.Context) {
	c.String(200, setting.GetStr(conf.RobotsTxt))
}
