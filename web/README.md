# guo web 前端

Vite + React + TypeScript + Tailwind CSS v4 + shadcn/ui（radix-nova 风格、neutral 基色，
令牌与仓库根 `ui-contract.tokens.json` / `mockups/theme.css` 同源）。

## 开发命令

```sh
pnpm -C web install        # 安装依赖
pnpm -C web dev            # 开发服务器（默认 :5173，/api 代理到 Go 服务）
pnpm -C web build          # 产物输出 web/dist（guo serve 会自动伺服该目录）
pnpm -C web lint           # oxlint
```

- 后端：仓库根 `go run ./cmd/guo serve`（`GUO_ADDR` 默认 `:8080`）。
- 代理目标可用 `GUO_DEV_API` 覆盖（默认 `http://localhost:8080`）。
- 主题：`document.documentElement` 上的 `.dark` 类，localStorage 键 `guo-theme`。

## 目录约定（后续工单遵守）

- `src/components/ui/`：shadcn 组件（`pnpm dlx shadcn@latest add <name>` 追加）。
- `src/components/`：跨页复用组件（PosterCard/PosterWall、AppShell、ThemeProvider）——保持通用。
- `src/pages/`：页面（browse 已有；search/rankings/detail/downloads/play 依次接入）。
- `src/lib/api.ts`：统一 fetch 封装，错误解包 `{message, hint}`。
- `src/types/`：与 Go 后端 JSON 一一对应的类型。
