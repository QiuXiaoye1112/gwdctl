import { HStack, VStack, Text } from "@hope-ui/solid"
import { createMemo, createSignal, For, Show } from "solid-js"
import { useContextMenu } from "solid-contextmenu"
import { usePath, useRouter, useT, useUtil } from "~/hooks"
import {
  allChecked,
  checkboxOpen,
  dirSort,
  getMainColor,
  isIndeterminate,
  local,
  objStore,
  OrderBy,
  selectAll,
  selectedMsg,
  toggleDirSort,
} from "~/store"
import { nextSortState, sortArrowFor } from "~/utils/sort"
import { cols, FolderItemActions, ListItem } from "./ListItem"
import { ItemCheckbox, useSelectWithMouse } from "./helper"
import { bus } from "~/utils"

export const ListTitle = (props: {
  sortCallback?: (orderBy: OrderBy, reverse?: boolean) => void
  disableCheckbox?: boolean
  orderBy?: OrderBy
  reverse?: boolean
  onSort?: (orderBy: OrderBy) => void
  hideOnMobile?: boolean
}) => {
  const t = useT()

  const [localOrderBy, setLocalOrderBy] = createSignal<OrderBy | undefined>()
  const [localReverse, setLocalReverse] = createSignal(false)

  const activeOrderBy = () =>
    props.onSort ? props.orderBy : localOrderBy()
  const activeReverse = () =>
    props.onSort ? !!props.reverse : localReverse()

  const handleSort = (colName: OrderBy) => {
    if (props.onSort) {
      props.onSort(colName)
      return
    }
    const cur = localOrderBy()
    const next = cur
      ? nextSortState({ orderBy: cur, reverse: localReverse() }, colName)
      : { orderBy: colName, reverse: false }
    setLocalOrderBy(next.orderBy)
    setLocalReverse(next.reverse)
    props.sortCallback?.(next.orderBy, next.reverse)
  }

  const timeColOrder = (): OrderBy => (props.onSort ? "created" : "modified")

  const colLabel = (colName: OrderBy) => {
    const label = t(`home.obj.${colName}`)
    if (activeOrderBy() === colName) {
      return `${label} ${sortArrowFor(colName, activeReverse())}`
    }
    return label
  }

  const colColor = (colName: OrderBy) =>
    activeOrderBy() === colName ? getMainColor() : "$neutral11"

  return (
    <HStack
      class="title"
      w="$full"
      p="$2"
      display={
        props.hideOnMobile ? { "@initial": "none", "@md": "flex" } : "flex"
      }
    >
      <HStack w={cols[0].w} spacing="$1">
        <Show when={!props.disableCheckbox && checkboxOpen()}>
          <ItemCheckbox
            checked={allChecked()}
            indeterminate={isIndeterminate()}
            onChange={(e: any) => {
              selectAll(e.target.checked as boolean)
            }}
          />
        </Show>
        <Show
          when={selectedMsg()}
          fallback={
            <Text
              fontWeight="bold"
              fontSize="$sm"
              color={colColor(cols[0].name)}
              textAlign={cols[0].textAlign as any}
              cursor="pointer"
              userSelect="none"
              onClick={() => handleSort(cols[0].name)}
            >
              {colLabel(cols[0].name)}
            </Text>
          }
        >
          <Text
            fontWeight="bold"
            fontSize="$sm"
            color="$neutral11"
            textAlign={cols[0].textAlign as any}
          >
            {selectedMsg()}
          </Text>
        </Show>
      </HStack>
      <Text
        w={cols[1].w}
        fontWeight="bold"
        fontSize="$sm"
        color={colColor(cols[1].name)}
        textAlign={cols[1].textAlign as any}
        cursor="pointer"
        userSelect="none"
        onClick={() => handleSort(cols[1].name)}
      >
        {colLabel(cols[1].name)}
      </Text>
      <Text
        w={cols[2].w}
        fontWeight="bold"
        fontSize="$sm"
        color={colColor(timeColOrder())}
        textAlign={cols[2].textAlign as any}
        cursor="pointer"
        userSelect="none"
        onClick={() => handleSort(timeColOrder())}
        display={{ "@initial": "none", "@md": "inline" }}
      >
        {colLabel(timeColOrder())}
      </Text>
    </HStack>
  )
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

const ListLayout = () => {
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
    <VStack
      onDragOver={onDragOver}
      oncapture:contextmenu={captureContentMenu}
      class="list viselect-container folder-view-enter"
      w="$full"
      spacing="$1"
    >
      <ListTitle
        orderBy={dirSort().orderBy}
        reverse={dirSort().reverse}
        onSort={toggleDirSort}
        hideOnMobile
      />
      <For each={objStore.objs}>
        {(obj, i) => {
          return <ListItem obj={obj} index={i()} actions={actions} />
        }}
      </For>
    </VStack>
  )
}

export default ListLayout
