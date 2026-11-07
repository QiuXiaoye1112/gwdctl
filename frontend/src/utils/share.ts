import { ShareInfo } from "~/types"
import { base_path } from "."
import { matchTemplate } from "./str"

const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

export const shareMessage = (share: ShareInfo, template: string, siteTitle: string) => {
  const baseUrl = `${location.origin}${base_path}`
  const url = `${baseUrl}/share/${encodeURIComponent(share.id)}`
  if (template?.trim()) {
    try {
      const rendered = matchTemplate(template, makeTemplateData(share, { site_title: siteTitle }))
        .split(`${baseUrl}/@s/${share.id}`).join(url)
        .split(`${baseUrl}/share/${share.id}`).join(url)
        .split(`${baseUrl}/${share.id}`).join(url)
      if (rendered.includes(url)) return rendered
    } catch {
      // Invalid templates fall back to a usable share link.
    }
  }
  return share.pwd ? `${url} 提取码: ${share.pwd}` : url
}

export const randomPwd = () => {
  const arr: string[] = Array.from(
    { length: 5 },
    () => letters[Math.floor(Math.random() * letters.length)],
  )
  return arr.join("")
}

export const makeTemplateData = (
  share: ShareInfo,
  other?: { [k: string]: any },
) => {
  return {
    base_url: location.origin + base_path,
    ...share,
    ...other,
  }
}

const expireDateIncrementRegExp =
  /^\+(?=\d+[wdHms])(?:(\d+)w)?(?:(\d+)d)?(?:(\d+)H)?(?:(\d+)m)?(?:(\d+)s)?(?:(\d+)ms)?$/

const toInt = (s: string | undefined) => {
  if (s === undefined) return 0
  const n = Number.parseInt(s)
  return Number.isNaN(n) ? 0 : n
}

export const getExpireDate = (dateStr: string) => {
  const match = expireDateIncrementRegExp.exec(dateStr)
  if (match) {
    const w = toInt(match[1])
    const d = toInt(match[2]) + w * 7
    const H = toInt(match[3]) + d * 24
    const m = toInt(match[4]) + H * 60
    const s = toInt(match[5]) + m * 60
    const ms = toInt(match[6]) + s * 1000
    return new Date(Date.now() + ms)
  } else {
    return new Date(dateStr)
  }
}
