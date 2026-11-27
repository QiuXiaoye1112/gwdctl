import { Markdown, MaybeLoading } from "~/components"
import { useFetchText, useT } from "~/hooks"
import { objStore } from "~/store"
import { ext } from "~/utils"

const MdPreview = (props: { outline?: boolean }) => {
  const [content] = useFetchText()
  const t = useT()
  return (
    <MaybeLoading loading={content.loading}>
      <Markdown
        children={content()?.content}
        ext={ext(objStore.obj.name)}
        outline={
          props.outline
            ? {
                label: t("home.preview.names.markdown_outline"),
                emptyLabel: t("home.preview.markdown_outline_empty"),
              }
            : undefined
        }
      />
    </MaybeLoading>
  )
}

export const MdOutlinePreview = () => <MdPreview outline />

export default MdPreview
