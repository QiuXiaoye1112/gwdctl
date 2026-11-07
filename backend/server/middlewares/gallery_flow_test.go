package middlewares

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/sign"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/OpenListTeam/OpenList/v4/server/handles"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type galleryMock struct {
	model.Storage
	addition struct{}
	upstream string
}

func (d *galleryMock) Config() driver.Config {
	return driver.Config{Name: "gallery-test-driver", OnlyProxy: true}
}
func (d *galleryMock) GetAddition() driver.Additional { return &d.addition }
func (d *galleryMock) Init(context.Context) error     { return nil }
func (d *galleryMock) Drop(context.Context) error     { return nil }
func (d *galleryMock) GetRoot(context.Context) (model.Obj, error) {
	return &model.Object{ID: "root", Name: "root", IsFolder: true}, nil
}
func (d *galleryMock) Get(_ context.Context, p string) (model.Obj, error) {
	if p == "/图片" {
		return &model.Object{ID: "folder", Name: "图片", IsFolder: true}, nil
	}
	if p == "/图片/20240623_170544.jpg" {
		return &model.Object{ID: "file", Name: "20240623_170544.jpg", Size: 5}, nil
	}
	return nil, fmt.Errorf("object not found: %s", p)
}
func (d *galleryMock) List(_ context.Context, _ model.Obj, _ model.ListArgs) ([]model.Obj, error) {
	return []model.Obj{&model.Object{ID: "file", Name: "20240623_170544.jpg", Size: 5}}, nil
}
func (d *galleryMock) Link(_ context.Context, _ model.Obj, _ model.LinkArgs) (*model.Link, error) {
	return &model.Link{URL: d.upstream, Header: http.Header{"Authorization": []string{"Bearer mock-google-token"}}}, nil
}

func TestGalleryAndSingleFileUseSignedProxy(t *testing.T) {
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
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer mock-google-token" {
			http.Error(w, "missing upstream auth", 401)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = io.WriteString(w, "image")
	}))
	defer upstream.Close()
	op.RegisterDriver(func() driver.Driver { return &galleryMock{upstream: upstream.URL} })
	storageID, err := op.CreateStorage(context.Background(), model.Storage{MountPath: "/Camera", Driver: "gallery-test-driver", Addition: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	defer op.DeleteStorageById(context.Background(), storageID)
	owner := &model.User{Username: "owner", Role: model.ADMIN, BasePath: "/"}
	router := gin.New()
	router.ContextWithFallback = true
	ownerContext := func(c *gin.Context) { common.GinAppendValues(c, conf.UserKey, owner, conf.ApiUrlKey, ""); c.Next() }
	router.POST("/api/fs/list", ownerContext, handles.FsListSplit)
	router.POST("/api/fs/get", ownerContext, handles.FsGetSplit)
	router.GET("/d/*path", PathParse, Down(sign.Verify), handles.Down)
	router.GET("/p/*path", PathParse, Down(sign.Verify), handles.Proxy)
	post := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	list := post("/api/fs/list", `{"path":"/Camera/图片","page":1,"per_page":0}`)
	var listing struct {
		Code int `json:"code"`
		Data struct {
			Content []struct{ Name, Sign string } `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if listing.Code != 200 || len(listing.Data.Content) != 1 || listing.Data.Content[0].Sign == "" {
		t.Fatalf("list missing signed image: %s", list.Body.String())
	}
	item := listing.Data.Content[0]
	imageURL := "/d/Camera/" + url.PathEscape("图片") + "/" + url.PathEscape(item.Name) + "?sign=" + url.QueryEscape(item.Sign)
	check := func(link string) {
		t.Helper()
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, link, nil))
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), []byte("image")) {
			t.Fatalf("image fetch failed: HTTP %d %s", w.Code, w.Body.String())
		}
	}
	check(imageURL)
	file := post("/api/fs/get", `{"path":"/Camera/图片/20240623_170544.jpg"}`)
	var detail struct {
		Code int `json:"code"`
		Data struct {
			RawURL string `json:"raw_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(file.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Code != 200 || !strings.Contains(detail.Data.RawURL, "sign=") {
		t.Fatalf("single-file link is not signed: %s", file.Body.String())
	}
	check(detail.Data.RawURL)
}
