import {
  Badge,
  Box,
  Button,
  Center,
  createDisclosure,
  Drawer,
  DrawerBody,
  DrawerCloseButton,
  DrawerContent,
  DrawerFooter,
  DrawerHeader,
  DrawerOverlay,
  HStack,
  Icon,
  IconButton,
  Progress,
  ProgressIndicator,
  Spacer,
  Spinner,
  Text,
  Tooltip,
  VStack,
} from "@hope-ui/solid"
import { createEffect, createSignal, For, onCleanup, onMount, Show } from "solid-js"
import { OcWorkflow2 } from "solid-icons/oc"
import { RiSystemRefreshLine } from "solid-icons/ri"
import { IoClose } from "solid-icons/io"
import { useT, useRouter, usePath } from "~/hooks"
import { bus, getFileSize, notify, r } from "~/utils"
import { TaskInfo, Resp } from "~/types"
import { me } from "~/store"
import { UserMethods } from "~/types"
import { uploadFiles, cancelUpload } from "../uploads/Upload"

export const [runningTaskCount, setRunningTaskCount] = createSignal(0)

interface RunningTaskItem extends TaskInfo {
  taskType: string
  typeLabel: string
  typeColor: "info" | "warning" | "success" | "neutral" | "primary" | "accent" | "danger"
}

const TASK_CATEGORIES: {
  type: string
  labelZh: string
  labelEn: string
  color: "info" | "warning" | "success" | "neutral" | "primary" | "accent" | "danger"
}[] = [
  { type: "decompress", labelZh: "解压缩", labelEn: "Decompress", color: "primary" },
  { type: "decompress_upload", labelZh: "解压转存", labelEn: "Upload Extract", color: "accent" },
  { type: "upload", labelZh: "上传", labelEn: "Upload", color: "info" },
  { type: "copy", labelZh: "复制", labelEn: "Copy", color: "primary" },
  { type: "move", labelZh: "移动", labelEn: "Move", color: "warning" },
]

function formatTaskName(name: string): string {
  if (!name) return ""
  // match decompress [device](path)[inner] to ...
  const decompressMatch = name.match(/decompress\s+\[.*?\]\((?:.*\/)?([^\/]+)\)/i)
  if (decompressMatch) return decompressMatch[1]
  // match upload filename to ...
  const uploadMatch = name.match(/^upload\s+(?:\[.*?\]\((?:.*\/)?([^\/]+)\)|([^\s]+))/i)
  if (uploadMatch) return uploadMatch[1] || uploadMatch[2]
  // match copy / move
  const copyMatch = name.match(/(?:copy|move)\s+\[.*?\]\((?:.*\/)?([^\/]+)\)/i)
  if (copyMatch) return copyMatch[1]
  return name
}

export const TasksDrawer = () => {
  const t = useT()
  const { to } = useRouter()
  const { refresh } = usePath()
  const { isOpen, onOpen, onClose } = createDisclosure()
  const [tasks, setTasks] = createSignal<RunningTaskItem[]>([])
  const [loading, setLoading] = createSignal(false)
  const [cancelingId, setCancelingId] = createSignal<string>("")
  let prevBackendCount = 0
  let fetchingTasks = false
  let refetchQueued = false
  const trackedTaskIds: Record<"copy" | "move", Set<string>> = {
    copy: new Set(),
    move: new Set(),
  }

  const notifyFailedTrackedTasks = async (
    taskType: "copy" | "move",
    finishedIds: Set<string>,
  ) => {
    try {
      const doneRes = (await r.get(`/task/${taskType}/done`)) as Resp<TaskInfo[]>
      if (doneRes.code !== 200 || !Array.isArray(doneRes.data)) return
      for (const item of doneRes.data) {
        if (!finishedIds.has(item.id) || item.state !== 7) continue
        const label = formatTaskName(item.name)
        const detail = item.error || item.status || "Task failed"
        notify.error(label ? `${label}: ${detail}` : detail)
      }
    } catch {
      // ignore notification lookup failure
    }
  }

  const activeUploads = () =>
    uploadFiles.uploads.filter(
      (u) => !["success", "error"].includes(u.status),
    )

  const totalRunningCount = () => activeUploads().length + tasks().length

  createEffect(() => {
    setRunningTaskCount(totalRunningCount())
  })

  const fetchRunningTasks = async (showLoading = false) => {
    if (UserMethods.is_guest(me())) {
      setTasks([])
      return
    }
    if (fetchingTasks) {
      refetchQueued = true
      return
    }

    fetchingTasks = true
    if (showLoading) setLoading(true)
    try {
      const succeededCategories = new Set<string>()
      const runningIdsByType: Record<string, Set<string>> = {}
      const results = await Promise.all(
        TASK_CATEGORIES.map(async (cat) => {
          try {
            const res = (await r.get(`/task/${cat.type}/undone`)) as Resp<TaskInfo[]>
            if (res.code === 200 && Array.isArray(res.data)) {
              succeededCategories.add(cat.type)
              runningIdsByType[cat.type] = new Set(res.data.map((item) => item.id))
              return res.data.map((item: TaskInfo) => ({
                ...item,
                taskType: cat.type,
                typeLabel: localStorage.getItem("lang") === "en" ? cat.labelEn : cat.labelZh,
                typeColor: cat.color,
              }))
            }
          } catch {
            // ignore network/auth errors for specific categories
          }
          return []
        })
      )
      const allRunning = results.flat()
      let shouldRefresh = prevBackendCount > 0 && allRunning.length < prevBackendCount

      for (const trackedType of ["copy", "move"] as const) {
        if (!succeededCategories.has(trackedType) || trackedTaskIds[trackedType].size === 0) {
          continue
        }
        const stillRunning = runningIdsByType[trackedType]
        const finishedIds = new Set<string>()
        for (const id of trackedTaskIds[trackedType]) {
          if (!stillRunning.has(id)) {
            finishedIds.add(id)
          }
        }
        if (finishedIds.size > 0) {
          for (const id of finishedIds) {
            trackedTaskIds[trackedType].delete(id)
          }
          shouldRefresh = true
          void notifyFailedTrackedTasks(trackedType, finishedIds)
        }
      }

      if (shouldRefresh) {
        refresh(false, true)
      }
      prevBackendCount = allRunning.length
      setTasks(allRunning)
    } finally {
      fetchingTasks = false
      if (showLoading) setLoading(false)
      if (refetchQueued) {
        refetchQueued = false
        void fetchRunningTasks(false)
      }
    }
  }

  const cancelTask = async (taskType: string, tid: string) => {
    setCancelingId(tid)
    try {
      const res = (await r.post(`/task/${taskType}/cancel?tid=${tid}`)) as Resp<any>
      if (res.code === 200) {
        notify.success(t("tasks.state.4") || "已取消")
        await fetchRunningTasks()
      } else {
        notify.error(res.message)
      }
    } catch (e: any) {
      notify.error(e.message || "Failed to cancel")
    } finally {
      setCancelingId("")
    }
  }

  const handler = (name: string) => {
    if (name === "tasks") {
      onOpen()
      fetchRunningTasks(true)
    } else if (name === "tasks_refresh") {
      fetchRunningTasks(false)
    }
  }
  const trackTasksHandler = ({
    type,
    ids,
  }: {
    type: "copy" | "move"
    ids: string[]
  }) => {
    for (const id of ids) {
      if (id) {
        trackedTaskIds[type].add(id)
      }
    }
    void fetchRunningTasks(false)
  }
  bus.on("tool", handler)
  bus.on("track_tasks", trackTasksHandler)
  onCleanup(() => {
    bus.off("tool", handler)
    bus.off("track_tasks", trackTasksHandler)
  })

  // Polling interval
  let timer: any
  onMount(() => {
    fetchRunningTasks()
    timer = setInterval(() => {
      fetchRunningTasks(false)
    }, 1500)
  })
  onCleanup(() => {
    if (timer) clearInterval(timer)
  })

  return (
    <Drawer opened={isOpen()} placement="right" size="md" onClose={onClose}>
      <DrawerOverlay />
      <DrawerContent>
        <DrawerHeader>
          <HStack spacing="$2" w="$full" alignItems="center">
            <Icon as={OcWorkflow2} boxSize="$5" color="$info9" />
            <Text fontWeight="$bold" color="$info9">
              {t("tasks.undone") || "运行中的任务"}
            </Text>
            <Badge colorScheme="info" rounded="$full">
              {totalRunningCount()}
            </Badge>
            <Spacer />
            <HStack spacing="$1" alignItems="center">
              <Tooltip label={t("home.toolbar.refresh") || "刷新"}>
                <IconButton
                  aria-label="refresh tasks"
                  size="sm"
                  variant="ghost"
                  colorScheme="neutral"
                  icon={<RiSystemRefreshLine />}
                  loading={loading()}
                  onClick={() => fetchRunningTasks(true)}
                />
              </Tooltip>
              <Tooltip label={t("global.close") || "关闭"}>
                <IconButton
                  aria-label="close drawer"
                  size="sm"
                  variant="ghost"
                  colorScheme="neutral"
                  icon={<IoClose />}
                  onClick={onClose}
                />
              </Tooltip>
            </HStack>
          </HStack>
        </DrawerHeader>
        <DrawerBody>
          <Show
            when={!loading() || totalRunningCount() > 0}
            fallback={
              <Center h="$48">
                <Spinner size="lg" color="$info9" />
              </Center>
            }
          >
            <Show
              when={totalRunningCount() > 0}
              fallback={
                <Center h="$64" flexDirection="column">
                  <Icon as={OcWorkflow2} boxSize="$12" color="$neutral7" mb="$3" />
                  <Text color="$neutral10" fontSize="$sm">
                    {localStorage.getItem("lang") === "en"
                      ? "No running tasks at the moment"
                      : "当前没有正在运行的任务"}
                  </Text>
                </Center>
              }
            >
              <VStack spacing="$3" alignItems="stretch">
                <For each={activeUploads()}>
                  {(up) => (
                    <Box
                      p="$3"
                      rounded="$md"
                      border="1px solid"
                      borderColor="$neutral5"
                      bgColor="$neutral2"
                      _hover={{ borderColor: "$info7" }}
                      transition="border-color 0.2s"
                    >
                      <HStack justifyContent="space-between" mb="$2">
                        <HStack spacing="$2" overflow="hidden" maxW="70%">
                          <Badge colorScheme="info" flexShrink={0}>
                            {localStorage.getItem("lang") === "en" ? "Upload" : "上传"}
                          </Badge>
                          <Text
                            fontWeight="$semibold"
                            fontSize="$sm"
                            noOfLines={1}
                            title={up.path}
                          >
                            {up.path}
                          </Text>
                        </HStack>
                        <Badge colorScheme="success" flexShrink={0}>
                          {getFileSize(up.speed)}/s
                        </Badge>
                      </HStack>
                      <HStack spacing="$2">
                        <Progress
                          value={up.progress}
                          w="$full"
                          size="sm"
                          rounded="$full"
                        >
                          <ProgressIndicator
                            color={up.progress >= 100 ? "$success9" : "$info9"}
                            rounded="$full"
                          />
                        </Progress>
                        <Text
                          fontSize="$xs"
                          fontWeight="$medium"
                          color="$neutral11"
                          minW="40px"
                          textAlign="right"
                        >
                          {up.progress.toFixed(0)}%
                        </Text>
                      </HStack>
                      <HStack
                        justifyContent="space-between"
                        alignItems="center"
                        mt="$2"
                      >
                        <Text
                          fontSize="$xs"
                          color="$neutral9"
                          noOfLines={1}
                          maxW="75%"
                          title={up.path}
                        >
                          {getFileSize(up.size)}
                        </Text>
                        <Button
                          size="xs"
                          colorScheme="danger"
                          variant="subtle"
                          onClick={() => cancelUpload(up.path)}
                        >
                          {localStorage.getItem("lang") === "en"
                            ? "Cancel"
                            : "取消"}
                        </Button>
                      </HStack>
                    </Box>
                  )}
                </For>
                <For each={tasks()}>
                  {(task) => (
                    <Box
                      p="$3"
                      rounded="$md"
                      border="1px solid"
                      borderColor="$neutral5"
                      bgColor="$neutral2"
                      _hover={{ borderColor: "$info7" }}
                      transition="border-color 0.2s"
                    >
                      <HStack justifyContent="space-between" mb="$2">
                        <HStack spacing="$2" overflow="hidden" maxW="70%">
                          <Badge colorScheme={task.typeColor} flexShrink={0}>
                            {task.typeLabel}
                          </Badge>
                          <Text
                            fontWeight="$semibold"
                            fontSize="$sm"
                            noOfLines={1}
                            title={task.name}
                          >
                            {formatTaskName(task.name)}
                          </Text>
                        </HStack>
                        <Badge
                          colorScheme={task.state === 1 ? "success" : "info"}
                          flexShrink={0}
                        >
                          {t(`tasks.state.${task.state}`) || (task.state === 1 ? "运行中" : "等待中")}
                        </Badge>
                      </HStack>

                      {/* Progress bar */}
                      <HStack spacing="$2" mb="$2">
                        <Progress
                          value={task.progress}
                          w="$full"
                          size="sm"
                          rounded="$full"
                        >
                          <ProgressIndicator
                            color={task.progress >= 100 ? "$success9" : "$info9"}
                            rounded="$full"
                          />
                        </Progress>
                        <Text
                          fontSize="$xs"
                          fontWeight="$medium"
                          color="$neutral11"
                          minW="40px"
                          textAlign="right"
                        >
                          {task.progress.toFixed(0)}%
                        </Text>
                      </HStack>

                      {/* Task full name / details & cancel button */}
                      <HStack justifyContent="space-between" alignItems="center">
                        <Text
                          fontSize="$xs"
                          color="$neutral9"
                          noOfLines={1}
                          maxW="75%"
                          title={task.name}
                        >
                          {task.name}
                        </Text>
                        <Button
                          size="xs"
                          colorScheme="danger"
                          variant="subtle"
                          loading={cancelingId() === task.id}
                          onClick={() => cancelTask(task.taskType, task.id)}
                        >
                          {localStorage.getItem("lang") === "en" ? "Cancel" : "取消"}
                        </Button>
                      </HStack>
                    </Box>
                  )}
                </For>
              </VStack>
            </Show>
          </Show>
        </DrawerBody>
        <DrawerFooter justifyContent="space-between">
          <Button
            size="sm"
            variant="ghost"
            onClick={() => {
              onClose()
              to("/@manage/tasks/decompress")
            }}
          >
            {localStorage.getItem("lang") === "en" ? "Task Center" : "管理任务中心"}
          </Button>
          <Button size="sm" onClick={onClose}>
            {t("global.close") || "关闭"}
          </Button>
        </DrawerFooter>
      </DrawerContent>
    </Drawer>
  )
}
