package google_drive

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/go-resty/resty/v2"
)

type GoogleDrive struct {
	model.Storage
	Addition
	AccessToken string
}

func (d *GoogleDrive) Config() driver.Config {
	return config
}

func (d *GoogleDrive) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *GoogleDrive) Init(ctx context.Context) error {
	if d.ChunkSize == 0 {
		d.ChunkSize = 5
	}
	return d.refreshToken()
}

func (d *GoogleDrive) Drop(ctx context.Context) error {
	return nil
}

type ShortcutObj interface {
	GetTargetID() string
}

func getTargetID(obj model.Obj) string {
	for obj != nil {
		if sc, ok := obj.(ShortcutObj); ok && sc.GetTargetID() != "" {
			return sc.GetTargetID()
		}
		if unwrap, ok := obj.(model.ObjUnwrap); ok {
			obj = unwrap.Unwrap()
		} else {
			break
		}
	}
	return ""
}

func getReadID(obj model.Obj) string {
	if obj == nil {
		return ""
	}
	if tid := getTargetID(obj); tid != "" {
		return tid
	}
	return obj.GetID()
}

func (d *GoogleDrive) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	files, err := d.getFiles(getReadID(dir))
	if err != nil {
		return nil, err
	}
	return utils.SliceConvert(files, func(src File) (model.Obj, error) {
		return fileToObj(src), nil
	})
}

func (d *GoogleDrive) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	fileURL := fmt.Sprintf("https://www.googleapis.com/drive/v3/files/%s?includeItemsFromAllDrives=true&supportsAllDrives=true", getReadID(file))
	var metadata File
	_, err := d.request(fileURL, http.MethodGet, nil, &metadata)
	if err != nil {
		return nil, err
	}
	contentURL := fileURL + "&alt=media&acknowledgeAbuse=true"
	if metadata.MimeType == "application/vnd.google-apps.document" {
		contentURL = fmt.Sprintf("https://www.googleapis.com/drive/v3/files/%s/export?mimeType=%s", getReadID(file), url.QueryEscape("text/markdown"))
	}
	link := model.Link{
		URL: contentURL,
		Header: http.Header{
			"Authorization": []string{"Bearer " + d.AccessToken},
		},
	}
	return &link, nil
}

func (d *GoogleDrive) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) error {
	data := base.Json{
		"name":     dirName,
		"parents":  []string{getReadID(parentDir)},
		"mimeType": "application/vnd.google-apps.folder",
	}
	_, err := d.request("https://www.googleapis.com/drive/v3/files", http.MethodPost, func(req *resty.Request) {
		req.SetBody(data)
	}, nil)
	return err
}

func (d *GoogleDrive) Move(ctx context.Context, srcObj, dstDir model.Obj) error {
	var file struct {
		Parents []string `json:"parents"`
	}
	url := "https://www.googleapis.com/drive/v3/files/" + srcObj.GetID()
	_, err := d.request(url, http.MethodGet, func(req *resty.Request) {
		req.SetQueryParam("fields", "parents")
	}, &file)
	if err != nil {
		return err
	}
	query := map[string]string{
		"addParents":    getReadID(dstDir),
		"removeParents": strings.Join(file.Parents, ","),
	}
	_, err = d.request(url, http.MethodPatch, func(req *resty.Request) {
		req.SetQueryParams(query)
	}, nil)
	return err
}

func (d *GoogleDrive) Rename(ctx context.Context, srcObj model.Obj, newName string) error {
	data := base.Json{
		"name": newName,
	}
	url := "https://www.googleapis.com/drive/v3/files/" + srcObj.GetID()
	_, err := d.request(url, http.MethodPatch, func(req *resty.Request) {
		req.SetBody(data)
	}, nil)
	return err
}

func (d *GoogleDrive) Copy(ctx context.Context, srcObj, dstDir model.Obj) error {
	if srcObj.IsDir() {
		return errs.NotSupport
	}
	data := base.Json{
		"name":    srcObj.GetName(),
		"parents": []string{getReadID(dstDir)},
	}
	url := fmt.Sprintf("https://www.googleapis.com/drive/v3/files/%s/copy", srcObj.GetID())
	_, err := d.request(url, http.MethodPost, func(req *resty.Request) {
		req.SetBody(data)
	}, nil)
	return err
}

func (d *GoogleDrive) Remove(ctx context.Context, obj model.Obj) error {
	if obj == nil || obj.GetID() == "" {
		return errs.ObjectNotFound
	}
	data := base.Json{
		"trashed": true,
	}
	apiURL := "https://www.googleapis.com/drive/v3/files/" + url.PathEscape(obj.GetID())
	_, err := d.request(apiURL, http.MethodPatch, func(req *resty.Request) {
		if ctx != nil {
			req.SetContext(ctx)
		}
		req.SetBody(data)
	}, nil)
	return err
}

func (d *GoogleDrive) Put(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) error {
	obj := stream.GetExist()
	var (
		e    Error
		url  string
		data base.Json
		res  *resty.Response
		err  error
	)
	if obj != nil {
		url = fmt.Sprintf("https://www.googleapis.com/upload/drive/v3/files/%s?uploadType=resumable&supportsAllDrives=true", obj.GetID())
		data = base.Json{}
	} else {
		data = base.Json{
			"name":    stream.GetName(),
			"parents": []string{getReadID(dstDir)},
		}
		url = "https://www.googleapis.com/upload/drive/v3/files?uploadType=resumable&supportsAllDrives=true"
	}
	req := base.NoRedirectClient.R().
		SetHeaders(map[string]string{
			"Authorization":           "Bearer " + d.AccessToken,
			"X-Upload-Content-Type":   stream.GetMimetype(),
			"X-Upload-Content-Length": strconv.FormatInt(stream.GetSize(), 10),
		}).
		SetError(&e).SetBody(data).SetContext(ctx)
	if obj != nil {
		res, err = req.Patch(url)
	} else {
		res, err = req.Post(url)
	}
	if err != nil {
		return err
	}
	if res.StatusCode() == http.StatusUnauthorized || e.Error.Code == 401 {
		err = d.refreshToken()
		if err != nil {
			return err
		}
		return d.Put(ctx, dstDir, stream, up)
	}
	if res.IsError() {
		if e.Error.Code != 0 {
			return fmt.Errorf("%s: %v", e.Error.Message, e.Error.Errors)
		}
		return fmt.Errorf("init upload session failed with HTTP %d: %s", res.StatusCode(), res.String())
	}
	putUrl := res.Header().Get("location")
	if putUrl == "" {
		return fmt.Errorf("init upload session failed: missing Location header in response (HTTP %d)", res.StatusCode())
	}
	return d.chunkUpload(ctx, stream, putUrl, up)
}

func (d *GoogleDrive) GetDetails(ctx context.Context) (*model.StorageDetails, error) {
	if d.DisableDiskUsage {
		return nil, errs.NotImplement
	}
	about, err := d.getAbout(ctx)
	if err != nil {
		return nil, err
	}
	var total, used int64
	if about.StorageQuota.Limit == nil {
		total = 0
	} else {
		total, err = strconv.ParseInt(*about.StorageQuota.Limit, 10, 64)
		if err != nil {
			return nil, err
		}
	}
	used, err = strconv.ParseInt(about.StorageQuota.Usage, 10, 64)
	if err != nil {
		return nil, err
	}
	return &model.StorageDetails{
		DiskUsage: model.DiskUsage{
			TotalSpace: total,
			UsedSpace:  used,
		},
	}, nil
}

var _ driver.Driver = (*GoogleDrive)(nil)
