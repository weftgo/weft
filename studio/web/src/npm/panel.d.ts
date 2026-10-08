// The side-effect bundle (studio/dist/panel/panel.js — the bytes
// /studio/panel.js serves) sits beside index.js in the npm package.
// It exports nothing: importing it defines <weft-devtools> and mounts
// the dock the way the script tag does. This declaration only lets
// `import "./panel.js"` type-check; the build marks the import
// external (vite.npm.config.ts) and vitest points it at the committed
// bundle.
export {}
