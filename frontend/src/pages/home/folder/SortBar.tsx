import { Box, HStack, Text } from "@hope-ui/solid"
import { Show } from "solid-js"
import { useT } from "~/hooks"
import {
  dirSort,
  getMainColor,
  local,
  objStore,
  OrderBy,
  toggleDirSort,
} from "~/store"
import { hoverColor } from "~/utils"
import { sortArrowFor } from "~/utils/sort"

const SortOptionButton = (props: {
  orderBy: OrderBy
  label: string
  tip: string
}) => {
  const active = () => dirSort().orderBy === props.orderBy
  const arrow = () => ` ${sortArrowFor(props.orderBy, dirSort().reverse)}`

  return (
    <Box
      as="button"
      type="button"
      title={props.tip}
      aria-pressed={active()}
      px="$2"
      py="2px"
      rounded="$md"
      fontSize="$xs"
      lineHeight="1.4"
      fontWeight={active() ? "$semibold" : "$normal"}
      color={active() ? getMainColor() : "$neutral11"}
      bgColor={active() ? hoverColor() : "transparent"}
      border="none"
      outline="none"
      cursor="pointer"
      userSelect="none"
      transition="background-color 0.15s, color 0.15s"
      _hover={{
        bgColor: hoverColor(),
      }}
      _focus={{
        outline: "none",
        boxShadow: "none",
      }}
      _focusVisible={{
        outline: "2px solid $info8",
        outlineOffset: "1px",
      }}
      onClick={() => toggleDirSort(props.orderBy)}
    >
      {props.label}
      <Show when={active()}>{arrow()}</Show>
    </Box>
  )
}

export const SortBar = (props: {
  countText: () => string
  isGrid: () => boolean
}) => {
  const t = useT()
  const showCount = () => local["show_count_msg"] === "visible"

  return (
    <Show when={objStore.objs.length > 0}>
      <HStack
        class="folder-sort-bar"
        w="$full"
        px="$2"
        justifyContent="space-between"
        alignItems="center"
        flexWrap="wrap"
        gap="$2"
        display={
          props.isGrid() || showCount()
            ? "flex"
            : { "@initial": "flex", "@md": "none" }
        }
      >
        <Show when={showCount()} fallback={<Box />}>
          <Text size="sm" color="$neutral11">
            {props.countText()}
          </Text>
        </Show>
        <HStack
          spacing="$1"
          alignItems="center"
          display={
            props.isGrid() ? "flex" : { "@initial": "flex", "@md": "none" }
          }
        >
          <SortOptionButton
            orderBy="name"
            label={t("home.obj.sort_name")}
            tip={t("home.obj.sort_name_tip")}
          />
          <SortOptionButton
            orderBy="size"
            label={t("home.obj.sort_size")}
            tip={t("home.obj.sort_size_tip")}
          />
          <SortOptionButton
            orderBy="created"
            label={t("home.obj.sort_created")}
            tip={t("home.obj.sort_created_tip")}
          />
        </HStack>
      </HStack>
    </Show>
  )
}
