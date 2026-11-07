package data

import (
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSiteTitleMigration(t *testing.T) {
	conf.Conf = conf.DefaultConfig(t.TempDir())
	conf.Conf.Database.Type = "sqlite3"
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.Init(database)
	defer db.Close()
	for _, tc := range []struct{ old, want string }{
		{"Google Drive", "Cloud Drive"}, {"OpenList", "Cloud Drive"}, {"My own title", "My own title"},
	} {
		t.Run(tc.old, func(t *testing.T) {
			if err := db.SaveSettingItem(&model.SettingItem{Key: conf.SiteTitle, Value: tc.old}); err != nil {
				t.Fatal(err)
			}
			op.SettingCacheUpdate()
			initSettings()
			item, err := db.GetSettingItemByKey(conf.SiteTitle)
			if err != nil {
				t.Fatal(err)
			}
			if item.Value != tc.want {
				t.Fatalf("title = %q, want %q", item.Value, tc.want)
			}
		})
	}
}

func TestSharePreviewSettingsOnUpgrade(t *testing.T) {
	conf.Conf = conf.DefaultConfig(t.TempDir())
	conf.Conf.Database.Type = "sqlite3"
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.Init(database)
	defer db.Close()
	for _, item := range []model.SettingItem{
		{Key: conf.SharePreviewDownloadByDefault, Value: "true"},
		{Key: conf.SharePreviewArchivesByDefault, Value: "false"},
		{Key: conf.PreviewDownloadByDefault, Value: "false"},
		{Key: conf.PreviewArchivesByDefault, Value: "true"},
	} {
		if err := db.SaveSettingItem(&item); err != nil {
			t.Fatal(err)
		}
	}
	op.SettingCacheUpdate()
	initSettings()
	settings := op.GetPublicSettingsMap()
	for key, want := range map[string]string{
		conf.SharePreview: "true", conf.ShareArchivePreview: "true",
		conf.PreviewDownloadByDefault: "false", conf.PreviewArchivesByDefault: "true",
	} {
		if got := settings[key]; got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if _, ok := settings[conf.SharePreviewDownloadByDefault]; ok {
		t.Error("obsolete independent sharing preference still registered")
	}
}
