import { PEmptyResp, ShareInfo } from "~/types"
import { useFetch, useRouter, useT, useUtil } from "~/hooks"
import { Button, HStack, Td, Tr } from "@hope-ui/solid"
import {
  handleResp,
  handleRespWithNotifySuccess,
  shareMessage,
  notify,
  r,
} from "~/utils"
import { DeletePopover } from "../common/DeletePopover"
import { getSetting } from "~/store"

interface ShareProps {
  share: ShareInfo
  refresh: () => void
}

function ShareOp(props: ShareProps) {
  const t = useT()
  const { to } = useRouter()
  const [deleteLoading, deleteStorage] = useFetch((): PEmptyResp =>
    r.post(`/share/delete?id=${props.share.id}`),
  )
  const [enableOrDisableLoading, enableOrDisable] = useFetch((): PEmptyResp =>
    r.post(
      `/share/${props.share.disabled ? "enable" : "disable"}?id=${props.share.id}`,
    ),
  )
  return (
    <>
      <Button
        onClick={() => {
          to(`/@manage/shares/edit/${props.share.id}`)
        }}
      >
        {t("global.edit")}
      </Button>
      <Button
        loading={enableOrDisableLoading()}
        colorScheme={props.share.disabled ? "success" : "warning"}
        onClick={async () => {
          const resp = await enableOrDisable()
          handleRespWithNotifySuccess(resp, () => {
            props.refresh()
          })
        }}
      >
        {t(`global.${props.share.disabled ? "enable" : "disable"}`)}
      </Button>
      <DeletePopover
        name={props.share.id}
        loading={deleteLoading()}
        onClick={async () => {
          const resp = await deleteStorage()
          handleResp(resp, () => {
            notify.success(t("global.delete_success"))
            props.refresh()
          })
        }}
      />
    </>
  )
}

export function ShareListItem(props: ShareProps) {
  const t = useT()
  const path = () => {
    if (props.share.files.length === 0) {
      return ""
    }
    let p = props.share.files[0]
    if (props.share.files.length > 1) {
      p += t("shares.files_etc", {
        more: props.share.files.length - 1,
      })
    }
    return p
  }
  const accessed = () => {
    return props.share.max_accessed > 0
      ? `${props.share.accessed} / ${props.share.max_accessed}`
      : `${props.share.accessed}`
  }
  const status = () => {
    if (props.share.disabled) {
      return "disabled"
    }
    if (
      (props.share.max_accessed > 0 &&
        props.share.accessed >= props.share.max_accessed) ||
      props.share.files.length === 0 ||
      (props.share.expires && new Date(props.share.expires) < new Date())
    ) {
      return "invalid"
    }
    return "work"
  }
  const { copy } = useUtil()
  return (
    <Tr>
      <Td>{path()}</Td>
      <Td>{props.share.id}</Td>
      <Td>
        {props.share.expires
          ? new Date(props.share.expires).toLocaleString()
          : ""}
      </Td>
      <Td>{accessed()}</Td>
      <Td>{t(`shares.status_list.${status()}`)}</Td>
      <Td>{props.share.remark}</Td>
      <Td>
        <HStack spacing="$2">
          <Button
            colorScheme="primary"
            onClick={() => {
              copy(shareMessage(props.share, getSetting("share_summary_content"), getSetting("site_title")))
            }}
          >
            {t("shares.copy_msg")}
          </Button>
          <ShareOp {...props} />
        </HStack>
      </Td>
    </Tr>
  )
}
