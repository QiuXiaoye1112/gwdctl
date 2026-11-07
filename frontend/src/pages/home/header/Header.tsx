import {
  Box,
  HStack,
  useColorModeValue,
  Image,
  Center,
  Icon,
  IconButton,
  Badge,
  Tooltip,
  Kbd,
  CenterProps,
} from "@hope-ui/solid"
import { changeColor } from "seemly"
import { Show, createMemo } from "solid-js"
import { getMainColor, getSetting, local, objStore, State, me } from "~/store"
import { UserMethods } from "~/types"
import { BsSearch } from "solid-icons/bs"
import { OcWorkflow2 } from "solid-icons/oc"
import { CenterLoading } from "~/components"
import { Container } from "../Container"
import { bus } from "~/utils"
import { Layout } from "./layout"
import { isMac } from "~/utils/compatibility"
import { useRouter, useT } from "~/hooks"
import { runningTaskCount } from "../toolbar/TasksDrawer"

export const Header = (props: { flush?: boolean } = {}) => {
  const { isShare } = useRouter()
  const logos = getSetting("logo").split("\n")
  const logo = useColorModeValue(logos[0], logos.pop())

  const stickyProps = createMemo<CenterProps>(() => {
    switch (local["position_of_header_navbar"]) {
      case "sticky":
        return { position: "sticky", zIndex: "$sticky", top: 0 }
      default:
        return { position: undefined, zIndex: undefined, top: undefined }
    }
  })

  const content = () => (
    <HStack
      px="calc(2% + 0.5rem)"
      py="$2"
      w="$full"
      justifyContent="space-between"
    >
      <HStack class="header-left" h="44px" spacing="$2">
        <Image src={logo()!} h="$full" w="auto" fallback={<CenterLoading />} />
      </HStack>
      <HStack class="header-right" spacing="$2">
        <Show when={objStore.state === State.Folder}>
          <Show when={getSetting("search_index") !== "none"}>
            <HStack
              bg="$neutral4"
              w="$32"
              p="$1"
              rounded="$md"
              justifyContent="space-between"
              border="2px solid transparent"
              cursor="pointer"
              color={getMainColor()}
              bgColor={changeColor(getMainColor(), { alpha: 0.15 })}
              _hover={{
                bgColor: changeColor(getMainColor(), { alpha: 0.2 }),
              }}
              onClick={() => {
                bus.emit("tool", "search")
              }}
            >
              <Icon as={BsSearch} />
              <HStack>
                {isMac ? <Kbd>Cmd</Kbd> : <Kbd>Ctrl</Kbd>}
                <Kbd>K</Kbd>
              </HStack>
            </HStack>
          </Show>
          <Show when={!isShare() && !UserMethods.is_guest(me())}>
            <Tooltip label={useT()("home.toolbar.tasks") || "任务"}>
              <Box pos="relative" display="inline-flex">
                <IconButton
                  aria-label="tasks"
                  icon={<OcWorkflow2 />}
                  color={getMainColor()}
                  bgColor={changeColor(getMainColor(), { alpha: 0.15 })}
                  _hover={{
                    bgColor: changeColor(getMainColor(), { alpha: 0.2 }),
                  }}
                  compact
                  size="lg"
                  onClick={() => {
                    bus.emit("tool", "tasks")
                  }}
                />
                <Show when={runningTaskCount() > 0}>
                  <Badge
                    pos="absolute"
                    top="-6px"
                    right="-6px"
                    colorScheme="danger"
                    rounded="$full"
                    fontSize="10px"
                    px="$1"
                    minW="18px"
                    textAlign="center"
                    lineHeight="14px"
                  >
                    {runningTaskCount()}
                  </Badge>
                </Show>
              </Box>
            </Tooltip>
          </Show>
          <Layout />
        </Show>
      </HStack>
    </HStack>
  )

  return (
    <Center
      {...stickyProps}
      bgColor="$background"
      class="header"
      w="$full"
      // shadow="$md"
    >
      <Show when={props.flush} fallback={<Container>{content()}</Container>}>
        <Box w="$full">{content()}</Box>
      </Show>
    </Center>
  )
}
