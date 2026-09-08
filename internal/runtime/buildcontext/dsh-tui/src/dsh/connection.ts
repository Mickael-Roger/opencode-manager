export interface AuthenticatedConnection {
  origin: URL
  cookie: string
}

// DSH Web accepts the launch token only on GET /?token=..., then returns an
// authority-bound HttpOnly cookie. The token is never sent to Remote endpoints.
export async function authenticateLaunchUrl(launchUrl: string): Promise<AuthenticatedConnection> {
  const url = new URL(launchUrl)
  if (!url.searchParams.get("token")) {
    throw new Error("--dsh-url must include the launch token printed by dsh web")
  }
  const response = await fetch(url, { redirect: "manual" })
  if (response.status !== 303) {
    throw new Error(`DSH launch authentication failed: expected HTTP 303, got ${response.status}`)
  }
  const setCookie = response.headers.get("set-cookie")
  if (!setCookie) {
    throw new Error("DSH launch authentication did not return a session cookie")
  }
  url.pathname = "/"
  url.search = ""
  url.hash = ""
  return { origin: url, cookie: setCookie.split(";", 1)[0]! }
}

// OCM persists only the changing DSH Web token. Reconstruct the launch URL from
// the container-local loopback address and assigned port rather than persisting a
// credential-bearing URL in the workspace manifest.
export async function resolveLaunchUrl(explicit?: string): Promise<string> {
  if (explicit) return explicit
  if (process.env.DSH_URL) return process.env.DSH_URL
  const port = process.env.OCM_DSH_PORT
  if (!port) throw new Error("OCM_DSH_PORT is not set; provide --dsh-url instead")
  const home = process.env.DSH_HOME ?? `${process.env.HOME ?? "/home/debian"}/.config/deepseek`
  const token = (await readFile(`${home}/web-token`, "utf8")).trim()
  if (!token) throw new Error("DSH Web token is empty; wait for dsh web to finish starting")
  return `http://127.0.0.1:${port}/?token=${encodeURIComponent(token)}`
}
import { readFile } from "node:fs/promises"
