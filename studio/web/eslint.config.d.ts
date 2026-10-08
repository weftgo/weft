// Types for eslint.config.js's exports, so a test can import the G1
// rule (src/lib/links.lint.test.ts) under the strict tsconfig.
import type { Linter } from "eslint"

export declare const deepLinkRules: Linter.RulesRecord
declare const config: Linter.Config[]
export default config
