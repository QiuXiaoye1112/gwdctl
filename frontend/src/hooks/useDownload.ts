import { useSelectedLink } from "~/hooks"

export const useDownload = () => {
  const { rawLinks } = useSelectedLink()
  return {
    batchDownloadSelected: () => {
      const urls = rawLinks(true)
      urls.forEach((url) => {
        window.open(url, "_blank")
      })
    },
  }
}
