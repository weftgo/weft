// This file exists to keep the Go toolchain out of the web project:
// a nested module is excluded from the parent module's ./... package
// walk, so node_modules (which contains vendored Go packages, e.g.
// flatted/golang) never enters studio's build or test set. The web
// project itself is Bun's; this module has no Go packages.
module github.com/weftgo/weft/studio/web

go 1.26
