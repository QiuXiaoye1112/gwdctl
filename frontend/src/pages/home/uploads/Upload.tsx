import {
  VStack,
  Input,
  Heading,
  HStack,
  IconButton,
  Text,
  Badge,
  Progress,
  ProgressIndicator,
  Button,
} from "@hope-ui/solid"
import { createSignal, For, Show } from "solid-js"
import { usePath, useRouter, useT } from "~/hooks"
import { getMainColor, getSettingBool, objStore, password } from "~/store"
import {
  RiDocumentFolderUploadFill,
  RiDocumentFileUploadFill,
} from "solid-icons/ri"
import { bus, fsList, fsMkdir, getFileSize, notify, pathJoin } from "~/utils"
import { asyncPool } from "~/utils/async_pool"
import { createStore } from "solid-js/store"
import { UploadFileProps, StatusBadge, UploadOverwrite } from "./types"
import { File2Upload, traverseFileTree } from "./util"
import { MultipartUpload } from "./multipart"
import { StreamUpload } from "./stream"
import { setConflictOpen, setPendingConflict } from "./conflict"

export const [uploadFiles, setUploadFiles] = createStore<{
  uploads: UploadFileProps[]
}>({
  uploads: [],
})

const abortControllers = new Map<string, AbortController>()
const canceledPaths = new Set<string>()
const canceledBatches = new Set<string>()
const fileMap = new Map<
  string,
  { file: File; baseDir: string; overwrite: UploadOverwrite }
>()
let uploadBatchSeq = 0

const uploadKeyOf = (u: UploadFileProps) => u.fullPath || u.path

export const cancelUpload = (key: string) => {
  canceledPaths.add(key)
  const controller = abortControllers.get(key)
  if (controller) {
    controller.abort()
    abortControllers.delete(key)
  }
  setUploadFiles("uploads", (uploads) =>
    uploads.filter((u) => uploadKeyOf(u) !== key),
  )
  bus.emit("tool", "tasks_refresh")
  notify.info(
    localStorage.getItem("lang") === "en" ? "Upload canceled" : "已取消上传",
  )
}

export const cancelUploadBatch = (batchId: string) => {
  canceledBatches.add(batchId)
  const targets = uploadFiles.uploads.filter(
    (u) => (u.batchId || "__default__") === batchId,
  )
  const activeCount = targets.filter(
    (u) => !["success", "error"].includes(u.status),
  ).length
  for (const u of targets) {
    const key = uploadKeyOf(u)
    canceledPaths.add(key)
    const controller = abortControllers.get(key)
    if (controller) {
      controller.abort()
      abortControllers.delete(key)
    }
  }
  setUploadFiles("uploads", (uploads) =>
    uploads.filter((u) => (u.batchId || "__default__") !== batchId),
  )
  bus.emit("tool", "tasks_refresh")
  if (activeCount > 0) {
    notify.info(
      localStorage.getItem("lang") === "en"
        ? `Canceled ${activeCount} uploads`
        : `已取消 ${activeCount} 个上传任务`,
    )
  }
}

export const cancelAllUploads = () => {
  const activeUploads = uploadFiles.uploads.filter(
    (u) => !["success", "error"].includes(u.status),
  )
  const activeKeys = activeUploads.map((u) => uploadKeyOf(u))
  if (activeKeys.length === 0) return
  for (const u of activeUploads) {
    if (u.batchId) {
      canceledBatches.add(u.batchId)
    }
  }
  for (const key of activeKeys) {
    canceledPaths.add(key)
    const controller = abortControllers.get(key)
    if (controller) {
      controller.abort()
      abortControllers.delete(key)
    }
  }
  setUploadFiles("uploads", [])
  bus.emit("tool", "tasks_refresh")
  notify.info(
    localStorage.getItem("lang") === "en"
      ? `Canceled ${activeKeys.length} uploads`
      : `已取消 ${activeKeys.length} 个上传任务`,
  )
}

const UploadFile = (props: UploadFileProps & { onRetry?: () => void }) => {
  const t = useT()
  return (
    <VStack
      w="$full"
      spacing="$1"
      rounded="$lg"
      border="1px solid $neutral7"
      alignItems="start"
      p="$2"
      _hover={{
        border: `1px solid ${getMainColor()}`,
      }}
    >
      <Text
        css={{
          wordBreak: "break-all",
        }}
      >
        {props.path}
      </Text>
      <HStack spacing="$2" w="$full" justifyContent="space-between">
        <HStack spacing="$2">
          <Badge colorScheme={StatusBadge[props.status]}>
            {t(`home.upload.${props.status}`)}
          </Badge>
          <Text>{getFileSize(props.speed)}/s</Text>
        </HStack>
        <HStack spacing="$2">
          <Show when={props.status === "error" && props.onRetry}>
            <Button
              compact
              size="xs"
              colorScheme="accent"
              onClick={() => props.onRetry?.()}
            >
              {t("home.upload.retry")}
            </Button>
          </Show>
          <Show
            when={["uploading", "backending", "pending"].includes(props.status)}
          >
            <Button
              compact
              size="xs"
              colorScheme="danger"
              variant="subtle"
              onClick={() => cancelUpload(uploadKeyOf(props))}
            >
              {localStorage.getItem("lang") === "en" ? "Cancel" : "取消"}
            </Button>
          </Show>
          <Text color="$neutral11">{getFileSize(props.size)}</Text>
        </HStack>
      </HStack>
      <Progress
        w="$full"
        trackColor="$info3"
        rounded="$full"
        value={props.progress}
        size="sm"
      >
        <ProgressIndicator color={getMainColor()} rounded="$md" />
      </Progress>
      <Text color="$danger10">{props.msg}</Text>
    </VStack>
  )
}

const UPLOAD_CONCURRENCY = 4

const getRelPath = (file: File) => file.webkitRelativePath || file.name

const deriveUploadBatchLabel = (files: File[]): string => {
  if (files.length === 1) {
    return getRelPath(files[0])
  }
  let sharedTopDir: string | null = null
  for (const file of files) {
    const rel = getRelPath(file).replace(/\\/g, "/").replace(/^\/+/, "")
    const slashIdx = rel.indexOf("/")
    if (slashIdx <= 0) {
      return ""
    }
    const topDir = rel.slice(0, slashIdx)
    if (sharedTopDir === null) {
      sharedTopDir = topDir
    } else if (sharedTopDir !== topDir) {
      return ""
    }
  }
  return sharedTopDir || ""
}

const Upload = () => {
  const t = useT()
  const { pathname } = useRouter()
  const { refresh } = usePath()
  const [drag, setDrag] = createSignal(false)
  const [uploading, setUploading] = createSignal(false)
  const allDone = () => {
    return uploadFiles.uploads.every(({ status }) =>
      ["success", "error"].includes(status),
    )
  }
  let fileInput!: HTMLInputElement
  let folderInput!: HTMLInputElement
  const setUpload = (
    key: string,
    field: keyof UploadFileProps,
    value: any,
    batchId?: string,
  ) => {
    setUploadFiles(
      "uploads",
      (upload) =>
        uploadKeyOf(upload) === key &&
        (!batchId || (upload.batchId || "__default__") === batchId),
      field,
      value,
    )
  }

  const runUpload = (
    uploadPath: string,
    file: File,
    overwrite: UploadOverwrite,
    signal: AbortSignal,
    batchId?: string,
  ) => {
    const uploader = getSettingBool("multipart_enabled")
      ? MultipartUpload
      : StreamUpload
    return uploader(
      uploadPath,
      file,
      (key, value) => {
        setUpload(uploadPath, key, value, batchId)
      },
      overwrite,
      false,
      undefined,
      signal,
    )
  }

  const runFileUpload = async (
    file: File,
    baseDir: string,
    overwrite: UploadOverwrite,
    batchId?: string,
    ensureDir?: (relDir: string) => Promise<void>,
  ): Promise<boolean> => {
    const relPath = getRelPath(file)
    const uploadPath = pathJoin(baseDir, relPath)
    if ((batchId && canceledBatches.has(batchId)) || canceledPaths.has(uploadPath)) {
      canceledPaths.delete(uploadPath)
      return false
    }
    setUpload(uploadPath, "status", "uploading", batchId)
    if (ensureDir) {
      const normRel = relPath.replace(/\\/g, "/")
      const lastSlash = normRel.lastIndexOf("/")
      if (lastSlash > 0) {
        await ensureDir(normRel.slice(0, lastSlash))
        if ((batchId && canceledBatches.has(batchId)) || canceledPaths.has(uploadPath)) {
          canceledPaths.delete(uploadPath)
          return false
        }
      }
    }
    const controller = new AbortController()
    abortControllers.set(uploadPath, controller)
    try {
      const err = await runUpload(
        uploadPath,
        file,
        overwrite,
        controller.signal,
        batchId,
      ).catch((err) => err)
      if (
        controller.signal.aborted ||
        (batchId && canceledBatches.has(batchId)) ||
        canceledPaths.has(uploadPath)
      ) {
        canceledPaths.delete(uploadPath)
        return false
      }
      if (!err) {
        setUpload(uploadPath, "status", "success", batchId)
        setUpload(uploadPath, "progress", 100, batchId)
        setUpload(uploadPath, "speed", 0, batchId)
        return true
      } else {
        setUpload(uploadPath, "status", "error", batchId)
        setUpload(uploadPath, "speed", 0, batchId)
        setUpload(uploadPath, "msg", err.message, batchId)
        notify.error(`${relPath}: ${err.message}`)
        return false
      }
    } catch (e: any) {
      if (
        controller.signal.aborted ||
        (batchId && canceledBatches.has(batchId)) ||
        canceledPaths.has(uploadPath)
      ) {
        canceledPaths.delete(uploadPath)
        return false
      }
      console.error(e)
      setUpload(uploadPath, "status", "error", batchId)
      setUpload(uploadPath, "speed", 0, batchId)
      setUpload(uploadPath, "msg", e.message, batchId)
      notify.error(`${relPath}: ${e.message}`)
      return false
    } finally {
      if (abortControllers.get(uploadPath) === controller) {
        abortControllers.delete(uploadPath)
      }
    }
  }

  const startBatchUpload = async (
    files: File[],
    baseDir: string,
    overwrite: UploadOverwrite,
  ) => {
    if (files.length === 0) return
    const batchId = `upload-${Date.now()}-${++uploadBatchSeq}`
    const batchLabel = deriveUploadBatchLabel(files)
    const batchTotal = files.length

    const newUploads: UploadFileProps[] = []
    const newKeys = new Set<string>()
    for (const file of files) {
      const upload = File2Upload(file)
      const fullPath = pathJoin(baseDir, upload.path)
      upload.fullPath = fullPath
      upload.batchId = batchId
      upload.batchLabel = batchLabel
      upload.batchTotal = batchTotal

      const existingController = abortControllers.get(fullPath)
      if (existingController) {
        existingController.abort()
        abortControllers.delete(fullPath)
      }
      canceledPaths.delete(fullPath)
      fileMap.set(fullPath, { file, baseDir, overwrite })
      if (newKeys.has(fullPath)) {
        const idx = newUploads.findIndex((u) => uploadKeyOf(u) === fullPath)
        if (idx !== -1) newUploads.splice(idx, 1)
      }
      newKeys.add(fullPath)
      newUploads.push(upload)
    }
    setUploadFiles("uploads", (uploads) => [
      ...uploads.filter((u) => !newKeys.has(uploadKeyOf(u))),
      ...newUploads,
    ])
    bus.emit("tool", "close_upload")
    bus.emit("tool", "tasks_refresh")

    try {
      const dirPromises = new Map<string, Promise<void>>()
      const ensureDir = (relDir: string): Promise<void> => {
        if (!relDir || canceledBatches.has(batchId)) {
          return Promise.resolve()
        }
        const existing = dirPromises.get(relDir)
        if (existing) {
          return existing
        }
        const promise = (async () => {
          const lastSlash = relDir.lastIndexOf("/")
          if (lastSlash > 0) {
            await ensureDir(relDir.slice(0, lastSlash))
          }
          if (canceledBatches.has(batchId)) return
          try {
            await fsMkdir(pathJoin(baseDir, relDir))
          } catch {
            // Ignore; individual file puts still call MakeDir as fallback.
          }
        })()
        dirPromises.set(relDir, promise)
        return promise
      }

      let anySucceeded = false
      for await (const ok of asyncPool(UPLOAD_CONCURRENCY, files, (f) =>
        runFileUpload(f, baseDir, overwrite, batchId, ensureDir),
      )) {
        if (ok) anySucceeded = true
      }
      const batchDone = uploadFiles.uploads
        .filter((u) => (u.batchId || "__default__") === batchId)
        .every(({ status }) => ["success", "error"].includes(status))
      if (batchDone) {
        setUploadFiles("uploads", (uploads) =>
          uploads.filter((u) => (u.batchId || "__default__") !== batchId),
        )
      }
      bus.emit("tool", "tasks_refresh")
      if (anySucceeded && pathname() === baseDir) {
        refresh(false, true)
      }
    } finally {
      canceledBatches.delete(batchId)
    }
  }

  const handleAddFiles = async (files: File[]) => {
    if (files.length === 0) return
    const baseDir = pathname()
    const existingNames = new Set(objStore.objs.map((o) => o.name))
    if (objStore.total > objStore.objs.length) {
      try {
        const resp = await fsList(baseDir, password(), 1, 0, false)
        if (resp.code === 200 && Array.isArray(resp.data?.content)) {
          for (const item of resp.data.content) {
            existingNames.add(item.name)
          }
        }
      } catch {
        // Fallback silently to loaded objStore.objs if listing fails.
      }
    }
    const basePrefix =
      baseDir === "/" ? "/" : baseDir.replace(/\/+$/, "") + "/"
    for (const u of uploadFiles.uploads) {
      if (u.status === "error") continue
      const key = uploadKeyOf(u).replace(/\\/g, "/")
      if (!key.startsWith(basePrefix)) continue
      const rel = key.slice(basePrefix.length)
      if (!rel) continue
      const slashIdx = rel.indexOf("/")
      const topName = slashIdx > 0 ? rel.slice(0, slashIdx) : rel
      if (topName) {
        existingNames.add(topName)
      }
    }
    const conflicts: string[] = []
    const seenConflicts = new Set<string>()
    const directFileConflicts = new Set<string>()

    for (const file of files) {
      const rel = getRelPath(file).replace(/\\/g, "/").replace(/^\/+/, "")
      if (!rel) continue
      const slashIdx = rel.indexOf("/")
      const isDirEntry = slashIdx > 0
      const topName = isDirEntry ? rel.slice(0, slashIdx) : rel
      if (!existingNames.has(topName)) continue
      const label = isDirEntry ? `${topName}/` : topName
      if (!seenConflicts.has(label)) {
        seenConflicts.add(label)
        conflicts.push(label)
      }
      if (!isDirEntry) {
        directFileConflicts.add(getRelPath(file))
      }
    }

    if (conflicts.length === 0) {
      await startBatchUpload(files, baseDir, "skip")
    } else {
      bus.emit("tool", "close_upload")
      setPendingConflict({
        baseDir,
        files,
        conflicts,
        directFileConflicts,
        onConfirm: (toUpload, dir, ov) =>
          void startBatchUpload(toUpload, dir, ov),
      })
      setConflictOpen(true)
    }
  }

  const retryFile = async (key: string) => {
    const item = fileMap.get(key)
    if (!item) return
    canceledPaths.delete(key)
    setUpload(key, "msg", "")
    setUpload(key, "progress", 0)
    setUpload(key, "speed", 0)
    const ok = await runFileUpload(item.file, item.baseDir, item.overwrite)
    if (ok && pathname() === item.baseDir) {
      refresh(false, true)
    }
  }
  return (
    <VStack w="$full" pb="$2" spacing="$2">
      <Show
        when={!uploading()}
        fallback={
          <>
            <HStack spacing="$2">
              <Button
                colorScheme="accent"
                onClick={() => {
                  setUploadFiles("uploads", (_uploads) =>
                    _uploads.filter(
                      ({ status }) => !["success", "error"].includes(status),
                    ),
                  )
                }}
              >
                {t("home.upload.clear_done")}
              </Button>
              <Show when={allDone()}>
                <Button
                  onClick={() => {
                    setUploading(false)
                  }}
                >
                  {t("home.upload.back")}
                </Button>
              </Show>
            </HStack>
            <For each={uploadFiles.uploads}>
              {(upload) => (
                <UploadFile
                  {...upload}
                  onRetry={() => retryFile(uploadKeyOf(upload))}
                />
              )}
            </For>
          </>
        }
      >
        <Input
          type="file"
          multiple
          ref={fileInput}
          display="none"
          onChange={(e) => {
            // @ts-ignore
            const selected = Array.from(e.target.files ?? [])
            e.currentTarget.value = ""
            handleAddFiles(selected)
          }}
        />
        <Input
          type="file"
          multiple
          // @ts-ignore
          webkitdirectory
          ref={folderInput}
          display="none"
          onChange={(e) => {
            // @ts-ignore
            const selected = Array.from(e.target.files ?? [])
            e.currentTarget.value = ""
            handleAddFiles(selected)
          }}
        />
        <VStack
          w="$full"
          justifyContent="center"
          border={`2px dashed ${drag() ? getMainColor() : "$neutral8"}`}
          rounded="$lg"
          spacing="$4"
          p="$6"
          minH="$56"
          onDragOver={(e: DragEvent) => {
            e.preventDefault()
            setDrag(true)
          }}
          onDragLeave={() => {
            setDrag(false)
          }}
          onDrop={async (e: DragEvent) => {
            e.preventDefault()
            e.stopPropagation()
            setDrag(false)
            const res: File[] = []
            const items = Array.from(e.dataTransfer?.items ?? [])
            const files = Array.from(e.dataTransfer?.files ?? [])
            let itemLength = items.length
            const folderEntries = []
            for (let i = 0; i < itemLength; i++) {
              const item = items[i]
              const entry = item.webkitGetAsEntry()
              if (entry?.isFile) {
                res.push(files[i])
              } else if (entry?.isDirectory) {
                folderEntries.push(entry)
              }
            }
            for (const entry of folderEntries) {
              const innerFiles = await traverseFileTree(entry)
              res.push(...innerFiles)
            }
            if (res.length === 0) {
              notify.warning(t("home.upload.no_files_drag"))
            }
            handleAddFiles(res)
          }}
        >
          <Show
            when={!drag()}
            fallback={<Heading>{t("home.upload.release")}</Heading>}
          >
            <Heading size="lg" textAlign="center">
              {t("home.upload.upload-tips")}
            </Heading>
            <HStack spacing="$4">
              <VStack spacing="$2" alignItems="center">
                <IconButton
                  compact
                  size="xl"
                  aria-label={t("home.upload.upload_folder")}
                  colorScheme="accent"
                  icon={<RiDocumentFolderUploadFill size="1.2em" />}
                  onClick={() => {
                    folderInput.click()
                  }}
                />
                <Text fontSize="$sm" color="$neutral11" textAlign="center">
                  {t("home.upload.upload_folder")}
                </Text>
              </VStack>

              <VStack spacing="$2" alignItems="center">
                <IconButton
                  compact
                  size="xl"
                  aria-label={t("home.upload.upload_files")}
                  icon={<RiDocumentFileUploadFill size="1.2em" />}
                  onClick={() => {
                    fileInput.click()
                  }}
                />
                <Text fontSize="$sm" color="$neutral11" textAlign="center">
                  {t("home.upload.upload_files")}
                </Text>
              </VStack>
            </HStack>
          </Show>
        </VStack>
      </Show>
    </VStack>
  )
}

export default Upload
