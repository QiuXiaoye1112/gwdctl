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
