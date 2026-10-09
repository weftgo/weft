// Types for eslint.config.js's exports, so a test can import the G1
// rule (src/lib/links.lint.test.ts) and the H3 rule
// (src/lib/controls.lint.test.ts) under the strict tsconfig.
import type { Linter } from "eslint"

export declare const deepLinkRules: Linter.RulesRecord
export declare const controlRules: Linter.RulesRecord
export declare const controlFiles: string[]
declare const config: Linter.Config[]
export default config
