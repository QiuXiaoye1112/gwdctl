import {
  createEffect,
  createMemo,
  onCleanup,
  Switch,
  Match,
  on,
} from "solid-js"
import {
  countMsg,
  layout,
  local,
  objStore,
  selectAll,
  smartCountMsg,
  syncSortForPath,
} from "~/store"
import { ContextMenu } from "./context-menu"
import { Pager } from "./Pager"
import { useLink, useRouter } from "~/hooks"
import { ObjType } from "~/types"
import { bus } from "~/utils"
import { clearSelectionCache } from "./helper"
import { SortBar } from "./SortBar"
import lightGallery from "lightgallery"
import lgThumbnail from "lightgallery/plugins/thumbnail"
import lgZoom from "lightgallery/plugins/zoom"
import lgRotate from "lightgallery/plugins/rotate"
import lgAutoplay from "lightgallery/plugins/autoplay"
import lgFullscreen from "lightgallery/plugins/fullscreen"
import "lightgallery/css/lightgallery-bundle.css"
import { LightGallery } from "lightgallery/lightgallery"
import { Search } from "./Search"

import ListLayout from "./List"
import GridLayout from "./Grid"

const Folder = () => {
  const { pathname } = useRouter()
  createEffect(
    on(
      pathname,
      (p) => {
        selectAll(false)
        clearSelectionCache()
        syncSortForPath(p)
      },
      { defer: false },
    ),
  )
  const { rawLink } = useLink()
  const images = createMemo(() =>
    objStore.objs.filter((obj) => obj.type === ObjType.IMAGE),
  )

  let dynamicGallery: LightGallery | undefined
  const initGallery = () => {
    dynamicGallery = lightGallery(document.createElement("div"), {
      addClass: "lightgallery-container",
      dynamic: true,
      thumbnail: local["show_gallery_thumbnails"] === "visible",
      plugins: [lgZoom, lgThumbnail, lgRotate, lgAutoplay, lgFullscreen],
      dynamicEl: images().map((obj) => {
        const raw = rawLink(obj, true)
        return {
          src: raw,
          thumb: obj.thumb === "" ? raw : obj.thumb,
          subHtml: `<h4>${obj.name}</h4>`,
        }
      }),
    })
  }
  createEffect(
    on([images, () => local["show_gallery_thumbnails"]], () => {
      dynamicGallery?.destroy()
      dynamicGallery = undefined
    }),
  )
  bus.on("gallery", (name) => {
    if (!dynamicGallery) {
      initGallery()
    }
    dynamicGallery?.openGallery(images().findIndex((obj) => obj.name === name))
  })
  onCleanup(() => {
    bus.off("gallery")
    dynamicGallery?.destroy()
  })
  return (
    <>
      <SortBar
        countText={() => (layout() === "grid" ? smartCountMsg() : countMsg())}
        isGrid={() => layout() === "grid"}
      />
      <Switch>
        <Match when={layout() === "list"}>
          <ListLayout />
        </Match>
        <Match when={layout() === "grid"}>
          <GridLayout />
        </Match>
      </Switch>
      <Pager />
      <Search />
      <ContextMenu />
    </>
  )
}

export default Folder
