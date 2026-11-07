package model

const (
	SINGLE           = 0
	SITE             = 1
	STYLE            = 2
	PREVIEW          = 3
	GLOBAL           = 4
	OFFLINE_DOWNLOAD = 5
	INDEX            = 6
	SSO              = 7
	LDAP             = 8
	S3               = 9
	FTP              = 10
	TRAFFIC          = 11
	MEDIA            = 12
	TRANSCODE        = 13
)

const (
	PUBLIC = iota
	PRIVATE
	READONLY
	DEPRECATED
)

type SettingItem struct {
	Key            string `json:"key" gorm:"primaryKey" binding:"required"` // unique key
	Value          string `json:"value"`                                    // value
	MigrationValue string `json:"-" gorm:"-:all"`                           // deprecated value
	Help           string `json:"help"`                                     // help message
	Type           string `json:"type"`                                     // string, number, bool, select
	Options        string `json:"options"`                                  // values for select
	Group          int    `json:"group"`                                    // use to group setting in frontend
	Flag           int    `json:"flag"`                                     // 0 = public, 1 = private, 2 = readonly, 3 = deprecated, etc.
	Index          uint   `json:"index"`
}

func (s SettingItem) IsDeprecated() bool {
	return s.Flag == DEPRECATED
}
