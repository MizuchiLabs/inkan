// Package worker carries the built worker, so inkan worker export can write a
// folder that is ready to deploy.
package worker

import "embed"

// Bundle holds cloudflare.js, node.js and scaleway.zip under bundle/. A build
// that skipped go generate has none of them.
//
//go:generate pnpm install
//go:generate pnpm build
//go:embed all:bundle
var Bundle embed.FS

// EnvExample lists every setting a worker takes besides its keys.
//
//go:embed worker.env.example
var EnvExample string

// Wrangler is the Cloudflare config of the repository.
//
//go:embed wrangler.jsonc
var Wrangler string
