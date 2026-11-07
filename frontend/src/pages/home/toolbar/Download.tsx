import {
  Menu,
  MenuTrigger,
  MenuContent,
  MenuItem,
  Modal,
  ModalContent,
  ModalHeader,
  ModalOverlay,
  createDisclosure,
} from "@hope-ui/solid"
import { createMemo, lazy, onCleanup, Show, Suspense } from "solid-js"
import { FullLoading } from "~/components"
import { useDownload, useT } from "~/hooks"
import { getSettingBool, oneChecked, selectedObjs } from "~/store"
import { bus } from "~/utils"
import { CenterIcon } from "./Icon"

export const Download = () => {
  const t = useT()
  const colorScheme = "neutral"
  const { downloadSelected } = useDownload()

  const objs = () => selectedObjs()
  const hasDir = createMemo(() => objs().some((obj) => obj.is_dir))
  // 仅在多选纯文件时展示二选一菜单；单选或包含文件夹时一键直达
  const showMenu = createMemo(() => objs().length > 1 && !hasDir())

  const handleDirectClick = () => {
    if (oneChecked() && !hasDir()) {
      downloadSelected()
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
          <MenuItem colorScheme={colorScheme} onSelect={downloadSelected}>
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

const PackageDownload = lazy(() => import("./PackageDownload"))

export const PackageDownloadModal = () => {
  const t = useT()
  const { isOpen, onOpen, onClose } = createDisclosure()
  const handler = (name: string) => {
    if (name === "package_download") {
      if (!getSettingBool("package_download")) return
      onOpen()
    }
  }
  bus.on("tool", handler)
  onCleanup(() => {
    bus.off("tool", handler)
  })
  return (
    <Modal
      blockScrollOnMount={false}
      opened={isOpen()}
      onClose={onClose}
      closeOnOverlayClick={false}
      closeOnEsc={false}
    >
      <ModalOverlay />
      <ModalContent>
        <ModalHeader>{t("home.toolbar.package_download")}</ModalHeader>
        <Suspense fallback={<FullLoading />}>
          <Show when={isOpen()}>
            <PackageDownload onClose={onClose} />
          </Show>
        </Suspense>
      </ModalContent>
    </Modal>
  )
}
