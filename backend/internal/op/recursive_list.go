package op

import (
	"context"
	stdpath "path"
	"sync/atomic"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
)

func RecursivelyListStorage(ctx context.Context, storage driver.Driver, actualPath string, limiter *rate.Limiter, counter *atomic.Uint64) {
	objs, err := List(ctx, storage, actualPath, model.ListArgs{Refresh: true})
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			log.Errorf("error recursively list: failed list (%s)[%s]: %v", storage.GetStorage().MountPath, actualPath, err)
		}
		return
	}
	if counter != nil {
		counter.Add(uint64(len(objs)))
	}
	for _, obj := range objs {
		if utils.IsCanceled(ctx) {
			return
		}
		if !obj.IsDir() {
			continue
		}
		if limiter != nil {
			if err = limiter.Wait(ctx); err != nil {
				return
			}
		}
		nextPath := stdpath.Join(actualPath, obj.GetName())
		RecursivelyListStorage(ctx, storage, nextPath, limiter, counter)
	}
}
