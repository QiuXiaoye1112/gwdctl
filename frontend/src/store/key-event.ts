export const keyPressed: Record<string, boolean> = {}
document.addEventListener("keydown", (e) => {
  keyPressed[e.key] = true
})
document.addEventListener("keyup", (e) => {
  // keyPressed[e.key] = false;
  delete keyPressed[e.key]
})
window.addEventListener("blur", () => {
  for (const k in keyPressed) {
    delete keyPressed[k]
  }
})
document.addEventListener("visibilitychange", () => {
  if (document.hidden) {
    for (const k in keyPressed) {
      delete keyPressed[k]
    }
  }
})

