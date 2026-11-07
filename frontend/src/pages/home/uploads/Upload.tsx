import {
  VStack,
  Input,
  Heading,
  HStack,
  IconButton,
  Checkbox,
  Text,
  Badge,
  Progress,
  ProgressIndicator,
  Button,
  Box,
  Stack,
} from "@hope-ui/solid"
import { createSignal, For, Show } from "solid-js"
import { usePath, useRouter, useT } from "~/hooks"
import { getMainColor, uploadConfig, setUploadConfig } from "~/store"
import {
  RiDocumentFolderUploadFill,
  RiDocumentFileUploadFill,
} from "solid-icons/ri"
import { bus, getFileSize, notify, pathJoin } from "~/utils"
import { asyncPool } from "~/utils/async_pool"
import { createStore } from "solid-js/store"
import { UploadFileProps, StatusBadge } from "./types"
import { File2Upload, traverseFileTree } from "./util"
import { SelectWrapper } from "~/components"
import { getUploads } from "./uploads"

export const [uploadFiles, setUploadFiles] = createStore<{
  uploads: UploadFileProps[]
}>({
  uploads: [],
})

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
  const fileMap = new Map<string, File>()
  const setUpload = (path: string, key: keyof UploadFileProps, value: any) => {
    setUploadFiles("uploads", (upload) => upload.path === path, key, value)
  }

  const uploaders = getUploads()
  const [curUploader, setCurUploader] = createSignal(uploaders[0])
  const asTaskUnsupported = () => curUploader()?.name === "Multipart"

  const runFileUpload = async (
    file: File,
    baseDir: string,
    uploader: ReturnType<typeof getUploads>[0],
    asTask: boolean,
    overwrite: boolean,
  ) => {
    const path = file.webkitRelativePath ? file.webkitRelativePath : file.name
    setUpload(path, "status", "uploading")
    const uploadPath = pathJoin(baseDir, path)
    try {
      const err = await uploader
        .upload(
          uploadPath,
          file,
          (key, value) => {
            setUpload(path, key, value)
          },
          asTask,
          overwrite,
          false,
        )
        .catch((err) => err)
      if (!err) {
        setUpload(path, "status", "success")
        setUpload(path, "progress", 100)
        refresh(false, true)
      } else {
        setUpload(path, "status", "error")
        setUpload(path, "msg", err.message)
        notify.error(`${path}: ${err.message}`)
      }
    } catch (e: any) {
      console.error(e)
      setUpload(path, "status", "error")
      setUpload(path, "msg", e.message)
      notify.error(`${path}: ${e.message}`)
    }
  }

  const handleAddFiles = async (files: File[]) => {
    if (files.length === 0) return
    const baseDir = pathname()
    const uploader = curUploader()
    const asTask = asTaskUnsupported() ? false : uploadConfig.asTask
    const overwrite = uploadConfig.overwrite
    for (const file of files) {
      const upload = File2Upload(file)
      fileMap.set(upload.path, file)
      setUploadFiles("uploads", (uploads) => [
        ...uploads.filter((u) => u.path !== upload.path),
        upload,
      ])
    }
    bus.emit("tool", "close_upload")
    bus.emit("tool", "tasks_refresh")
    for await (const _ of asyncPool(3, files, (f) =>
      runFileUpload(f, baseDir, uploader, asTask, overwrite),
    )) {
      // background upload pool
    }
    setUploadFiles("uploads", (uploads) =>
      uploads.filter(({ status }) => !["success", "error"].includes(status)),
    )
    bus.emit("tool", "tasks_refresh")
    refresh(false, true)
  }

  const retryFile = (path: string) => {
    const file = fileMap.get(path)
    if (!file) return
    setUpload(path, "msg", "")
    setUpload(path, "progress", 0)
    setUpload(path, "speed", 0)
    runFileUpload(
      file,
      pathname(),
      curUploader(),
      asTaskUnsupported() ? false : uploadConfig.asTask,
      uploadConfig.overwrite,
    )
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
                  onRetry={() => retryFile(upload.path)}
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
            handleAddFiles(Array.from(e.target.files ?? []))
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
            handleAddFiles(Array.from(e.target.files ?? []))
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
            <Box w={{ "@initial": "80%", "@md": "30%" }}>
              <SelectWrapper
                value={curUploader()?.name}
                onChange={(name) => {
                  setCurUploader(
                    uploaders.find((uploader) => uploader.name === name)!,
                  )
                }}
                options={uploaders.map((uploader) => {
                  return {
                    label: uploader.name,
                    value: uploader.name,
                  }
                })}
              />
            </Box>
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
            <Stack
              spacing={{ "@initial": "$2", "@md": "$4" }}
              direction={{ "@initial": "column", "@md": "row" }}
            >
              <Checkbox
                checked={!asTaskUnsupported() && uploadConfig.asTask}
                disabled={asTaskUnsupported()}
                onChange={() => {
                  setUploadConfig({ asTask: !uploadConfig.asTask })
                }}
              >
                {t("home.upload.add_as_task")}
              </Checkbox>
              <Checkbox
                checked={uploadConfig.overwrite}
                onChange={() => {
                  setUploadConfig({ overwrite: !uploadConfig.overwrite })
                }}
              >
                {t("home.conflict_policy.overwrite_existing")}
              </Checkbox>
            </Stack>
          </Show>
        </VStack>
      </Show>
    </VStack>
  )
}

export default Upload
