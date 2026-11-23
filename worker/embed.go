package worker

import "embed"

// Bundle is empty in a build that skipped go generate.
//
//go:generate pnpm install
//go:generate pnpm build
//go:embed all:bundle
var Bundle embed.FS

//go:embed worker.env.example
var EnvExample string

//go:embed wrangler.jsonc
var Wrangler string
