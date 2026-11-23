import { Grid } from "@hope-ui/solid"
import { createMemo, For } from "solid-js"
import { useContextMenu } from "solid-contextmenu"
import { GridItem } from "./GridItem"
import { FolderItemActions } from "./ListItem"
import "lightgallery/css/lightgallery-bundle.css"
import { usePath, useRouter, useUtil } from "~/hooks"
import { local, objStore } from "~/store"
import { bus } from "~/utils"
import { useSelectWithMouse } from "./helper"

const safeGridItemSize = () => {
  const n = Number.parseInt(local["grid_item_size"], 10)
  return Number.isFinite(n) ? Math.min(300, Math.max(50, n)) : 90
}

const getFilenameClass = () => {
  switch (local["list_item_filename_overflow"]) {
    case "multi_line":
      return "filename-multi-line"
    case "scrollable":
      return "filename-scrollable"
    default:
      return "filename-ellipsis"
  }
}

const GridLayout = () => {
  const { pushHref, to } = useRouter()
  const { isHide } = useUtil()
  const { setPathAs } = usePath()
  const { show: showContextMenu } = useContextMenu({ id: 1 })
  const {
    registerSelectContainer,
    captureContentMenu,
    openWithDoubleClick,
    toggleWithClick,
    restoreSelectionCache,
  } = useSelectWithMouse()
  registerSelectContainer()

  const itemSize = createMemo(safeGridItemSize)
  const filenameClass = createMemo(getFilenameClass)
  const actions: FolderItemActions = {
    isHide,
    setPathAs,
    showContextMenu,
    pushHref,
    to,
    openWithDoubleClick,
    toggleWithClick,
    restoreSelectionCache,
    filenameClass,
  }

  const onDragOver = (e: DragEvent) => {
    const items = Array.from(e.dataTransfer?.items ?? [])
    for (let i = 0; i < items.length; i++) {
      const item = items[i]
      if (item.kind === "file") {
        bus.emit("tool", "upload")
        e.preventDefault()
        break
      }
    }
  }

  return (
    <Grid
      onDragOver={onDragOver}
      oncapture:contextmenu={captureContentMenu}
      class="viselect-container folder-view-enter"
      w="$full"
      gap="$1"
      alignItems="start"
      templateColumns={`repeat(auto-fill, minmax(${
        itemSize() + 20
      }px,1fr))`}
    >
      <For each={objStore.objs}>
        {(obj, i) => {
          return (
            <GridItem
              obj={obj}
              index={i()}
              itemSize={itemSize}
              actions={actions}
            />
          )
        }}
      </For>
    </Grid>
  )
}

export default GridLayout
