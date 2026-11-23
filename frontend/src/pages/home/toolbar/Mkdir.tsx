import { createDisclosure } from "@hope-ui/solid"
import { ModalInput } from "~/components"
import { useFetch, usePath, useRouter } from "~/hooks"
import { bus, fsMkdir, handleRespWithNotifySuccess, pathJoin } from "~/utils"
import { createSignal, onCleanup } from "solid-js"

export const Mkdir = () => {
  const { isOpen, onOpen, onClose } = createDisclosure()
  const [loading, ok] = useFetch(fsMkdir)
  const [submitting, setSubmitting] = createSignal(false)
  const busy = () => loading() || submitting()
  const { pathname } = useRouter()
  const { refresh } = usePath()
  const handler = (name: string) => {
    if (name === "mkdir") {
      onOpen()
    }
  }
  bus.on("tool", handler)
  onCleanup(() => {
    bus.off("tool", handler)
  })
  return (
    <ModalInput
      title="home.toolbar.input_dir_name"
      validateFilename={true}
      opened={isOpen()}
      onClose={onClose}
      loading={busy()}
      onSubmit={async (name) => {
        if (busy()) return
        const p = pathJoin(pathname(), name)
        setSubmitting(true)
        try {
          const resp = await ok(p)
          let succeeded = false
          handleRespWithNotifySuccess(resp, () => {
            succeeded = true
          })
          if (succeeded) {
            await refresh()
            onClose()
          }
        } finally {
          setSubmitting(false)
        }
      }}
    />
  )
}
