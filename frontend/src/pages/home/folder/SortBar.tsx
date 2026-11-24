import {
  Box,
  HStack,
  Icon,
  Menu,
  MenuContent,
  MenuItem,
  MenuTrigger,
  Text,
} from "@hope-ui/solid"
import { For, Show } from "solid-js"
import { FaSolidAngleDown } from "solid-icons/fa"
import { useT } from "~/hooks"
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
  smartCountMsg,
  toggleDirSort,
} from "~/store"
import { hoverColor } from "~/utils"
import { sortArrowFor } from "~/utils/sort"
import { ItemCheckbox } from "./helper"

const SORT_FIELDS: readonly ("name" | "size" | "created")[] = [
  "name",
  "size",
  "created",
] as const

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

const CompactSortMenu = () => {
  const t = useT()
  const currentOrderBy = () =>
    dirSort().orderBy === "modified" ? "created" : dirSort().orderBy

  return (
    <Menu placement="bottom-end" offset={4}>
      <MenuTrigger
        type="button"
        title={t(`home.obj.sort_${currentOrderBy()}_tip`)}
        display="inline-flex"
        alignItems="center"
        gap="$1"
        px="$2"
        py="3px"
        rounded="$md"
        fontSize="$xs"
        lineHeight="1.4"
        fontWeight="$semibold"
        color={getMainColor()}
        bgColor={hoverColor()}
        border="none"
        outline="none"
        cursor="pointer"
        userSelect="none"
        flexShrink={0}
        css={{ whiteSpace: "nowrap" }}
        transition="background-color 0.15s, color 0.15s"
        _focus={{
          outline: "none",
          boxShadow: "none",
        }}
        _focusVisible={{
          outline: "2px solid $info8",
          outlineOffset: "1px",
        }}
      >
        {t(`home.obj.sort_${currentOrderBy()}`)}{" "}
        {sortArrowFor(currentOrderBy(), dirSort().reverse)}
        <Icon as={FaSolidAngleDown} boxSize="11px" opacity={0.8} />
      </MenuTrigger>
      <MenuContent minW="120px">
        <For each={SORT_FIELDS}>
          {(field) => {
            const active = () => currentOrderBy() === field
            return (
              <MenuItem onSelect={() => toggleDirSort(field)}>
                <HStack
                  w="$full"
                  justifyContent="space-between"
                  alignItems="center"
                  spacing="$3"
                >
                  <Text
                    fontWeight={active() ? "$semibold" : "$normal"}
                    color={active() ? getMainColor() : undefined}
                  >
                    {t(`home.obj.sort_${field}`)}
                  </Text>
                  <Show when={active()}>
                    <Text fontWeight="$semibold" color={getMainColor()}>
                      {sortArrowFor(field, dirSort().reverse)}
                    </Text>
                  </Show>
                </HStack>
              </MenuItem>
            )
          }}
        </For>
      </MenuContent>
    </Menu>
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
        py={{ "@initial": "$1", "@md": "0" }}
        justifyContent="space-between"
        alignItems="center"
        flexWrap={{ "@initial": "nowrap", "@md": "wrap" }}
        gap="$2"
        display={
          props.isGrid() || showCount()
            ? "flex"
            : { "@initial": "flex", "@md": "none" }
        }
      >
        <Box display="flex" alignItems="center" minW="0" flex="1" gap="$2">
          <Show when={checkboxOpen()}>
            <Box
              display={
                props.isGrid()
                  ? "inline-flex"
                  : { "@initial": "inline-flex", "@md": "none" }
              }
              alignItems="center"
              flexShrink={0}
            >
              <ItemCheckbox
                checked={allChecked()}
                indeterminate={isIndeterminate()}
                onChange={(e: any) => {
                  selectAll(e.target.checked as boolean)
                }}
              />
            </Box>
          </Show>
          <Text
            size="sm"
            color="$neutral11"
            minW="0"
            css={{
              whiteSpace: "nowrap",
              overflow: "hidden",
              textOverflow: "ellipsis",
            }}
            display={{ "@initial": "block", "@md": "none" }}
          >
            {smartCountMsg()}
          </Text>
          <Show when={showCount() || (props.isGrid() && !!selectedMsg())}>
            <Text
              size="sm"
              color="$neutral11"
              display={{ "@initial": "none", "@md": "block" }}
            >
              {props.countText()}
            </Text>
          </Show>
        </Box>
        <Box
          display={{ "@initial": "inline-flex", "@md": "none" }}
          flexShrink={0}
        >
          <CompactSortMenu />
        </Box>
        <Show when={props.isGrid()}>
          <Box
            display={{ "@initial": "none", "@md": "flex" }}
            alignItems="center"
            gap="$1"
            flexShrink={0}
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
          </Box>
        </Show>
      </HStack>
    </Show>
  )
}
