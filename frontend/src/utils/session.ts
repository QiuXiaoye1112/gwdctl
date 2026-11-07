import { bus, notify } from "~/utils"
import { setMe, Me } from "~/store"
import { uploadFiles } from "~/pages/home/uploads/Upload"

export const IDLE_TIMEOUT_MS = 10 * 60 * 1000 // 10 minutes

export const getToken = (): string => {
  try {
    return sessionStorage.getItem("token") || ""
  } catch {
    return ""
  }
}

export const recordUserActivity = () => {
  if (!getToken()) return
  try {
    sessionStorage.setItem("last_activity_time", String(Date.now()))
  } catch {}
}

export const clearSession = () => {
  try {
    sessionStorage.removeItem("token")
    sessionStorage.removeItem("last_activity_time")
    localStorage.removeItem("token")
  } catch {}
}

export const checkSessionTimeout = (onLogout: () => void) => {
  const token = getToken()
  if (!token) return

  // Do not log out if already on login or init page
  if (
    location.pathname.startsWith("/@login") ||
    location.pathname.startsWith("/@init")
  ) {
    return
  }

  // If there are ongoing uploads, treat session as active
  try {
    if (
      uploadFiles &&
      uploadFiles.uploads.some((u) =>
        ["uploading", "backending", "pending"].includes(u.status),
      )
    ) {
      recordUserActivity()
      return
    }
  } catch {}

  const lastActive = Number(
    sessionStorage.getItem("last_activity_time") || 0,
  )
  if (!lastActive) {
    recordUserActivity()
    return
  }

  const idle = Date.now() - lastActive
  if (idle >= IDLE_TIMEOUT_MS) {
    onLogout()
    setMe({} as Me)
    notify.warning(
      localStorage.getItem("lang") === "en"
        ? "Session expired due to 10 minutes of inactivity. Please log in again."
        : "会话已超时（10分钟无操作），请重新登录。",
    )
    bus.emit("to", `/@login?redirect=${encodeURIComponent(location.pathname)}`)
  }
}

export const setupSessionSecurity = (onLogout: () => void) => {
  // Check immediately on setup
  checkSessionTimeout(onLogout)

  // Throttle mousemove
  let lastMove = 0
  const onMouseMove = () => {
    const now = Date.now()
    if (now - lastMove > 5000) {
      lastMove = now
      recordUserActivity()
    }
  }

  const onActivity = () => {
    recordUserActivity()
  }

  const events = ["mousedown", "keydown", "scroll", "touchstart", "click"]
  events.forEach((evt) => {
    window.addEventListener(evt, onActivity, { passive: true })
  })
  window.addEventListener("mousemove", onMouseMove, { passive: true })

  const check = () => checkSessionTimeout(onLogout)
  window.addEventListener("focus", check)
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") {
      check()
    }
  })

  // Periodic interval check every 5 seconds
  const timer = setInterval(check, 5000)

  return () => {
    events.forEach((evt) => {
      window.removeEventListener(evt, onActivity)
    })
    window.removeEventListener("mousemove", onMouseMove)
    window.removeEventListener("focus", check)
    clearInterval(timer)
  }
}
