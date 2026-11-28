package google_drive

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/go-resty/resty/v2"
)

const driveChangesURL = "https://www.googleapis.com/drive/v3/changes"

var errSizeIndexNeedsRebuild = errors.New("Drive folder size change feed needs a rebuild")

type driveSizeChange struct {
	FileID     string        `json:"fileId"`
	ChangeType string        `json:"changeType"`
	Removed    bool          `json:"removed"`
	File       *sizeMetaFile `json:"file"`
}

type sizeChangesResp struct {
	Changes           []driveSizeChange `json:"changes"`
	NextPageToken     string            `json:"nextPageToken"`
	NewStartPageToken string            `json:"newStartPageToken"`
}

// Values are the final metadata for each changed ID. A nil value means removal.
type sizeChanges map[string]*sizeMetaFile

type sizeIndexUpdate struct {
	mode     string
	token    string
	changes  sizeChanges
	delta    map[string]int64
	files    map[string]sizeMetaFile
	sizes    map[string]int64
	children sizeChildIndex
}

func (u sizeIndexUpdate) publish(idx *folderSizeIndex) {
	if u.files != nil {
		idx.files = u.files
		idx.sizes = u.sizes
		idx.children = u.children
	} else {
		for id, file := range u.changes {
			idx.children.remove(idx.files[id])
			if file != nil {
				idx.children.add(*file)
			}
		}
		u.changes.apply(idx.files)
		for id, delta := range u.delta {
			if n := idx.sizes[id] + delta; n != 0 {
				idx.sizes[id] = n
			} else {
				delete(idx.sizes, id)
			}
		}
	}
	idx.changeToken = u.token
}

func (changes sizeChanges) apply(files map[string]sizeMetaFile) {
	for id, file := range changes {
		if file == nil {
			delete(files, id)
		} else {
			files[id] = *file
		}
	}
}

func sizeMetadataByID(files []sizeMetaFile) map[string]sizeMetaFile {
	items := make(map[string]sizeMetaFile, len(files))
	for _, file := range files {
		if file.Id != "" && !file.Trashed {
			items[file.Id] = file
		}
	}
	return items
}

// Reverse edges let a topology change touch its subtree without walking unrelated files.
type sizeChildIndex map[string]map[string]struct{}

func newSizeChildIndex(files map[string]sizeMetaFile) sizeChildIndex {
	children := make(sizeChildIndex)
	for _, f := range files {
		children.add(f)
	}
	return children
}

func (children sizeChildIndex) add(file sizeMetaFile) {
	for _, parent := range file.Parents {
		if parent == "" {
			continue
		}
		if children[parent] == nil {
			children[parent] = make(map[string]struct{})
		}
		children[parent][file.Id] = struct{}{}
	}
}

func (children sizeChildIndex) remove(file sizeMetaFile) {
	for _, parent := range file.Parents {
		if ids := children[parent]; ids != nil {
			delete(ids, file.Id)
			if len(ids) == 0 {
				delete(children, parent)
			}
		}
	}
}

func (d *GoogleDrive) prepareSizeIndexUpdate(ctx context.Context, idx *folderSizeIndex) (sizeIndexUpdate, error) {
	if idx.changeToken == "" || idx.files == nil || idx.children == nil {
		return d.rebuildSizeIndex(ctx)
	}
	changes, token, err := d.fetchSizeChanges(ctx, idx.changeToken)
	if errors.Is(err, errSizeIndexNeedsRebuild) {
		return d.rebuildSizeIndex(ctx)
	}
	if err != nil {
		return sizeIndexUpdate{}, err
	}
	update, ok := stageSizeChanges(idx, changes, token)
	if !ok {
		return d.rebuildSizeIndex(ctx)
	}
	return update, nil
}

func stageSizeChanges(idx *folderSizeIndex, changes sizeChanges, token string) (sizeIndexUpdate, bool) {
	u := sizeIndexUpdate{mode: "updated", token: token, changes: changes, delta: make(map[string]int64)}
	oldFile := func(id string) sizeMetaFile { return idx.files[id] }
	newFile := func(id string) sizeMetaFile {
		if file, changed := changes[id]; changed {
			if file == nil {
				return sizeMetaFile{}
			}
			return *file
		}
		return oldFile(id)
	}
	affected := make(map[string]struct{}, len(changes))
	removedParents := make(map[string]struct{})
	collect := make([]string, 0)
	for id, file := range changes {
		affected[id] = struct{}{}
		before, after := oldFile(id), newFile(id)
		wasDir, isDir := before.MimeType == driveFolderMime, after.MimeType == driveFolderMime
		if file == nil && (wasDir || len(idx.children[id]) != 0) {
			removedParents[id] = struct{}{}
			for child := range idx.children[id] {
				if slices.Contains(newFile(child).Parents, id) {
					// A folder removal can implicitly hide unseen descendant
					// changes. Rebuild rather than guess from a non-empty tree.
					return sizeIndexUpdate{}, false
				}
			}
		}
		if wasDir != isDir || (isDir && !slices.Equal(before.Parents, after.Parents)) {
			collect = append(collect, id)
		}
	}
	for _, file := range changes {
		if file == nil {
			continue
		}
		for _, parent := range file.Parents {
			if _, missing := removedParents[parent]; missing {
				return sizeIndexUpdate{}, false
			}
		}
	}
	visited := make(map[string]struct{})
	for len(collect) > 0 {
		id := collect[len(collect)-1]
		collect = collect[:len(collect)-1]
		if _, seen := visited[id]; seen {
			continue
		}
		visited[id] = struct{}{}
		affected[id] = struct{}{}
		for child := range idx.children[id] {
			collect = append(collect, child)
		}
	}
	for id := range affected {
		addFolderSize(u.delta, oldFile, oldFile(id), -1)
		addFolderSize(u.delta, newFile, newFile(id), 1)
	}
	for id, delta := range u.delta {
		if idx.sizes[id]+delta < 0 {
			return sizeIndexUpdate{}, false
		}
	}
	return u, true
}

func (d *GoogleDrive) rebuildSizeIndex(ctx context.Context) (sizeIndexUpdate, error) {
	var start struct {
		Token string `json:"startPageToken"`
	}
	_, err := d.requestWithContext(ctx, driveChangesURL+"/startPageToken", http.MethodGet, func(req *resty.Request) {
		req.QueryParam.Del("includeItemsFromAllDrives")
		req.SetQueryParam("fields", "startPageToken")
	}, &start)
	if err != nil {
		return sizeIndexUpdate{}, err
	}
	if start.Token == "" {
		return sizeIndexUpdate{}, errors.New("Drive response did not contain an initial size index cursor")
	}
	// Take the cursor before listing, then apply changes that arrived during
	// the scan so the new snapshot and cursor cover the same window.
	files, incomplete, err := d.fetchAllSizeMetadata(ctx)
	if err != nil {
		return sizeIndexUpdate{}, err
	}
	if incomplete {
		return sizeIndexUpdate{}, errors.New("Drive folder size metadata listing is incomplete")
	}
	changes, token, err := d.fetchSizeChanges(ctx, start.Token)
	if err != nil {
		return sizeIndexUpdate{}, err
	}
	items := sizeMetadataByID(files)
	changes.apply(items)
	return sizeIndexUpdate{
		mode: "rebuilt", token: token, changes: changes, files: items, sizes: aggregateFolderSizes(items), children: newSizeChildIndex(items),
	}, nil
}

func (d *GoogleDrive) fetchSizeChanges(ctx context.Context, token string) (sizeChanges, string, error) {
	changes := make(sizeChanges)
	for range sizeIndexMaxPages {
		var resp sizeChangesResp
		_, err := d.requestWithContext(ctx, driveChangesURL, http.MethodGet, func(req *resty.Request) {
			req.SetQueryParams(map[string]string{
				"pageToken":      token,
				"pageSize":       "1000",
				"spaces":         "drive",
				"includeRemoved": "true",
				"fields":         "nextPageToken,newStartPageToken,changes(fileId,changeType,removed,file(" + sizeMetaFields + "))",
			})
		}, &resp)
		if err != nil {
			var status *driveHTTPError
			if errors.As(err, &status) && (status.status == http.StatusGone || status.status == http.StatusNotFound || status.status == http.StatusBadRequest) {
				return nil, "", fmt.Errorf("%w: %v", errSizeIndexNeedsRebuild, err)
			}
			return nil, "", err
		}
		for _, change := range resp.Changes {
			if change.ChangeType == "drive" {
				continue
			}
			if change.FileID == "" {
				return nil, "", fmt.Errorf("%w: missing changed file ID", errSizeIndexNeedsRebuild)
			}
			switch {
			case change.Removed || change.File != nil && change.File.Trashed:
				changes[change.FileID] = nil
			case change.File == nil || change.File.Id != change.FileID || change.File.MimeType == "":
				return nil, "", fmt.Errorf("%w: incomplete changed file metadata", errSizeIndexNeedsRebuild)
			default:
				changes[change.FileID] = change.File
			}
		}
		if resp.NextPageToken == "" {
			if resp.NewStartPageToken != "" {
				return changes, resp.NewStartPageToken, nil
			}
			break
		}
		if resp.NextPageToken == token {
			break
		}
		token = resp.NextPageToken
	}
	return nil, "", fmt.Errorf("%w: incomplete or excessive change pagination", errSizeIndexNeedsRebuild)
}
