package server

import (
	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/message"
	multipartPkg "github.com/OpenListTeam/OpenList/v4/internal/multipart"
	"github.com/OpenListTeam/OpenList/v4/internal/sign"
	"github.com/OpenListTeam/OpenList/v4/internal/stream"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/OpenListTeam/OpenList/v4/server/handles"
	"github.com/OpenListTeam/OpenList/v4/server/middlewares"
	"github.com/OpenListTeam/OpenList/v4/server/static"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func Init(e *gin.Engine) {
	e.ContextWithFallback = true
	if !utils.SliceContains([]string{"", "/"}, conf.URL.Path) {
		e.GET("/", func(c *gin.Context) {
			c.Redirect(302, conf.URL.Path)
		})
	}
	Cors(e)
	g := e.Group(conf.URL.Path)
	if conf.Conf.Scheme.HttpPort != -1 && conf.Conf.Scheme.HttpsPort != -1 && conf.Conf.Scheme.ForceHttps {
		e.Use(middlewares.ForceHttps)
	}
	g.Any("/ping", func(c *gin.Context) {
		c.String(200, "pong")
	})
	g.GET("/favicon.ico", handles.Favicon)
	g.GET("/robots.txt", handles.Robots)
	g.GET("/manifest.json", static.ManifestJSON)
	g.GET("/i/:link_name", handles.Plist)
	common.SecretKey = []byte(conf.Conf.JwtSecret)
	g.Use(middlewares.StoragesLoaded)
	if conf.Conf.MaxConnections > 0 {
		g.Use(middlewares.MaxAllowed(conf.Conf.MaxConnections))
	}

	downloadLimiter := middlewares.DownloadRateLimiter(stream.ClientDownloadLimit)
	signCheck := middlewares.Down(sign.Verify)
	g.GET("/d/*path", middlewares.PathParse, signCheck, downloadLimiter, handles.Down)
	g.GET("/p/*path", middlewares.PathParse, signCheck, downloadLimiter, handles.Proxy)
	g.HEAD("/d/*path", middlewares.PathParse, signCheck, handles.Down)
	g.HEAD("/p/*path", middlewares.PathParse, signCheck, handles.Proxy)
	archiveSignCheck := middlewares.Down(sign.VerifyArchive)
	g.GET("/ad/*path", middlewares.PathParse, archiveSignCheck, downloadLimiter, handles.ArchiveDown)
	g.GET("/ap/*path", middlewares.PathParse, archiveSignCheck, downloadLimiter, handles.ArchiveProxy)
	g.GET("/ae/*path", middlewares.PathParse, archiveSignCheck, downloadLimiter, handles.ArchiveInternalExtract)
	g.HEAD("/ad/*path", middlewares.PathParse, archiveSignCheck, handles.ArchiveDown)
	g.HEAD("/ap/*path", middlewares.PathParse, archiveSignCheck, handles.ArchiveProxy)
	g.HEAD("/ae/*path", middlewares.PathParse, archiveSignCheck, handles.ArchiveInternalExtract)

	g.GET("/sd/:sid", middlewares.EmptyPathParse, middlewares.SharingIdParse, downloadLimiter, handles.SharingDown)
	g.GET("/sd/:sid/*path", middlewares.PathParse, middlewares.SharingIdParse, downloadLimiter, handles.SharingDown)
	g.HEAD("/sd/:sid", middlewares.EmptyPathParse, middlewares.SharingIdParse, handles.SharingDown)
	g.HEAD("/sd/:sid/*path", middlewares.PathParse, middlewares.SharingIdParse, handles.SharingDown)
	g.GET("/sad/:sid", middlewares.EmptyPathParse, middlewares.SharingIdParse, downloadLimiter, handles.SharingArchiveExtract)
	g.GET("/sad/:sid/*path", middlewares.PathParse, middlewares.SharingIdParse, downloadLimiter, handles.SharingArchiveExtract)
	g.HEAD("/sad/:sid", middlewares.EmptyPathParse, middlewares.SharingIdParse, handles.SharingArchiveExtract)
	g.HEAD("/sad/:sid/*path", middlewares.PathParse, middlewares.SharingIdParse, handles.SharingArchiveExtract)

	api := g.Group("/api")
	ownerAuth := api.Group("", middlewares.RequireOwner)

	api.POST("/auth/login", handles.Login)
	api.POST("/auth/login/hash", handles.LoginHash)
	ownerAuth.GET("/me", handles.CurrentUser)
	ownerAuth.POST("/me/update", handles.UpdateCurrent)
	ownerAuth.POST("/auth/2fa/generate", handles.Generate2FA)
	ownerAuth.POST("/auth/2fa/verify", handles.Verify2FA)
	ownerAuth.POST("/auth/2fa/cancel", handles.Cancel2FA)
	ownerAuth.GET("/auth/logout", handles.LogOut)

	public := api.Group("/public")
	public.Any("/settings", handles.PublicSettings)
	public.Any("/archive_extensions", handles.ArchiveExtensions)
	public.Any("/init_status", handles.InitStatus)
	public.POST("/init/setup", handles.InitSetup)

	_fs(ownerAuth.Group("/fs"))
	fsAndShare(api.Group("/fs", middlewares.Auth(true)))
	_task(ownerAuth.Group("/task"))
	_sharing(ownerAuth.Group("/share"))
	admin(ownerAuth.Group("/admin"))
	if flags.Debug || flags.Dev {
		debug(g.Group("/debug"))
	}
	static.Static(g, func(handlers ...gin.HandlerFunc) {
		e.NoRoute(handlers...)
	})
}

func admin(g *gin.RouterGroup) {
	setting := g.Group("/setting")
	setting.GET("/get", handles.GetSetting)
	setting.GET("/list", handles.ListSettings)
	setting.POST("/save", handles.SaveSettings)
	setting.POST("/delete", handles.DeleteSetting)
	setting.POST("/default", handles.DefaultSettings)
	setting.POST("/reset_token", handles.ResetToken)

	user := g.Group("/user")
	user.POST("/del_cache", handles.DelUserCache)

	_task(g.Group("/task"))

	ms := g.Group("/message")
	ms.POST("/get", message.HttpInstance.GetHandle)
	ms.POST("/send", message.HttpInstance.SendHandle)

	index := g.Group("/index")
	index.POST("/build", middlewares.SearchIndex, handles.BuildIndex)
	index.POST("/update", middlewares.SearchIndex, handles.UpdateIndex)
	index.POST("/stop", middlewares.SearchIndex, handles.StopIndex)
	index.POST("/clear", middlewares.SearchIndex, handles.ClearIndex)
	index.GET("/progress", middlewares.SearchIndex, handles.GetProgress)

	scan := g.Group("/scan")
	scan.POST("/start", handles.StartManualScan)
	scan.POST("/stop", handles.StopManualScan)
	scan.GET("/progress", handles.GetManualScanProgress)
}

func fsAndShare(g *gin.RouterGroup) {
	g.Any("/list", handles.FsListSplit)
	g.Any("/get", handles.FsGetSplit)
	a := g.Group("/archive")
	a.Any("/meta", handles.FsArchiveMetaSplit)
	a.Any("/list", handles.FsArchiveListSplit)
}

func _fs(g *gin.RouterGroup) {
	g.Any("/search", middlewares.SearchIndex, handles.Search)
	g.Any("/other", handles.FsOther)
	g.Any("/dirs", handles.FsDirs)
	g.POST("/mkdir", handles.FsMkdir)
	g.POST("/rename", handles.FsRename)
	g.POST("/batch_rename", handles.FsBatchRename)
	g.POST("/regex_rename", handles.FsRegexRename)
	g.POST("/move", handles.FsMove)
	g.POST("/recursive_move", handles.FsRecursiveMove)
	g.POST("/copy", handles.FsCopy)
	g.POST("/remove", handles.FsRemove)
	g.POST("/remove_empty_directory", handles.FsRemoveEmptyDirectory)
	uploadLimiter := middlewares.UploadRateLimiter(stream.ClientUploadLimit)
	g.PUT("/put", middlewares.FsUp, uploadLimiter, handles.FsStream)
	g.PUT("/form", middlewares.FsUp, uploadLimiter, handles.FsForm)
	multipartPkg.DefaultManager.StartGC()
	multipart := g.Group("/multipart")
	multipart.POST("/init", middlewares.FsUp, handles.MultipartInit)
	multipart.PUT("/chunk", uploadLimiter, handles.MultipartChunk)
	multipart.POST("/complete", handles.MultipartComplete)
	multipart.GET("/status", handles.MultipartStatus)
	multipart.POST("/abort", handles.MultipartAbort)
	g.POST("/link", middlewares.AuthAdmin, handles.Link)
	g.POST("/archive/decompress", handles.FsArchiveDecompress)
	g.POST("/get_direct_upload_info", handles.FsGetDirectUploadInfo)
}

func _task(g *gin.RouterGroup) {
	handles.SetupTaskRoute(g)
}

func _sharing(g *gin.RouterGroup) {
	g.Any("/list", handles.ListSharings)
	g.GET("/get", handles.GetSharing)
	g.POST("/create", handles.CreateSharing)
	g.POST("/update", handles.UpdateSharing)
	g.POST("/delete", handles.DeleteSharing)
	g.POST("/enable", handles.SetEnableSharing(false))
	g.POST("/disable", handles.SetEnableSharing(true))
}

func Cors(r *gin.Engine) {
	config := cors.DefaultConfig()
	config.AllowOrigins = conf.Conf.Cors.AllowOrigins
	config.AllowHeaders = conf.Conf.Cors.AllowHeaders
	config.AllowMethods = conf.Conf.Cors.AllowMethods
	r.Use(cors.New(config))
}
