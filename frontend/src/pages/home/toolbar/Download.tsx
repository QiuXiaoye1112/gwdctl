import {
  Button,
  Menu,
  MenuTrigger,
  MenuContent,
  MenuItem,
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  createDisclosure,
} from "@hope-ui/solid"
import { createMemo, createSignal, lazy, onCleanup, Show, Suspense } from "solid-js"
import { FullLoading } from "~/components"
import { useDownload, useT } from "~/hooks"
import { getSettingBool, oneChecked, selectedObjs } from "~/store"
import { bus } from "~/utils"
import { CenterIcon } from "./Icon"

export const Download = () => {
  const t = useT()
  const colorScheme = "neutral"

  const objs = () => selectedObjs()
  const hasDir = createMemo(() => objs().some((obj) => obj.is_dir))
  // 仅在多选纯文件时展示二选一菜单；单选或包含文件夹时一键直达
  const showMenu = createMemo(() => objs().length > 1 && !hasDir())

  const handleDirectClick = () => {
    if (oneChecked() && !hasDir()) {
      bus.emit("tool", "direct_download")
    } else {
      bus.emit("tool", "package_download")
    }
  }

  return (
    <Show
      when={showMenu()}
      fallback={<CenterIcon name="download" onClick={handleDirectClick} />}
    >
      <Menu placement="top" offset={10}>
        <MenuTrigger as={CenterIcon} name="download" />
        <MenuContent>
          <MenuItem
            colorScheme={colorScheme}
            onSelect={() => {
              bus.emit("tool", "direct_download")
            }}
          >
            {t("home.toolbar.direct_download")}
          </MenuItem>
          <MenuItem
            colorScheme={colorScheme}
            onSelect={() => {
              bus.emit("tool", "package_download")
            }}
          >
            {t("home.toolbar.package_download")}
          </MenuItem>
        </MenuContent>
      </Menu>
    </Show>
  )
}

export const DirectDownloadModal = () => {
  const t = useT()
  const { downloadSelected } = useDownload()
  const { isOpen, onOpen, onClose } = createDisclosure()

  const handler = (name: string) => {
    if (name === "direct_download") {
      onOpen()
    }
  }
  bus.on("tool", handler)
  onCleanup(() => {
    bus.off("tool", handler)
  })

  const objs = () => selectedObjs()
  const confirmTips = () => {
    if (objs().length === 1) {
      return t("home.toolbar.download_confirm_single", {
        name: objs()[0]?.name ?? "",
      })
    }
    return t("home.toolbar.download_confirm_multi", {
      count: objs().length,
    })
  }

  return (
    <Modal
      blockScrollOnMount={false}
      opened={isOpen()}
      onClose={onClose}
      size={{
        "@initial": "xs",
        "@md": "md",
      }}
    >
      <ModalOverlay />
      <ModalContent>
        <ModalHeader>{t("home.toolbar.download_confirm_title")}</ModalHeader>
        <ModalBody>
          <p>{confirmTips()}</p>
        </ModalBody>
        <ModalFooter display="flex" gap="$2">
          <Button onClick={onClose} colorScheme="neutral">
            {t("global.cancel")}
          </Button>
          <Button
            colorScheme="info"
            onClick={() => {
              onClose()
              downloadSelected()
            }}
          >
            {t("global.confirm")}
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

const PackageDownload = lazy(() => import("./PackageDownload"))

export const PackageDownloadModal = () => {
  const t = useT()
  const { isOpen, onOpen, onClose } = createDisclosure()
  const [step, setStep] = createSignal<"confirm" | "downloading">("confirm")

  const handler = (name: string) => {
    if (name === "package_download") {
      if (!getSettingBool("package_download")) return
      setStep("confirm")
      onOpen()
    }
  }
  bus.on("tool", handler)
  onCleanup(() => {
    bus.off("tool", handler)
  })

  const objs = () => selectedObjs()
  const confirmTips = () => {
    if (objs().length === 1) {
      return t("home.toolbar.package_download_confirm_single", {
        name: objs()[0]?.name ?? "",
      })
    }
    return t("home.toolbar.package_download_confirm_multi", {
      count: objs().length,
    })
  }

  return (
    <Modal
      blockScrollOnMount={false}
      opened={isOpen()}
      onClose={onClose}
      closeOnOverlayClick={false}
      closeOnEsc={false}
      size={{
        "@initial": "xs",
        "@md": "md",
      }}
    >
      <ModalOverlay />
      <ModalContent>
        <ModalHeader>{t("home.toolbar.package_download_confirm_title")}</ModalHeader>
        <Show
          when={step() === "confirm"}
          fallback={
            <Suspense fallback={<FullLoading />}>
              <PackageDownload onClose={onClose} />
            </Suspense>
          }
        >
          <ModalBody>
            <p>{confirmTips()}</p>
          </ModalBody>
          <ModalFooter display="flex" gap="$2">
            <Button onClick={onClose} colorScheme="neutral">
              {t("global.cancel")}
            </Button>
            <Button
              colorScheme="info"
              onClick={() => {
                setStep("downloading")
              }}
            >
              {t("global.confirm")}
            </Button>
          </ModalFooter>
        </Show>
      </ModalContent>
    </Modal>
  )
}
