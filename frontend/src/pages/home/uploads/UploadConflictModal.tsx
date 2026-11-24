import {
  Box,
  Button,
  HStack,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  Text,
  VStack,
} from "@hope-ui/solid"
import { createEffect, createSignal, For, Show } from "solid-js"
import { useT } from "~/hooks"
import { getMainColor } from "~/store"
import { notify } from "~/utils"
import { conflictOpen, pendingConflict, setConflictOpen, UploadConflictRequest } from "./conflict"

const MAX_DISPLAY_CONFLICTS = 50
const getRelPath = (file: File) => file.webkitRelativePath || file.name
const isAllDirectFileConflicts = (req: UploadConflictRequest) =>
  req.files.length > 0 &&
  req.files.every((f) => req.directFileConflicts.has(getRelPath(f)))
const hasDirConflicts = (req: UploadConflictRequest) =>
  req.conflicts.some((c) => c.endsWith("/"))

export const UploadConflictModal = () => {
  const t = useT()
  const [conflictAction, setConflictAction] = createSignal<"skip" | "overwrite">(
    "skip",
  )

  createEffect(() => {
    if (conflictOpen()) {
      setConflictAction("skip")
    }
  })

  const replaceOnly = () => {
    const p = pendingConflict()
    return p ? isAllDirectFileConflicts(p) : false
  }

  const confirmConflictUpload = () => {
    const pending = pendingConflict()
    if (!pending) return
    const action = conflictAction()
    setConflictOpen(false)
    if (isAllDirectFileConflicts(pending)) {
      void pending.onConfirm(pending.files, pending.baseDir, true)
      return
    }
    if (action === "skip") {
      const toUpload =
        pending.directFileConflicts.size > 0
          ? pending.files.filter(
              (f) => !pending.directFileConflicts.has(getRelPath(f)),
            )
          : pending.files
      if (toUpload.length === 0) {
        notify.info(t("home.upload.all_skipped"))
        return
      }
      if (pending.directFileConflicts.size > 0) {
        notify.info(
          t("home.upload.skipped_conflicts", {
            count: pending.directFileConflicts.size,
          }),
        )
      }
      void pending.onConfirm(toUpload, pending.baseDir, "skip")
    } else {
      void pending.onConfirm(pending.files, pending.baseDir, true)
    }
  }

  return (
    <Modal
      opened={conflictOpen()}
      onClose={() => setConflictOpen(false)}
      motionPreset="scale"
      centered
      blockScrollOnMount={false}
      initialFocus="#upload-conflict-confirm"
      size={{
        "@initial": "xs",
        "@md": "md",
      }}
    >
      <ModalOverlay />
      <ModalContent>
        <ModalCloseButton />
        <ModalHeader>
          {t("home.upload.conflict_title", {
            count: pendingConflict()?.conflicts.length ?? 0,
          })}
        </ModalHeader>
        <ModalBody>
          <Show when={pendingConflict()}>
            {(conflictInfo) => {
              const visibleConflicts = () =>
                conflictInfo().conflicts.slice(0, MAX_DISPLAY_CONFLICTS)
              const extraCount = () =>
                Math.max(
                  0,
                  conflictInfo().conflicts.length - MAX_DISPLAY_CONFLICTS,
                )
              return (
                <VStack w="$full" alignItems="stretch" spacing="$3">
                  <Text fontSize="$sm" color="$neutral11">
                    {isAllDirectFileConflicts(conflictInfo())
                      ? t("home.upload.conflict_replace_desc")
                      : t("home.upload.conflict_desc")}
                  </Text>

                  <VStack
                    w="$full"
                    alignItems="start"
                    spacing="$1"
                    p="$2_5"
                    rounded="$md"
                    border="1px solid $neutral6"
                    bg="$neutral2"
                    css={{ maxHeight: "180px", overflowY: "auto" }}
                  >
                    <For each={visibleConflicts()}>
                      {(item) => (
                        <Text
                          fontSize="$xs"
                          color="$neutral12"
                          css={{ wordBreak: "break-all" }}
                        >
                          {item}
                        </Text>
                      )}
                    </For>
                    <Show when={extraCount() > 0}>
                      <Text fontSize="$xs" color="$neutral10">
                        {t("home.upload.conflict_more", {
                          count: extraCount(),
                        })}
                      </Text>
                    </Show>
                  </VStack>

                  <Show when={!isAllDirectFileConflicts(conflictInfo())}>
                    <VStack w="$full" spacing="$2" role="radiogroup">
                      <HStack
                        w="$full"
                        p="$3"
                        rounded="$md"
                        spacing="$3"
                        alignItems="start"
                        cursor="pointer"
                        role="radio"
                        tabIndex={0}
                        aria-checked={conflictAction() === "skip"}
                        border={
                          conflictAction() === "skip"
                            ? `1px solid ${getMainColor()}`
                            : "1px solid $neutral6"
                        }
                        bg={
                          conflictAction() === "skip"
                            ? "$info2"
                            : "transparent"
                        }
                        onClick={() => setConflictAction("skip")}
                        onKeyDown={(e: KeyboardEvent) => {
                          if (e.key === "Enter" || e.key === " ") {
                            e.preventDefault()
                            setConflictAction("skip")
                          }
                        }}
                      >
                        <Box
                          mt="3px"
                          w="16px"
                          h="16px"
                          rounded="$full"
                          flexShrink={0}
                          border={
                            conflictAction() === "skip"
                              ? `5px solid ${getMainColor()}`
                              : "2px solid $neutral8"
                          }
                        />
                        <VStack alignItems="start" spacing="$0_5">
                          <Text fontSize="$sm" fontWeight="$medium">
                            {hasDirConflicts(conflictInfo())
                              ? t("home.upload.conflict_folder_skip")
                              : t("home.upload.conflict_skip")}
                          </Text>
                          <Text fontSize="$xs" color="$neutral11">
                            {hasDirConflicts(conflictInfo())
                              ? t("home.upload.conflict_folder_skip_desc")
                              : t("home.upload.conflict_skip_desc")}
                          </Text>
                        </VStack>
                      </HStack>

                      <HStack
                        w="$full"
                        p="$3"
                        rounded="$md"
                        spacing="$3"
                        alignItems="start"
                        cursor="pointer"
                        role="radio"
                        tabIndex={0}
                        aria-checked={conflictAction() === "overwrite"}
                        border={
                          conflictAction() === "overwrite"
                            ? `1px solid ${getMainColor()}`
                            : "1px solid $neutral6"
                        }
                        bg={
                          conflictAction() === "overwrite"
                            ? "$info2"
                            : "transparent"
                        }
                        onClick={() => setConflictAction("overwrite")}
                        onKeyDown={(e: KeyboardEvent) => {
                          if (e.key === "Enter" || e.key === " ") {
                            e.preventDefault()
                            setConflictAction("overwrite")
                          }
                        }}
                      >
                        <Box
                          mt="3px"
                          w="16px"
                          h="16px"
                          rounded="$full"
                          flexShrink={0}
                          border={
                            conflictAction() === "overwrite"
                              ? `5px solid ${getMainColor()}`
                              : "2px solid $neutral8"
                          }
                        />
                        <VStack alignItems="start" spacing="$0_5">
                          <Text fontSize="$sm" fontWeight="$medium">
                            {hasDirConflicts(conflictInfo())
                              ? t("home.upload.conflict_folder_overwrite")
                              : t("home.upload.conflict_overwrite")}
                          </Text>
                          <Text fontSize="$xs" color="$neutral11">
                            {hasDirConflicts(conflictInfo())
                              ? t("home.upload.conflict_folder_overwrite_desc")
                              : t("home.upload.conflict_overwrite_desc")}
                          </Text>
                        </VStack>
                      </HStack>
                    </VStack>
                  </Show>
                </VStack>
              )
            }}
          </Show>
        </ModalBody>
        <ModalFooter display="flex" gap="$2">
          <Button colorScheme="neutral" onClick={() => setConflictOpen(false)}>
            {t("global.cancel")}
          </Button>
          <Button
            id="upload-conflict-confirm"
            colorScheme="accent"
            onClick={confirmConflictUpload}
          >
            {replaceOnly()
              ? t("home.upload.confirm_replace")
              : t("home.upload.confirm_upload")}
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}
