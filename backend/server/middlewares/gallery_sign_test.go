package middlewares

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/sign"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGalleryLinksWithGuestDisabledAndSignAllOff(t *testing.T) {
	gin.SetMode(gin.TestMode)
	conf.Conf = conf.DefaultConfig(t.TempDir())
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.Init(database)
	defer func() {
		_ = op.DelUserCache("guest")
		op.SettingCacheUpdate()
		db.Close()
	}()
	for _, item := range []model.SettingItem{{Key: conf.SignAll, Value: "false"}, {Key: conf.Token, Value: "gallery-test-token"}} {
		if err := db.SaveSettingItem(&item); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.CreateUser(&model.User{Username: "guest", Role: model.GUEST, Disabled: true, BasePath: "/"}); err != nil {
		t.Fatal(err)
	}
	op.SettingCacheUpdate()
	router := gin.New()
	router.GET("/d/*path", PathParse, Down(sign.Verify), func(c *gin.Context) { c.String(http.StatusOK, "image bytes") })
	path := "/Camera/图片/20240623_170544.jpg"
	signature := common.Sign(&model.Object{Name: "20240623_170544.jpg"}, "/Camera/图片", false)
	if signature == "" {
		t.Fatal("authenticated directory listing did not issue a signature while guest access is disabled")
	}
	for _, tc := range []struct {
		name, signature string
		want            int
	}{
		{"missing", "", http.StatusUnauthorized},
		{"invalid", "invalid", http.StatusUnauthorized},
		{"valid", signature, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/d"+url.PathEscape(path), nil)
			q := req.URL.Query()
			if tc.signature != "" {
				q.Set("sign", tc.signature)
			}
			req.URL.RawQuery = q.Encode()
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
	t.Run("public access remains unsigned when allowed", func(t *testing.T) {
		guest, err := op.GetGuest()
		if err != nil {
			t.Fatal(err)
		}
		guest.Disabled = false
		if err := op.UpdateUser(guest); err != nil {
			t.Fatal(err)
		}
		if got := common.Sign(&model.Object{Name: "20240623_170544.jpg"}, "/Camera/图片", false); got != "" {
			t.Fatalf("unexpected signature for public file: %q", got)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/d"+url.PathEscape(path), nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("public access: got %d", rec.Code)
		}
	})
	t.Run("sign all still rejects unsigned requests", func(t *testing.T) {
		if err := db.SaveSettingItem(&model.SettingItem{Key: conf.SignAll, Value: "true"}); err != nil {
			t.Fatal(err)
		}
		op.SettingCacheUpdate()
		if got := common.Sign(&model.Object{Name: "20240623_170544.jpg"}, "/Camera/图片", false); got == "" {
			t.Fatal("missing signature with sign_all enabled")
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/d"+url.PathEscape(path), nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("unsigned access: got %d", rec.Code)
		}
	})
}
