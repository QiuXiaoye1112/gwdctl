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
import {
  batch,
  createEffect,
  createMemo,
  createSignal,
  For,
  onCleanup,
  onMount,
  Show,
} from "solid-js"
import { OcWorkflow2 } from "solid-icons/oc"
import { RiSystemRefreshLine } from "solid-icons/ri"
import { IoClose } from "solid-icons/io"
import { useT, useRouter, usePath } from "~/hooks"
import { bus, getFileSize, notify, r } from "~/utils"
import { TaskInfo, Resp } from "~/types"
import { me } from "~/store"
import { UserMethods } from "~/types"
import { uploadFiles, cancelUpload, cancelUploadBatch } from "../uploads/Upload"

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
  { type: "copy", labelZh: "复制", labelEn: "Copy", color: "primary" },
  { type: "move", labelZh: "移动", labelEn: "Move", color: "warning" },
]

function pathBaseName(p: string): string {
  const cleaned = p.replace(/\/+$/, "")
  const parts = cleaned.split("/").filter(Boolean)
  return parts[parts.length - 1] || p
}

function formatTaskName(name: string): string {
  if (!name) return ""
  // match upload [device](path) or upload <filename with spaces> to [dstMp](dstPath)
  const uploadBracketMatch = name.match(/^upload\s+\[[^\]]*\]\(([^)]+)\)/i)
  if (uploadBracketMatch) return pathBaseName(uploadBracketMatch[1])
  const uploadToMatch = name.match(/^upload\s+(.+?)\s+to\s+\[/i)
  if (uploadToMatch) return pathBaseName(uploadToMatch[1])
  const uploadFallbackMatch = name.match(/^upload\s+([^\s]+)/i)
  if (uploadFallbackMatch) return pathBaseName(uploadFallbackMatch[1])
  // match copy / move / merge [srcMp](srcPath) to [dstMp](dstPath)
  const copyMatch = name.match(/(?:copy|move|merge)\s+\[[^\]]*\]\(([^)]+)\)/i)
  if (copyMatch) return pathBaseName(copyMatch[1])
  return name
}

function parseTransferGroup(
  task: RunningTaskItem,
): { key: string; batchId?: string; label: string; dstPath?: string } | null {
  if (task.batch_id) {
    return {
      key: `${task.taskType}|batch|${task.batch_id}`,
      batchId: task.batch_id,
      label: task.batch_label || formatTaskName(task.name),
    }
  }
  let match: RegExpMatchArray | null = null
  if (task.taskType === "copy" || task.taskType === "move") {
    match = task.name.match(
      /(?:copy|move|merge)\s+\[[^\]]*\]\([^)]+\)\s+to\s+\[([^\]]*)\]\(([^)]+)\)/i,
    )
  }
  if (!match) return null
  const dstMount = match[1] || "/"
  const dstPath = match[2].replace(/\/+$/, "") || "/"
  const key = `${task.taskType}|${dstMount}|${dstPath}`
  const label = dstPath === "/" ? dstMount : pathBaseName(dstPath)
  return { key, label, dstPath }
}

interface TaskGroupEntry {
  kind: "single" | "group"
  key: string
  batchId?: string
  taskType: string
  typeLabel: string
  typeColor: RunningTaskItem["typeColor"]
  label: string
  dstPath?: string
  tasks: RunningTaskItem[]
}

export const TasksDrawer = () => {
  const t = useT()
  const { to } = useRouter()
  const { refresh } = usePath()
  const { isOpen, onOpen, onClose } = createDisclosure()
  const [tasks, setTasks] = createSignal<RunningTaskItem[]>([])
  const [groupTotals, setGroupTotals] = createSignal<Record<string, number>>({})
  const [stickyMultiKeys, setStickyMultiKeys] = createSignal<
    Record<string, boolean>
  >({})
  const [expandedGroups, setExpandedGroups] = createSignal<
    Record<string, boolean>
  >({})
  const [uploadsExpanded, setUploadsExpanded] = createSignal<
    Record<string, boolean>
  >({})
  const [loading, setLoading] = createSignal(false)
  const [cancelingId, setCancelingId] = createSignal<string>("")
  const [cancelingGroupKey, setCancelingGroupKey] = createSignal<string>("")
  const prevCountByCategory: Record<string, number> = {}
  const prevGroupCounts: Record<string, number> = {}
  const batchSeenIds = new Map<string, Set<string>>()
  const stickyMultiSet = new Set<string>()
  let fetchingTasks = false
  let refetchQueued = false
  const trackedTaskIds: Record<"copy" | "move", Set<string>> = {
    copy: new Set(),
    move: new Set(),
  }
  const trackedBatchIds: Record<"copy" | "move", Set<string>> = {
    copy: new Set(),
    move: new Set(),
  }
  const notifiedFailedIds = new Set<string>()

  const isEn = () => localStorage.getItem("lang") === "en"

  const groupedEntries = createMemo<TaskGroupEntry[]>(() => {
    const all = tasks()
    const sticky = stickyMultiKeys()
    const groupMap = new Map<string, TaskGroupEntry>()
    const entries: TaskGroupEntry[] = []

    for (const item of all) {
      const parsed = parseTransferGroup(item)
      if (!parsed) {
        entries.push({
          kind: "single",
          key: item.id,
          taskType: item.taskType,
          typeLabel: item.typeLabel,
          typeColor: item.typeColor,
          label: formatTaskName(item.name),
          tasks: [item],
        })
        continue
      }
      let existing = groupMap.get(parsed.key)
      if (!existing) {
        existing = {
          kind: sticky[parsed.key] ? "group" : "single",
          key: parsed.key,
          batchId: parsed.batchId,
          taskType: item.taskType,
          typeLabel: item.typeLabel,
          typeColor: item.typeColor,
          label: parsed.label,
          dstPath: parsed.dstPath,
          tasks: [item],
        }
        groupMap.set(parsed.key, existing)
        entries.push(existing)
      } else {
        existing.tasks.push(item)
        existing.kind = "group"
      }
    }

    return entries
  })

  const notifyFailedTrackedTasks = async (
    taskType: "copy" | "move",
    finishedIds: Set<string>,
    finishedBatchIds: Set<string>,
  ) => {
    try {
      const doneRes = (await r.get(`/task/${taskType}/done`, {
        skipActivity: true,
      } as any)) as Resp<TaskInfo[]>
      if (doneRes.code !== 200 || !Array.isArray(doneRes.data)) return
      for (const item of doneRes.data) {
        if (item.state !== 7 || notifiedFailedIds.has(item.id)) continue
        const matchedById = finishedIds.has(item.id)
        const matchedByBatch = Boolean(
          item.batch_id && finishedBatchIds.has(item.batch_id),
        )
        if (!matchedById && !matchedByBatch) continue
        notifiedFailedIds.add(item.id)
        const label = formatTaskName(item.name)
        const detail = item.error || item.status || "Task failed"
        notify.error(label ? `${label}: ${detail}` : detail)
      }
    } catch {
      // ignore notification lookup failure
    }
  }

  const activeUploadBatches = createMemo(() => {
    const grouped = new Map<
      string,
      { id: string; label: string; total: number; items: typeof uploadFiles.uploads }
    >()
    for (const item of uploadFiles.uploads) {
      const batchId = item.batchId || "__default__"
      let entry = grouped.get(batchId)
      if (!entry) {
        entry = {
          id: batchId,
          label: item.batchLabel || "",
          total: item.batchTotal ?? 0,
          items: [],
        }
        grouped.set(batchId, entry)
      } else if (!entry.label && item.batchLabel) {
        entry.label = item.batchLabel
      }
      entry.items.push(item)
    }
    return Array.from(grouped.values()).filter((b) =>
      b.items.some((u) => !["success", "error"].includes(u.status)),
    )
  })

  const totalRunningCount = () =>
    activeUploadBatches().length + groupedEntries().length

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
      const prevTasks = tasks()
      const succeededCategories = new Set<string>()
      const runningIdsByType: Record<string, Set<string>> = {}
      const results = await Promise.all(
        TASK_CATEGORIES.map(async (cat) => {
          try {
            const res = (await r.get(`/task/${cat.type}/undone`, {
              skipActivity: true,
            } as any)) as Resp<TaskInfo[]>
            if (res.code === 200 && Array.isArray(res.data)) {
              succeededCategories.add(cat.type)
              runningIdsByType[cat.type] = new Set(res.data.map((item) => item.id))
              return res.data.map((item: TaskInfo) => ({
                ...item,
                taskType: cat.type,
                typeLabel: isEn() ? cat.labelEn : cat.labelZh,
                typeColor: cat.color,
              }))
            }
          } catch {
            // ignore network/auth errors for specific categories
          }
          // Preserve previous items on transient poll failure so cards do not flicker.
          return prevTasks.filter((item) => item.taskType === cat.type)
        }),
      )
      const allRunning = results.flat()
      let shouldRefresh = false

      // Automatically track child tasks belonging to tracked batches so nested failures notify cleanly.
      const activeBatchIdsByType: Record<"copy" | "move", Set<string>> = {
        copy: new Set(),
        move: new Set(),
      }
      const finishedBatchIdsByType: Record<"copy" | "move", Set<string>> = {
        copy: new Set(),
        move: new Set(),
      }
      for (const item of allRunning) {
        if (item.batch_id && (item.taskType === "copy" || item.taskType === "move")) {
          activeBatchIdsByType[item.taskType].add(item.batch_id)
          if (trackedBatchIds[item.taskType].has(item.batch_id)) {
            trackedTaskIds[item.taskType].add(item.id)
          }
        }
      }
      for (const trackedType of ["copy", "move"] as const) {
        if (!succeededCategories.has(trackedType)) continue
        for (const bid of trackedBatchIds[trackedType]) {
          if (!activeBatchIdsByType[trackedType].has(bid)) {
            trackedBatchIds[trackedType].delete(bid)
            finishedBatchIdsByType[trackedType].add(bid)
          }
        }
      }

      // Only refresh once a category's running tasks completely drain to 0,
      // preventing a refresh storm when a folder copy spawns hundreds of sub-tasks.
      for (const cat of TASK_CATEGORIES) {
        if (!succeededCategories.has(cat.type)) continue
        const prev = prevCountByCategory[cat.type] ?? 0
        const curr = runningIdsByType[cat.type]?.size ?? 0
        if (prev > 0 && curr === 0) {
          shouldRefresh = true
        }
        prevCountByCategory[cat.type] = curr
      }

      for (const trackedType of ["copy", "move"] as const) {
        if (!succeededCategories.has(trackedType)) {
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
          if (stillRunning.size === 0) {
            shouldRefresh = true
          }
        }
        if (
          finishedIds.size > 0 ||
          finishedBatchIdsByType[trackedType].size > 0
        ) {
          void notifyFailedTrackedTasks(
            trackedType,
            finishedIds,
            finishedBatchIdsByType[trackedType],
          )
        }
      }

      // Track seen task IDs per batch so each operation stays a single stable card until completion.
      const currentGroupCounts: Record<string, number> = {}
      const succeededSet = succeededCategories
      for (const item of allRunning) {
        const parsed = parseTransferGroup(item)
        if (!parsed) continue
        currentGroupCounts[parsed.key] = (currentGroupCounts[parsed.key] ?? 0) + 1
        let seen = batchSeenIds.get(parsed.key)
        if (!seen) {
          seen = new Set<string>()
          batchSeenIds.set(parsed.key, seen)
        }
        seen.add(item.id)
        if (seen.size >= 2) {
          stickyMultiSet.add(parsed.key)
        }
      }
      for (const prevKey of Object.keys(prevGroupCounts)) {
        const keyType = prevKey.split("|")[0]
        if (!succeededSet.has(keyType)) continue
        if (!currentGroupCounts[prevKey]) {
          delete prevGroupCounts[prevKey]
          batchSeenIds.delete(prevKey)
          stickyMultiSet.delete(prevKey)
          shouldRefresh = true
        }
      }
      for (const [key, count] of Object.entries(currentGroupCounts)) {
        prevGroupCounts[key] = count
      }

      const nextTotals: Record<string, number> = {}
      for (const [key, seen] of batchSeenIds.entries()) {
        nextTotals[key] = seen.size
      }
      const nextSticky: Record<string, boolean> = {}
      for (const key of stickyMultiSet) {
        nextSticky[key] = true
      }

      if (shouldRefresh) {
        refresh(false, true)
      }
      batch(() => {
        setGroupTotals(nextTotals)
        setStickyMultiKeys(nextSticky)
        setTasks(allRunning)
        setExpandedGroups((prev) => {
          let changed = false
          const next: Record<string, boolean> = {}
          for (const [key, expanded] of Object.entries(prev)) {
            if ((currentGroupCounts[key] ?? 0) > 0 && expanded) {
              next[key] = true
            } else if (expanded) {
              changed = true
            }
          }
          return changed ? next : prev
        })
      })
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

  const cancelTaskGroup = async (entry: TaskGroupEntry) => {
    setCancelingGroupKey(entry.key)
    try {
      if (entry.batchId) {
        const res = (await r.post(
          `/task/${entry.taskType}/cancel_batch?batch_id=${encodeURIComponent(entry.batchId)}`,
        )) as Resp<{ canceled?: number }>
        if (res.code === 200) {
          const canceledCount = res.data?.canceled ?? entry.tasks.length
          notify.success(
            isEn()
              ? `Canceled ${canceledCount} tasks`
              : `已取消 ${canceledCount} 个任务`,
          )
          await fetchRunningTasks()
        } else {
          notify.error(res.message)
        }
        return
      }
      const ids = entry.tasks.map((item) => item.id)
      const res = (await r.post(
        `/task/${entry.taskType}/cancel_some`,
        ids,
      )) as Resp<Record<string, string>>
      if (res.code === 200) {
        notify.success(
          isEn()
            ? `Canceled ${ids.length} tasks`
            : `已取消 ${ids.length} 个任务`,
        )
        await fetchRunningTasks()
      } else {
        notify.error(res.message)
      }
    } catch (e: any) {
      notify.error(e.message || "Failed to cancel")
    } finally {
      setCancelingGroupKey("")
    }
  }

  const toggleGroupExpand = (key: string) => {
    setExpandedGroups((prev) => ({
      ...prev,
      [key]: !prev[key],
    }))
  }

  const toggleUploadExpand = (batchId: string) => {
    setUploadsExpanded((prev) => ({
      ...prev,
      [batchId]: !prev[batchId],
    }))
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
    batchIds,
  }: {
    type: "copy" | "move"
    ids: string[]
    batchIds?: string[]
  }) => {
    for (const id of ids) {
      if (id) {
        trackedTaskIds[type].add(id)
      }
    }
    for (const bid of batchIds ?? []) {
      if (bid) {
        trackedBatchIds[type].add(bid)
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
                <For each={activeUploadBatches()}>
                  {(uploadBatch) => {
                    const activeItems = () =>
                      uploadBatch.items.filter(
                        (u) => !["success", "error"].includes(u.status),
                      )
                    const batchNonError = () =>
                      uploadBatch.items.filter((u) => u.status !== "error")
                    const isSingle = () =>
                      (uploadBatch.total || uploadBatch.items.length) === 1 &&
                      activeItems().length === 1
                    return (
                      <Show
                        when={!isSingle()}
                        fallback={(() => {
                          const up = activeItems()[0]
                          if (!up) return null
                          return (
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
                                    {isEn() ? "Upload" : "上传"}
                                  </Badge>
                                  <Text
                                    fontWeight="$semibold"
                                    fontSize="$sm"
                                    noOfLines={1}
                                    title={up.fullPath || up.path}
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
                                    color={
                                      up.progress >= 100
                                        ? "$success9"
                                        : "$info9"
                                    }
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
                                  title={up.fullPath || up.path}
                                >
                                  {getFileSize(up.size)}
                                </Text>
                                <Button
                                  size="xs"
                                  colorScheme="danger"
                                  variant="subtle"
                                  onClick={() =>
                                    cancelUpload(up.fullPath || up.path)
                                  }
                                >
                                  {isEn() ? "Cancel" : "取消"}
                                </Button>
                              </HStack>
                            </Box>
                          )
                        })()}
                      >
                        {(() => {
                          const totalSpeed = () =>
                            activeItems().reduce(
                              (acc, u) =>
                                acc + (Number.isFinite(u.speed) ? u.speed : 0),
                              0,
                            )
                          const totalBatchCount = () =>
                            Math.max(
                              uploadBatch.total || 0,
                              batchNonError().length,
                            )
                          const completedBatchCount = () =>
                            Math.max(
                              0,
                              totalBatchCount() - activeItems().length,
                            )
                          const overallUploadProgress = () => {
                            const list = batchNonError()
                            if (list.length === 0) return 0
                            const totalBytes = list.reduce(
                              (acc, u) => acc + (u.size > 0 ? u.size : 0),
                              0,
                            )
                            if (totalBytes > 0) {
                              const loaded = list.reduce((acc, u) => {
                                const size = u.size > 0 ? u.size : 0
                                const pct =
                                  u.status === "success"
                                    ? 100
                                    : Number.isFinite(u.progress)
                                      ? u.progress
                                      : 0
                                return acc + (size * pct) / 100
                              }, 0)
                              return Math.min(100, (loaded / totalBytes) * 100)
                            }
                            const sum = list.reduce((acc, u) => {
                              const pct =
                                u.status === "success"
                                  ? 100
                                  : Number.isFinite(u.progress)
                                    ? u.progress
                                    : 0
                              return acc + pct
                            }, 0)
                            return Math.min(100, sum / list.length)
                          }
                          const expanded = () =>
                            !!uploadsExpanded()[uploadBatch.id]
                          const titleText = () => {
                            const countText =
                              completedBatchCount() > 0
                                ? `${completedBatchCount()}/${totalBatchCount()}`
                                : isEn()
                                  ? `${totalBatchCount()} files`
                                  : `${totalBatchCount()} 项`
                            if (uploadBatch.label) {
                              return `${uploadBatch.label} (${countText})`
                            }
                            return isEn()
                              ? `Uploading (${countText})`
                              : `批量上传 (${countText})`
                          }
                          return (
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
                                <HStack spacing="$2" overflow="hidden" maxW="65%">
                                  <Badge colorScheme="info" flexShrink={0}>
                                    {isEn() ? "Upload" : "上传"}
                                  </Badge>
                                  <Text
                                    fontWeight="$semibold"
                                    fontSize="$sm"
                                    noOfLines={1}
                                    title={uploadBatch.label || titleText()}
                                  >
                                    {titleText()}
                                  </Text>
                                </HStack>
                                <Badge colorScheme="success" flexShrink={0}>
                                  {getFileSize(totalSpeed())}/s
                                </Badge>
                              </HStack>
                              <HStack spacing="$2" mb="$2">
                                <Progress
                                  value={overallUploadProgress()}
                                  w="$full"
                                  size="sm"
                                  rounded="$full"
                                >
                                  <ProgressIndicator
                                    color={
                                      overallUploadProgress() >= 100
                                        ? "$success9"
                                        : "$info9"
                                    }
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
                                  {overallUploadProgress().toFixed(0)}%
                                </Text>
                              </HStack>
                              <HStack
                                justifyContent="space-between"
                                alignItems="center"
                                spacing="$2"
                              >
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  colorScheme="neutral"
                                  px="$1"
                                  onClick={() =>
                                    toggleUploadExpand(uploadBatch.id)
                                  }
                                >
                                  {expanded()
                                    ? isEn()
                                      ? "Hide details"
                                      : "收起明细"
                                    : isEn()
                                      ? `Details (${activeItems().length} left)`
                                      : `展开明细 (剩余 ${activeItems().length})`}
                                </Button>
                                <Button
                                  size="xs"
                                  colorScheme="danger"
                                  variant="subtle"
                                  onClick={() =>
                                    cancelUploadBatch(uploadBatch.id)
                                  }
                                >
                                  {isEn() ? "Cancel All" : "全部取消"}
                                </Button>
                              </HStack>
                              <Show when={expanded()}>
                                <VStack
                                  spacing="$1_5"
                                  alignItems="stretch"
                                  mt="$2"
                                  pt="$2"
                                  borderTop="1px solid $neutral5"
                                  maxH="240px"
                                  overflowY="auto"
                                >
                                  <For each={activeItems()}>
                                    {(up) => (
                                      <HStack
                                        justifyContent="space-between"
                                        alignItems="center"
                                        spacing="$2"
                                        px="$1"
                                        py="$0_5"
                                        rounded="$sm"
                                        _hover={{ bgColor: "$neutral3" }}
                                      >
                                        <Text
                                          fontSize="$xs"
                                          noOfLines={1}
                                          flex="1"
                                          title={up.fullPath || up.path}
                                        >
                                          {up.path}
                                        </Text>
                                        <Text
                                          fontSize="$xs"
                                          color="$neutral9"
                                          flexShrink={0}
                                        >
                                          {up.status === "pending"
                                            ? isEn()
                                              ? "Pending"
                                              : "等待中"
                                            : `${up.progress.toFixed(0)}%`}
                                        </Text>
                                        <Button
                                          size="xs"
                                          variant="ghost"
                                          colorScheme="danger"
                                          px="$1"
                                          minW="auto"
                                          h="$5"
                                          onClick={() =>
                                            cancelUpload(up.fullPath || up.path)
                                          }
                                        >
                                          {isEn() ? "Cancel" : "取消"}
                                        </Button>
                                      </HStack>
                                    )}
                                  </For>
                                </VStack>
                              </Show>
                            </Box>
                          )
                        })()}
                      </Show>
                    )
                  }}
                </For>
                <For each={groupedEntries()}>
                  {(entry) => (
                    <Show
                      when={entry.kind === "group"}
                      fallback={(() => {
                        const task = entry.tasks[0]
                        return (
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
                                {t(`tasks.state.${task.state}`) ||
                                  (task.state === 1 ? "运行中" : "等待中")}
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
                                  color={
                                    task.progress >= 100 ? "$success9" : "$info9"
                                  }
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
                            <HStack
                              justifyContent="space-between"
                              alignItems="center"
                            >
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
                                {isEn() ? "Cancel" : "取消"}
                              </Button>
                            </HStack>
                          </Box>
                        )
                      })()}
                    >
                      {(() => {
                        const total = () =>
                          Math.max(
                            groupTotals()[entry.key] ?? entry.tasks.length,
                            entry.tasks.length,
                          )
                        const remaining = () => entry.tasks.length
                        const completed = () => total() - remaining()
                        const runningCount = () =>
                          entry.tasks.filter((item) => item.state === 1).length
                        const overallProgress = () => {
                          const tCount = total()
                          if (tCount <= 0) return 0
                          const sumProgress = entry.tasks.reduce(
                            (acc, item) =>
                              acc +
                              (Number.isFinite(item.progress)
                                ? item.progress
                                : 0),
                            0,
                          )
                          return Math.min(
                            100,
                            ((completed() * 100 + sumProgress) /
                              (tCount * 100)) *
                              100,
                          )
                        }
                        const expanded = () => !!expandedGroups()[entry.key]
                        return (
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
                              <HStack spacing="$2" overflow="hidden" maxW="68%">
                                <Badge colorScheme={entry.typeColor} flexShrink={0}>
                                  {entry.typeLabel}
                                </Badge>
                                <Text
                                  fontWeight="$semibold"
                                  fontSize="$sm"
                                  noOfLines={1}
                                  title={entry.dstPath || entry.label}
                                >
                                  {entry.label}
                                </Text>
                              </HStack>
                              <Badge
                                colorScheme={
                                  runningCount() > 0 ? "success" : "info"
                                }
                                flexShrink={0}
                              >
                                {completed() > 0
                                  ? `${completed()}/${total()}`
                                  : isEn()
                                    ? `${remaining()} items`
                                    : `${remaining()} 项`}
                              </Badge>
                            </HStack>

                            {/* Batch progress bar */}
                            <HStack spacing="$2" mb="$2">
                              <Progress
                                value={overallProgress()}
                                w="$full"
                                size="sm"
                                rounded="$full"
                              >
                                <ProgressIndicator
                                  color={
                                    overallProgress() >= 100
                                      ? "$success9"
                                      : "$info9"
                                  }
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
                                {overallProgress().toFixed(0)}%
                              </Text>
                            </HStack>

                            {/* Destination path, expand toggle & cancel all */}
                            <HStack
                              justifyContent="space-between"
                              alignItems="center"
                              spacing="$2"
                            >
                              <Button
                                size="xs"
                                variant="ghost"
                                colorScheme="neutral"
                                px="$1"
                                onClick={() => toggleGroupExpand(entry.key)}
                              >
                                {expanded()
                                  ? isEn()
                                    ? "Hide details"
                                    : "收起明细"
                                  : isEn()
                                    ? `Details (${remaining()} left)`
                                    : `展开明细 (剩余 ${remaining()})`}
                              </Button>
                              <Button
                                size="xs"
                                colorScheme="danger"
                                variant="subtle"
                                loading={cancelingGroupKey() === entry.key}
                                onClick={() => cancelTaskGroup(entry)}
                              >
                                {isEn() ? "Cancel All" : "全部取消"}
                              </Button>
                            </HStack>

                            <Show when={expanded()}>
                              <VStack
                                spacing="$1_5"
                                alignItems="stretch"
                                mt="$2"
                                pt="$2"
                                borderTop="1px solid $neutral5"
                                maxH="240px"
                                overflowY="auto"
                              >
                                <For each={entry.tasks}>
                                  {(subTask) => (
                                    <HStack
                                      justifyContent="space-between"
                                      alignItems="center"
                                      spacing="$2"
                                      px="$1"
                                      py="$0_5"
                                      rounded="$sm"
                                      _hover={{ bgColor: "$neutral3" }}
                                    >
                                      <Text
                                        fontSize="$xs"
                                        noOfLines={1}
                                        flex="1"
                                        title={subTask.name}
                                      >
                                        {formatTaskName(subTask.name)}
                                      </Text>
                                      <Text
                                        fontSize="$xs"
                                        color="$neutral9"
                                        flexShrink={0}
                                      >
                                        {subTask.state === 1
                                          ? `${subTask.progress.toFixed(0)}%`
                                          : t(`tasks.state.${subTask.state}`) ||
                                            (isEn() ? "Pending" : "等待中")}
                                      </Text>
                                      <Button
                                        size="xs"
                                        variant="ghost"
                                        colorScheme="danger"
                                        px="$1"
                                        minW="auto"
                                        h="$5"
                                        loading={cancelingId() === subTask.id}
                                        onClick={() =>
                                          cancelTask(
                                            subTask.taskType,
                                            subTask.id,
                                          )
                                        }
                                      >
                                        {isEn() ? "Cancel" : "取消"}
                                      </Button>
                                    </HStack>
                                  )}
                                </For>
                              </VStack>
                            </Show>
                          </Box>
                        )
                      })()}
                    </Show>
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
              const firstType = tasks()[0]?.taskType
              const target = firstType === "move" ? "move" : "copy"
              to(`/@manage/tasks/${target}`)
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
