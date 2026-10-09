/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly PUBLIC_BASE_URL?: string
  readonly PUBLIC_API_URL?: string
  readonly PUBLIC_RPC_URL?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
