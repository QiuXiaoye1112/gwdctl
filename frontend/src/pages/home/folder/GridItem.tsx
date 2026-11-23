import { Center, VStack, Icon, Text } from "@hope-ui/solid"
import { batch, createSignal, Show } from "solid-js"
import { LinkWithPush } from "~/components"
import { checkboxOpen, getMainColor, selectIndex } from "~/store"
import { ObjType, StoreObj } from "~/types"
import { bus, getFileSize, hoverColor } from "~/utils"
import { getIconByObj } from "~/utils/icon"
import { ItemCheckbox } from "./helper"
import { FolderItemActions } from "./ListItem"

export const GridItem = (props: {
  obj: StoreObj
  index: number
  itemSize: () => number
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
  const [imgErr, setImgErr] = createSignal(false)
  const objIcon = (
    <Icon
      color={getMainColor()}
      boxSize={`${props.itemSize() - 30}px`}
      as={getIconByObj(props.obj)}
    />
  )
  const itemTitle = () =>
    props.obj.size > 0
      ? `${props.obj.name} (${getFileSize(props.obj.size)})`
      : props.obj.name
  return (
    <VStack
      classList={{ selected: !!props.obj.selected }}
      class="grid-item viselect-item"
      data-index={props.index}
      title={itemTitle()}
      w="$full"
      p="$1"
      spacing="$1"
      rounded="$lg"
      transition="background-color 0.15s, transform 0.15s"
      _hover={{
        transform: "scale(1.06)",
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
      <Center
        class="item-thumbnail"
        h={`${props.itemSize()}px`}
        w="$full"
        cursor={props.obj.type !== ObjType.IMAGE ? "inherit" : "pointer"}
        on:click={(e: MouseEvent) => {
          if (props.obj.type !== ObjType.IMAGE) return
          if (e.ctrlKey || e.metaKey || e.shiftKey) return
          if (!restoreSelectionCache()) return
          bus.emit("gallery", props.obj.name)
          e.preventDefault()
          e.stopPropagation()
        }}
        pos="relative"
      >
        <Show when={checkboxOpen()}>
          <ItemCheckbox
            pos="absolute"
            left="$1"
            top="$1"
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
        <Show when={props.obj.thumb && !imgErr()} fallback={objIcon}>
          <img
            class="folder-grid-thumb"
            src={props.obj.thumb}
            alt={props.obj.name}
            loading="lazy"
            decoding="async"
            onError={() => setImgErr(true)}
          />
        </Show>
      </Center>
      <Text
        class={filenameClass()}
        w="$full"
        textAlign="center"
        fontSize="$sm"
        title={itemTitle()}
      >
        {props.obj.name}
      </Text>
    </VStack>
  )
}
