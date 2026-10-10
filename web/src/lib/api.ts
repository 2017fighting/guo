// 统一 API 客户端：JSON fetch + {message,hint} 错误解包。
// 后端错误口径（spec §4）：人话 message + hint，不说错误码。

export class ApiError extends Error {
  readonly hint: string
  readonly status: number

  constructor(message: string, hint: string, status: number) {
    super(message)
    this.name = 'ApiError'
    this.hint = hint
    this.status = status
  }
}

async function unwrap<T>(resp: Response): Promise<T> {
  if (!resp.ok) {
    let message = `请求失败（HTTP ${resp.status}）`
    let hint = '请稍后重试'
    try {
      const body = (await resp.json()) as { message?: string; hint?: string }
      if (body.message) message = body.message
      if (body.hint) hint = body.hint
    } catch {
      // 非 JSON 错误体，保留默认文案
    }
    throw new ApiError(message, hint, resp.status)
  }
  return (await resp.json()) as T
}

export async function api<T>(
  path: string,
  params?: Record<string, string | number | undefined | null>,
): Promise<T> {
  const url = new URL(path, window.location.origin)
  for (const [key, value] of Object.entries(params ?? {})) {
    if (value !== undefined && value !== null && value !== '') {
      url.searchParams.set(key, String(value))
    }
  }
  const resp = await fetch(url, { headers: { Accept: 'application/json' } })
  return unwrap<T>(resp)
}

// apiPut 带体写请求（设置保存等）；错误解包与 api 同口径。
export async function apiPut<T>(path: string, body: unknown): Promise<T> {
  const resp = await fetch(new URL(path, window.location.origin), {
    method: 'PUT',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  return unwrap<T>(resp)
}
