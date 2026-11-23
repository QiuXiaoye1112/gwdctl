import {
  HStack,
  Icon,
  Progress,
  ProgressIndicator,
  ProgressLabel,
  Text,
} from "@hope-ui/solid"
import { batch, Show } from "solid-js"
import { LinkWithPush } from "~/components"
import {
  checkboxOpen,
  getMainColor,
  getSettingBool,
  OrderBy,
  selectIndex,
} from "~/store"
import { Obj, ObjType, StoreObj } from "~/types"
import {
  bus,
  formatDate,
  getFileSize,
  hoverColor,
  showDiskUsage,
  usedPercentage,
  toReadableUsage,
  nearlyFull,
} from "~/utils"
import { getIconByObj } from "~/utils/icon"
import { ItemCheckbox } from "./helper"

export interface Col {
  name: OrderBy
  textAlign: "left" | "right"
  w: any
}

export const cols: Col[] = [
  { name: "name", textAlign: "left", w: { "@initial": "76%", "@md": "50%" } },
  { name: "size", textAlign: "right", w: { "@initial": "24%", "@md": "17%" } },
  { name: "modified", textAlign: "right", w: { "@initial": 0, "@md": "33%" } },
]

export interface FolderItemActions {
  isHide: (obj: Obj) => boolean
  setPathAs: (path: string, dir?: boolean, push?: boolean) => void
  showContextMenu: (e: MouseEvent, opts: { props: StoreObj }) => void
  pushHref: (path: string) => string
  to: (path: string) => void
  openWithDoubleClick: () => boolean
  toggleWithClick: () => boolean
  restoreSelectionCache: () => boolean
  filenameClass: () => string
}

export const ListItem = (props: {
  obj: StoreObj
  index: number
  actions: FolderItemActions
}) => {
  const {
    isHide,
    setPathAs,
    showContextMenu,
    pushHref,
    to,
    openWithDoubleClick,
    toggleWithClick,
    restoreSelectionCache,
    filenameClass,
  } = props.actions
  if (isHide(props.obj)) {
    return null
  }
  const hasValidCreated = () =>
    !!props.obj.created && Date.parse(props.obj.created) > 0
  const displayTime = () =>
    hasValidCreated() ? props.obj.created : props.obj.modified
  return (
    <HStack
      classList={{ selected: !!props.obj.selected }}
      class="list-item viselect-item"
      data-index={props.index}
      w="$full"
      p="$2"
      rounded="$lg"
      transition="background-color 0.15s, transform 0.15s"
      _hover={{
        transform: "scale(1.01)",
        bgColor: hoverColor(),
      }}
      as={LinkWithPush}
      href={props.obj.name}
      cursor={
        openWithDoubleClick() || toggleWithClick() ? "default" : "pointer"
      }
      bgColor={props.obj.selected ? hoverColor() : undefined}
      on:dblclick={() => {
        if (!openWithDoubleClick()) return
        selectIndex(props.index, true, true)
        to(pushHref(props.obj.name))
      }}
      on:click={(e: MouseEvent) => {
        e.preventDefault()
        if (openWithDoubleClick()) return
        if (e.ctrlKey || e.metaKey || e.shiftKey) return
        if (!restoreSelectionCache()) return
        if (toggleWithClick())
          return selectIndex(props.index, !props.obj.selected)
        to(pushHref(props.obj.name))
      }}
      onMouseEnter={() => {
        setPathAs(props.obj.name, props.obj.is_dir, true)
      }}
      onContextMenu={(e: MouseEvent) => {
        batch(() => {
          selectIndex(props.index, true, true)
        })
        showContextMenu(e, { props: props.obj })
      }}
    >
      <HStack class="name-box" spacing="$1" w={cols[0].w} minW="0">
        <Show when={checkboxOpen()}>
          <ItemCheckbox
            flexShrink={0}
            on:mousedown={(e: MouseEvent) => {
              e.stopPropagation()
            }}
            on:click={(e: MouseEvent) => {
              e.stopPropagation()
            }}
            checked={props.obj.selected}
            onChange={(e: any) => {
              selectIndex(props.index, e.target.checked)
            }}
          />
        </Show>
        <Icon
          class="icon"
          boxSize="$6"
          flexShrink={0}
          color={getMainColor()}
          as={getIconByObj(props.obj)}
          mr="$1"
          cursor={props.obj.type !== ObjType.IMAGE ? "inherit" : "pointer"}
          on:click={(e: MouseEvent) => {
            if (props.obj.type !== ObjType.IMAGE) return
            if (e.ctrlKey || e.metaKey || e.shiftKey) return
            if (!restoreSelectionCache()) return
            bus.emit("gallery", props.obj.name)
            e.preventDefault()
            e.stopPropagation()
          }}
        />
        <Text
          class={`name ${filenameClass()}`}
          flex="1"
          minW="0"
          title={props.obj.name}
        >
          {props.obj.name}
        </Text>
      </HStack>
      <Show
        fallback={
          <Text
            class="size"
            w={cols[1].w}
            flexShrink={0}
            textAlign={cols[1].textAlign as any}
          >
            {getFileSize(props.obj.size)}
          </Text>
        }
        when={showDiskUsage(props.obj.mount_details)}
      >
        <Show
          fallback={
            <Text
              class="size"
              w={cols[1].w}
              flexShrink={0}
              textAlign={cols[1].textAlign as any}
            >
              {toReadableUsage(props.obj.mount_details!)}
            </Text>
          }
          when={!getSettingBool("show_disk_usage_in_plain_text")}
        >
          <Progress
            w={cols[1].w}
            class="disk-usage-percentage"
            trackColor="$info3"
            rounded="$full"
            size="md"
            value={usedPercentage(props.obj.mount_details!)}
          >
            <ProgressIndicator
              color={
                nearlyFull(props.obj.mount_details!) ? "$danger6" : "$info6"
              }
              rounded="$md"
            />
            <ProgressLabel class="disk-usage-text">
              {toReadableUsage(props.obj.mount_details!)}
            </ProgressLabel>
          </Progress>
        </Show>
      </Show>
      <Text
        class="modified"
        display={{ "@initial": "none", "@md": "inline" }}
        w={cols[2].w}
        flexShrink={0}
        textAlign={cols[2].textAlign as any}
        title={
          hasValidCreated()
            ? `${formatDate(props.obj.created)} / ${formatDate(props.obj.modified)}`
            : formatDate(props.obj.modified)
        }
      >
        {formatDate(displayTime())}
      </Text>
    </HStack>
  )
}
