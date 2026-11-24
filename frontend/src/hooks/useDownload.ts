import { useSelectedLink } from "~/hooks"

export const triggerDownload = (url: string, filename = "") => {
  const a = document.createElement("a")
  a.href = url
  a.download = filename
  a.style.display = "none"
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
}

export const useDownload = () => {
  const { rawLinks } = useSelectedLink()
  return {
    downloadSelected: () => {
      const urls = rawLinks(true)
      urls.forEach((url, index) => {
        if (index === 0) {
          triggerDownload(url)
        } else {
          setTimeout(() => {
            triggerDownload(url)
          }, index * 250)
        }
      })
    },
  }
}
